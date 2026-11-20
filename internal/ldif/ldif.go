// Package ldif writes (and, for tests and round trips, reads) entries in LDIF, the
// standard text format for LDAP data (RFC 2849): values that are not plain ASCII
// "safe strings" are base64-encoded, and lines are folded at 76 characters.
package ldif

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

// Attr is an attribute with its raw values (binary-safe).
type Attr struct {
	Name   string
	Values [][]byte
}

// Entry is one directory entry.
type Entry struct {
	DN    string
	Attrs []Attr
}

const maxLine = 76

// SafeString reports whether a value can be written as is after "attr: " (RFC 2849
// SAFE-STRING). Anything else, including every non-ASCII byte and a value that starts
// or ends with a space, must be base64-encoded.
func SafeString(b []byte) bool {
	for i, c := range b {
		switch {
		case c == 0 || c == '\n' || c == '\r' || c > 0x7f:
			return false
		case i == 0 && (c == ' ' || c == ':' || c == '<'):
			return false
		}
	}
	return len(b) == 0 || b[len(b)-1] != ' '
}

// Encode writes the entries separated by blank lines, preceded by "version: 1".
func Encode(w io.Writer, entries []Entry) error {
	bw := bufio.NewWriter(w)
	if _, err := bw.WriteString("version: 1\n"); err != nil {
		return err
	}
	for _, e := range entries {
		if _, err := bw.WriteString("\n"); err != nil {
			return err
		}
		if err := writeLine(bw, "dn", []byte(e.DN)); err != nil {
			return err
		}
		for _, a := range e.Attrs {
			for _, v := range a.Values {
				if err := writeLine(bw, a.Name, v); err != nil {
					return err
				}
			}
		}
	}
	return bw.Flush()
}

func writeLine(w *bufio.Writer, name string, value []byte) error {
	var line string
	if SafeString(value) {
		line = name + ": " + string(value)
	} else {
		line = name + ":: " + base64.StdEncoding.EncodeToString(value)
	}
	return fold(w, line)
}

// fold writes a line cut at 76 characters; continuation lines start with one space.
func fold(w *bufio.Writer, line string) error {
	first := true
	for len(line) > 0 {
		n := maxLine
		if !first {
			n = maxLine - 1 // the leading space counts
			if _, err := w.WriteString(" "); err != nil {
				return err
			}
		}
		if n > len(line) {
			n = len(line)
		}
		if _, err := w.WriteString(line[:n] + "\n"); err != nil {
			return err
		}
		line = line[n:]
		first = false
	}
	if first { // an empty line cannot happen for "name:" but be safe
		_, err := w.WriteString("\n")
		return err
	}
	return nil
}

// Decode reads LDIF written by Encode (and ordinary LDIF files without changetypes or
// url values). It is used by the tests to prove that what we export reads back identical.
func Decode(r io.Reader) ([]Entry, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	// Unfold: a line that starts with one space continues the previous one.
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(l, " ") && len(lines) > 0 {
			lines[len(lines)-1] += l[1:]
			continue
		}
		lines = append(lines, l)
	}
	var out []Entry
	var cur *Entry
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}
	for _, l := range lines {
		if l == "" {
			flush()
			continue
		}
		if strings.HasPrefix(l, "#") {
			continue
		}
		name, value, err := splitLine(l)
		if err != nil {
			return nil, err
		}
		if name == "version" && cur == nil {
			continue
		}
		if name == "dn" {
			flush()
			cur = &Entry{DN: string(value)}
			continue
		}
		if cur == nil {
			return nil, fmt.Errorf("ldif: %q outside an entry", name)
		}
		found := false
		for i := range cur.Attrs {
			if cur.Attrs[i].Name == name {
				cur.Attrs[i].Values = append(cur.Attrs[i].Values, value)
				found = true
			}
		}
		if !found {
			cur.Attrs = append(cur.Attrs, Attr{Name: name, Values: [][]byte{value}})
		}
	}
	flush()
	return out, nil
}

func splitLine(l string) (name string, value []byte, err error) {
	i := strings.IndexByte(l, ':')
	if i <= 0 {
		return "", nil, fmt.Errorf("ldif: malformed line %q", l)
	}
	name, rest := l[:i], l[i+1:]
	if strings.HasPrefix(rest, ":") {
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rest[1:]))
		if err != nil {
			return "", nil, fmt.Errorf("ldif: bad base64 in %q: %w", name, err)
		}
		return name, b, nil
	}
	if strings.HasPrefix(rest, "<") {
		return "", nil, fmt.Errorf("ldif: url values are not supported (%q)", name)
	}
	return name, bytes.TrimPrefix([]byte(rest), []byte(" ")), nil
}
