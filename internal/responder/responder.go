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
// dev is closed. Replies are written to dev, each after its delay.
func (r *Responder) Serve(dev io.ReadWriter) error {
	var wmu sync.Mutex
	buf := make([]byte, 65536)
	for {
		n, err := dev.Read(buf)
		if err != nil {
			if errors.Is(err, os.ErrClosed) || errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		pkt := buf[:n]
		req, ok := icmp.ParseEchoRequest(pkt)
		if !ok {
			continue
		}
		d := r.Rules.Decide(req.Dst)
		ev := engine.Event{
			Time: time.Now(), Src: req.Src.String(), Dst: req.Dst.String(), ID: req.ID, Seq: req.Seq,
			Result: d.Outcome.String(), Reply: d.Outcome == rule.Reply, Rule: d.Rule, Phase: d.Phase + 1,
		}
		var out []byte
		switch d.Outcome {
		case rule.Reply:
			out = icmp.BuildEchoReply(pkt)
		case rule.Unreachable:
			out = icmp.BuildUnreachable(pkt, d.Code)
			ev.Code = d.Code.String()
		}
		if out != nil {
			ev.Delay = d.Delay.String()
		}
		r.History.Add(ev)
		if r.Verbose {
			what := d.Outcome.String()
			if out != nil {
				what += " in " + d.Delay.String()
			}
			if ev.Code != "" {
				what += " (" + ev.Code + ")"
			}
			log.Printf("%s -> %s id=%d seq=%d: %s [rule %s, phase %d]", req.Src, req.Dst, req.ID, req.Seq, what, d.Rule, d.Phase+1)
		}
		if out == nil {
			continue
		}
		time.AfterFunc(d.Delay, func() {
			wmu.Lock()
			defer wmu.Unlock()
			if _, err := dev.Write(out); err != nil && !errors.Is(err, os.ErrClosed) {
				log.Printf("write: %v", err)
			}
		})
	}
}
