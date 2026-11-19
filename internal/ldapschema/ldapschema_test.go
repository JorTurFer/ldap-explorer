package ldapschema

import (
	"reflect"
	"strings"
	"testing"
)

// Values as published by OpenLDAP.
var (
	attrCN     = "( 2.5.4.3 NAME ( 'cn' 'commonName' ) DESC 'RFC4519: common name(s) for which the entity is known by' SUP name )"
	attrName   = "( 2.5.4.41 NAME 'name' DESC 'RFC4519: common supertype of name attributes' EQUALITY caseIgnoreMatch SUBSTR caseIgnoreSubstringsMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15{32768} )"
	attrMail   = "( 0.9.2342.19200300.100.1.3 NAME ( 'mail' 'rfc822Mailbox' ) DESC 'RFC1274: RFC822 Mailbox' EQUALITY caseIgnoreIA5Match SUBSTR caseIgnoreIA5SubstringsMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.26{256} )"
	attrCreate = "( 2.5.18.1 NAME 'createTimestamp' DESC 'RFC4512: time which object was created' EQUALITY generalizedTimeMatch ORDERING generalizedTimeOrderingMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.24 SINGLE-VALUE NO-USER-MODIFICATION USAGE directoryOperation )"
	classPers  = "( 2.5.6.6 NAME 'person' DESC 'RFC2256: a person' SUP top STRUCTURAL MUST ( sn $ cn ) MAY ( userPassword $ telephoneNumber $ seeAlso $ description ) )"
	classTop   = "( 2.5.6.0 NAME 'top' DESC 'top of the superclass chain' ABSTRACT MUST objectClass )"
	classAux   = "( 1.3.6.1.1.1.2.0 NAME 'posixAccount' DESC 'Abstraction of an account with POSIX attributes' SUP top AUXILIARY MUST ( cn $ uid ) MAY ( userPassword ) )"
)

func TestParseAttributes(t *testing.T) {
	s, errs := Parse([]string{attrCN, attrName, attrMail, attrCreate}, nil)
	if len(errs) != 0 {
		t.Fatal(errs)
	}

	name := s.Lookup("name")
	if name == nil || name.Syntax != rfc4517+"15" || !name.CanSubstring() || name.CanOrder() {
		t.Fatalf("name: %+v", name)
	}

	// cn has no matching rules of its own: everything is inherited through SUP.
	cn := s.Lookup("commonName")
	if cn == nil || cn.Name() != "cn" || !reflect.DeepEqual(cn.Names, []string{"cn", "commonName"}) {
		t.Fatalf("cn: %+v", cn)
	}
	if cn.Syntax != rfc4517+"15" || !cn.CanEqual() || !cn.CanSubstring() || cn.Kind() != "string" || cn.SyntaxName() != "Directory String" {
		t.Errorf("cn did not inherit from name: %+v", cn)
	}

	mail := s.Lookup("MAIL") // case-insensitive, and by alias
	if mail != s.Lookup("rfc822mailbox") || mail.Syntax != rfc4517+"26" {
		t.Errorf("mail: %+v", mail)
	}
	if s.Lookup("0.9.2342.19200300.100.1.3") != mail {
		t.Error("lookup by OID")
	}

	ct := s.Lookup("createTimestamp")
	if !ct.Operational() || !ct.NoUserMod || !ct.SingleValue || !ct.CanOrder() || ct.Kind() != "time" {
		t.Errorf("createTimestamp: %+v", ct)
	}
	if mail.Operational() || mail.Usage != "" {
		t.Errorf("mail should be a user attribute: %+v", mail)
	}
	if s.Lookup("nonexistent") != nil {
		t.Error("unknown attribute should not be found")
	}
}

func TestParseObjectClasses(t *testing.T) {
	s, errs := Parse(nil, []string{classPers, classTop, classAux})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	byName := map[string]*ObjectClass{}
	for _, c := range s.Classes {
		byName[c.Name()] = c
	}
	p := byName["person"]
	if p.Kind != "STRUCTURAL" || !reflect.DeepEqual(p.Sup, []string{"top"}) ||
		!reflect.DeepEqual(p.Must, []string{"sn", "cn"}) || len(p.May) != 4 {
		t.Errorf("person: %+v", p)
	}
	if byName["top"].Kind != "ABSTRACT" || !reflect.DeepEqual(byName["top"].Must, []string{"objectClass"}) {
		t.Errorf("top: %+v", byName["top"])
	}
	if byName["posixAccount"].Kind != "AUXILIARY" {
		t.Errorf("posixAccount: %+v", byName["posixAccount"])
	}
	// sorted by name
	if s.Classes[0].Name() != "person" || s.Classes[2].Name() != "top" {
		t.Errorf("classes not sorted: %v %v %v", s.Classes[0].Name(), s.Classes[1].Name(), s.Classes[2].Name())
	}
}

func TestParseEscapesAndExtensions(t *testing.T) {
	s, errs := Parse([]string{
		`( 1.2.3.4 NAME 'x' DESC 'it\27s a \5C test' X-ORIGIN 'RFC 4519' X-LIST ( 'a' 'b' ) SYNTAX 1.3.6.1.4.1.1466.115.121.1.7 OBSOLETE )`,
	}, nil)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	a := s.Lookup("x")
	if a.Desc != `it's a \ test` || a.Kind() != "boolean" || !a.Obsolete {
		t.Errorf("%+v", a)
	}
}

func TestParseErrorsAreReportedAndSkipped(t *testing.T) {
	s, errs := Parse([]string{
		"not a description",
		"( 1.2.3 NAME 'unterminated )",
		"( 1.2.3 NAME )",
		attrName,
	}, []string{"( 2.5.6.6 SUP ( a $ b )"})
	if len(errs) != 4 {
		t.Fatalf("expected 4 errors, got %d: %v", len(errs), errs)
	}
	if len(s.Attributes) != 1 || s.Lookup("name") == nil {
		t.Errorf("the valid value should survive: %+v", s.Attributes)
	}
	if !strings.Contains(errs[0].Error(), "not a description") {
		t.Errorf("error should mention the value: %v", errs[0])
	}
}

func TestSupCycleDoesNotHang(t *testing.T) {
	s, errs := Parse([]string{
		"( 1.1 NAME 'a' SUP b )",
		"( 1.2 NAME 'b' SUP a )",
		"( 1.3 NAME 'c' SUP missing )",
	}, nil)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if s.Lookup("a").Syntax != "" || s.Lookup("c").CanEqual() {
		t.Error("nothing to inherit")
	}
}

func TestSyntaxKinds(t *testing.T) {
	for oid, want := range map[string]string{
		rfc4517 + "7": "boolean", rfc4517 + "12": "dn", rfc4517 + "15": "string", rfc4517 + "24": "time",
		rfc4517 + "27": "integer", rfc4517 + "28": "binary", rfc4517 + "40": "binary", "9.9.9": "other", "": "other",
	} {
		if got := SyntaxKind(oid); got != want {
			t.Errorf("SyntaxKind(%q) = %q, want %q", oid, got, want)
		}
	}
	if SyntaxName("9.9.9") != "" {
		t.Error("unknown syntax has no name")
	}
}
