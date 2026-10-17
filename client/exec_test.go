package client_test

import (
	"context"
	"os/exec"
	"testing"

	"github.com/nanmuyao/pingmock/client"
)

// TestUnderPingmockExec runs only inside pingmock exec, the way users are
// meant to test with pingmock:
//
//	go build -o pingmock .
//	go test -exec "$PWD/pingmock exec --" -run TestUnderPingmockExec ./client
func TestUnderPingmockExec(t *testing.T) {
	pm, err := client.FromEnv()
	if err != nil {
		t.Skip(err)
	}
	ctx := context.Background()
	if err := pm.ClearHistory(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pm.SetRule(ctx, "10.99.0.1", "delay=30ms,pattern=.u"); err != nil {
		t.Fatal(err)
	}
	// Stands in for the code under test, which would ping on its own.
	out, _ := exec.Command("ping", "-c", "2", "-i", "0.2", "-W", "1", "10.99.0.1").CombinedOutput()
	t.Logf("ping:\n%s", out)

	events, err := pm.History(ctx, "10.99.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Result != "reply" || events[0].Delay != "30ms" ||
		events[1].Result != "unreachable" || events[1].Code != "host" {
		t.Errorf("history = %+v", events)
	}
	st, err := pm.Stats(ctx, "10.99.0.1")
	if err != nil || st.Replied != 1 || st.Unreachable != 1 {
		t.Errorf("stats = %+v, %v", st, err)
	}
}
