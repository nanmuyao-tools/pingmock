package app

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
	"time"

	"github.com/nanmuyao/pingmock/internal/dns"
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

Runs CMD in a private network namespace with pingmock answering pings,
TCP connections and UDP datagrams there, and DNS queries for names with
a -dns rule.
Root is not needed: as an unprivileged user, pingmock creates a user
namespace too. The host network is not touched, several runs can happen
at once, and pingmock exits with CMD's exit status, or 125 if it fails
itself. When CMD exits, any processes it left running are stopped.

CMD can reach the control API at the address in $PINGMOCK_API. It has no
network access other than the mocked targets, unless -outside gives it the
host's network through pasta (package passt): real addresses and names then
work, mocked ones keep their rules, and -outside-port makes 127.0.0.1:PORT
reach the host's, for a database published there. A rule may be written for a
name: CMD then resolves it to a mocked address, through a private
/etc/hosts and a DNS server that forwards other names to the host's
resolver; the system's files are not touched. -host picks the address.
-dns makes the queries for a name fail or come late, for mocked and real
names alike.

Examples:
  pingmock exec -rule 10.99.0.1=50ms -- ping -c 3 10.99.0.1
  pingmock exec -seed 42 -rule 8.8.8.8=120ms -- ./my-app
  pingmock exec -rule api.example.com=80ms -rule db.example.com=timeout -- ./my-app
  pingmock exec -host api.example.com=10.99.0.1 -rule 10.99.0.1=80ms -- ./my-app
  pingmock exec -tcp db.example.com:5432=timeout -tcp 10.99.0.2:5432=5ms -- ./my-app
  pingmock exec -tcp 10.99.0.3:443=after=reset -dns api.example.com=nxdomain -- ./my-app
  pingmock exec -outside -outside-port 5432 -rule api.example.com=timeout -- go test ./...
  go test -exec 'pingmock exec -config pingmock.yaml --' ./...

Flags:
`)
	fs.PrintDefaults()
}

// execFailed is the exit status of pingmock exec when it fails itself,
// before or around the command: bad flags or rules, or a network that
// cannot be set up. As for docker run, env and timeout, it is 125, so
// that it is not taken for the command's own status; 126 and 127 say the
// command could not be run or found.
const execFailed = 125

// runExec starts a copy of pingmock in new namespaces, which then runs the
// command. It returns the command's exit status.
func runExec(args []string) int {
	s, cmdArgs, err := parseSettings("pingmock exec", args, execUsage, os.Stderr)
	if isHelp(err) {
		return 0
	}
	if err != nil {
		log.Print(err)
		return execFailed
	}
	if len(cmdArgs) == 0 {
		log.Print("pingmock exec: no command given; usage: pingmock exec [flags] -- CMD [ARGS...]")
		return execFailed
	}
	self, err := os.Executable()
	if err != nil {
		log.Printf("pingmock exec: %v", err)
		return execFailed
	}
	var pasta string
	if s.outside {
		if pasta, err = exec.LookPath("pasta"); err != nil {
			log.Print("pingmock exec: -outside needs pasta, from the package passt, which is not in $PATH")
			return execFailed
		}
	}
	child := exec.Command(self, append([]string{execChildArg}, args...)...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	child.Env = os.Environ()
	// A PID namespace makes the child its init process: when it exits, the
	// kernel kills every process left in the namespace, so nothing the
	// command started in the background outlives the run. Pdeathsig does
	// the same if pingmock exec itself is killed.
	attr := &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNET | syscall.CLONE_NEWPID, Pdeathsig: syscall.SIGKILL}
	var childEnd *os.File
	if s.names() {
		// A mount namespace, for the command's own /etc/hosts and
		// resolv.conf, and a socket pair on which this process, which keeps
		// the host's network, resolves the names the child's DNS server
		// does not know.
		attr.Cloneflags |= syscall.CLONE_NEWNS
		fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
		if err != nil {
			log.Printf("pingmock exec: %v", err)
			return execFailed
		}
		parentEnd := os.NewFile(uintptr(fds[0]), "dns-relay")
		childEnd = os.NewFile(uintptr(fds[1]), "dns-relay")
		child.ExtraFiles = []*os.File{childEnd} // relayFD in the child
		// The C library's default timeout: a slower host resolver times
		// out here as it would for the command without pingmock.
		forward := dns.Forwarder(dns.Nameservers(resolvFile), 5*time.Second)
		go func() {
			defer parentEnd.Close()
			dns.ServeRelay(parentEnd, forward)
		}()
	}
	// With outside access, the child waits on a pipe until pasta is
	// attached (outsideFD), so that its routes come after pasta's.
	var outsideRead, outsideWrite *os.File
	if s.outside {
		if outsideRead, outsideWrite, err = os.Pipe(); err != nil {
			log.Printf("pingmock exec: %v", err)
			return execFailed
		}
		defer outsideWrite.Close()
		child.ExtraFiles = append(child.ExtraFiles, outsideRead)
	}
	rootless := os.Geteuid() != 0
	switch {
	case rootless:
		attr.Cloneflags |= syscall.CLONE_NEWUSER
		attr.UidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Geteuid(), Size: 1}}
		attr.GidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getegid(), Size: 1}}
		attr.GidMappingsEnableSetgroups = false
		child.Env = append(child.Env, envRootless+"=1")
	case s.outside:
		attr.Cloneflags |= syscall.CLONE_NEWUSER
		attr.UidMappings, attr.GidMappings = allIDs(), allIDs()
		attr.GidMappingsEnableSetgroups = true
	}
	child.SysProcAttr = attr
	err = child.Start()
	if childEnd != nil {
		childEnd.Close() // the child has its own copy now
	}
	if outsideRead != nil {
		outsideRead.Close()
	}
	if err != nil {
		if rootless {
			log.Printf("pingmock exec: cannot create a user namespace: %v\n"+
				"Unprivileged user namespaces seem to be disabled on this system. Run pingmock exec with sudo,\n"+
				"or enable them (for example sysctl kernel.unprivileged_userns_clone=1 or user.max_user_namespaces).", err)
		} else {
			log.Printf("pingmock exec: cannot create a network namespace: %v", err)
		}
		return execFailed
	}
	if s.outside {
		stopPasta, err := attachPasta(pasta, s.outsidePorts, !rootless, child.Process.Pid)
		if err != nil {
			log.Printf("pingmock exec: %v", err)
			child.Process.Kill()
			child.Wait()
			return execFailed
		}
		defer stopPasta()
		outsideWrite.Write([]byte{1})
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
		return execFailed
	}
	s.exec = true
	if s.names() {
		if err := setupNames(s.hostsFile(), s.dnsNames()); err != nil {
			log.Print(explain(err, rootless))
			return execFailed
		}
	}
	// The new namespace starts with loopback down; the API and the DNS
	// server listen on it.
	if err := tun.LinkUp("lo"); err != nil {
		log.Print(explain(err, rootless))
		return execFailed
	}
	if s.outside {
		if err := waitOutside(s.outsidePorts); err != nil {
			log.Print(err)
			return execFailed
		}
	}
	in, err := start(s)
	if err != nil {
		log.Print(explain(err, rootless))
		return execFailed
	}
	defer in.Close()
	if s.names() {
		if err := serveDNS(s.hosts, in.rules, in.history, s.verbose); err != nil {
			log.Print(explain(err, rootless))
			return execFailed
		}
	}

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
	return execFailed
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
	case errors.Is(err, syscall.EPERM):
		return fmt.Errorf("%w\nThe user namespace has no network privileges. On Ubuntu 23.10 and later, AppArmor restricts\n"+
			"unprivileged user namespaces. Either run pingmock exec with sudo, or let pingmock use them with the\n"+
			"AppArmor profile in contrib/apparmor/pingmock (see the README), or lift the restriction for all\n"+
			"programs with: sudo sysctl kernel.apparmor_restrict_unprivileged_userns=0", err)
	}
	return err
}
