package app

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/nanmuyao/pingmock/internal/config"
)

// The files name resolution reads, on every system pingmock runs on.
const (
	hostsFile    = "/etc/hosts"
	resolvFile   = "/etc/resolv.conf"
	nsswitchFile = "/etc/nsswitch.conf"
)

// privateMounts makes the mounts of the new mount namespace private, so
// that what is mounted here does not propagate back to the host; unshare
// leaves them shared on most systems. It runs once, before the files
// below are replaced.
func privateMounts() error {
	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("making mounts private: %w", err)
	}
	return nil
}

// mountOver replaces path, for this mount namespace only, with a file
// holding content. The replacement is a temporary file unlinked once
// mounted: the mount keeps it alive, readable by everyone, and nothing is
// left behind. Mounting follows symlinks, so a path that is one (as
// /etc/resolv.conf often is) gets its target replaced.
func mountOver(path string, content []byte) error {
	f, err := os.CreateTemp("", "pingmock-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	f.Close()
	if err := syscall.Mount(tmp, path, "", syscall.MS_BIND, ""); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s does not exist, so there is nothing to replace", path)
		}
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// mountHosts gives the command its own /etc/hosts: the system's, with the
// given names on top so that they win over any existing entry.
func mountHosts(hosts []config.Host) error {
	system, err := os.ReadFile(hostsFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("-host: %w", err)
	}
	var b strings.Builder
	b.WriteString("# Added by pingmock exec for this command only.\n")
	for _, h := range hosts {
		fmt.Fprintf(&b, "%s\t%s\n", h.Addr, h.Name)
	}
	b.WriteString("\n")
	b.Write(system)
	if err := mountOver(hostsFile, []byte(b.String())); err != nil {
		return fmt.Errorf("-host: %w", err)
	}
	return nil
}

// mountResolvConf points the command's resolver at pingmock's DNS server:
// the system's resolv.conf keeps its search domains and options, and
// server becomes its only nameserver.
func mountResolvConf(server string) error {
	system, _ := os.ReadFile(resolvFile) // follows symlinks; missing is fine
	var b strings.Builder
	b.WriteString("# Written by pingmock exec for this command only: pingmock's DNS server,\n# which answers for the mocked names and forwards the rest to the host's resolver.\n")
	for _, line := range strings.Split(string(system), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] == "nameserver" || strings.HasPrefix(f[0], "#") || strings.HasPrefix(f[0], ";") {
			continue
		}
		b.WriteString(line + "\n")
	}
	fmt.Fprintf(&b, "nameserver %s\n", server)
	if err := mountOver(resolvFile, []byte(b.String())); err != nil {
		return fmt.Errorf("DNS: %w", err)
	}
	return nil
}

// mountNsswitch keeps the C library from asking systemd-resolved directly
// (the "resolve" source talks to it over a socket that crosses network
// namespaces, so it would answer from the host's network and bypass
// pingmock's DNS): if nsswitch.conf names it for hosts, the command gets
// a copy that resolves hosts with files and DNS only.
func mountNsswitch() error {
	system, err := os.ReadFile(nsswitchFile)
	if err != nil {
		return nil // no file, no resolve source
	}
	lines := strings.Split(string(system), "\n")
	changed := false
	for i, line := range lines {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "hosts:" {
			continue
		}
		for _, source := range f[1:] {
			if source == "resolve" {
				lines[i] = "hosts: files dns   # pingmock exec: was: " + strings.TrimSpace(line)
				changed = true
				break
			}
		}
	}
	if !changed {
		return nil
	}
	if err := mountOver(nsswitchFile, []byte(strings.Join(lines, "\n"))); err != nil {
		return fmt.Errorf("DNS: %w", err)
	}
	return nil
}
