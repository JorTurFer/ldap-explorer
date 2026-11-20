// Package ldapschema parses the schema descriptions an LDAP server publishes in
// its subschema entry (RFC 4512) and derives what a query builder needs to know:
// the syntax of each attribute and which matching rules it supports.
package ldapschema

import (
	"fmt"
	"sort"
	"strings"
)

// Attribute is an attributeTypes description.
type Attribute struct {
	Names       []string
	OID         string
	Desc        string
	Obsolete    bool
	Sup         string
	Equality    string
	Ordering    string
	Substr      string
	Syntax      string // OID without the {length} suffix
	SingleValue bool
	NoUserMod   bool
	Usage       string // userApplications (default), directoryOperation, dSAOperation, distributedOperation
}

// ObjectClass is an objectClasses description.
type ObjectClass struct {
	Names    []string
	OID      string
	Desc     string
	Obsolete bool
	Sup      []string
	Kind     string // STRUCTURAL (default), AUXILIARY or ABSTRACT
	Must     []string
	May      []string
}

// Schema holds the parsed schema, with attribute inheritance (SUP) resolved.
type Schema struct {
	Attributes []*Attribute
	Classes    []*ObjectClass
	byName     map[string]*Attribute
}

// Parse builds a Schema from the raw attributeTypes and objectClasses values.
// Values that cannot be parsed are skipped and reported in the returned errors.
func Parse(attributeTypes, objectClasses []string) (*Schema, []error) {
	s := &Schema{byName: map[string]*Attribute{}}
	var errs []error
	for _, raw := range attributeTypes {
		a, err := parseAttribute(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("attributeType %q: %w", abbreviate(raw), err))
			continue
		}
		s.Attributes = append(s.Attributes, a)
	}
	for _, raw := range objectClasses {
		c, err := parseClass(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("objectClass %q: %w", abbreviate(raw), err))
			continue
		}
		s.Classes = append(s.Classes, c)
	}
	for _, a := range s.Attributes {
		s.byName[strings.ToLower(a.OID)] = a
		for _, n := range a.Names {
			s.byName[strings.ToLower(n)] = a
		}
	}
	for _, a := range s.Attributes {
		s.inherit(a)
	}
	sort.Slice(s.Attributes, func(i, j int) bool { return s.Attributes[i].Name() < s.Attributes[j].Name() })
	sort.Slice(s.Classes, func(i, j int) bool { return s.Classes[i].Name() < s.Classes[j].Name() })
	return s, errs
}

// inherit fills Equality, Ordering, Substr and Syntax from the SUP chain.
func (s *Schema) inherit(a *Attribute) {
	seen := map[*Attribute]bool{a: true}
	for cur := a; cur.Sup != ""; {
		p := s.byName[strings.ToLower(cur.Sup)]
		if p == nil || seen[p] {
			return
		}
		seen[p] = true
		if a.Equality == "" {
			a.Equality = p.Equality
		}
		if a.Ordering == "" {
			a.Ordering = p.Ordering
		}
		if a.Substr == "" {
			a.Substr = p.Substr
		}
		if a.Syntax == "" {
			a.Syntax = p.Syntax
		}
		cur = p
	}
}

// Lookup finds an attribute by any of its names or by OID (case-insensitive).
func (s *Schema) Lookup(name string) *Attribute { return s.byName[strings.ToLower(name)] }

// Name returns the first (preferred) name, or the OID if it has none.
func (a *Attribute) Name() string {
	if len(a.Names) > 0 {
		return a.Names[0]
	}
	return a.OID
}

// Name returns the first (preferred) name, or the OID if it has none.
func (c *ObjectClass) Name() string {
	if len(c.Names) > 0 {
		return c.Names[0]
	}
	return c.OID
}

// Operational reports whether the attribute is an operational one (not returned
// by default and usually maintained by the server).
func (a *Attribute) Operational() bool {
	return a.Usage != "" && !strings.EqualFold(a.Usage, "userApplications")
}

// RulesUnknown reports that the schema says nothing about how this attribute can be compared:
// no equality, ordering or substring rule at all (not even inherited), and it is not binary.
// Active Directory publishes no matching rules, yet it answers equality and substring searches
// on most attributes, so for these we go by the attribute's syntax instead of refusing.
func (a *Attribute) RulesUnknown() bool {
	return a.Equality == "" && a.Ordering == "" && a.Substr == "" && a.Kind() != "binary"
}

// CanEqual, CanOrder and CanSubstring say which filter operators make sense: what the matching
// rules allow, or, when the schema declares none (RulesUnknown), what the syntax suggests.
func (a *Attribute) CanEqual() bool { return a.Equality != "" || a.RulesUnknown() }
func (a *Attribute) CanOrder() bool {
	return a.Ordering != "" || (a.RulesUnknown() && (a.Kind() == "time" || a.Kind() == "integer"))
}
func (a *Attribute) CanSubstring() bool {
	return a.Substr != "" || (a.RulesUnknown() && (a.Kind() == "string" || a.Kind() == "other"))
}

// Kind classifies the attribute value for a UI: string, integer, boolean, time,
// dn, binary or other.
func (a *Attribute) Kind() string { return SyntaxKind(a.Syntax) }

// SyntaxName is a human-readable name for the attribute's syntax.
func (a *Attribute) SyntaxName() string { return SyntaxName(a.Syntax) }

func abbreviate(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}
