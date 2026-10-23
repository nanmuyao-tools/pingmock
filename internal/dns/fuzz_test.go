package dns

import "testing"

// Answer reads queries from the command: it must never panic, whatever
// arrives, and with a rule for every name.
func FuzzAnswer(f *testing.F) {
	f.Add(query(1, "api.example.com", typeA))
	f.Add(query(2, "1.0.99.10.in-addr.arpa", typePTR))
	f.Add(query(3, "x", typeAAAA))
	f.Add([]byte{0, 1, 2})
	s := newServer(nil)
	f.Fuzz(func(t *testing.T, q []byte) {
		s.Answer(q)
		s.answer(q, true)
	})
}
