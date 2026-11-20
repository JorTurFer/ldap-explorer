package recipes

import (
	"strings"
	"testing"
	"time"

	"github.com/JorTurFer/ldap-explorer/internal/ldapfilter"
)

var now = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

func set(names ...string) func(string) bool {
	m := map[string]bool{}
	for _, n := range names {
		m[strings.ToLower(n)] = true
	}
	return func(n string) bool { return m[strings.ToLower(n)] }
}

// Servers as their schemas look, trimmed to what the recipes look at.
var servers = map[string]*Env{
	"openldap": {Now: now, HasSchema: true,
		Attr:  set("cn", "sn", "givenName", "uid", "mail", "departmentNumber", "member", "uniqueMember", "memberUid", "createTimestamp", "modifyTimestamp"),
		Class: set("person", "organizationalPerson", "inetOrgPerson", "groupOfNames", "groupOfUniqueNames", "posixGroup")},
	"389ds": {Now: now, HasSchema: true,
		Attr:  set("cn", "sn", "uid", "mail", "member", "uniqueMember", "memberOf", "createTimestamp", "modifyTimestamp", "nsAccountLock", "passwordExpirationTime"),
		Class: set("person", "inetOrgPerson", "groupOfNames", "groupOfUniqueNames")},
	"ad": {Now: now, HasSchema: true,
		Attr:  set("cn", "displayName", "sAMAccountName", "userPrincipalName", "mail", "department", "member", "memberOf", "whenCreated", "whenChanged"),
		Class: set("person", "organizationalPerson", "user", "group")},
	"ppolicy": {Now: now, HasSchema: true,
		Attr:  set("cn", "uid", "mail", "pwdAccountLockedTime", "krbPrincipalExpiration"),
		Class: set("person", "inetOrgPerson")},
	"bare":     {Now: now, HasSchema: true, Attr: set(), Class: set()},
	"noschema": {Now: now, HasSchema: false, Attr: set(), Class: set()},
}

func TestAvailabilityPerServer(t *testing.T) {
	want := map[string]map[string]bool{
		"openldap": {"find-entry": true, "find-person": true, "people-without-email": true, "people-by-department": true, "group-members": true, "user-groups": true,
			"locked-accounts": false, "expired-accounts": false, "recently-created": true, "recently-modified": true,
			"by-class": true, "with-attribute": true, "missing-attribute": true},
		"389ds": {"find-person": true, "people-without-email": true, "people-by-department": false, "group-members": true, "user-groups": true,
			"locked-accounts": true, "expired-accounts": true, "recently-created": true, "recently-modified": true},
		"ad": {"find-person": true, "people-without-email": true, "people-by-department": true, "group-members": true, "user-groups": true,
			"locked-accounts": false, "expired-accounts": false, "recently-created": true, "recently-modified": true},
		"ppolicy": {"locked-accounts": true, "expired-accounts": true, "group-members": false, "user-groups": false, "recently-created": false},
		"bare":    {"find-entry": false, "find-person": false, "people-without-email": false, "group-members": false, "recently-created": false, "by-class": true, "missing-attribute": true},
		// without a schema only the recipes that need no schema knowledge are offered
		"noschema": {"find-entry": false, "find-person": false, "group-members": false, "locked-accounts": false, "by-class": true, "with-attribute": true, "missing-attribute": true},
	}
	for server, expect := range want {
		infos := map[string]Info{}
		for _, i := range List(servers[server]) {
			infos[i.ID] = i
		}
		if len(infos) != len(all) {
			t.Fatalf("%s: %d recipes listed, want %d", server, len(infos), len(all))
		}
		for id, avail := range expect {
			i, ok := infos[id]
			if !ok {
				t.Fatalf("%s: unknown recipe %s", server, id)
			}
			if i.Available != avail {
				t.Errorf("%s / %s: available=%v, want %v (reason %q)", server, id, i.Available, avail, i.Reason)
			}
			if !i.Available && i.Reason == "" {
				t.Errorf("%s / %s: unavailable without a reason", server, id)
			}
			if i.Available && i.Reason != "" {
				t.Errorf("%s / %s: available but has a reason %q", server, id, i.Reason)
			}
		}
	}
}

func TestUnavailableReasonsNameWhatIsMissing(t *testing.T) {
	infos := map[string]Info{}
	for _, i := range List(servers["openldap"]) {
		infos[i.ID] = i
	}
	if r := infos["locked-accounts"].Reason; !strings.Contains(r, "nsAccountLock") || !strings.Contains(r, "pwdAccountLockedTime") {
		t.Errorf("locked-accounts reason: %q", r)
	}
	for _, i := range List(servers["noschema"]) {
		if i.ID == "find-person" && !strings.Contains(i.Reason, "does not publish its schema") {
			t.Errorf("no-schema reason: %q", i.Reason)
		}
	}
}

// Every plan must be a query the filter builder accepts for that same server.
func lookupFor(e *Env) func(string) (ldapfilter.AttrInfo, bool) {
	return func(a string) (ldapfilter.AttrInfo, bool) {
		if !e.Attr(a) && !strings.EqualFold(a, "objectClass") {
			return ldapfilter.AttrInfo{}, false
		}
		l := strings.ToLower(a)
		isTime := strings.HasSuffix(l, "timestamp") || strings.HasPrefix(l, "when") || strings.HasSuffix(l, "expirationtime") || strings.HasSuffix(l, "expiration") || l == "pwdaccountlockedtime"
		if isTime {
			return ldapfilter.AttrInfo{Name: a, Kind: "time", CanEqual: true, CanOrder: true}, true
		}
		if l == "objectclass" {
			return ldapfilter.AttrInfo{Name: a, Kind: "string", CanEqual: true}, true
		}
		return ldapfilter.AttrInfo{Name: a, Kind: "string", CanEqual: true, CanSubstring: true}, true
	}
}

func build(t *testing.T, server, id string, params map[string]string) (*Plan, string) {
	t.Helper()
	e := servers[server]
	plan, err := Apply(e, id, params)
	if err != nil {
		t.Fatalf("%s / %s: %v", server, id, err)
	}
	res, errs := ldapfilter.Build(plan.Query, ldapfilter.Options{Lookup: lookupFor(e), Now: now})
	if len(errs) > 0 {
		t.Fatalf("%s / %s: the filter builder rejects the plan: %v", server, id, errs)
	}
	return plan, res.Filter
}

func TestPlansAreValidAndAdaptToTheServer(t *testing.T) {
	for _, tc := range []struct {
		server, id string
		params     map[string]string
		filter     string
		attrs      string
	}{
		{"openldap", "find-entry", map[string]string{"text": "ali"},
			"(|(cn=*ali*)(uid=*ali*)(mail=*ali*)(sn=*ali*)(givenName=*ali*))", "cn, uid, mail"},
		{"ad", "find-entry", map[string]string{"text": "ali"},
			"(|(cn=*ali*)(displayName=*ali*)(sAMAccountName=*ali*)(userPrincipalName=*ali*)(mail=*ali*))", "cn, sAMAccountName, mail"},
		{"openldap", "find-person", map[string]string{"text": "ali"},
			"(|(cn=*ali*)(givenName=*ali*)(sn=*ali*)(uid=*ali*)(mail=*ali*))", "cn, uid, mail"},
		{"ad", "find-person", map[string]string{"text": "ali"},
			"(|(cn=*ali*)(displayName=*ali*)(sAMAccountName=*ali*)(userPrincipalName=*ali*)(mail=*ali*))", "cn, sAMAccountName, mail"},
		{"openldap", "people-without-email", nil,
			"(&(|(objectClass=inetOrgPerson)(objectClass=person))(!(mail=*)))", "cn, uid"},
		{"ad", "people-without-email", nil, "(&(|(objectClass=person)(objectClass=user))(!(mail=*)))", "cn, sAMAccountName"},
		{"openldap", "people-by-department", map[string]string{"text": "Sales"}, "(departmentNumber=*Sales*)", "cn, uid, departmentNumber"},
		{"ad", "people-by-department", map[string]string{"text": "Sales"}, "(department=*Sales*)", "cn, sAMAccountName, department"},
		{"openldap", "group-members", map[string]string{"group": "adm"},
			"(&(|(objectClass=groupOfNames)(objectClass=groupOfUniqueNames)(objectClass=posixGroup))(cn=*adm*))", "cn, member, uniqueMember, memberUid"},
		{"ad", "group-members", map[string]string{"group": "adm"}, "(&(objectClass=group)(cn=*adm*))", "cn, member"},
		{"openldap", "user-groups", map[string]string{"dn": "uid=a,dc=x", "uid": "a"},
			"(&(|(objectClass=groupOfNames)(objectClass=groupOfUniqueNames)(objectClass=posixGroup))(|(member=uid=a,dc=x)(uniqueMember=uid=a,dc=x)(memberUid=a)))", "cn, description"},
		{"389ds", "locked-accounts", nil, "(nsAccountLock=true)", "cn, uid, nsAccountLock"}, // a Boolean attribute would be normalised to TRUE
		{"ppolicy", "locked-accounts", nil, "(pwdAccountLockedTime=*)", "cn, uid, pwdAccountLockedTime"},
		{"389ds", "expired-accounts", nil, "(passwordExpirationTime<=20261119120000Z)", "cn, uid, passwordExpirationTime"},
		{"ppolicy", "expired-accounts", nil, "(krbPrincipalExpiration<=20261119120000Z)", "cn, uid, krbPrincipalExpiration"},
		{"openldap", "recently-created", map[string]string{"days": "3"}, "(createTimestamp>=20261116120000Z)", "cn, uid, createTimestamp"},
		{"ad", "recently-created", nil, "(whenCreated>=20261112120000Z)", "cn, sAMAccountName, whenCreated"}, // default 7 days
		{"389ds", "recently-modified", map[string]string{"days": "1"}, "(modifyTimestamp>=20261118120000Z)", "cn, uid, modifyTimestamp"},
		{"bare", "by-class", map[string]string{"class": "person"}, "(objectClass=person)", "cn"},
		{"openldap", "with-attribute", map[string]string{"attribute": "mail"}, "(mail=*)", "mail"},
		{"openldap", "missing-attribute", map[string]string{"class": "inetOrgPerson", "attribute": "mail"}, "(&(objectClass=inetOrgPerson)(!(mail=*)))", "cn"},
	} {
		t.Run(tc.server+"/"+tc.id, func(t *testing.T) {
			plan, filter := build(t, tc.server, tc.id, tc.params)
			if filter != tc.filter {
				t.Errorf("filter\n got %s\nwant %s", filter, tc.filter)
			}
			if plan.Attrs != tc.attrs {
				t.Errorf("attrs %q, want %q", plan.Attrs, tc.attrs)
			}
			if plan.Scope != "sub" || plan.Title == "" {
				t.Errorf("scope %q title %q", plan.Scope, plan.Title)
			}
		})
	}
}

func TestApplyErrors(t *testing.T) {
	e := servers["openldap"]
	for _, tc := range []struct {
		name, id string
		params   map[string]string
		msg      string
	}{
		{"unknown recipe", "nope", nil, "unknown recipe"},
		{"required parameter", "find-person", map[string]string{"text": "  "}, "Name, login or email is required"},
		{"bad days", "recently-created", map[string]string{"days": "soon"}, "between 1 and 36500"},
		{"days out of range", "recently-created", map[string]string{"days": "0"}, "between 1 and 36500"},
		{"unavailable on this server", "locked-accounts", nil, "cannot answer it"},
		{"user-groups needs a DN", "user-groups", map[string]string{"uid": "a"}, "User DN is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Apply(e, tc.id, tc.params)
			if err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("error %v, want it to contain %q", err, tc.msg)
			}
		})
	}
	if _, err := Apply(e, "nope", nil); err != ErrUnknown {
		t.Errorf("unknown recipe should return ErrUnknown, got %v", err)
	}
}

func TestUserGroupsWithOnlyPosixMembership(t *testing.T) {
	e := &Env{Now: now, HasSchema: true, Attr: set("cn", "memberUid"), Class: set("posixGroup")}
	_, err := Apply(e, "user-groups", map[string]string{"dn": "uid=a,dc=x"})
	if err == nil || !strings.Contains(err.Error(), "enter the login too") {
		t.Errorf("error: %v", err)
	}
	plan, err := Apply(e, "user-groups", map[string]string{"dn": "uid=a,dc=x", "uid": "a"})
	if err != nil || plan.Query == nil {
		t.Fatalf("%v", err)
	}
}

func TestUserGroupsMentionsMemberOf(t *testing.T) {
	plan, err := Apply(servers["389ds"], "user-groups", map[string]string{"dn": "uid=a,dc=x"})
	if err != nil || !strings.Contains(plan.Note, "memberOf") {
		t.Errorf("note: %v %v", plan, err)
	}
	plan, _ = Apply(servers["openldap"], "user-groups", map[string]string{"dn": "uid=a,dc=x"})
	if plan.Note != "" {
		t.Errorf("no note expected without memberOf: %q", plan.Note)
	}
}

func TestValuesAreNotInterpretedAsFilterSyntax(t *testing.T) {
	_, f := build(t, "openldap", "find-person", map[string]string{"text": "x)(uid=*"})
	if strings.Count(f, "(uid=") != 1 || !strings.Contains(f, `\29\28uid=\2a`) {
		t.Errorf("value leaked into the filter structure: %s", f)
	}
}

func TestCatalogIsConsistent(t *testing.T) {
	ids := map[string]bool{}
	for _, r := range all {
		if ids[r.ID] {
			t.Errorf("duplicate id %s", r.ID)
		}
		ids[r.ID] = true
		if r.Title == "" || r.Description == "" || r.Category == "" || r.Build == nil {
			t.Errorf("%s is incomplete", r.ID)
		}
		for _, p := range r.Params {
			if p.Name == "" || p.Label == "" || !strings.Contains(" text days dn class attribute ", " "+p.Kind+" ") {
				t.Errorf("%s: bad parameter %+v", r.ID, p)
			}
		}
	}
}

// The UI iterates over params, so the JSON must always carry an array.
func TestParamsAreNeverNil(t *testing.T) {
	for _, i := range List(servers["openldap"]) {
		if i.Params == nil {
			t.Errorf("%s: params is nil (would be JSON null)", i.ID)
		}
	}
}
