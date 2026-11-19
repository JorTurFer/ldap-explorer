// Package ldapfilter turns a tree of simple conditions ("mail contains
// example", "created in the last 7 days") into an RFC 4515 LDAP filter, with
// correct escaping, validation against what the server's schema allows, and a
// plain-language explanation. It is the single place that builds filters, so
// user input can never alter the structure of a filter.
package ldapfilter

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Node is either a group (and, or, not) with Items, or a condition on one attribute.
//
// Condition operators: equals, notEquals, contains, startsWith, endsWith,
// wildcard (the value may use * as a wildcard), present, notPresent,
// gte (on or after / at least), lte (on or before / at most), withinDays
// (a time attribute is within the last N days).
type Node struct {
	Op    string  `json:"op"`
	Attr  string  `json:"attr,omitempty"`
	Value string  `json:"value,omitempty"`
	Items []*Node `json:"items,omitempty"`
}

// AttrInfo is what the schema says about an attribute.
type AttrInfo struct {
	Name         string
	Kind         string // string, integer, boolean, time, dn, binary, other
	CanEqual     bool
	CanOrder     bool
	CanSubstring bool
}

// Options tune Build. Lookup may be nil (no schema: everything is accepted and
// treated as a string). Now defaults to time.Now.
type Options struct {
	Lookup func(attr string) (AttrInfo, bool)
	Now    time.Time
}

// Result is a successfully built filter.
type Result struct {
	Filter  string `json:"filter"`
	Explain string `json:"explain"`
}

// Error points at the offending node ("", "items[1]", "items[1].items[0]").
type Error struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (e Error) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

// Build validates the tree and returns the filter and its explanation. It
// reports every problem it finds, not just the first.
func Build(n *Node, opt Options) (*Result, []Error) {
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	b := &builder{opt: opt}
	if n == nil {
		return nil, []Error{{Message: "the query is empty"}}
	}
	filter, lines := b.node(n, "", 0)
	if len(b.errs) > 0 {
		return nil, b.errs
	}
	return &Result{Filter: filter, Explain: strings.Join(lines, "\n")}, nil
}

// Escape escapes a value for use inside a filter (RFC 4515 section 3).
func Escape(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			sb.WriteString(`\5c`)
		case '*':
			sb.WriteString(`\2a`)
		case '(':
			sb.WriteString(`\28`)
		case ')':
			sb.WriteString(`\29`)
		case 0:
			sb.WriteString(`\00`)
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// escapeKeepStar escapes everything except the * wildcard.
func escapeKeepStar(s string) string {
	parts := strings.Split(s, "*")
	for i := range parts {
		parts[i] = Escape(parts[i])
	}
	return strings.Join(parts, "*")
}

type builder struct {
	opt  Options
	errs []Error
}

func (b *builder) fail(path, format string, args ...any) {
	b.errs = append(b.errs, Error{Path: path, Message: fmt.Sprintf(format, args...)})
}

func join(path, seg string) string {
	if path == "" {
		return seg
	}
	return path + "." + seg
}

// node returns the filter for n and its explanation lines (indented by depth).
func (b *builder) node(n *Node, path string, depth int) (string, []string) {
	if n == nil {
		b.fail(path, "empty item")
		return "", nil
	}
	switch n.Op {
	case "and", "or", "not":
		return b.group(n, path, depth)
	default:
		f, text := b.condition(n, path)
		return f, []string{strings.Repeat("  ", depth) + text}
	}
}

func (b *builder) group(n *Node, path string, depth int) (string, []string) {
	if len(n.Items) == 0 {
		b.fail(path, "a group needs at least one condition")
		return "", nil
	}
	if n.Op == "not" && len(n.Items) != 1 {
		b.fail(path, "NOT applies to exactly one item")
		return "", nil
	}
	var filters []string
	var sub [][]string
	for i, it := range n.Items {
		f, l := b.node(it, join(path, fmt.Sprintf("items[%d]", i)), depth+1)
		filters = append(filters, f)
		sub = append(sub, l)
	}
	if len(b.errs) > 0 {
		return "", nil
	}
	pad := strings.Repeat("  ", depth)

	// A single item needs no wrapper: (&(a=1)) is just (a=1).
	if n.Op != "not" && len(filters) == 1 {
		return filters[0], unindentOne(sub[0])
	}
	var head, prefix string
	switch n.Op {
	case "and":
		head, prefix = "All of these must be true:", "&"
	case "or":
		head, prefix = "At least one of these must be true:", "|"
	default:
		head, prefix = "This must NOT be true:", "!"
	}
	lines := []string{pad + head}
	for _, l := range sub {
		lines = append(lines, l...)
	}
	return "(" + prefix + strings.Join(filters, "") + ")", lines
}

// unindentOne is used when a single-item group collapses: its child was indented
// one level deeper than the group it replaces.
func unindentOne(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimPrefix(l, "  ")
	}
	return out
}

var (
	// An attribute description: a name or numeric OID, optionally with ;options.
	attrRe     = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9-]*|[0-9]+(\.[0-9]+)+)(;[A-Za-z0-9-]+)*$`)
	intRe      = regexp.MustCompile(`^-?[0-9]{1,18}$`)
	generalRe  = regexp.MustCompile(`^[0-9]{4,14}(\.[0-9]+)?(Z|[+-][0-9]{2}([0-9]{2})?)?$`)
	validOps   = map[string]bool{"equals": true, "notEquals": true, "contains": true, "startsWith": true, "endsWith": true, "wildcard": true, "present": true, "notPresent": true, "gte": true, "lte": true, "withinDays": true}
	needsValue = func(op string) bool { return op != "present" && op != "notPresent" }
)

func (b *builder) condition(n *Node, path string) (string, string) {
	if !validOps[n.Op] {
		if n.Op == "" {
			b.fail(path, "missing operator")
		} else {
			b.fail(path, "unknown operator %q", n.Op)
		}
		return "", ""
	}
	attr := strings.TrimSpace(n.Attr)
	if attr == "" {
		b.fail(path, "choose an attribute")
		return "", ""
	}
	if !attrRe.MatchString(attr) {
		b.fail(path, "%q is not a valid attribute name", attr)
		return "", ""
	}
	if needsValue(n.Op) && n.Value == "" {
		b.fail(path, "%s needs a value", attr)
		return "", ""
	}

	info := AttrInfo{Name: attr, Kind: "string", CanEqual: true, CanOrder: true, CanSubstring: true}
	if b.opt.Lookup != nil && !strings.Contains(attr, ";") {
		got, ok := b.opt.Lookup(attr)
		if !ok {
			b.fail(path, "%q is not an attribute of this server's schema", attr)
			return "", ""
		}
		info = got
		if info.Name == "" {
			info.Name = attr
		}
	}
	label := info.Name

	// Capabilities.
	switch n.Op {
	case "contains", "startsWith", "endsWith", "wildcard":
		if !info.CanSubstring || info.Kind == "binary" {
			b.fail(path, "%s cannot be searched with %q: the server has no substring matching for it", label, opName(n.Op))
			return "", ""
		}
	case "equals", "notEquals":
		if !info.CanEqual || info.Kind == "binary" {
			b.fail(path, "%s cannot be compared for equality: the server has no equality matching for it (use \"exists\" instead)", label)
			return "", ""
		}
	case "gte", "lte", "withinDays":
		if !info.CanOrder && n.Op != "withinDays" {
			b.fail(path, "%s cannot be compared with %q: the server has no ordering rule for it", label, opName(n.Op))
			return "", ""
		}
	}

	switch n.Op {
	case "present":
		return "(" + attr + "=*)", label + " exists (has any value)"
	case "notPresent":
		return "(!(" + attr + "=*))", label + " does not exist (has no value)"
	case "contains":
		return "(" + attr + "=*" + Escape(n.Value) + "*)", fmt.Sprintf("%s contains %q", label, n.Value)
	case "startsWith":
		return "(" + attr + "=" + Escape(n.Value) + "*)", fmt.Sprintf("%s starts with %q", label, n.Value)
	case "endsWith":
		return "(" + attr + "=*" + Escape(n.Value) + ")", fmt.Sprintf("%s ends with %q", label, n.Value)
	case "wildcard":
		return "(" + attr + "=" + escapeKeepStar(n.Value) + ")", fmt.Sprintf("%s matches the pattern %q (* stands for any text)", label, n.Value)
	case "withinDays":
		return b.withinDays(n, attr, label, info, path)
	}

	// equals, notEquals, gte, lte: the value depends on the attribute type.
	val, shown, ok := b.typedValue(n.Value, info, path)
	if !ok {
		return "", ""
	}
	switch n.Op {
	case "equals":
		return "(" + attr + "=" + val + ")", fmt.Sprintf("%s is %s", label, shown)
	case "notEquals":
		return "(!(" + attr + "=" + val + "))", fmt.Sprintf("%s is not %s (entries without %s also match)", label, shown, label)
	case "gte":
		return "(" + attr + ">=" + val + ")", fmt.Sprintf("%s %s %s", label, cmpWords(info.Kind, true), shown)
	default: // lte
		return "(" + attr + "<=" + val + ")", fmt.Sprintf("%s %s %s", label, cmpWords(info.Kind, false), shown)
	}
}

func opName(op string) string {
	switch op {
	case "contains":
		return "contains"
	case "startsWith":
		return "starts with"
	case "endsWith":
		return "ends with"
	case "wildcard":
		return "pattern"
	case "gte":
		return "at least / after"
	case "lte":
		return "at most / before"
	}
	return op
}

func cmpWords(kind string, gte bool) string {
	if kind == "time" {
		if gte {
			return "is on or after"
		}
		return "is on or before"
	}
	if gte {
		return "is greater than or equal to"
	}
	return "is less than or equal to"
}

// typedValue converts the user's text to the filter value for the attribute
// type, plus how to show it in the explanation.
func (b *builder) typedValue(v string, info AttrInfo, path string) (filter, shown string, ok bool) {
	switch info.Kind {
	case "boolean":
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "yes", "1":
			return "TRUE", "true", true
		case "false", "no", "0":
			return "FALSE", "false", true
		}
		b.fail(path, "%s is a yes/no attribute: use true or false", info.Name)
		return "", "", false
	case "integer":
		s := strings.TrimSpace(v)
		if !intRe.MatchString(s) {
			b.fail(path, "%s needs a whole number, got %q", info.Name, v)
			return "", "", false
		}
		return s, s, true
	case "time":
		g, human, err := generalizedTime(v)
		if err != nil {
			b.fail(path, "%s needs a date such as 2026-11-19 or 2026-11-19T10:30 (UTC): %v", info.Name, err)
			return "", "", false
		}
		return g, human, true
	case "binary":
		b.fail(path, "%s holds binary data and cannot be compared", info.Name)
		return "", "", false
	}
	return Escape(v), strconv.Quote(v), true
}

func (b *builder) withinDays(n *Node, attr, label string, info AttrInfo, path string) (string, string) {
	if info.Kind != "time" {
		b.fail(path, "%s is not a date/time attribute, so \"in the last N days\" does not apply", label)
		return "", ""
	}
	days, err := strconv.Atoi(strings.TrimSpace(n.Value))
	if err != nil || days < 1 || days > 36500 {
		b.fail(path, "%s: give a number of days between 1 and 36500", label)
		return "", ""
	}
	since := b.opt.Now.UTC().Add(-time.Duration(days) * 24 * time.Hour)
	return "(" + attr + ">=" + since.Format("20060102150405") + "Z)",
		fmt.Sprintf("%s is within the last %d day(s) (since %s UTC)", label, days, since.Format("2006-01-02 15:04"))
}

// generalizedTime accepts a date or date-time (UTC unless it carries an
// offset) or an existing LDAP GeneralizedTime, and returns the LDAP form.
func generalizedTime(v string) (ldap, human string, err error) {
	s := strings.TrimSpace(v)
	if generalRe.MatchString(s) && len(s) >= 8 && !strings.Contains(s, "-") {
		return s, s, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, e := time.Parse(layout, s); e == nil {
			t = t.UTC()
			return t.Format("20060102150405") + "Z", t.Format("2006-01-02 15:04") + " UTC", nil
		}
	}
	return "", "", fmt.Errorf("could not read %q", v)
}
