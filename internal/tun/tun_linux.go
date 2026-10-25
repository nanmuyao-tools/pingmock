// Package tun creates the TUN device that receives the mocked traffic and
// manages its addresses and routes.
package tun

import (
	"fmt"
	"log"
	"net/netip"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

const (
	iffTUN    = 0x0001
	iffNoPI   = 0x1000
	tunSetIFF = 0x400454ca
)

// ifreq mirrors the kernel's struct ifreq for the TUNSETIFF ioctl.
type ifreq struct {
	name  [16]byte
	flags uint16
	_     [22]byte
}

// Open creates a non-persistent TUN device. The device disappears
// (together with its addresses and routes) when the returned file is closed
// or the process exits.
func Open(name string) (*os.File, string, error) {
	fd, err := syscall.Open("/dev/net/tun", syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", fmt.Errorf("open /dev/net/tun: %w", err)
	}
	var req ifreq
	copy(req.name[:], name)
	req.flags = iffTUN | iffNoPI
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), tunSetIFF, uintptr(unsafe.Pointer(&req))); errno != 0 {
		syscall.Close(fd)
		return nil, "", fmt.Errorf("TUNSETIFF: %w (are you root / CAP_NET_ADMIN?)", errno)
	}
	// Non-blocking so the Go runtime poller can interrupt Read on Close.
	if err := syscall.SetNonblock(fd, true); err != nil {
		syscall.Close(fd)
		return nil, "", err
	}
	actual := strings.TrimRight(string(req.name[:]), "\x00")
	return os.NewFile(uintptr(fd), "/dev/net/tun"), actual, nil
}

// Router adds routes for targets outside the device subnets, so that real
// addresses can be mocked too, and removes them when no rule needs them
// any more. Routes vanish with the device on exit.
type Router struct {
	mu      sync.Mutex
	dev     string
	subnets []netip.Prefix
	routed  map[netip.Prefix][]netip.Prefix // each target's routes, as planRoutes made them
}

// NewRouter returns a router for targets of the device dev, whose own
// subnets (at most one per address family) are already routed to it.
func NewRouter(dev string, subnets ...netip.Prefix) *Router {
	r := &Router{dev: dev, routed: map[netip.Prefix][]netip.Prefix{}}
	for _, s := range subnets {
		r.subnets = append(r.subnets, s.Masked())
	}
	return r
}

// Ensure routes p through the device unless it is already. It fails for an
// IPv6 target when the device has no IPv6 subnet.
func (r *Router) Ensure(p netip.Prefix) error {
	p = p.Masked()
	family := false
	for _, s := range r.subnets {
		if s.Addr().Is4() != p.Addr().Is4() {
			continue
		}
		family = true
		if s.Contains(p.Addr()) && p.Bits() >= s.Bits() {
			return nil // already routed through the device subnet
		}
	}
	if !family {
		return fmt.Errorf("cannot mock %s: IPv6 is not enabled on %s (see -addr6)", p, r.dev)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.routed[p]; ok {
		return nil
	}
	plan, kept, err := planRoutes(p, r.existing)
	if err != nil {
		return err
	}
	for _, q := range plan {
		if err := replaceRoute(q, r.dev); err != nil {
			return err
		}
	}
	r.routed[p] = plan
	if len(kept) == 0 {
		log.Printf("routing %s through %s (hijacked: real traffic to it is mocked while a rule needs it)", p, r.dev)
	} else {
		parts := make([]string, len(plan))
		for i, q := range plan {
			parts[i] = q.String()
		}
		log.Printf("routing %s through %s as %s, leaving the host's %s in place (hijacked: real traffic to it is mocked while a rule needs it)",
			p, r.dev, strings.Join(parts, " and "), strings.Join(kept, " and "))
	}
	return nil
}

// Sync routes every prefix of want through the device, and removes the
// routes it added for any other prefix, so that real traffic to a target
// no rule mocks any more goes where it did before. It only ever removes
// its own routes: the host's are never replaced in the first place, and a
// route two targets share stays while one of them needs it.
func (r *Router) Sync(want []netip.Prefix) error {
	for _, p := range want {
		if err := r.Ensure(p); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	targets, routes := unneeded(r.routed, want)
	var first error
	for _, q := range routes {
		if err := deleteRoute(q, r.dev); err != nil && first == nil {
			first = err
		}
	}
	for _, p := range targets {
		delete(r.routed, p)
		log.Printf("no longer routing %s through %s: no rule needs it", p, r.dev)
	}
	return first
}

// Routes returns the targets routed through the device besides its own
// subnets, sorted.
func (r *Router) Routes() []netip.Prefix {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]netip.Prefix, 0, len(r.routed))
	for p := range r.routed {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// existing describes the routes of the main table for exactly p that do not
// go through the device; see existingRoutes.
func (r *Router) existing(p netip.Prefix) ([]string, error) {
	routes, err := mainRoutes(p)
	if err != nil {
		return nil, err
	}
	var others []string
	for _, route := range routes {
		if route != "dev "+r.dev {
			others = append(others, route)
		}
	}
	return others, nil
}
