// Command fastest pings servers and prints the fastest one:
//
//	fastest 10.99.0.1 10.99.0.2 10.99.0.3
package main

import (
	"context"
	"fmt"
	"os"

	fastest "example.com/go-demo"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: fastest SERVER...")
		os.Exit(2)
	}
	best, results, err := fastest.Pick(context.Background(), os.Args[1:], fastest.Options{})
	for _, r := range results {
		fmt.Println(r)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("fastest:", best)
}
