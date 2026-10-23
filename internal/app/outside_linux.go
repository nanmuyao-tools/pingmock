package app

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Outside access: with -outside, pingmock exec gives the command's network
// a way out through pasta (https://passt.top), which the parent attaches to
// the child's namespaces. Mocked targets keep winning, because pingmock's
// routes for them are more specific than pasta's default route, and the
// child adds them only once pasta is done.

// outsideFD is the file descriptor on which the child waits for pasta: the
// second extra file the parent passes, after relayFD, since outside access
// always comes with the command's own DNS server.
const outsideFD = 4

// The command's address and gateway in pasta's network. They are fixed, so
// that the command's network looks the same on every host, rather than a
// copy of the host's address, pasta's default, which would make the host's
// own address local to the command.
const (
	outsideAddr    = "10.0.2.15"
	outsidePrefix  = "24"
	outsideGateway = "10.0.2.2"
)

// pastaArgs are the arguments that attach pasta to the namespaces of pid.
// Pasta's defaults would forward every port the command listens on to the
// host, the control API included, and bind ports there that other runs
// need, so forwarding is off except for ports: the host loopback ports the
// command reaches, such as "5432/tcp". As root, pasta would drop to nobody,
// who cannot enter the namespaces; --runas 0 keeps it root, since the
// command's user namespace belongs to root.
func pastaArgs(ports []string, root bool, pid int, pidFile string) []string {
	var tcp, udp []string
	for _, p := range ports {
		port, proto, _ := strings.Cut(p, "/")
		if proto == "udp" {
			udp = append(udp, port)
		} else {
			tcp = append(tcp, port)
		}
	}
	list := func(ports []string) string {
		if len(ports) == 0 {
			return "none"
		}
		return strings.Join(ports, ",")
	}
	args := []string{"--config-net", "--quiet", "--ipv4-only",
		"-t", "none", "-u", "none", "-T", list(tcp), "-U", list(udp), "--no-map-gw",
		"-a", outsideAddr, "-n", outsidePrefix, "-g", outsideGateway, "-P", pidFile}
	if root {
		args = append(args, "--runas", "0")
	}
	return append(args, strconv.Itoa(pid))
}

// attachPasta runs pasta for the child pid and returns once the child's
// network has its default route. Pasta then runs in the background until
// stop is called: recent versions quit when the namespaces are gone, but
// older ones, such as Debian 12's, keep running.
func attachPasta(pasta string, ports []string, root bool, pid int) (stop func(), err error) {
	f, err := os.CreateTemp("", "pingmock-pasta-*.pid")
	if err != nil {
		return nil, err
	}
	pidFile := f.Name()
	f.Close()
	out, err := exec.Command(pasta, pastaArgs(ports, root, pid, pidFile)...).CombinedOutput()
	if err != nil {
		os.Remove(pidFile)
		return nil, fmt.Errorf("pasta, for -outside: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return func() {
		defer os.Remove(pidFile)
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		if p, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && p > 0 {
			syscall.Kill(p, syscall.SIGTERM)
		}
	}, nil
}

// allIDs maps every user and group to itself. As root, the command gets a
// user namespace with it, so that pasta can enter the namespaces as root
// does rootless: the command still runs as the same users, but with
// privileges over its own namespaces only.
func allIDs() []syscall.SysProcIDMap {
	size := int(min(uint64(^uint(0)>>1), 1<<32-1)) // every ID but -1, which is invalid
	return []syscall.SysProcIDMap{{ContainerID: 0, HostID: 0, Size: size}}
}

// apiFacts are what GET /rules reports of the outside access.
func (s *settings) apiFacts() map[string]any {
	if !s.outside {
		return nil
	}
	ports := s.outsidePorts
	if ports == nil {
		ports = []string{}
	}
	return map[string]any{"outside": map[string]any{
		"address": outsideAddr + "/" + outsidePrefix, "gateway": outsideGateway, "ports": ports}}
}

// waitOutside blocks the child until the parent has attached pasta, and
// fails if the parent could not.
func waitOutside(ports []string) error {
	f := os.NewFile(outsideFD, "outside")
	if f == nil {
		return fmt.Errorf("outside access: the pipe from pingmock exec is missing")
	}
	defer f.Close()
	var b [1]byte
	if _, err := io.ReadFull(f, b[:]); err != nil {
		return fmt.Errorf("outside access was not set up")
	}
	hostPorts := "none"
	if len(ports) > 0 {
		hostPorts = strings.Join(ports, ", ")
	}
	log.Printf("outside access through pasta: address %s/%s, gateway %s; host loopback ports: %s",
		outsideAddr, outsidePrefix, outsideGateway, hostPorts)
	return nil
}
