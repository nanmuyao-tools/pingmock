package dns

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nanmuyao/pingmock/internal/rule"
)

// query builds a query for name of the given type, as a stub resolver
// sends it, with the given ID.
func query(id uint16, name string, qtype uint16) []byte {
	q := make([]byte, 12)
	binary.BigEndian.PutUint16(q[0:], id)
	q[2] = 0x01 // recursion desired
	binary.BigEndian.PutUint16(q[4:], 1)
	q = append(q, encodeName(name)...)
	q = binary.BigEndian.AppendUint16(q, qtype)
	return binary.BigEndian.AppendUint16(q, classIN)
}

// parsed is what a test needs to know about a response.
type parsed struct {
	id      uint16
	rcode   byte
	aa      bool
	answers [][]byte // record data, in order
	qname   string
}

func parse(t *testing.T, resp []byte) parsed {
	t.Helper()
	if len(resp) < 12 || resp[2]&0x80 == 0 {
		t.Fatalf("not a response: % x", resp)
	}
	p := parsed{id: binary.BigEndian.Uint16(resp[0:]), rcode: resp[3] & 0x0f, aa: resp[2]&0x04 != 0}
	i := 12
	if binary.BigEndian.Uint16(resp[4:]) == 1 {
		name, _, _, end, err := question(resp)
		if err != nil {
			t.Fatal(err)
		}
		p.qname, i = name, end
	}
	for n := binary.BigEndian.Uint16(resp[6:]); n > 0; n-- {
		if resp[i] != 0xc0 || resp[i+1] != 12 {
			t.Fatalf("answer %d does not point at the question: % x", n, resp[i:])
		}
		l := int(binary.BigEndian.Uint16(resp[i+10:]))
		p.answers = append(p.answers, resp[i+12:i+12+l])
		i += 12 + l
	}
	return p
}

func newServer(forward func([]byte, bool) ([]byte, error)) *Server {
	tb := NewTable()
	tb.Add("API.example.com.", netip.MustParseAddr("10.99.0.1"))
	tb.Add("api.example.com", netip.MustParseAddr("fd99::1"))
	tb.Add("alias.example.com", netip.MustParseAddr("10.99.0.1"))
	return &Server{Table: tb, Forward: forward}
}

func TestAnswersMockedNames(t *testing.T) {
	s := newServer(nil)
	p := parse(t, s.Answer(query(7, "Api.Example.COM", typeA)))
	if p.id != 7 || p.rcode != rcodeOK || !p.aa || p.qname != "api.example.com" || len(p.answers) != 1 || net.IP(p.answers[0]).String() != "10.99.0.1" {
		t.Errorf("A: %+v", p)
	}
	p = parse(t, s.Answer(query(8, "api.example.com", typeAAAA)))
	if p.rcode != rcodeOK || len(p.answers) != 1 || net.IP(p.answers[0]).String() != "fd99::1" {
		t.Errorf("AAAA: %+v", p)
	}
	// A name with only an IPv4 address exists but has no AAAA: NOERROR and
	// no data, so that resolvers go on to ask for A.
	p = parse(t, s.Answer(query(9, "alias.example.com", typeAAAA)))
	if p.rcode != rcodeOK || !p.aa || len(p.answers) != 0 {
		t.Errorf("AAAA without IPv6: %+v", p)
	}
}

func TestAnswersReverse(t *testing.T) {
	s := newServer(nil)
	p := parse(t, s.Answer(query(1, "1.0.99.10.in-addr.arpa", typePTR)))
	if p.rcode != rcodeOK || len(p.answers) != 1 || string(p.answers[0]) != string(encodeName("api.example.com")) {
		t.Errorf("PTR v4: %+v", p) // the first name added for the address
	}
	p = parse(t, s.Answer(query(2, "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.9.9.d.f.ip6.arpa", typePTR)))
	if p.rcode != rcodeOK || len(p.answers) != 1 || string(p.answers[0]) != string(encodeName("api.example.com")) {
		t.Errorf("PTR v6: %+v", p)
	}
	if p := parse(t, s.Answer(query(3, "2.0.99.10.in-addr.arpa", typePTR))); p.rcode != rcodeNXDomain {
		t.Errorf("PTR of an unmocked address: %+v", p)
	}
}

func TestUnknownNamesAreForwarded(t *testing.T) {
	var forwarded []byte
	s := newServer(func(q []byte, _ bool) ([]byte, error) {
		forwarded = q
		return respond(q, q[12:], rcodeOK, [][]byte{record(typeA, []byte{93, 184, 216, 34})}), nil
	})
	q := query(5, "real.example", typeA)
	p := parse(t, s.Answer(q))
	if string(forwarded) != string(q) || p.rcode != rcodeOK || len(p.answers) != 1 || net.IP(p.answers[0]).String() != "93.184.216.34" {
		t.Errorf("forwarded: %+v", p)
	}
	// Mocked names never leave: the forwarder is not called for them.
	forwarded = nil
	s.Answer(query(6, "api.example.com", typeA))
	if forwarded != nil {
		t.Error("a mocked name was forwarded")
	}
	// A forwarder that fails means no answer, as when the host's resolver
	// does not answer: the resolver asking times out as it would without
	// pingmock, rather than getting a SERVFAIL it would not have got.
	s.Forward = func([]byte, bool) ([]byte, error) { return nil, errors.New("down") }
	if resp := s.Answer(query(7, "real.example", typeA)); resp != nil {
		t.Errorf("forwarder down: answered %+v", parse(t, resp))
	}
	// Without a forwarder, unknown names do not exist.
	s.Forward = nil
	if p := parse(t, s.Answer(query(8, "real.example", typeA))); p.rcode != rcodeNXDomain || !p.aa {
		t.Errorf("no forwarder: %+v", p)
	}
}

func TestRejectsWhatItCannotHandle(t *testing.T) {
	s := newServer(nil)
	if s.Answer([]byte{1, 2, 3}) != nil {
		t.Error("answered a packet too short to be a query")
	}
	resp := s.Answer(query(1, "api.example.com", typeA))
	if s.Answer(resp) != nil {
		t.Error("answered a response")
	}
	q := query(2, "api.example.com", typeA)
	q[2] |= 1 << 3 // inverse query
	if p := parse(t, s.Answer(q)); p.rcode != rcodeNotImpl {
		t.Errorf("inverse query: %+v", p)
	}
	q = query(3, "api.example.com", typeA)
	binary.BigEndian.PutUint16(q[4:], 2) // two questions
	if p := parse(t, s.Answer(q)); p.rcode != rcodeFormErr {
		t.Errorf("two questions: %+v", p)
	}
	if p := parse(t, s.Answer(q[:20])); p.rcode != rcodeFormErr {
		t.Errorf("truncated: %+v", p)
	}
}

func TestServeOverUDP(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go newServer(nil).Serve(pc)
	resp, err := ask(pc.LocalAddr().(*net.UDPAddr).IP.String()+":"+itoa(pc.LocalAddr().(*net.UDPAddr).Port), query(9, "api.example.com", typeA), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if p := parse(t, resp); p.id != 9 || len(p.answers) != 1 {
		t.Errorf("over UDP: %+v", p)
	}
}

func TestRelayAndStreamForwarder(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	release := make(chan struct{})
	go ServeRelay(a, func(q []byte, tcp bool) ([]byte, error) {
		switch string(q) {
		case "fail":
			return nil, errors.New("down")
		case "slow":
			<-release
		}
		return append([]byte(fmt.Sprintf("ok tcp=%v:", tcp)), q...), nil
	})
	fwd := StreamForwarder(b)
	if resp, err := fwd([]byte("hello"), false); err != nil || string(resp) != "ok tcp=false:hello" {
		t.Errorf("forwarded = %q, %v", resp, err)
	}
	if resp, err := fwd([]byte("big"), true); err != nil || string(resp) != "ok tcp=true:big" {
		t.Errorf("forwarded over TCP = %q, %v", resp, err)
	}
	if _, err := fwd([]byte("fail"), false); err == nil {
		t.Error("a failed forward was not reported")
	}
	// A slow query holds up no other: the next one is answered first.
	slow := make(chan string)
	go func() {
		resp, _ := fwd([]byte("slow"), false)
		slow <- string(resp)
	}()
	time.Sleep(20 * time.Millisecond)
	if resp, err := fwd([]byte("again"), false); err != nil || string(resp) != "ok tcp=false:again" {
		t.Errorf("while a slow query waits = %q, %v", resp, err)
	}
	close(release)
	if resp := <-slow; resp != "ok tcp=false:slow" {
		t.Errorf("slow query = %q", resp)
	}
	// Once the relay is gone, queries fail at once rather than wait.
	b.Close()
	time.Sleep(20 * time.Millisecond)
	if _, err := fwd([]byte("late"), false); err == nil {
		t.Error("a query after the relay closed succeeded")
	}
}

// A slow query, over UDP or TCP, holds up no other query.
func TestServeAnswersConcurrently(t *testing.T) {
	release := make(chan struct{})
	s := newServer(func(q []byte, _ bool) ([]byte, error) {
		<-release
		return respond(q, q[12:], rcodeOK, nil), nil
	})
	defer close(release)
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go s.Serve(pc)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go s.ServeTCP(l)

	udp := pc.LocalAddr().String()
	go ask(udp, query(1, "slow.example", typeA), 2*time.Second) // forwarded, held
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	resp, err := ask(udp, query(2, "api.example.com", typeA), time.Second)
	if err != nil || parse(t, resp).id != 2 || time.Since(start) > 500*time.Millisecond {
		t.Errorf("UDP: mocked name behind a slow one: %v after %v", err, time.Since(start))
	}
	// Over TCP, on one connection: the second query is answered while the
	// first waits.
	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	WriteFrame(conn, query(3, "slow.example", typeA))
	WriteFrame(conn, query(4, "api.example.com", typeAAAA))
	resp, err = ReadFrame(conn)
	if err != nil {
		t.Fatalf("TCP: %v", err)
	}
	if p := parse(t, resp); p.id != 4 || len(p.answers) != 1 || net.IP(p.answers[0]).String() != "fd99::1" {
		t.Errorf("TCP: %+v", p)
	}
}

// Queries over TCP are forwarded over TCP, so that an answer too large for
// UDP, which resolvers ask again for over TCP, can come back.
func TestTCPQueriesAreForwardedOverTCP(t *testing.T) {
	var gotTCP bool
	s := newServer(func(q []byte, tcp bool) ([]byte, error) {
		gotTCP = tcp
		return respond(q, q[12:], rcodeOK, nil), nil
	})
	if resp := s.answer(query(1, "real.example", typeA), true); resp == nil || !gotTCP {
		t.Errorf("TCP query forwarded with tcp=%v", gotTCP)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		q, _ := ReadFrame(conn)
		big := respond(q, q[12:], rcodeOK, nil)
		big = append(big, make([]byte, 3000)...) // larger than UDP would carry
		WriteFrame(conn, big)
	}()
	resp, err := Forwarder([]string{l.Addr().String()}, time.Second)(query(5, "big.example", 16), true)
	if err != nil || len(resp) < 3000 || binary.BigEndian.Uint16(resp) != 5 {
		t.Errorf("forwarded over TCP: %d bytes, %v", len(resp), err)
	}
}

func TestNameservers(t *testing.T) {
	path := t.TempDir() + "/resolv.conf"
	write := func(s string) { writeFile(t, path, s) }
	write("# comment\nnameserver 127.0.0.53\noptions edns0\nnameserver 2001:db8::1\nnameserver not-an-ip\n")
	if got := strings.Join(Nameservers(path), " "); got != "127.0.0.53 2001:db8::1" {
		t.Errorf("Nameservers = %q", got)
	}
	write("search example.com\n")
	if got := Nameservers(path); len(got) != 1 || got[0] != "127.0.0.1" {
		t.Errorf("without nameservers = %v, want the C library's default", got)
	}
	if got := Nameservers(path + ".missing"); len(got) != 1 || got[0] != "127.0.0.1" {
		t.Errorf("missing file = %v", got)
	}
}

// ask's "wrong ID" handling: a stray response is skipped, the right one
// returned.
func TestAskSkipsOtherResponses(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 512)
		n, from, _ := pc.ReadFrom(buf)
		stray := append([]byte(nil), buf[:n]...)
		stray[0] ^= 0xff
		stray[2] |= 0x80
		pc.WriteTo(stray, from)
		right := append([]byte(nil), buf[:n]...)
		right[2] |= 0x80
		pc.WriteTo(right, from)
	}()
	addr := pc.LocalAddr().(*net.UDPAddr)
	resp, err := ask(addr.IP.String()+":"+itoa(addr.Port), query(4, "x.example", typeA), time.Second)
	if err != nil || binary.BigEndian.Uint16(resp) != 4 {
		t.Errorf("ask = % x, %v", resp, err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A DNS rule decides how a query is answered, for mocked and forwarded
// names alike; PTR queries and names without a rule are answered as
// before.
func TestRules(t *testing.T) {
	forwarded := 0
	s := newServer(func(q []byte, _ bool) ([]byte, error) {
		forwarded++
		return respond(q, q[12:], rcodeOK, [][]byte{record(typeA, []byte{93, 184, 216, 34})}), nil
	})
	var asked []string
	s.Rule = func(name string, qtype uint16) (rule.Outcome, time.Duration, bool) {
		asked = append(asked, fmt.Sprintf("%s/%d", name, qtype))
		switch name {
		case "api.example.com":
			return rule.Reply, 30 * time.Millisecond, true
		case "gone.example":
			return rule.NXDomain, 0, true
		case "broken.example":
			return rule.ServFail, 0, true
		case "slow.example":
			return rule.Drop, 0, true
		case "real.example":
			return rule.Reply, 20 * time.Millisecond, true
		}
		return 0, 0, false
	}
	start := time.Now()
	p := parse(t, s.Answer(query(1, "api.example.com", typeA)))
	if p.rcode != rcodeOK || len(p.answers) != 1 || time.Since(start) < 30*time.Millisecond {
		t.Errorf("mocked name with a delay: %+v after %v", p, time.Since(start))
	}
	if p := parse(t, s.Answer(query(2, "gone.example", typeAAAA))); p.rcode != rcodeNXDomain || p.qname != "gone.example" {
		t.Errorf("nxdomain: %+v", p)
	}
	if p := parse(t, s.Answer(query(3, "broken.example", typeA))); p.rcode != rcodeServFail {
		t.Errorf("servfail: %+v", p)
	}
	if resp := s.Answer(query(4, "slow.example", typeA)); resp != nil {
		t.Errorf("drop answered % x", resp)
	}
	if forwarded != 0 {
		t.Errorf("faults were forwarded %d times", forwarded)
	}
	// A forwarded name: the answer from the host, after the rule's delay.
	start = time.Now()
	if p := parse(t, s.Answer(query(5, "real.example", typeA))); p.rcode != rcodeOK || forwarded != 1 || time.Since(start) < 20*time.Millisecond {
		t.Errorf("forwarded with a delay: %+v", p)
	}
	// No rule: forwarded at once; PTR: never asked about.
	s.Answer(query(6, "other.example", typeA))
	s.Answer(query(7, "1.0.99.10.in-addr.arpa", typePTR))
	want := "[api.example.com/1 gone.example/28 broken.example/1 slow.example/1 real.example/1 other.example/1]"
	if fmt.Sprint(asked) != want {
		t.Errorf("asked %v, want %s", asked, want)
	}
}
