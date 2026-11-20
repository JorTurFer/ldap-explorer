package ldif

import (
	"bytes"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
)

func enc(t *testing.T, entries ...Entry) string {
	t.Helper()
	var b bytes.Buffer
	if err := Encode(&b, entries); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func attr(name string, values ...string) Attr {
	a := Attr{Name: name}
	for _, v := range values {
		a.Values = append(a.Values, []byte(v))
	}
	return a
}

func TestEncodeSimpleEntries(t *testing.T) {
	got := enc(t,
		Entry{DN: "uid=alice,ou=people,dc=example,dc=org", Attrs: []Attr{attr("objectClass", "top", "person"), attr("cn", "Alice Liddell"), attr("empty", "")}},
		Entry{DN: "uid=bob,dc=example,dc=org", Attrs: []Attr{attr("mail", "bob@example.org")}})
	want := "version: 1\n" +
		"\n" +
		"dn: uid=alice,ou=people,dc=example,dc=org\n" +
		"objectClass: top\n" +
		"objectClass: person\n" +
		"cn: Alice Liddell\n" +
		"empty: \n" +
		"\n" +
		"dn: uid=bob,dc=example,dc=org\n" +
		"mail: bob@example.org\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestSafeString(t *testing.T) {
	for in, want := range map[string]bool{
		"": true, "plain": true, "with space inside": true, "a:b": true, "x<y": true, "tab\tinside": true,
		" leading": false, "trailing ": false, ":colon": false, "<less": false, "new\nline": false, "car\rriage": false,
		"nul\x00": false, "ñandú": false, "日本語": false, "\xff": false,
	} {
		if got := SafeString([]byte(in)); got != want {
			t.Errorf("SafeString(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestUnsafeValuesAreBase64(t *testing.T) {
	got := enc(t, Entry{DN: "cn=Zoë,dc=x", Attrs: []Attr{
		attr("cn", "Zoë Ñandú"), attr("description", " leading space"), attr("note", "two\nlines"), attr("path", ":starts with colon"),
	}})
	for _, want := range []string{
		"dn:: " + base64.StdEncoding.EncodeToString([]byte("cn=Zoë,dc=x")),
		"cn:: " + base64.StdEncoding.EncodeToString([]byte("Zoë Ñandú")),
		"description:: " + base64.StdEncoding.EncodeToString([]byte(" leading space")),
		"note:: " + base64.StdEncoding.EncodeToString([]byte("two\nlines")),
		"path:: " + base64.StdEncoding.EncodeToString([]byte(":starts with colon")),
	} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Zoë") || strings.Contains(got, "Ñandú") {
		t.Error("non-ASCII text must never appear raw")
	}
}

func TestLongLinesAreFoldedAt76(t *testing.T) {
	long := strings.Repeat("0123456789", 30) // 300 characters
	got := enc(t, Entry{DN: "cn=x", Attrs: []Attr{attr("description", long), {Name: "jpegPhoto", Values: [][]byte{bytes.Repeat([]byte{0xff, 0xd8}, 200)}}}})
	for i, l := range strings.Split(got, "\n") {
		if len(l) > 76 {
			t.Errorf("line %d has %d characters: %q", i, len(l), l)
		}
	}
	if !strings.Contains(got, "description: 0123456789") {
		t.Error("the value starts on the attribute line")
	}
	// continuation lines start with exactly one space
	folded := false
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, " ") {
			folded = true
			if strings.HasPrefix(l, "  ") {
				t.Errorf("a continuation line starts with more than one space: %q", l)
			}
		}
	}
	if !folded {
		t.Error("nothing was folded")
	}
}

// What we write must read back identical, for every kind of value.
func TestRoundTrip(t *testing.T) {
	allBytes := make([]byte, 256)
	for i := range allBytes {
		allBytes[i] = byte(i)
	}
	in := []Entry{
		{DN: "dc=example,dc=org", Attrs: []Attr{attr("objectClass", "top", "dcObject"), attr("dc", "example")}},
		{DN: "cn=Weird (Name) *star* \\5Cback,ou=special,dc=example,dc=org", Attrs: []Attr{attr("cn", `Weird (Name) *star* \back`), attr("sn", "Weird")}},
		{DN: "uid=zoe,ou=people,dc=example,dc=org", Attrs: []Attr{
			attr("cn", "Zoë Ñandú"), attr("description", " leading", "trailing ", "", ":colon", "<lt", "tab\t", "x:y"),
			{Name: "jpegPhoto", Values: [][]byte{allBytes}},
			attr("note", strings.Repeat("long line ", 60)), attr("multi", "a", "b", "c"),
		}},
		{DN: "cn=ñandú,dc=x", Attrs: []Attr{attr("sn", "日本語")}},
	}
	text := enc(t, in...)
	out, err := Decode(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("%d entries read back, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i].DN != in[i].DN {
			t.Errorf("entry %d DN: %q, want %q", i, out[i].DN, in[i].DN)
		}
		if !reflect.DeepEqual(out[i].Attrs, in[i].Attrs) {
			t.Errorf("entry %d attributes differ:\n got %q\nwant %q", i, out[i].Attrs, in[i].Attrs)
		}
	}
}

func TestEmptyListIsJustTheHeader(t *testing.T) {
	if got := enc(t); got != "version: 1\n" {
		t.Errorf("%q", got)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"no colon here\n", "dn: x\nattr:: !!!notbase64!!!\n", "cn: orphan before any dn\n", "dn: x\nphoto:< file:///etc/passwd\n"} {
		if _, err := Decode(strings.NewReader(bad)); err == nil {
			t.Errorf("Decode(%q) should fail", bad)
		}
	}
	// comments and CRLF are fine
	got, err := Decode(strings.NewReader("# a comment\r\nversion: 1\r\n\r\ndn: cn=a\r\ncn: a\r\n"))
	if err != nil || len(got) != 1 || got[0].DN != "cn=a" {
		t.Errorf("CRLF and comments: %+v %v", got, err)
	}
}
