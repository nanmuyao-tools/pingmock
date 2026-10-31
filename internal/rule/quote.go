package rule

import (
	"fmt"
	"strconv"
	"strings"
)

// MaxData is the most bytes a TCP rule may send at once, with send= or
// reply=: well below the window a client's kernel opens on a new
// connection, since ghostwire never sends anything again.
const MaxData = 16 << 10

// splitUnquoted splits s at every sep outside double quotes, so that the
// bytes of send= and reply= may contain the separators of the text form.
// A backslash inside quotes escapes the next character, as in Go.
func splitUnquoted(s string, sep byte) []string {
	var parts []string
	start, quoted := 0, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quoted && c == '\\':
			i++
		case c == '"':
			quoted = !quoted
		case !quoted && c == sep:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

// lastIndexUnquoted is strings.LastIndex for a sub that occurs outside
// double quotes, or -1.
func lastIndexUnquoted(s, sub string) int {
	last, quoted := -1, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quoted && c == '\\':
			i++
		case c == '"':
			quoted = !quoted
		case !quoted && strings.HasPrefix(s[i:], sub):
			last = i
		}
	}
	return last
}

// parseData parses the bytes of send= or reply=: a Go string literal in
// double quotes, such as "SSH-2.0-OpenSSH_9.6\r\n" or "\x00\x01".
func parseData(key, val string) ([]byte, error) {
	val = strings.TrimSpace(val)
	if !strings.HasPrefix(val, `"`) {
		return nil, fmt.Errorf("%s takes bytes in double quotes, as in Go: %s=\"HTTP/1.1 503 Service Unavailable\\r\\n\\r\\n\"", key, key)
	}
	s, err := strconv.Unquote(val)
	if err != nil {
		return nil, fmt.Errorf("%s=%s: not a Go string in double quotes", key, val)
	}
	return checkData(key, []byte(s))
}

func checkData(key string, b []byte) ([]byte, error) {
	switch {
	case len(b) == 0:
		return nil, fmt.Errorf("%s: no bytes to send", key)
	case len(b) > MaxData:
		return nil, fmt.Errorf("%s: %d bytes, more than the %d a rule may send", key, len(b), MaxData)
	}
	return b, nil
}

// quoteData writes bytes in the form parseData reads, in ASCII.
func quoteData(b []byte) string { return strconv.QuoteToASCII(string(b)) }
