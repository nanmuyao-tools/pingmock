// Package dns is the small DNS server that pingmock exec runs for the
// command: it answers for the mocked names and addresses, and forwards
// every other query to the host's resolver through the parent process,
// which still has the host's network.
//
// It handles the one shape of query stub resolvers send, a single question
// over UDP, and nothing more. Answers carry A, AAAA and PTR records only.
package dns

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Table maps names to addresses, and addresses back to names.
type Table struct {
	mu    sync.RWMutex
	names map[string][]netip.Addr
	addrs map[netip.Addr]string
}

func NewTable() *Table {
	return &Table{names: map[string][]netip.Addr{}, addrs: map[netip.Addr]string{}}
}

// Add makes name resolve to a, as well as to the addresses it already had.
// The first name added for a is what a resolves back to.
func (t *Table) Add(name string, a netip.Addr) {
	key := Canonical(name)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.names[key] = append(t.names[key], a)
	if _, ok := t.addrs[a]; !ok {
		t.addrs[a] = key
	}
}

// Lookup returns the addresses of name, or nil if it is not mocked.
func (t *Table) Lookup(name string) []netip.Addr {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.names[Canonical(name)]
}

// Reverse returns the name of a mocked address.
func (t *Table) Reverse(a netip.Addr) (string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	n, ok := t.addrs[a]
	return n, ok
}

// Names returns every name with its addresses, sorted by name.
func (t *Table) Names() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, 0, len(t.names))
	for n := range t.names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Canonical is the form names are compared in: lower case, no trailing dot.
func Canonical(name string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
}

// Server answers queries from its table and forwards the rest.
type Server struct {
	Table *Table
	// Forward returns the response to a query the table cannot answer. If
	// nil, or if it fails, the name does not exist.
	Forward func(query []byte) ([]byte, error)
	// Log, if set, is told about every query.
	Log func(format string, args ...any)
}

// Serve answers queries on pc until it is closed.
func (s *Server) Serve(pc net.PacketConn) {
	buf := make([]byte, 4096)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		if resp := s.Answer(append([]byte(nil), buf[:n]...)); resp != nil {
			pc.WriteTo(resp, from)
		}
	}
}

// DNS record types and response codes used here (RFC 1035, RFC 3596).
const (
	typeA    = 1
	typePTR  = 12
	typeAAAA = 28
	classIN  = 1

	rcodeOK       = 0
	rcodeFormErr  = 1
	rcodeServFail = 2
	rcodeNXDomain = 3
	rcodeNotImpl  = 4
)

// Answer returns the response to one query, or nil if it is not a query
// worth answering.
func (s *Server) Answer(q []byte) []byte {
	if len(q) < 12 || q[2]&0x80 != 0 { // too short, or a response
		return nil
	}
	if opcode := (q[2] >> 3) & 0x0f; opcode != 0 {
		return respond(q, nil, rcodeNotImpl, nil)
	}
	name, qtype, qclass, qend, err := question(q)
	if err != nil || binary.BigEndian.Uint16(q[4:6]) != 1 {
		return respond(q, nil, rcodeFormErr, nil)
	}
	if qclass != classIN {
		return respond(q, q[12:qend], rcodeOK, nil)
	}
	var records [][]byte
	known := false
	switch qtype {
	case typePTR:
		if a, ok := reverseName(name); ok {
			if n, ok := s.Table.Reverse(a); ok {
				known = true
				records = append(records, record(typePTR, encodeName(n)))
			}
		}
	default:
		if addrs := s.Table.Lookup(name); addrs != nil {
			known = true
			for _, a := range addrs {
				switch {
				case qtype == typeA && a.Is4():
					b := a.As4()
					records = append(records, record(typeA, b[:]))
				case qtype == typeAAAA && a.Is6():
					b := a.As16()
					records = append(records, record(typeAAAA, b[:]))
				}
			}
		}
	}
	if s.Log != nil {
		s.Log("dns: %s type %d: mocked=%v", name, qtype, known)
	}
	if known {
		return respond(q, q[12:qend], rcodeOK, records)
	}
	if s.Forward != nil {
		if resp, err := s.Forward(q); err == nil && len(resp) >= 12 {
			return resp
		} else if s.Log != nil {
			s.Log("dns: forwarding %s: %v", name, err)
		}
		return respond(q, q[12:qend], rcodeServFail, nil)
	}
	return respond(q, q[12:qend], rcodeNXDomain, nil)
}

// question parses the single question of q: the name (canonical), type,
// class, and where the question ends.
func question(q []byte) (name string, qtype, qclass uint16, end int, err error) {
	var labels []string
	i := 12
	for {
		if i >= len(q) {
			return "", 0, 0, 0, errors.New("truncated name")
		}
		n := int(q[i])
		i++
		if n == 0 {
			break
		}
		if n&0xc0 != 0 || i+n > len(q) {
			return "", 0, 0, 0, errors.New("bad label")
		}
		labels = append(labels, string(q[i:i+n]))
		i += n
	}
	if i+4 > len(q) {
		return "", 0, 0, 0, errors.New("truncated question")
	}
	return Canonical(strings.Join(labels, ".")), binary.BigEndian.Uint16(q[i:]), binary.BigEndian.Uint16(q[i+2:]), i + 4, nil
}

// respond builds a response to q: the header with the flags of a reply,
// the question as sent (if given) and the records, each for the question's
// name.
func respond(q, question []byte, rcode byte, records [][]byte) []byte {
	out := make([]byte, 12, 12+len(question)+64*len(records))
	copy(out[0:2], q[0:2])        // ID
	out[2] = 0x80 | (q[2] & 0x79) // response; opcode and RD as asked; AA below
	out[3] = 0x80 | rcode         // recursion available
	if rcode == rcodeOK || rcode == rcodeNXDomain {
		out[2] |= 0x04 // authoritative
	}
	if question != nil {
		binary.BigEndian.PutUint16(out[4:6], 1)
		out = append(out, question...)
	}
	binary.BigEndian.PutUint16(out[6:8], uint16(len(records)))
	for _, r := range records {
		out = append(out, r...)
	}
	return out
}

// record builds a resource record whose name points at the question's
// (offset 12, the standard compression pointer), of the given type and
// data, with TTL 0 so that nothing caches what a test may change.
func record(rtype uint16, data []byte) []byte {
	r := make([]byte, 12, 12+len(data))
	r[0], r[1] = 0xc0, 12
	binary.BigEndian.PutUint16(r[2:], rtype)
	binary.BigEndian.PutUint16(r[4:], classIN)
	binary.BigEndian.PutUint16(r[10:], uint16(len(data)))
	return append(r, data...)
}

// encodeName encodes a domain name in wire format.
func encodeName(name string) []byte {
	var out []byte
	for _, label := range strings.Split(Canonical(name), ".") {
		if label == "" {
			continue
		}
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	return append(out, 0)
}

// reverseName turns a name under in-addr.arpa or ip6.arpa back into the
// address it stands for.
func reverseName(name string) (netip.Addr, bool) {
	if s, ok := strings.CutSuffix(name, ".in-addr.arpa"); ok {
		parts := strings.Split(s, ".")
		if len(parts) != 4 {
			return netip.Addr{}, false
		}
		var b [4]byte
		for i, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil || n < 0 || n > 255 {
				return netip.Addr{}, false
			}
			b[3-i] = byte(n)
		}
		return netip.AddrFrom4(b), true
	}
	if s, ok := strings.CutSuffix(name, ".ip6.arpa"); ok {
		nibbles := strings.Split(s, ".")
		if len(nibbles) != 32 {
			return netip.Addr{}, false
		}
		var b [16]byte
		for i, n := range nibbles {
			v, err := strconv.ParseUint(n, 16, 4)
			if err != nil {
				return netip.Addr{}, false
			}
			j := 31 - i
			if j%2 == 0 {
				b[j/2] |= byte(v) << 4
			} else {
				b[j/2] |= byte(v)
			}
		}
		return netip.AddrFrom16(b), true
	}
	return netip.Addr{}, false
}

// --- the host side: forwarding to the host's resolver -------------------

// Nameservers returns the nameservers of a resolv.conf file, following
// symlinks, in order. Without any, the C library's default, 127.0.0.1.
func Nameservers(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{"127.0.0.1"}
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "nameserver" {
			if a, err := netip.ParseAddr(f[1]); err == nil {
				out = append(out, a.String())
			}
		}
	}
	if len(out) == 0 {
		out = []string{"127.0.0.1"}
	}
	return out
}

// Forwarder returns a function that sends a query to each server in turn
// over UDP, each given timeout to answer, and returns the first response.
func Forwarder(servers []string, timeout time.Duration) func([]byte) ([]byte, error) {
	return func(q []byte) ([]byte, error) {
		var last error
		for _, s := range servers {
			resp, err := ask(s, q, timeout)
			if err == nil {
				return resp, nil
			}
			last = err
		}
		if last == nil {
			last = errors.New("no nameserver")
		}
		return nil, last
	}
}

// ask sends q to server, an address with port 53 unless another is given.
func ask(server string, q []byte, timeout time.Duration) ([]byte, error) {
	if _, _, err := net.SplitHostPort(server); err != nil {
		server = net.JoinHostPort(server, "53")
	}
	conn, err := net.DialTimeout("udp", server, timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(q); err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", server, err)
		}
		if n >= 2 && buf[0] == q[0] && buf[1] == q[1] { // the answer to this query
			return append([]byte(nil), buf[:n]...), nil
		}
	}
}

// --- the stream between the command's network and the host's ----------

// ServeRelay answers queries that arrive on conn as frames, with forward,
// until conn is closed. The parent of pingmock exec runs it.
func ServeRelay(conn io.ReadWriter, forward func([]byte) ([]byte, error)) {
	for {
		q, err := ReadFrame(conn)
		if err != nil {
			return
		}
		resp, err := forward(q)
		if err != nil {
			resp = nil
		}
		if WriteFrame(conn, resp) != nil {
			return
		}
	}
}

// StreamForwarder returns a Forward function that sends each query over
// conn as a frame and waits for the framed response, one query at a time.
func StreamForwarder(conn io.ReadWriter) func([]byte) ([]byte, error) {
	var mu sync.Mutex
	return func(q []byte) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		if err := WriteFrame(conn, q); err != nil {
			return nil, err
		}
		resp, err := ReadFrame(conn)
		if err != nil {
			return nil, err
		}
		if len(resp) == 0 {
			return nil, errors.New("the host's resolver did not answer")
		}
		return resp, nil
	}
}

// ReadFrame reads one length-prefixed message.
func ReadFrame(r io.Reader) ([]byte, error) {
	var n [2]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return nil, err
	}
	buf := make([]byte, binary.BigEndian.Uint16(n[:]))
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// WriteFrame writes one length-prefixed message; nil is an empty one.
func WriteFrame(w io.Writer, b []byte) error {
	if len(b) > 0xffff {
		b = b[:0xffff]
	}
	var n [2]byte
	binary.BigEndian.PutUint16(n[:], uint16(len(b)))
	if _, err := w.Write(n[:]); err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	_, err := w.Write(b)
	return err
}
