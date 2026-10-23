// Package responder reads packets from the TUN device and answers echo
// requests according to the rules, after the decided delay.
package responder

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/nanmuyao/pingmock/internal/engine"
	"github.com/nanmuyao/pingmock/internal/icmp"
	"github.com/nanmuyao/pingmock/internal/rule"
)

// Responder answers echo requests read from a device.
type Responder struct {
	Rules   *engine.RuleSet
	History *engine.History
	Verbose bool // log every request
}

// Serve handles packets from dev until reading fails. It returns nil when
// dev is closed. Each answer is written to dev after its delay.
func (r *Responder) Serve(dev io.ReadWriter) error {
	var wmu sync.Mutex // one goroutine writes each delayed answer
	write := func(pkts [][]byte) {
		wmu.Lock()
		defer wmu.Unlock()
		for _, pkt := range pkts {
			if _, err := dev.Write(pkt); err != nil && !errors.Is(err, os.ErrClosed) {
				log.Printf("write: %v", err)
			}
		}
	}
	var q connQueue
	buf := make([]byte, 65536)
	for {
		n, err := dev.Read(buf)
		if err != nil {
			if errors.Is(err, os.ErrClosed) || errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		pl := r.decide(buf[:n])
		if len(pl.now) > 0 {
			write(pl.now)
		}
		switch {
		case len(pl.later) == 0:
		case pl.conn != "":
			q.schedule(pl.conn, pl.delay, func() { write(pl.later) })
		case pl.delay == 0:
			// Answers without a delay go out at once, in the order of
			// what they answer: a timer each would let a reset overtake
			// the FIN it follows.
			write(pl.later)
		default:
			time.AfterFunc(pl.delay, func() { write(pl.later) })
		}
	}
}

// connQueue writes the answers of each TCP connection in the order they
// were decided, each after its own delay: data held back by wait=, and
// the FIN after it, must not be overtaken by what comes next.
type connQueue struct {
	mu      sync.Mutex
	pending map[string]chan struct{} // closed when the connection's last answer is written
}

// schedule runs write after delay, and after the answers scheduled before
// for the same connection. With nothing pending and no delay, it runs at
// once.
func (q *connQueue) schedule(conn string, delay time.Duration, write func()) {
	q.mu.Lock()
	prev := q.pending[conn]
	if prev == nil && delay == 0 {
		q.mu.Unlock()
		write()
		return
	}
	done := make(chan struct{})
	if q.pending == nil {
		q.pending = map[string]chan struct{}{}
	}
	q.pending[conn] = done
	q.mu.Unlock()
	go func() {
		time.Sleep(delay)
		if prev != nil {
			<-prev
		}
		write()
		close(done)
		q.mu.Lock()
		if q.pending[conn] == done {
			delete(q.pending, conn)
		}
		q.mu.Unlock()
	}()
}

// plan is how to answer one packet.
type plan struct {
	now   [][]byte      // to write at once
	later [][]byte      // to write after delay, in order
	delay time.Duration //
	conn  string        // the TCP connection later belongs to, kept in order with its others; empty for the rest
}

// decide decides how to answer one packet and records the request. The
// plan is empty if there is nothing to send: the packet was not a probe,
// the target has no path and it was not an echo request, or the rule
// drops it.
func (r *Responder) decide(pkt []byte) plan {
	req, ok := icmp.ParseProbe(pkt)
	if !ok {
		return plan{}
	}
	if req.Kind == icmp.Segment {
		a, ok := r.Rules.DecideSegment(req)
		if !ok {
			return plan{} // not a connection pingmock answers after the handshake
		}
		if a.Sent > 0 {
			d := a.Accepted
			d.Outcome, d.Delay = rule.Sent, a.Delay
			r.recordSent(req, d, a.Sent)
		}
		if a.Action != nil {
			d := *a.Action
			d.Delay = a.Delay // the FIN or reset goes out with what it follows
			r.record(req, d, true)
		}
		pl := plan{delay: a.Delay, conn: netip.AddrPortFrom(req.Src, req.SrcPort).String() + ">" + netip.AddrPortFrom(req.Dst, req.DstPort).String()}
		for _, sg := range a.Now {
			pl.now = append(pl.now, icmp.BuildSegment(pkt, a.TTL, sg.Flags, sg.Seq, sg.Ack, sg.Data))
		}
		for _, sg := range a.Ordered {
			pl.later = append(pl.later, icmp.BuildSegment(pkt, a.TTL, sg.Flags, sg.Seq, sg.Ack, sg.Data))
		}
		return pl
	}
	d := r.Rules.DecideProbe(req)
	if d.Rule == "none" {
		return plan{} // a UDP or TCP packet to a target without a path: not ours
	}
	var answer []byte
	switch d.Outcome {
	case rule.Reply:
		answer = icmp.BuildEchoReply(pkt, d.TTL)
	case rule.Unreachable:
		answer = icmp.BuildUnreachable(pkt, d.Code, d.TTL)
	case rule.TimeExceeded:
		answer = icmp.BuildTimeExceeded(pkt, d.From, d.TTL)
	case rule.PortUnreachable:
		answer = icmp.BuildPortUnreachable(pkt, d.TTL)
	case rule.SynAck:
		answer = icmp.BuildSynAck(pkt, d.TTL)
	case rule.Refused:
		if req.Kind == icmp.UDP {
			answer = icmp.BuildPortUnreachable(pkt, d.TTL) // a UDP rule's refuse: nothing listens
		} else {
			answer = icmp.BuildRST(pkt, d.TTL)
		}
	}
	r.record(req, d, answer != nil)
	if answer == nil {
		return plan{}
	}
	return plan{later: [][]byte{answer}, delay: d.Delay}
}

// recordSent records the bytes a TCP rule sent on a connection.
func (r *Responder) recordSent(req icmp.Probe, d engine.Decision, n int) {
	r.History.Add(engine.Event{
		Time: time.Now(), Src: req.Src, Dst: req.Dst, Result: d.Outcome.String(), Rule: d.Rule, Phase: d.Phase,
		Delay: d.Delay.String(), Probe: "tcp", Port: req.DstPort, Bytes: n,
	})
	if r.Verbose {
		log.Printf("%s -> %s tcp: sent %d bytes in %s [rule %s, phase %d]", req.Src, netip.AddrPortFrom(req.Dst, req.DstPort), n, d.Delay, d.Rule, d.Phase)
	}
}

// record adds the request to the history and logs it if asked to.
func (r *Responder) record(req icmp.Probe, d engine.Decision, answered bool) {
	ev := engine.Event{
		Time: time.Now(), Src: req.Src, Dst: req.Dst, ID: req.ID, Seq: req.Seq,
		Result: d.Outcome.String(), Reply: d.Outcome == rule.Reply, Rule: d.Rule, Phase: d.Phase,
	}
	if d.Outcome == rule.Unreachable {
		ev.Code = d.Code.String()
	}
	if answered {
		ev.Delay = d.Delay.String()
	}
	if req.Kind != icmp.Echo {
		ev.Probe = req.Kind.String()
	}
	if req.Kind != icmp.Echo {
		ev.Port, ev.Repeat = req.DstPort, d.Repeat
	}
	if d.Hop > 0 || d.Rule == "path" || req.Kind != icmp.Echo {
		ev.TTL, ev.Hop = req.TTL, d.Hop
		if d.From.IsValid() {
			ev.From = d.From.String()
		}
	}
	r.History.Add(ev)
	if r.Verbose {
		what := ev.Result
		if answered {
			what += " in " + ev.Delay
		}
		if ev.Code != "" {
			what += " (" + ev.Code + ")"
		}
		if ev.Hop > 0 {
			what += fmt.Sprintf(" at hop %d (%s)", ev.Hop, ev.From)
		}
		if ev.Repeat {
			what += " (SYN sent again)"
		}
		dst := req.Dst.String()
		if req.Kind != icmp.Echo {
			dst = netip.AddrPortFrom(req.Dst, req.DstPort).String()
		}
		log.Printf("%s -> %s %s ttl=%d id=%d seq=%d: %s [rule %s, phase %d]", req.Src, dst, req.Kind, req.TTL, req.ID, req.Seq, what, d.Rule, ev.Phase)
	}
}
