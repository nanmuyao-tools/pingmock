package tun

import (
	"fmt"
	"net/netip"
	"reflect"
	"syscall"
	"testing"
)

// route builds one message of a route dump, as the kernel sends it.
func route(dst string, typ, table uint8, flags uint32, attrs ...attr) syscall.NetlinkMessage {
	p := netip.MustParsePrefix(dst)
	body := rtmsg(p, rtProtBoot, syscall.RT_SCOPE_UNIVERSE, typ)
	body[4] = table
	native.PutUint32(body[8:], flags)
	if p.Bits() > 0 {
		attrs = append([]attr{{syscall.RTA_DST, p.Addr().AsSlice()}}, attrs...)
	}
	b := message(syscall.RTM_NEWROUTE, 0, body, attrs...)
	msgs, err := syscall.ParseNetlinkMessage(b)
	if err != nil || len(msgs) != 1 {
		panic(fmt.Sprint(msgs, err))
	}
	return msgs[0]
}

func u32(v uint32) []byte {
	b := make([]byte, 4)
	native.PutUint32(b, v)
	return b
}

func TestMatchRoutes(t *testing.T) {
	name := func(i uint32) string { return map[uint32]string{2: "eth0", 3: "pingmock0", 4: "vlan20"}[i] }
	// struct rtnexthop for devices 2 and 4, each 8 bytes with no attributes.
	multipath := []byte{8, 0, 0, 0, 2, 0, 0, 0, 8, 0, 0, 0, 4, 0, 0, 0}
	dump := []syscall.NetlinkMessage{
		route("0.0.0.0/0", syscall.RTN_UNICAST, rtTableMain, 0, attr{syscall.RTA_OIF, u32(2)}),
		route("192.168.20.0/24", syscall.RTN_UNICAST, rtTableMain, 0, attr{syscall.RTA_OIF, u32(4)}),
		route("192.168.20.0/24", syscall.RTN_UNICAST, rtTableMain, 0, attr{syscall.RTA_OIF, u32(3)}),
		route("192.168.20.0/25", syscall.RTN_UNICAST, rtTableMain, 0, attr{syscall.RTA_OIF, u32(3)}),
		route("192.168.30.0/24", syscall.RTN_UNICAST, rtTableMain, 0, attr{syscall.RTA_OIF, u32(4)}),
		route("192.168.20.0/24", syscall.RTN_UNICAST, 100, 0, attr{syscall.RTA_OIF, u32(2)}),
		route("192.168.20.0/24", syscall.RTN_UNICAST, 252, 0, attr{syscall.RTA_TABLE, u32(1000)}, attr{syscall.RTA_OIF, u32(2)}),
		route("172.31.0.0/16", syscall.RTN_UNREACHABLE, rtTableMain, 0),
		route("10.1.0.0/16", syscall.RTN_UNICAST, rtTableMain, 0, attr{rtaMultipath, multipath}),
		route("8.8.8.8/32", syscall.RTN_UNICAST, rtTableMain, syscall.RTM_F_CLONED, attr{syscall.RTA_OIF, u32(2)}),
		route("fd77::/64", syscall.RTN_UNICAST, rtTableMain, 0, attr{syscall.RTA_OIF, u32(4)}),
	}
	cases := map[string][]string{
		"0.0.0.0/0":       {"dev eth0"},
		"192.168.20.0/24": {"dev vlan20", "dev pingmock0"},
		"192.168.20.1/24": {"dev vlan20", "dev pingmock0"},
		"192.168.20.0/25": {"dev pingmock0"},
		"192.168.20.0/23": nil,
		"172.31.0.0/16":   {"type unreachable"},
		"10.1.0.0/16":     {"dev eth0,vlan20"},
		"8.8.8.8/32":      nil, // a cached route, not one of the table
		"fd77::/64":       {"dev vlan20"},
		"::/0":            nil,
	}
	for p, want := range cases {
		got, err := matchRoutes(dump, netip.MustParsePrefix(p), name)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("matchRoutes(%s) = %q, %v; want %q", p, got, err, want)
		}
	}
}

func TestMessage(t *testing.T) {
	p := netip.MustParsePrefix("8.8.8.8/32")
	b := message(syscall.RTM_NEWROUTE, syscall.NLM_F_CREATE|syscall.NLM_F_REPLACE, rtmsg(p, rtProtBoot, syscall.RT_SCOPE_LINK, syscall.RTN_UNICAST),
		attr{syscall.RTA_DST, p.Addr().AsSlice()}, attr{syscall.RTA_OIF, u32(7)})
	msgs, err := syscall.ParseNetlinkMessage(b)
	if err != nil || len(msgs) != 1 {
		t.Fatal(msgs, err)
	}
	h := msgs[0].Header
	if int(h.Len) != len(b) || h.Type != syscall.RTM_NEWROUTE || h.Flags != syscall.NLM_F_REQUEST|syscall.NLM_F_ACK|syscall.NLM_F_CREATE|syscall.NLM_F_REPLACE {
		t.Errorf("header %+v", h)
	}
	if got := msgs[0].Data[:8]; !reflect.DeepEqual(got, []byte{syscall.AF_INET, 32, 0, 0, rtTableMain, rtProtBoot, syscall.RT_SCOPE_LINK, syscall.RTN_UNICAST}) {
		t.Errorf("rtmsg %v", got)
	}
	attrs, err := syscall.ParseNetlinkRouteAttr(&msgs[0])
	if err != nil || len(attrs) != 2 || attrs[0].Attr.Type != syscall.RTA_DST || netip.AddrFrom4([4]byte(attrs[0].Value)) != p.Addr() ||
		attrs[1].Attr.Type != syscall.RTA_OIF || native.Uint32(attrs[1].Value) != 7 {
		t.Errorf("attributes %+v, %v", attrs, err)
	}
}
