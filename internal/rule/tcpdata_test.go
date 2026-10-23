package rule

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseTCPData(t *testing.T) {
	cases := []struct{ in, want string }{
		{`send="SSH-2.0-OpenSSH_9.6\r\n"`, `delay=0s,send="SSH-2.0-OpenSSH_9.6\r\n"`},
		{`5ms,reply="HTTP/1.1 503 Service Unavailable\r\n\r\n",after=close`, `delay=5ms,reply="HTTP/1.1 503 Service Unavailable\r\n\r\n",after=close`},
		{`limit=1000,after=reset`, `delay=0s,limit=1000,after=reset`},
		{`wait=2s,reply="x"`, `delay=0s,reply="x",wait=2s`},
		{`send="a",reply="b",after=reset`, `delay=0s,send="a",reply="b",after=reset`},
		// Separators of the text form inside the bytes, and bytes that are
		// not printable or not UTF-8.
		{`reply="a,b;c for 2 requests=d"`, `delay=0s,reply="a,b;c for 2 requests=d"`},
		{`send="\x00\xff\t\"q\"\\"`, `delay=0s,send="\x00\xff\t\"q\"\\"`},
		{`send="é"`, `delay=0s,send="\u00e9"`},
		{`reply="; for 1 request; " for 2 requests; 5ms`, `delay=0s,reply="; for 1 request; " for 2 requests; delay=5ms`},
	}
	for _, c := range cases {
		got, err := ParseTCPScenario(c.in)
		if err != nil {
			t.Errorf("ParseTCPScenario(%s): %v", c.in, err)
			continue
		}
		if got.String() != c.want {
			t.Errorf("ParseTCPScenario(%s) = %s, want %s", c.in, got, c.want)
		}
		again, err := ParseTCPScenario(got.String())
		if err != nil || again.String() != got.String() {
			t.Errorf("round trip of %s: %s, %v", got, again, err)
		}
		// The JSON of GET /rules carries the text form; it must come back.
		b, _ := json.Marshal(got)
		var back TCPScenario
		if err := json.Unmarshal(b, &back); err != nil || back.String() != got.String() {
			t.Errorf("JSON round trip of %s: %s, %v", b, back, err)
		}
	}
	sp, _ := ParseTCPSpec(`send="\x00\xff",reply="é"`)
	if string(sp.Send) != "\x00\xff" || string(sp.Reply) != "é" {
		t.Errorf("bytes: %q %q", sp.Send, sp.Reply)
	}
	for bad, want := range map[string]string{
		`send=hello`:         "double quotes",
		`send="unterminated`: "not a Go string",
		`send=""`:            "no bytes",
		`send="` + strings.Repeat("a", MaxData+1) + `"`: "more than",
		`limit=1000`:          "after=close or after=reset",
		`limit=0,after=close`: "at least 1",
		`limit=x,after=close`: "at least 1",
		`wait=2s`:             "neither",
		`wait=2s,after=close`: "neither",
		`refuse,send="x"`:     "accepts none",
		`loss=100%,reply="x"`: "accepts none",
		`pattern=xr,send="x"`: "accepts none",
	} {
		if _, err := ParseTCPScenario(bad); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseTCPScenario(%.40s) = %v, want an error containing %q", bad, err, want)
		}
	}
	// Ping and DNS rules have no connection to send on.
	for _, text := range []string{`send="x"`, `reply="x"`, `limit=5`, `wait=1s`} {
		if _, err := ParseScenario(text); err == nil || !strings.Contains(err.Error(), "TCP") {
			t.Errorf("ParseScenario(%s) = %v", text, err)
		}
		if _, err := ParseDNSScenario(text); err == nil || !strings.Contains(err.Error(), "TCP") {
			t.Errorf("ParseDNSScenario(%s) = %v", text, err)
		}
	}
}

func TestTCPDataJSON(t *testing.T) {
	var sc TCPScenario
	in := `{"reply": "HTTP/1.1 503 x\r\n\r\n", "after": "close", "wait": "1s"}`
	if err := json.Unmarshal([]byte(in), &sc); err != nil {
		t.Fatal(err)
	}
	sp := sc.Phases[0].Spec
	if string(sp.Reply) != "HTTP/1.1 503 x\r\n\r\n" || sp.Wait != time.Second || sp.After != AfterClose {
		t.Errorf("%+v", sp)
	}
	if err := json.Unmarshal([]byte(`{"limit": 10, "after": "reset", "send": "hi"}`), &sc); err != nil || sc.Phases[0].Spec.Limit != 10 {
		t.Errorf("limit: %v %+v", err, sc)
	}
	for _, bad := range []string{`{"limit": 10}`, `{"send": ""}`, `{"wait": "1s"}`, `{"limit": -1, "after": "close"}`} {
		if err := json.Unmarshal([]byte(bad), &sc); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	var ping Scenario
	if err := json.Unmarshal([]byte(`{"send": "x"}`), &ping); err == nil {
		t.Error("send accepted in a ping rule")
	}
	b, _ := json.Marshal(Spec{Send: []byte("hi"), Limit: 3, After: AfterReset})
	if !strings.Contains(string(b), `"send":"hi"`) || !strings.Contains(string(b), `"limit":3`) {
		t.Errorf("spec JSON %s", b)
	}
	if b, _ := json.Marshal(Spec{Delay: time.Millisecond}); strings.Contains(string(b), "send") || strings.Contains(string(b), "limit") {
		t.Errorf("ping spec JSON %s mentions TCP data", b)
	}
}
