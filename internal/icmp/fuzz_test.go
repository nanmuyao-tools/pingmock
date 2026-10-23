package icmp

import (
	"net/netip"
	"testing"
)

// ParseProbe reads whatever arrives on the device: it must never panic,
// and the answers built from a probe it accepts must not panic either.
func FuzzParseProbe(f *testing.F) {
	c, t4 := netip.MustParseAddr("10.99.0.254"), netip.MustParseAddr("10.99.0.1")
	f.Add(BuildEchoRequest(c, t4, 1, 1, []byte("payload")))
	f.Add(BuildEchoRequest(client6, target6, 1, 1, nil))
	f.Add(buildProbe(TCP, c, t4, 64, 40000, 80, 1000))
	f.Add(buildProbe(UDP, client6, target6, 1, 40000, 33434, 0))
	f.Fuzz(func(t *testing.T, pkt []byte) {
		p, ok := ParseProbe(pkt)
		if !ok {
			return
		}
		switch p.Kind {
		case Echo:
			BuildEchoReply(pkt, 64)
		case TCP:
			BuildSynAck(pkt, 64)
			BuildRST(pkt, 64)
		case Segment:
			BuildSegment(pkt, 64, FlagACK, SynAckSeq+1, p.TCPSeq, []byte("x"))
		}
		BuildTimeExceeded(pkt, t4, 64)
		BuildPortUnreachable(pkt, 64)
	})
}
