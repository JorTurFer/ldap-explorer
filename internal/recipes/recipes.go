// Package recipes holds ready-made queries ("find a person", "members of a
// group", "accounts created recently") for people who do not know the schema.
//
// A recipe is a Go function that looks at what the connected server's schema
// really defines and builds a query only from attributes and classes that
// exist, so it is only offered when the server can answer it, and it adapts to
// the server (OpenLDAP, 389 Directory Server, Active Directory...).
package recipes

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"ldap-explorer/internal/ldapfilter"
)

// Param is one input a recipe asks for.
type Param struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Kind        string `json:"kind"` // text, days, dn, class, attribute
	Placeholder string `json:"placeholder,omitempty"`
	Help        string `json:"help,omitempty"`
	Required    bool   `json:"required"`
	Default     string `json:"default,omitempty"`
}

// Env is what a recipe may know about the server.
type Env struct {
	Now       time.Time
	HasSchema bool
	Attr      func(name string) bool // the schema defines this attribute (by name or alias)
	Class     func(name string) bool // the schema defines this object class
}

// Plan is the result of applying a recipe.
type Plan struct {
	Title string           `json:"title"`
	Query *ldapfilter.Node `json:"query"`
	Attrs string           `json:"attrs"` // attributes to show ("" = all)
	Scope string           `json:"scope"` // sub, one, base
	Note  string           `json:"note,omitempty"`
}

// Recipe is a ready-made query.
type Recipe struct {
	ID          string
	Title       string
	Category    string
	Description string
	Params      []Param
	// Unavailable returns why the server cannot answer this recipe, or "".
	Unavailable func(e *Env) string
	Build       func(e *Env, p map[string]string) (*Plan, error)
}

// Info is a Recipe as shown to the user for a given server.
type Info struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	Params      []Param `json:"params"`
	Available   bool    `json:"available"`
	Reason      string  `json:"reason,omitempty"`
}

// List returns every recipe with its availability on this server.
func List(e *Env) []Info {
	out := make([]Info, 0, len(all))
	for _, r := range all {
		reason := ""
		if r.Unavailable != nil {
			reason = r.Unavailable(e)
		}
		params := r.Params
		if params == nil {
			params = []Param{} // JSON [], never null
		}
		out = append(out, Info{ID: r.ID, Title: r.Title, Category: r.Category, Description: r.Description,
			Params: params, Available: reason == "", Reason: reason})
	}
	return out
}

// ErrUnknown is returned by Apply for an unknown recipe id.
var ErrUnknown = fmt.Errorf("unknown recipe")

// Apply builds the query of a recipe from the user's parameters.
func Apply(e *Env, id string, params map[string]string) (*Plan, error) {
	for _, r := range all {
		if r.ID != id {
			continue
		}
		if r.Unavailable != nil {
			if reason := r.Unavailable(e); reason != "" {
				return nil, fmt.Errorf("this server cannot answer it: %s", reason)
			}
		}
		clean := map[string]string{}
		for _, p := range r.Params {
			v := strings.TrimSpace(params[p.Name])
			if v == "" {
				v = p.Default
			}
			if v == "" && p.Required {
				return nil, fmt.Errorf("%s is required", p.Label)
			}
			if p.Kind == "days" && v != "" {
				if n, err := strconv.Atoi(v); err != nil || n < 1 || n > 36500 {
					return nil, fmt.Errorf("%s: give a number of days between 1 and 36500", p.Label)
				}
			}
			clean[p.Name] = v
		}
		plan, err := r.Build(e, clean)
		if err != nil {
			return nil, err
		}
		plan.Title = r.Title
		if plan.Scope == "" {
			plan.Scope = "sub"
		}
		return plan, nil
	}
	return nil, ErrUnknown
}

// ---------- helpers ----------

// present returns the names the schema defines, in order.
func (e *Env) present(names ...string) []string {
	var out []string
	for _, n := range names {
		if e.Attr(n) {
			out = append(out, n)
		}
	}
	return out
}

func (e *Env) presentClasses(names ...string) []string {
	var out []string
	for _, n := range names {
		if e.Class(n) {
			out = append(out, n)
		}
	}
	return out
}

func cond(op, attr, value string) *ldapfilter.Node {
	return &ldapfilter.Node{Op: op, Attr: attr, Value: value}
}
func group(op string, items ...*ldapfilter.Node) *ldapfilter.Node {
	return &ldapfilter.Node{Op: op, Items: items}
}

// anyOf is OR over the given items; a single item stays as it is.
func anyOf(items []*ldapfilter.Node) *ldapfilter.Node {
	if len(items) == 1 {
		return items[0]
	}
	return group("or", items...)
}

func classIs(classes []string) *ldapfilter.Node {
	items := make([]*ldapfilter.Node, len(classes))
	for i, c := range classes {
		items[i] = cond("equals", "objectClass", c)
	}
	return anyOf(items)
}

func needSchema(e *Env) string {
	if !e.HasSchema {
		return "the server does not publish its schema, so ldap-explorer cannot tell which attributes it has"
	}
	return ""
}

func missing(what string) string {
	return "this server's schema has no " + what
}

var (
	personClasses = []string{"inetOrgPerson", "organizationalPerson", "person", "user"}
	groupClasses  = []string{"groupOfNames", "groupOfUniqueNames", "posixGroup", "group", "groupOfMembers"}
)
