// Command tcpdial connects to each ADDR:PORT given with Go's net.Dial, as
// Go programs do, and prints one line per target for test/e2e-compat.sh:
//
//	go 10.99.0.11:80 accepted 201ms read=timeout
//	go 10.99.0.12:80 refused
//
// The outcome is one of accepted, refused, timeout, unreachable or the
// error itself. On an accepted connection the client writes a request and
// reads the answer, which shows what ghostwire does after the handshake:
// nothing (timeout), close (closed) or reset (reset).
package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"time"
)

func main() {
	for _, target := range os.Args[1:] {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", target, time.Second)
		took := time.Since(start).Milliseconds()
		switch {
		case err == nil:
			conn.SetDeadline(time.Now().Add(300 * time.Millisecond))
			read := "data"
			if _, err := conn.Write([]byte("hello\n")); err != nil {
				read = outcome(err)
			} else if _, err := conn.Read(make([]byte, 1)); err != nil {
				read = outcome(err)
			}
			conn.Close()
			fmt.Printf("go %s accepted %dms read=%s\n", target, took, read)
		default:
			fmt.Printf("go %s %s\n", target, outcome(err))
		}
	}
}

// outcome names an error the way the checks expect.
func outcome(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "reset"
	case errors.Is(err, io.EOF):
		return "closed"
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "unreachable"
	case errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	}
	return "error: " + err.Error()
}
