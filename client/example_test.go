package client_test

import (
	"context"
	"fmt"
	"log"

	"github.com/nanmuyao-tools/pingmock/client"
)

// Change network conditions in the middle of a test and inspect what the
// code under test did. Run the tests with:
//
//	go test -exec 'pingmock exec -rule 10.99.0.1=20ms --' ./...
func Example() {
	pm, err := client.FromEnv()
	if err != nil {
		log.Fatal(err) // in a test: t.Skip(err)
	}
	ctx := context.Background()

	// The fast link becomes slow, then drops every other ping.
	pm.SetRule(ctx, "10.99.0.1", "500ms")
	pm.SetRule(ctx, "10.99.0.1", "delay=20ms,pattern=.x")

	events, _ := pm.History(ctx, "10.99.0.1", 10)
	for _, e := range events {
		fmt.Println(e.Seq, e.Result, e.Delay)
	}
}
