package app

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/nanmuyao/pingmock/internal/config"
)

// hostsFile is what name resolution reads first, on every system pingmock
// runs on.
const hostsFile = "/etc/hosts"

// mountHosts gives the command its own /etc/hosts: the system's, with the
// given names on top so that they win over any existing entry. It runs in
// the mount namespace created for the command, so the system's file is not
// touched and nothing is left behind: the replacement is an unlinked file
// that lives only as long as the mount.
func mountHosts(hosts []config.Host) error {
	// Mounts in the new namespace must not propagate back to the host's;
	// unshare leaves them shared on most systems.
	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("making mounts private for -host: %w", err)
	}
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

	// The replacement goes in a temporary file that is unlinked once
	// mounted; the mount keeps it alive and readable by everyone.
	f, err := os.CreateTemp("", "pingmock-hosts-")
	if err != nil {
		return fmt.Errorf("-host: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		return fmt.Errorf("-host: %w", err)
	}
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return fmt.Errorf("-host: %w", err)
	}
	f.Close()
	if err := syscall.Mount(tmp, hostsFile, "", syscall.MS_BIND, ""); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("-host: %s does not exist, so there is nothing to replace", hostsFile)
		}
		return fmt.Errorf("-host: replacing %s: %w", hostsFile, err)
	}
	return nil
}
