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
	buf := make([]byte, 65536)
	for {
		n, err := dev.Read(buf)
		if err != nil {
			if errors.Is(err, os.ErrClosed) || errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		answer, delay := r.handle(buf[:n])
		if answer == nil {
			continue
		}
		time.AfterFunc(delay, func() {
			wmu.Lock()
			defer wmu.Unlock()
			if _, err := dev.Write(answer); err != nil && !errors.Is(err, os.ErrClosed) {
				log.Printf("write: %v", err)
			}
		})
	}
}

// handle decides how to answer one packet and records the request. It
// returns the packet to send and when, or nil if there is nothing to send:
// the packet was not a probe, the target has no path and it was not an
// echo request, or the rule drops it.
func (r *Responder) handle(pkt []byte) ([]byte, time.Duration) {
	req, ok := icmp.ParseProbe(pkt)
	if !ok {
		return nil, 0
	}
	d := r.Rules.DecideProbe(req)
	if d.Rule == "none" {
		return nil, 0 // a UDP or TCP packet to a target without a path: not ours
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
		answer = icmp.BuildRST(pkt, d.TTL)
	}
	r.record(req, d, answer != nil)
	return answer, d.Delay
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
	if req.Kind == icmp.TCP {
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
