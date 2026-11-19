package ldapschema

import (
	"errors"
	"fmt"
	"strings"
)

// token kinds
const (
	tkOpen = iota
	tkClose
	tkDollar
	tkQuoted
	tkWord
)

type token struct {
	kind int
	text string
}

func tokenize(s string) ([]token, error) {
	var out []token
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			out = append(out, token{tkOpen, "("})
			i++
		case c == ')':
			out = append(out, token{tkClose, ")"})
			i++
		case c == '$':
			out = append(out, token{tkDollar, "$"})
			i++
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, errors.New("unterminated quoted string")
			}
			out = append(out, token{tkQuoted, unescape(s[i+1 : i+1+j])})
			i += j + 2
		default:
			j := i
			for j < len(s) && !strings.ContainsRune(" \t\r\n()$'", rune(s[j])) {
				j++
			}
			out = append(out, token{tkWord, s[i:j]})
			i = j
		}
	}
	return out, nil
}

// unescape decodes the \27 and \5C escapes allowed in qdstring (RFC 4512).
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	r := strings.NewReplacer(`\27`, "'", `\5C`, `\`, `\5c`, `\`)
	return r.Replace(s)
}

// description is the generic shape of an attributeTypes/objectClasses value:
// the OID followed by KEYWORD arguments.
type description struct {
	oid    string
	fields map[string][]string // keyword -> arguments (empty slice for flags)
}

var flagKeywords = map[string]bool{
	"OBSOLETE": true, "SINGLE-VALUE": true, "COLLECTIVE": true, "NO-USER-MODIFICATION": true,
	"ABSTRACT": true, "STRUCTURAL": true, "AUXILIARY": true,
}

func parseDescription(raw string) (*description, error) {
	toks, err := tokenize(raw)
	if err != nil {
		return nil, err
	}
	if len(toks) < 3 || toks[0].kind != tkOpen || toks[len(toks)-1].kind != tkClose {
		return nil, errors.New("not a parenthesised description")
	}
	toks = toks[1 : len(toks)-1]
	if toks[0].kind != tkWord {
		return nil, errors.New("missing OID")
	}
	d := &description{oid: toks[0].text, fields: map[string][]string{}}
	for i := 1; i < len(toks); {
		if toks[i].kind != tkWord {
			return nil, fmt.Errorf("unexpected %q", toks[i].text)
		}
		kw := strings.ToUpper(toks[i].text)
		i++
		args := []string{}
		if flagKeywords[kw] {
			d.fields[kw] = args
			continue
		}
		if i >= len(toks) {
			return nil, fmt.Errorf("keyword %s has no value", kw)
		}
		if toks[i].kind == tkOpen { // ( a $ b ) or ( 'a' 'b' )
			i++
			for i < len(toks) && toks[i].kind != tkClose {
				if toks[i].kind != tkDollar {
					args = append(args, toks[i].text)
				}
				i++
			}
			if i >= len(toks) {
				return nil, fmt.Errorf("unterminated list after %s", kw)
			}
			i++ // closing paren
		} else {
			args = append(args, toks[i].text)
			i++
		}
		d.fields[kw] = args
	}
	return d, nil
}

func first(d *description, kw string) string {
	if v := d.fields[kw]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func parseAttribute(raw string) (*Attribute, error) {
	d, err := parseDescription(raw)
	if err != nil {
		return nil, err
	}
	a := &Attribute{
		OID:      d.oid,
		Names:    d.fields["NAME"],
		Desc:     first(d, "DESC"),
		Sup:      first(d, "SUP"),
		Equality: first(d, "EQUALITY"),
		Ordering: first(d, "ORDERING"),
		Substr:   first(d, "SUBSTR"),
		Usage:    first(d, "USAGE"),
	}
	if syn := first(d, "SYNTAX"); syn != "" {
		if i := strings.IndexByte(syn, '{'); i >= 0 { // 1.3.6...15{32768}
			syn = syn[:i]
		}
		a.Syntax = syn
	}
	_, a.Obsolete = d.fields["OBSOLETE"]
	_, a.SingleValue = d.fields["SINGLE-VALUE"]
	_, a.NoUserMod = d.fields["NO-USER-MODIFICATION"]
	return a, nil
}

func parseClass(raw string) (*ObjectClass, error) {
	d, err := parseDescription(raw)
	if err != nil {
		return nil, err
	}
	c := &ObjectClass{
		OID:   d.oid,
		Names: d.fields["NAME"],
		Desc:  first(d, "DESC"),
		Sup:   d.fields["SUP"],
		Must:  d.fields["MUST"],
		May:   d.fields["MAY"],
		Kind:  "STRUCTURAL",
	}
	_, c.Obsolete = d.fields["OBSOLETE"]
	for _, k := range []string{"ABSTRACT", "AUXILIARY", "STRUCTURAL"} {
		if _, ok := d.fields[k]; ok {
			c.Kind = k
		}
	}
	return c, nil
}
