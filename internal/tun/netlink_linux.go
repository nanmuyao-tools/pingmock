package tun

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// The device's addresses and routes are set over rtnetlink, the interface
// the ip command (iproute2) uses itself, so ghostwire needs no programs at
// run time. Each function sends what the ip command named in its comment
// would send.

const (
	rtTableMain  = 254 // RT_TABLE_MAIN
	rtProtBoot   = 3   // RTPROT_BOOT, what ip route uses without "proto"
	ifaFNoDAD    = 0x02
	rtaMultipath = 9 // RTA_MULTIPATH
)

var native = binary.NativeEndian

// LinkUp brings the device up: ip link set NAME up.
func LinkUp(name string) error {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return fmt.Errorf("link set %s up: %w", name, err)
	}
	body := make([]byte, syscall.SizeofIfInfomsg)
	body[0] = syscall.AF_UNSPEC
	native.PutUint32(body[4:], uint32(ifi.Index))
	native.PutUint32(body[8:], syscall.IFF_UP)  // flags
	native.PutUint32(body[12:], syscall.IFF_UP) // the flags to change
	if err := request(message(syscall.RTM_NEWLINK, 0, body)); err != nil {
		return fmt.Errorf("link set %s up: %w", name, err)
	}
	return nil
}

// AddAddr gives the device the address p, with p's network: ip addr add P
// dev DEV, and for IPv6 also nodad, so that the address is usable at once
// instead of after duplicate address detection, which is pointless on a
// private TUN device. An IPv6 address fails with EACCES where IPv6 is
// disabled on the device.
func AddAddr(dev string, p netip.Prefix) error {
	what := fmt.Sprintf("addr add %s dev %s", p, dev)
	ifi, err := net.InterfaceByName(dev)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	body := make([]byte, syscall.SizeofIfAddrmsg)
	body[0] = family(p.Addr())
	body[1] = byte(p.Bits())
	if p.Addr().Is6() {
		body[2] = ifaFNoDAD
	}
	body[3] = syscall.RT_SCOPE_UNIVERSE
	native.PutUint32(body[4:], uint32(ifi.Index))
	a := p.Addr().AsSlice()
	msg := message(syscall.RTM_NEWADDR, syscall.NLM_F_CREATE|syscall.NLM_F_EXCL, body,
		attr{syscall.IFA_LOCAL, a}, attr{syscall.IFA_ADDRESS, a})
	if err := request(msg); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// replaceRoute routes p through the device: ip route replace P dev DEV.
func replaceRoute(p netip.Prefix, dev string) error {
	what := fmt.Sprintf("route replace %s dev %s", p, dev)
	oif, err := ifindex(dev)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	// ip route uses the link scope for an IPv4 route without a gateway.
	scope := byte(syscall.RT_SCOPE_LINK)
	if p.Addr().Is6() {
		scope = syscall.RT_SCOPE_UNIVERSE
	}
	body := rtmsg(p, rtProtBoot, scope, syscall.RTN_UNICAST)
	msg := message(syscall.RTM_NEWROUTE, syscall.NLM_F_CREATE|syscall.NLM_F_REPLACE, body,
		attr{syscall.RTA_DST, p.Addr().AsSlice()}, attr{syscall.RTA_OIF, oif})
	if err := request(msg); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// deleteRoute removes the device's route for p: ip route del P dev DEV. It
// fails with ESRCH if there is none.
func deleteRoute(p netip.Prefix, dev string) error {
	what := fmt.Sprintf("route del %s dev %s", p, dev)
	oif, err := ifindex(dev)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	// RT_SCOPE_NOWHERE matches any scope, as ip route sends it; with scope
	// 0 the kernel finds no route of the link scope to delete.
	body := rtmsg(p, 0, syscall.RT_SCOPE_NOWHERE, 0)
	msg := message(syscall.RTM_DELROUTE, 0, body,
		attr{syscall.RTA_DST, p.Addr().AsSlice()}, attr{syscall.RTA_OIF, oif})
	if err := request(msg); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// mainRoutes describes the routes of the main table for exactly p, as
// ip route show exact P table main lists them; see matchRoutes.
func mainRoutes(p netip.Prefix) ([]string, error) {
	what := fmt.Sprintf("route show exact %s table main", p)
	tab, err := syscall.NetlinkRIB(syscall.RTM_GETROUTE, int(family(p.Addr())))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, os.NewSyscallError("netlink", err))
	}
	msgs, err := syscall.ParseNetlinkMessage(tab)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	routes, err := matchRoutes(msgs, p, ifname)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return routes, nil
}

// matchRoutes picks the routes of the main table for exactly p out of a
// route dump, each described by its device, such as "dev vlan20" (several,
// comma-separated, for a route with several next hops), or for a route
// without a device by its type, such as "type unreachable".
func matchRoutes(msgs []syscall.NetlinkMessage, p netip.Prefix, name func(uint32) string) ([]string, error) {
	p = p.Masked()
	var out []string
	for i := range msgs {
		m := &msgs[i]
		if m.Header.Type != syscall.RTM_NEWROUTE || len(m.Data) < syscall.SizeofRtMsg {
			continue
		}
		rt := (*syscall.RtMsg)(unsafe.Pointer(&m.Data[0]))
		if rt.Family != family(p.Addr()) || int(rt.Dst_len) != p.Bits() || rt.Flags&syscall.RTM_F_CLONED != 0 {
			continue
		}
		attrs, err := syscall.ParseNetlinkRouteAttr(m)
		if err != nil {
			return nil, err
		}
		table := uint32(rt.Table)
		dst := netip.IPv4Unspecified() // the default route has no destination
		if p.Addr().Is6() {
			dst = netip.IPv6Unspecified()
		}
		var devs []string
		for _, a := range attrs {
			switch a.Attr.Type {
			case syscall.RTA_TABLE:
				if len(a.Value) >= 4 {
					table = native.Uint32(a.Value)
				}
			case syscall.RTA_DST:
				dst, _ = netip.AddrFromSlice(a.Value)
			case syscall.RTA_OIF:
				if len(a.Value) >= 4 {
					devs = append(devs, name(native.Uint32(a.Value)))
				}
			case rtaMultipath:
				// struct rtnexthop { u16 len; u8 flags; u8 hops; s32 ifindex }
				// followed by its attributes.
				for v := a.Value; len(v) >= 8; {
					devs = append(devs, name(native.Uint32(v[4:])))
					n := int(native.Uint16(v))
					if n < 8 || align4(n) >= len(v) {
						break
					}
					v = v[align4(n):]
				}
			}
		}
		if table != rtTableMain || dst != p.Addr() {
			continue
		}
		if len(devs) > 0 {
			out = append(out, "dev "+strings.Join(devs, ","))
		} else {
			out = append(out, "type "+typeName(rt.Type))
		}
	}
	return out, nil
}

func typeName(t uint8) string {
	switch t {
	case syscall.RTN_UNICAST:
		return "unicast" // through a next hop object, which has no device here
	case syscall.RTN_LOCAL:
		return "local"
	case syscall.RTN_BLACKHOLE:
		return "blackhole"
	case syscall.RTN_UNREACHABLE:
		return "unreachable"
	case syscall.RTN_PROHIBIT:
		return "prohibit"
	case syscall.RTN_THROW:
		return "throw"
	}
	return fmt.Sprint(t)
}

func ifname(index uint32) string {
	if ifi, err := net.InterfaceByIndex(int(index)); err == nil {
		return ifi.Name
	}
	return fmt.Sprintf("if%d", index)
}

func ifindex(dev string) ([]byte, error) {
	ifi, err := net.InterfaceByName(dev)
	if err != nil {
		return nil, err
	}
	b := make([]byte, 4)
	native.PutUint32(b, uint32(ifi.Index))
	return b, nil
}

func family(a netip.Addr) byte {
	if a.Is4() {
		return syscall.AF_INET
	}
	return syscall.AF_INET6
}

// rtmsg is a struct rtmsg for p in the main table.
func rtmsg(p netip.Prefix, proto, scope, typ byte) []byte {
	b := make([]byte, syscall.SizeofRtMsg)
	b[0] = family(p.Addr())
	b[1] = byte(p.Bits())
	b[4] = rtTableMain
	b[5] = proto
	b[6] = scope
	b[7] = typ
	return b
}

type attr struct {
	typ  uint16
	data []byte
}

func align4(n int) int { return (n + 3) &^ 3 }

// message is a netlink request of type typ, asking for an acknowledgement,
// with body followed by attrs.
func message(typ, flags uint16, body []byte, attrs ...attr) []byte {
	b := make([]byte, syscall.NLMSG_HDRLEN, 128)
	b = append(b, body...)
	for _, a := range attrs {
		h := make([]byte, syscall.SizeofRtAttr)
		native.PutUint16(h, uint16(syscall.SizeofRtAttr+len(a.data)))
		native.PutUint16(h[2:], a.typ)
		b = append(append(b, h...), a.data...)
		b = append(b, make([]byte, align4(len(b))-len(b))...)
	}
	native.PutUint32(b, uint32(len(b)))
	native.PutUint16(b[4:], typ)
	native.PutUint16(b[6:], syscall.NLM_F_REQUEST|syscall.NLM_F_ACK|flags)
	native.PutUint32(b[8:], 1) // sequence number
	return b
}

// request sends msg and returns the kernel's answer: nil, or its errno.
func request(msg []byte) error {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_ROUTE)
	if err != nil {
		return os.NewSyscallError("socket", err)
	}
	defer syscall.Close(fd)
	kernel := &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}
	if err := syscall.Bind(fd, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return os.NewSyscallError("bind", err)
	}
	if err := syscall.Sendto(fd, msg, 0, kernel); err != nil {
		return os.NewSyscallError("sendto", err)
	}
	buf := make([]byte, os.Getpagesize())
	for {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return os.NewSyscallError("recvfrom", err)
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return err
		}
		for _, m := range msgs {
			if m.Header.Seq != 1 || m.Header.Type != syscall.NLMSG_ERROR || len(m.Data) < 4 {
				continue
			}
			if code := int32(native.Uint32(m.Data)); code != 0 {
				return syscall.Errno(-code)
			}
			return nil
		}
	}
}
