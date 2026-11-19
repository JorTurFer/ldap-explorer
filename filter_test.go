package main

import (
	"slices"
	"strings"
	"testing"

	"ldap-explorer/internal/ldapfilter"
	"ldap-explorer/internal/slapdtest"
)

// The query builder's filters are built by the API and then RUN against a real
// OpenLDAP: what matters is that they return the right entries, including for
// values with characters that are special in filters.

type filterOK struct {
	Filter          string `json:"filter"`
	Explain         string `json:"explain"`
	SchemaAvailable bool   `json:"schemaAvailable"`
}

func cond(op, attr, value string) *ldapfilter.Node {
	return &ldapfilter.Node{Op: op, Attr: attr, Value: value}
}

func group(op string, items ...*ldapfilter.Node) *ldapfilter.Node {
	return &ldapfilter.Node{Op: op, Items: items}
}

func TestFilterAPIAgainstRealServer(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, alice(l), slapdtest.AlicePW)

	search := func(q *ldapfilter.Node, base string) (filterOK, []string) {
		t.Helper()
		var f filterOK
		if code := call(t, srv, "POST", "/api/filter", map[string]any{"query": q}, &f); code != 200 {
			var e filterError
			call(t, srv, "POST", "/api/filter", map[string]any{"query": q}, &e)
			t.Fatalf("filter %+v: %d %+v", q, code, e)
		}
		var res searchResp
		if code := call(t, srv, "POST", "/api/search", map[string]any{"base": base, "filter": f.Filter, "scope": "sub", "attrs": "1.1"}, &res); code != 200 {
			t.Fatalf("search with %s: %d", f.Filter, code)
		}
		var dns []string
		for _, e := range res.Entries {
			dns = append(dns, strings.SplitN(e.DN, ",", 2)[0])
		}
		slices.Sort(dns)
		return f, dns
	}
	people := "ou=people," + slapdtest.BaseDN
	person := cond("equals", "objectClass", "inetOrgPerson")

	for _, tc := range []struct {
		name string
		q    *ldapfilter.Node
		base string
		want []string
	}{
		{"contains", group("and", person, cond("contains", "mail", "example")), people, []string{"uid=alice", "uid=bob"}},
		{"startsWith", group("and", person, cond("startsWith", "uid", "b")), people, []string{"uid=bob"}},
		{"endsWith", group("and", person, cond("endsWith", "mail", "bob@example.org")), people, []string{"uid=bob"}},
		{"present", group("and", person, cond("present", "mail", "")), people, []string{"uid=alice", "uid=bob"}},
		{"notPresent", group("and", person, cond("notPresent", "mail", "")), people, []string{"uid=zoe"}},
		{"or", group("or", cond("equals", "uid", "alice"), cond("equals", "uid", "bob")), people, []string{"uid=alice", "uid=bob"}},
		{"not", group("and", person, group("not", cond("present", "mail", ""))), people, []string{"uid=zoe"}},
		{"notEquals", group("and", person, cond("notEquals", "uid", "bob")), people, []string{"uid=alice", "uid=zoe"}},
		{"wildcard", group("and", person, cond("wildcard", "cn", "A*Liddell")), people, []string{"uid=alice"}},
		{"case-insensitive attribute and value", cond("equals", "UID", "ALICE"), people, []string{"uid=alice"}},
		// unicode
		{"unicode contains", cond("contains", "cn", "Ñandú"), people, []string{"uid=zoe"}},
		{"unicode startsWith", cond("startsWith", "cn", "Zoë"), people, []string{"uid=zoe"}},
		// characters that are special in filters
		{"parentheses", cond("contains", "cn", "(Name)"), slapdtest.BaseDN, []string{"cn=Weird (Name) *star* \\5Cback"}},
		{"literal asterisks", cond("contains", "cn", "*star*"), slapdtest.BaseDN, []string{"cn=Weird (Name) *star* \\5Cback"}},
		{"backslash", cond("endsWith", "cn", `\back`), slapdtest.BaseDN, []string{"cn=Weird (Name) *star* \\5Cback"}},
		{"asterisk is literal in equals", cond("equals", "cn", "*"), slapdtest.BaseDN, nil},
		{"pattern with specials", cond("wildcard", "cn", `Weird (*) \back`), slapdtest.BaseDN, nil},
		{"injection attempt matches nothing", cond("equals", "cn", "x)(uid=*"), slapdtest.BaseDN, nil},
		// integers and dates (typed by the schema)
		{"integer equals", cond("equals", "shadowExpire", "15000"), people, []string{"uid=zoe"}},
		{"integer lte", cond("lte", "gidNumber", "2000"), slapdtest.BaseDN, []string{"cn=staff"}},
		{"integer gte", cond("gte", "gidNumber", "2000"), slapdtest.BaseDN, nil},
		{"date gte", group("and", cond("equals", "uid", "alice"), cond("gte", "createTimestamp", "2000-01-01")), people, []string{"uid=alice"}},
		{"date lte", group("and", cond("equals", "uid", "alice"), cond("lte", "createTimestamp", "2000-01-01")), people, nil},
		{"within days", group("and", cond("equals", "uid", "alice"), cond("withinDays", "createTimestamp", "1")), people, []string{"uid=alice"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, got := search(tc.q, tc.base)
			if !f.SchemaAvailable {
				t.Error("the schema should be available on OpenLDAP")
			}
			if f.Explain == "" {
				t.Error("missing explanation")
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("filter %s returned %v, want %v", f.Filter, got, tc.want)
			}
		})
	}

	t.Run("explanation of a group", func(t *testing.T) {
		f, _ := search(group("and", cond("contains", "mail", "example"), group("or", cond("equals", "uid", "alice"), cond("equals", "uid", "bob"))), people)
		if !strings.Contains(f.Explain, "All of these must be true:") || !strings.Contains(f.Explain, `  mail contains "example"`) ||
			!strings.Contains(f.Explain, "  At least one of these must be true:") {
			t.Errorf("explanation: %q", f.Explain)
		}
	})
}

func TestFilterAPIErrors(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)

	var e filterError
	if code := call(t, srv, "POST", "/api/filter", map[string]any{"query": cond("equals", "cn", "x")}, &errResp{}); code != 400 {
		t.Errorf("without a session: %d", code)
	}

	connectOK(t, srv, alice(l), slapdtest.AlicePW)
	for _, tc := range []struct {
		name string
		body any
		msg  string
		path string
	}{
		{"unknown attribute", map[string]any{"query": cond("equals", "colour", "red")}, "not an attribute of this server", ""},
		{"substring on objectClass", map[string]any{"query": cond("contains", "objectClass", "per")}, "no substring matching", ""},
		{"bad integer", map[string]any{"query": cond("gte", "gidNumber", "soon")}, "whole number", ""},
		// OpenLDAP's nis.schema gives shadowExpire an equality rule but no ordering rule:
		// the server could not answer <= or >=, so the builder refuses instead of returning nonsense.
		{"no ordering rule on the real server", map[string]any{"query": cond("lte", "shadowExpire", "20000")}, "no ordering rule", ""},
		{"error inside a group", map[string]any{"query": group("and", cond("present", "cn", ""), cond("equals", "colour", "x"))}, "not an attribute", "items[1]"},
		{"missing query", map[string]any{}, "empty", ""},
		{"empty group", map[string]any{"query": group("or")}, "at least one", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code := call(t, srv, "POST", "/api/filter", tc.body, &e); code != 400 {
				t.Fatalf("status %d", code)
			}
			if !strings.Contains(e.Error, tc.msg) || len(e.Errors) == 0 || e.Errors[0].Path != tc.path {
				t.Errorf("error %q, errors %+v (want %q at %q)", e.Error, e.Errors, tc.msg, tc.path)
			}
		})
	}
	if code := call(t, srv, "POST", "/api/filter", "not an object", &errResp{}); code != 400 {
		t.Errorf("invalid body: %d", code)
	}
}
