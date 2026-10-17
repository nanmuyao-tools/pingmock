package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
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

// openTUN creates a non-persistent TUN device. The device disappears
// (together with its addresses and routes) when the returned file is closed
// or the process exits.
func openTUN(name string) (*os.File, string, error) {
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

// ipCmd runs an iproute2 command.
func ipCmd(args ...string) error {
	out, err := exec.Command("ip", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
