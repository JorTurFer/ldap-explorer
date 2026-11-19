package ldapfilter

import (
	"strings"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
)

var now = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

// A small schema like the ones real servers publish.
var schema = map[string]AttrInfo{
	"cn":              {Name: "cn", Kind: "string", CanEqual: true, CanSubstring: true},
	"2.5.4.3":         {Name: "cn", Kind: "string", CanEqual: true, CanSubstring: true},
	"mail":            {Name: "mail", Kind: "string", CanEqual: true, CanSubstring: true},
	"uidnumber":       {Name: "uidNumber", Kind: "integer", CanEqual: true, CanOrder: true},
	"createtimestamp": {Name: "createTimestamp", Kind: "time", CanEqual: true, CanOrder: true},
	"enabled":         {Name: "enabled", Kind: "boolean", CanEqual: true},
	"member":          {Name: "member", Kind: "dn", CanEqual: true},
	"jpegphoto":       {Name: "jpegPhoto", Kind: "binary"},
	"nomatch":         {Name: "noMatch", Kind: "string"}, // no matching rules at all
}

func opts() Options {
	return Options{Now: now, Lookup: func(a string) (AttrInfo, bool) { i, ok := schema[strings.ToLower(a)]; return i, ok }}
}

func c(op, attr, value string) *Node    { return &Node{Op: op, Attr: attr, Value: value} }
func g(op string, items ...*Node) *Node { return &Node{Op: op, Items: items} }

func TestBuildConditions(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    *Node
		want string
		expl string
	}{
		{"equals", c("equals", "cn", "Alice"), `(cn=Alice)`, `cn is "Alice"`},
		{"notEquals", c("notEquals", "cn", "Alice"), `(!(cn=Alice))`, `cn is not "Alice" (entries without cn also match)`},
		{"contains", c("contains", "mail", "example"), `(mail=*example*)`, `mail contains "example"`},
		{"startsWith", c("startsWith", "cn", "Al"), `(cn=Al*)`, `cn starts with "Al"`},
		{"endsWith", c("endsWith", "mail", "@x.org"), `(mail=*@x.org)`, `mail ends with "@x.org"`},
		{"wildcard", c("wildcard", "cn", "Al*ce*"), `(cn=Al*ce*)`, `cn matches the pattern "Al*ce*" (* stands for any text)`},
		{"present", c("present", "mail", ""), `(mail=*)`, `mail exists (has any value)`},
		{"notPresent", c("notPresent", "mail", ""), `(!(mail=*))`, `mail does not exist (has no value)`},
		{"integer gte", c("gte", "uidNumber", " 1000 "), `(uidNumber>=1000)`, `uidNumber is greater than or equal to 1000`},
		{"integer lte negative", c("lte", "uidNumber", "-5"), `(uidNumber<=-5)`, `uidNumber is less than or equal to -5`},
		{"boolean yes", c("equals", "enabled", "Yes"), `(enabled=TRUE)`, `enabled is true`},
		{"boolean 0", c("equals", "enabled", "0"), `(enabled=FALSE)`, `enabled is false`},
		{"time date", c("gte", "createTimestamp", "2026-11-19"), `(createTimestamp>=20261119000000Z)`, `createTimestamp is on or after 2026-11-19 00:00 UTC`},
		{"time datetime", c("lte", "createTimestamp", "2026-11-19T10:30"), `(createTimestamp<=20261119103000Z)`, `createTimestamp is on or before 2026-11-19 10:30 UTC`},
		{"time with offset", c("gte", "createTimestamp", "2026-11-19T12:00:00+02:00"), `(createTimestamp>=20261119100000Z)`, `createTimestamp is on or after 2026-11-19 10:00 UTC`},
		{"time already ldap", c("gte", "createTimestamp", "20261119000000Z"), `(createTimestamp>=20261119000000Z)`, `createTimestamp is on or after 20261119000000Z`},
		{"within days", c("withinDays", "createTimestamp", "7"), `(createTimestamp>=20261112120000Z)`, `createTimestamp is within the last 7 day(s) (since 2026-11-12 12:00 UTC)`},
		{"dn is escaped as text", c("equals", "member", "uid=a,ou=p,dc=x"), `(member=uid=a,ou=p,dc=x)`, `member is "uid=a,ou=p,dc=x"`},
		{"canonical attribute name in the text", c("equals", "CN", "x"), `(CN=x)`, `cn is "x"`},
		{"attribute options", c("equals", "cn;lang-es", "x"), `(cn;lang-es=x)`, `cn;lang-es is "x"`},
		{"OID", c("present", "2.5.4.3", ""), `(2.5.4.3=*)`, `cn exists (has any value)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, errs := Build(tc.n, opts())
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			if r.Filter != tc.want {
				t.Errorf("filter %q, want %q", r.Filter, tc.want)
			}
			if r.Explain != tc.expl {
				t.Errorf("explanation %q, want %q", r.Explain, tc.expl)
			}
			// Whatever we build must be a filter a real client accepts.
			if _, err := ldap.CompileFilter(r.Filter); err != nil {
				t.Errorf("not a valid LDAP filter: %v", err)
			}
		})
	}
}

func TestEscaping(t *testing.T) {
	for in, want := range map[string]string{
		"plain":        "plain",
		`a*b`:          `a\2ab`,
		`(x)`:          `\28x\29`,
		`back\slash`:   `back\5cslash`,
		"nul\x00byte":  `nul\00byte`,
		"ñandú €":      "ñandú €",
		`x)(uid=*`:     `x\29\28uid=\2a`,
		`\2a already?`: `\5c2a already?`,
	} {
		if got := Escape(in); got != want {
			t.Errorf("Escape(%q) = %q, want %q", in, got, want)
		}
	}
	if got := escapeKeepStar(`a*(b)*`); got != `a*\28b\29*` {
		t.Errorf("escapeKeepStar: %q", got)
	}
}

// User input must never be able to change the structure of the filter.
func TestInjectionAttemptsStayInsideTheValue(t *testing.T) {
	groups := map[string]int{"equals": 1, "contains": 1, "startsWith": 1, "endsWith": 1, "notEquals": 2} // (cn=..) or (!(cn=..))
	for _, evil := range []string{`x)(uid=*`, `*)(objectClass=*`, `x)(|(uid=*)(cn=*`, `\29\28`, `a\`} {
		for op, want := range groups {
			r, errs := Build(c(op, "cn", evil), opts())
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			packet, err := ldap.CompileFilter(r.Filter)
			if err != nil {
				t.Fatalf("%s %q gave an invalid filter %s: %v", op, evil, r.Filter, err)
			}
			// Parsing it back must show the same number of nested tests as we built.
			back, err := ldap.DecompileFilter(packet)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(back, "("); got != want {
				t.Errorf("%s %q: %d filter groups instead of %d: %s", op, evil, got, want, r.Filter)
			}
			if strings.Contains(back, "uid") && !strings.Contains(evil, "uid") {
				t.Errorf("unexpected attribute test: %s", back)
			}
		}
	}
	// wildcard keeps * but still escapes everything else
	r, _ := Build(c("wildcard", "cn", `a*)(uid=*`), opts())
	if r.Filter != `(cn=a*\29\28uid=*)` {
		t.Errorf("wildcard: %s", r.Filter)
	}
}

func TestGroups(t *testing.T) {
	r, errs := Build(g("and",
		c("contains", "mail", "example"),
		g("or", c("equals", "cn", "alice"), c("equals", "cn", "bob")),
		g("not", c("present", "member", "")),
	), opts())
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if want := `(&(mail=*example*)(|(cn=alice)(cn=bob))(!(member=*)))`; r.Filter != want {
		t.Errorf("filter %q, want %q", r.Filter, want)
	}
	want := strings.Join([]string{
		"All of these must be true:",
		`  mail contains "example"`,
		"  At least one of these must be true:",
		`    cn is "alice"`,
		`    cn is "bob"`,
		"  This must NOT be true:",
		"    member exists (has any value)",
	}, "\n")
	if r.Explain != want {
		t.Errorf("explanation:\n%s\nwant:\n%s", r.Explain, want)
	}
}

func TestSingleItemGroupsCollapse(t *testing.T) {
	r, errs := Build(g("and", g("or", c("equals", "cn", "x"))), opts())
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if r.Filter != `(cn=x)` || r.Explain != `cn is "x"` {
		t.Errorf("%q / %q", r.Filter, r.Explain)
	}
	// NOT never collapses
	r, _ = Build(g("not", c("equals", "cn", "x")), opts())
	if r.Filter != `(!(cn=x))` {
		t.Errorf("not: %s", r.Filter)
	}
}

func TestValidationErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    *Node
		path string
		msg  string
	}{
		{"nil", nil, "", "empty"},
		{"empty group", g("and"), "", "at least one"},
		{"not with two items", g("not", c("present", "cn", ""), c("present", "mail", "")), "", "exactly one"},
		{"missing operator", &Node{Attr: "cn", Value: "x"}, "", "missing operator"},
		{"unknown operator", c("like", "cn", "x"), "", "unknown operator"},
		{"no attribute", c("equals", "", "x"), "", "choose an attribute"},
		{"bad attribute name", c("equals", "cn)(uid", "x"), "", "not a valid attribute name"},
		{"unknown attribute", c("equals", "colour", "x"), "", "not an attribute of this server"},
		{"missing value", c("contains", "cn", ""), "", "needs a value"},
		{"substring without rule", c("contains", "noMatch", "x"), "", "no substring matching"},
		{"substring on dn", c("contains", "member", "x"), "", "no substring matching"},
		{"equality without rule", c("equals", "noMatch", "x"), "", "no equality matching"},
		{"order without rule", c("gte", "cn", "x"), "", "no ordering rule"},
		{"binary equals", c("equals", "jpegPhoto", "x"), "", "no equality matching"},
		{"present binary is fine but gte is not", c("gte", "jpegPhoto", "x"), "", "no ordering rule"},
		{"bad boolean", c("equals", "enabled", "maybe"), "", "true or false"},
		{"bad integer", c("gte", "uidNumber", "12a"), "", "whole number"},
		{"bad date", c("gte", "createTimestamp", "yesterday"), "", "needs a date"},
		{"withinDays on non-time", c("withinDays", "cn", "7"), "", "not a date/time"},
		{"withinDays out of range", c("withinDays", "createTimestamp", "0"), "", "between 1 and 36500"},
		{"withinDays not a number", c("withinDays", "createTimestamp", "soon"), "", "between 1 and 36500"},
		{"error path in nested group", g("and", c("present", "cn", ""), g("or", c("present", "mail", ""), c("equals", "uidNumber", "x"))), "items[1].items[1]", "whole number"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, errs := Build(tc.n, opts())
			if r != nil || len(errs) == 0 {
				t.Fatalf("expected an error, got %+v", r)
			}
			if !strings.Contains(errs[0].Message, tc.msg) {
				t.Errorf("message %q does not contain %q", errs[0].Message, tc.msg)
			}
			if errs[0].Path != tc.path {
				t.Errorf("path %q, want %q", errs[0].Path, tc.path)
			}
		})
	}
}

func TestReportsAllErrors(t *testing.T) {
	_, errs := Build(g("and", c("equals", "uidNumber", "x"), c("equals", "enabled", "maybe"), c("present", "cn", "")), opts())
	if len(errs) != 2 || errs[0].Path != "items[0]" || errs[1].Path != "items[1]" {
		t.Errorf("errors: %+v", errs)
	}
}

func TestWithoutSchemaEverythingIsAString(t *testing.T) {
	r, errs := Build(g("and", c("contains", "colour", "re*d"), c("gte", "size", "10")), Options{Now: now})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if r.Filter != `(&(colour=*re\2ad*)(size>=10))` {
		t.Errorf("filter: %s", r.Filter)
	}
}

func TestAttributeOptionsSkipSchemaLookup(t *testing.T) {
	if _, errs := Build(c("equals", "cn;lang-fr", "x"), opts()); len(errs) != 0 {
		t.Errorf("attribute options should be accepted: %v", errs)
	}
}

func TestDefaultNowIsRealNow(t *testing.T) {
	r, errs := Build(c("withinDays", "createTimestamp", "1"), Options{Lookup: opts().Lookup})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	ts := strings.TrimSuffix(strings.TrimPrefix(r.Filter, "(createTimestamp>="), "Z)")
	got, err := time.Parse("20060102150405", ts)
	if err != nil || time.Since(got) < 23*time.Hour || time.Since(got) > 25*time.Hour {
		t.Errorf("default now is not the current time: %s (%v)", r.Filter, err)
	}
}
