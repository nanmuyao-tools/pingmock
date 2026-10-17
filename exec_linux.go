package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/nanmuyao/pingmock/internal/tun"
)

// execChildArg marks the re-executed pingmock that runs inside the new
// namespaces. It is not meant to be typed by users.
const execChildArg = "__pingmock-exec-child"

// envRootless tells the child that it runs in a user namespace created by
// an unprivileged user, so errors can explain what to do about it.
const envRootless = "PINGMOCK_EXEC_ROOTLESS"

// envAPI is set in the environment of the command run by pingmock exec.
const envAPI = "PINGMOCK_API"

func execUsage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprint(w, `Usage: pingmock exec [flags] -- CMD [ARGS...]

Runs CMD in a private network namespace with pingmock answering pings
there. Root is not needed: as an unprivileged user, pingmock creates a user
namespace too. The host network is not touched, several runs can happen at
once, and pingmock exits with CMD's exit status. When CMD exits, any
processes it left running are stopped.

CMD can reach the control API at the address in $PINGMOCK_API. It has no
network access other than the mocked targets.

Examples:
  pingmock exec -rule 10.99.0.1=50ms -- ping -c 3 10.99.0.1
  pingmock exec -seed 42 -rule 8.8.8.8=120ms -- ./my-app
  go test -exec 'pingmock exec -config pingmock.json --' ./...

Flags:
`)
	fs.PrintDefaults()
}

// runExec starts a copy of pingmock in new namespaces, which then runs the
// command. It returns the command's exit status.
func runExec(args []string) int {
	_, cmdArgs, err := parseSettings("pingmock exec", args, execUsage, os.Stderr)
	if isHelp(err) {
		return 0
	}
	if err != nil {
		log.Print(err)
		return 2
	}
	if len(cmdArgs) == 0 {
		log.Print("pingmock exec: no command given; usage: pingmock exec [flags] -- CMD [ARGS...]")
		return 2
	}
	self, err := os.Executable()
	if err != nil {
		log.Printf("pingmock exec: %v", err)
		return 1
	}
	child := exec.Command(self, append([]string{execChildArg}, args...)...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	child.Env = os.Environ()
	// A PID namespace makes the child its init process: when it exits, the
	// kernel kills every process left in the namespace, so nothing the
	// command started in the background outlives the run. Pdeathsig does
	// the same if pingmock exec itself is killed.
	attr := &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNET | syscall.CLONE_NEWPID, Pdeathsig: syscall.SIGKILL}
	rootless := os.Geteuid() != 0
	if rootless {
		attr.Cloneflags |= syscall.CLONE_NEWUSER
		attr.UidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Geteuid(), Size: 1}}
		attr.GidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getegid(), Size: 1}}
		attr.GidMappingsEnableSetgroups = false
		child.Env = append(child.Env, envRootless+"=1")
	}
	child.SysProcAttr = attr
	if err := child.Start(); err != nil {
		if rootless {
			log.Printf("pingmock exec: cannot create a user namespace: %v\n"+
				"Unprivileged user namespaces seem to be disabled on this system. Run pingmock exec with sudo,\n"+
				"or enable them (for example sysctl kernel.unprivileged_userns_clone=1 or user.max_user_namespaces).", err)
		} else {
			log.Printf("pingmock exec: cannot create a network namespace: %v", err)
		}
		return 1
	}
	stop := forwardSignals(child.Process)
	defer stop()
	return exitStatus(child.Wait())
}

// runExecChild runs inside the new namespaces: it starts pingmock, runs the
// command, and cleans up when the command exits.
func runExecChild(args []string) int {
	log.SetPrefix("[pingmock] ")
	rootless := os.Getenv(envRootless) == "1"
	os.Unsetenv(envRootless)
	s, cmdArgs, err := parseSettings("pingmock exec", args, execUsage, io.Discard)
	if err != nil {
		log.Print(err)
		return 2
	}
	// The new namespace starts with loopback down; the API listens on it.
	if err := tun.IP("link", "set", "lo", "up"); err != nil {
		log.Print(explain(err, rootless))
		return 1
	}
	in, err := start(s)
	if err != nil {
		log.Print(explain(err, rootless))
		return 1
	}
	defer in.Close()

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = os.Environ()
	if in.apiURL != "" {
		cmd.Env = append(cmd.Env, envAPI+"="+in.apiURL)
	}
	if err := cmd.Start(); err != nil {
		log.Print(err)
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return 127
		}
		return 126
	}
	stop := forwardSignals(cmd.Process)
	defer stop()
	go func() {
		if err := <-in.Done(); err != nil {
			log.Printf("read: %v; pings are no longer answered", err)
		}
	}()
	return exitStatus(cmd.Wait())
}

// forwardSignals passes termination signals on to p until stop is called.
// SIGINT is not forwarded: when it comes from the terminal, the command,
// which is in the same process group, already receives it, and a second
// delivery makes some programs quit without cleaning up.
func forwardSignals(p *os.Process) (stop func()) {
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-sigs:
				if sig != syscall.SIGINT {
					p.Signal(sig)
				}
			case <-done:
				return
			}
		}
	}()
	return func() { signal.Stop(sigs); close(done) }
}

// exitStatus turns the result of Wait into an exit status, using the shell
// convention 128+N for a process killed by signal N.
func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	log.Print(err)
	return 1
}

// explain adds advice to errors that unprivileged users commonly hit.
func explain(err error, rootless bool) error {
	if !rootless {
		return err
	}
	msg := err.Error()
	switch {
	case errors.Is(err, syscall.EACCES) && strings.Contains(msg, "/dev/net/tun"):
		return fmt.Errorf("%w\n/dev/net/tun cannot be opened without root on this system. Most distributions make it\n"+
			"readable and writable by everyone (crw-rw-rw-); run pingmock exec with sudo, or: sudo chmod 0666 /dev/net/tun", err)
	case errors.Is(err, syscall.EPERM) || strings.Contains(msg, "Operation not permitted"):
		return fmt.Errorf("%w\nThe user namespace has no network privileges. On Ubuntu 23.10 and later, AppArmor restricts\n"+
			"unprivileged user namespaces; run pingmock exec with sudo, or allow them with\n"+
			"sudo sysctl kernel.apparmor_restrict_unprivileged_userns=0", err)
	}
	return err
}
