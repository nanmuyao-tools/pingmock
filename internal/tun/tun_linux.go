// Package tun creates the TUN device that receives the mocked traffic and
// manages its addresses and routes.
package tun

import (
	"fmt"
	"log"
	"net/netip"
	"os"
	"os/exec"
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

// IP runs an iproute2 command.
func IP(args ...string) error {
	out, err := exec.Command("ip", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Router adds routes for targets outside the device subnets, so that real
// addresses can be mocked too. Routes vanish with the device on exit.
type Router struct {
	mu      sync.Mutex
	dev     string
	subnets []netip.Prefix
	routed  map[netip.Prefix]bool
}

// NewRouter returns a router for targets of the device dev, whose own
// subnets (at most one per address family) are already routed to it.
func NewRouter(dev string, subnets ...netip.Prefix) *Router {
	r := &Router{dev: dev, routed: map[netip.Prefix]bool{}}
	for _, s := range subnets {
		r.subnets = append(r.subnets, s.Masked())
	}
	return r
}

// Ensure routes p through the device unless it is already. It fails for an
// IPv6 target when the device has no IPv6 subnet.
func (r *Router) Ensure(p netip.Prefix) error {
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
	if r.routed[p] {
		return nil
	}
	if err := IP("route", "replace", p.String(), "dev", r.dev); err != nil {
		return err
	}
	r.routed[p] = true
	log.Printf("routing %s through %s (hijacked: real traffic to it is mocked until exit)", p, r.dev)
	return nil
}
