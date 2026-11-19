package ldapschema

import "strings"

const rfc4517 = "1.3.6.1.4.1.1466.115.121.1."

type syntaxInfo struct{ name, kind string }

// Syntaxes a query builder cares about. Anything else is reported as "other".
var syntaxes = map[string]syntaxInfo{
	rfc4517 + "5":    {"Binary", "binary"},
	rfc4517 + "7":    {"Boolean", "boolean"},
	rfc4517 + "8":    {"Certificate", "binary"},
	rfc4517 + "9":    {"Certificate List", "binary"},
	rfc4517 + "10":   {"Certificate Pair", "binary"},
	rfc4517 + "11":   {"Country String", "string"},
	rfc4517 + "12":   {"DN", "dn"},
	rfc4517 + "15":   {"Directory String", "string"},
	rfc4517 + "24":   {"Generalized Time", "time"},
	rfc4517 + "26":   {"IA5 String", "string"},
	rfc4517 + "27":   {"Integer", "integer"},
	rfc4517 + "28":   {"JPEG", "binary"},
	rfc4517 + "34":   {"Name and Optional UID", "dn"},
	rfc4517 + "36":   {"Numeric String", "string"},
	rfc4517 + "38":   {"OID", "string"},
	rfc4517 + "39":   {"Other Mailbox", "string"},
	rfc4517 + "40":   {"Octet String", "binary"},
	rfc4517 + "41":   {"Postal Address", "string"},
	rfc4517 + "44":   {"Printable String", "string"},
	rfc4517 + "50":   {"Telephone Number", "string"},
	"1.3.6.1.1.16.1": {"UUID", "string"},
}

// SyntaxName returns a readable name for a syntax OID ("" when unknown).
func SyntaxName(oid string) string { return syntaxes[strings.TrimSpace(oid)].name }

// SyntaxKind returns string, integer, boolean, time, dn, binary or other.
func SyntaxKind(oid string) string {
	if k := syntaxes[strings.TrimSpace(oid)].kind; k != "" {
		return k
	}
	return "other"
}
