// Package responder reads packets from the TUN device and answers echo
// requests according to the rules, after the decided delay.
package responder

import (
	"errors"
	"io"
	"log"
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
// the packet was not an echo request, or the rule drops it.
func (r *Responder) handle(pkt []byte) ([]byte, time.Duration) {
	req, ok := icmp.ParseEchoRequest(pkt)
	if !ok {
		return nil, 0
	}
	d := r.Rules.Decide(req.Dst)
	var answer []byte
	switch d.Outcome {
	case rule.Reply:
		answer = icmp.BuildEchoReply(pkt)
	case rule.Unreachable:
		answer = icmp.BuildUnreachable(pkt, d.Code)
	}
	r.record(req, d, answer != nil)
	return answer, d.Delay
}

// record adds the request to the history and logs it if asked to.
func (r *Responder) record(req icmp.EchoRequest, d engine.Decision, answered bool) {
	ev := engine.Event{
		Time: time.Now(), Src: req.Src.String(), Dst: req.Dst.String(), ID: req.ID, Seq: req.Seq,
		Result: d.Outcome.String(), Reply: d.Outcome == rule.Reply, Rule: d.Rule, Phase: d.Phase + 1,
	}
	if d.Outcome == rule.Unreachable {
		ev.Code = d.Code.String()
	}
	if answered {
		ev.Delay = d.Delay.String()
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
		log.Printf("%s -> %s id=%d seq=%d: %s [rule %s, phase %d]", req.Src, req.Dst, req.ID, req.Seq, what, d.Rule, ev.Phase)
	}
}
