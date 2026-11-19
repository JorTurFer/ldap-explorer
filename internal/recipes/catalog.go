package recipes

import (
	"errors"
	"strings"
	"time"

	"ldapnav/internal/ldapfilter"
)

const (
	catPeople   = "People"
	catGroups   = "Groups"
	catAccounts = "Accounts"
	catExplore  = "Explore"
)

var all = []*Recipe{
	{
		ID: "find-person", Category: catPeople, Title: "Find a person",
		Description: "Look for someone by name, login or email. Any of those fields can match.",
		Params:      []Param{{Name: "text", Label: "Name, login or email", Kind: "text", Placeholder: "e.g. alice", Required: true}},
		Unavailable: func(e *Env) string {
			if r := needSchema(e); r != "" {
				return r
			}
			if len(e.present("cn", "displayName", "uid", "mail", "sAMAccountName", "userPrincipalName")) == 0 {
				return missing("name, login or mail attribute")
			}
			return ""
		},
		Build: func(e *Env, p map[string]string) (*Plan, error) {
			attrs := e.present("cn", "displayName", "givenName", "sn", "uid", "sAMAccountName", "userPrincipalName", "mail")
			items := make([]*ldapfilter.Node, len(attrs))
			for i, a := range attrs {
				items[i] = cond("contains", a, p["text"])
			}
			return &Plan{Query: anyOf(items), Attrs: strings.Join(e.present("cn", "uid", "sAMAccountName", "mail"), ", ")}, nil
		},
	},
	{
		ID: "people-without-email", Category: catPeople, Title: "People without an email address",
		Description: "Find people entries that have no email, to spot incomplete data.",
		Unavailable: func(e *Env) string {
			if r := needSchema(e); r != "" {
				return r
			}
			if !e.Attr("mail") {
				return missing("mail attribute")
			}
			if len(e.presentClasses(personClasses...)) == 0 {
				return missing("person object class")
			}
			return ""
		},
		Build: func(e *Env, _ map[string]string) (*Plan, error) {
			q := group("and", classIs(e.presentClasses("inetOrgPerson", "person", "user")), group("not", cond("present", "mail", "")))
			return &Plan{Query: q, Attrs: strings.Join(e.present("cn", "uid", "sAMAccountName"), ", ")}, nil
		},
	},
	{
		ID: "people-by-department", Category: catPeople, Title: "People in a department",
		Description: "List the people whose department contains some text.",
		Params:      []Param{{Name: "text", Label: "Department", Kind: "text", Placeholder: "e.g. Sales", Required: true}},
		Unavailable: func(e *Env) string {
			if r := needSchema(e); r != "" {
				return r
			}
			if len(e.present("departmentNumber", "department")) == 0 {
				return missing("department attribute (departmentNumber or department)")
			}
			return ""
		},
		Build: func(e *Env, p map[string]string) (*Plan, error) {
			deps := e.present("departmentNumber", "department")
			items := make([]*ldapfilter.Node, len(deps))
			for i, a := range deps {
				items[i] = cond("contains", a, p["text"])
			}
			attrs := append(e.present("cn", "uid", "sAMAccountName"), deps...)
			return &Plan{Query: anyOf(items), Attrs: strings.Join(attrs, ", ")}, nil
		},
	},

	{
		ID: "group-members", Category: catGroups, Title: "Members of a group",
		Description: "Find a group by name and list its members. Use the Table view; click a member to open it.",
		Params:      []Param{{Name: "group", Label: "Group name", Kind: "text", Placeholder: "e.g. admins", Required: true}},
		Unavailable: func(e *Env) string {
			if r := needSchema(e); r != "" {
				return r
			}
			if len(e.presentClasses(groupClasses...)) == 0 {
				return missing("group object class")
			}
			return ""
		},
		Build: func(e *Env, p map[string]string) (*Plan, error) {
			q := group("and", classIs(e.presentClasses(groupClasses...)), cond("contains", "cn", p["group"]))
			attrs := append([]string{"cn"}, e.present("member", "uniqueMember", "memberUid")...)
			return &Plan{Query: q, Attrs: strings.Join(attrs, ", ")}, nil
		},
	},
	{
		ID: "user-groups", Category: catGroups, Title: "Groups a user belongs to",
		Description: "Find the groups that list a given user as a member.",
		Params: []Param{
			{Name: "dn", Label: "User DN", Kind: "dn", Placeholder: "uid=alice,ou=people,dc=example,dc=org",
				Help: "Open the user in the Entry tab and use \"copy\" to get the DN.", Required: true},
			{Name: "uid", Label: "Login (optional)", Kind: "text", Placeholder: "e.g. alice",
				Help: "Needed for POSIX groups, which list logins instead of DNs."},
		},
		Unavailable: func(e *Env) string {
			if r := needSchema(e); r != "" {
				return r
			}
			if len(e.presentClasses(groupClasses...)) == 0 {
				return missing("group object class")
			}
			if len(e.present("member", "uniqueMember", "memberUid")) == 0 {
				return missing("membership attribute (member, uniqueMember or memberUid)")
			}
			return ""
		},
		Build: func(e *Env, p map[string]string) (*Plan, error) {
			var by []*ldapfilter.Node
			for _, a := range e.present("member", "uniqueMember") {
				by = append(by, cond("equals", a, p["dn"]))
			}
			if p["uid"] != "" && e.Attr("memberUid") {
				by = append(by, cond("equals", "memberUid", p["uid"]))
			}
			if len(by) == 0 {
				return nil, errors.New("this server only lists group members by login (memberUid): enter the login too")
			}
			note := ""
			if e.Attr("memberOf") {
				note = "This server also has memberOf: open the user's entry and read memberOf for the same answer."
			}
			q := group("and", classIs(e.presentClasses(groupClasses...)), anyOf(by))
			return &Plan{Query: q, Attrs: "cn, description", Note: note}, nil
		},
	},

	{
		ID: "locked-accounts", Category: catAccounts, Title: "Locked or disabled accounts",
		Description: "Accounts the server has locked or deactivated. What \"locked\" means depends on the server, so this uses the attributes this server defines.",
		Unavailable: func(e *Env) string {
			if r := needSchema(e); r != "" {
				return r
			}
			if len(e.present("nsAccountLock", "pwdAccountLockedTime")) == 0 {
				return missing("account-lock attribute (nsAccountLock on 389 Directory Server, pwdAccountLockedTime with the OpenLDAP ppolicy overlay)")
			}
			return ""
		},
		Build: func(e *Env, _ map[string]string) (*Plan, error) {
			var items []*ldapfilter.Node
			if e.Attr("nsAccountLock") {
				items = append(items, cond("equals", "nsAccountLock", "true"))
			}
			if e.Attr("pwdAccountLockedTime") {
				items = append(items, cond("present", "pwdAccountLockedTime", ""))
			}
			attrs := append(e.present("cn", "uid"), e.present("nsAccountLock", "pwdAccountLockedTime")...)
			return &Plan{Query: anyOf(items), Attrs: strings.Join(attrs, ", ")}, nil
		},
	},
	{
		ID: "expired-accounts", Category: catAccounts, Title: "Accounts or passwords that have expired",
		Description: "Entries whose account or password expiry date is in the past.",
		Unavailable: func(e *Env) string {
			if r := needSchema(e); r != "" {
				return r
			}
			if len(e.present("passwordExpirationTime", "krbPrincipalExpiration", "krbPasswordExpiration")) == 0 {
				return missing("expiry-date attribute (passwordExpirationTime, krbPrincipalExpiration or krbPasswordExpiration)")
			}
			return ""
		},
		Build: func(e *Env, _ map[string]string) (*Plan, error) {
			now := e.Now.UTC().Format(time.RFC3339)
			var items []*ldapfilter.Node
			for _, a := range e.present("passwordExpirationTime", "krbPrincipalExpiration", "krbPasswordExpiration") {
				items = append(items, cond("lte", a, now))
			}
			attrs := append(e.present("cn", "uid"), e.present("passwordExpirationTime", "krbPrincipalExpiration", "krbPasswordExpiration")...)
			return &Plan{Query: anyOf(items), Attrs: strings.Join(attrs, ", ")}, nil
		},
	},
	{
		ID: "recently-created", Category: catAccounts, Title: "Created in the last N days",
		Description: "Entries created recently.",
		Params:      []Param{{Name: "days", Label: "Days", Kind: "days", Default: "7", Required: true}},
		Unavailable: func(e *Env) string {
			if r := needSchema(e); r != "" {
				return r
			}
			if len(e.present("createTimestamp", "whenCreated")) == 0 {
				return missing("creation-date attribute (createTimestamp or whenCreated)")
			}
			return ""
		},
		Build: func(e *Env, p map[string]string) (*Plan, error) {
			return recent(e, p, "createTimestamp", "whenCreated"), nil
		},
	},
	{
		ID: "recently-modified", Category: catAccounts, Title: "Changed in the last N days",
		Description: "Entries modified recently.",
		Params:      []Param{{Name: "days", Label: "Days", Kind: "days", Default: "7", Required: true}},
		Unavailable: func(e *Env) string {
			if r := needSchema(e); r != "" {
				return r
			}
			if len(e.present("modifyTimestamp", "whenChanged")) == 0 {
				return missing("modification-date attribute (modifyTimestamp or whenChanged)")
			}
			return ""
		},
		Build: func(e *Env, p map[string]string) (*Plan, error) {
			return recent(e, p, "modifyTimestamp", "whenChanged"), nil
		},
	},

	{
		ID: "by-class", Category: catExplore, Title: "Entries of an object class",
		Description: "List every entry of a kind, e.g. inetOrgPerson, groupOfNames or organizationalUnit.",
		Params:      []Param{{Name: "class", Label: "Object class", Kind: "class", Placeholder: "e.g. inetOrgPerson", Required: true}},
		Build: func(_ *Env, p map[string]string) (*Plan, error) {
			return &Plan{Query: cond("equals", "objectClass", p["class"]), Attrs: "cn"}, nil
		},
	},
	{
		ID: "with-attribute", Category: catExplore, Title: "Entries that have an attribute",
		Description: "Find the entries where an attribute has any value.",
		Params:      []Param{{Name: "attribute", Label: "Attribute", Kind: "attribute", Placeholder: "e.g. mail", Required: true}},
		Build: func(_ *Env, p map[string]string) (*Plan, error) {
			return &Plan{Query: cond("present", p["attribute"], ""), Attrs: p["attribute"]}, nil
		},
	},
	{
		ID: "missing-attribute", Category: catExplore, Title: "Entries missing an attribute",
		Description: "Of one kind of entry, find those that lack an attribute: handy to spot incomplete data.",
		Params: []Param{
			{Name: "class", Label: "Object class", Kind: "class", Placeholder: "e.g. inetOrgPerson", Required: true},
			{Name: "attribute", Label: "Missing attribute", Kind: "attribute", Placeholder: "e.g. mail", Required: true},
		},
		Build: func(_ *Env, p map[string]string) (*Plan, error) {
			q := group("and", cond("equals", "objectClass", p["class"]), group("not", cond("present", p["attribute"], "")))
			return &Plan{Query: q, Attrs: "cn"}, nil
		},
	},
}

func recent(e *Env, p map[string]string, names ...string) *Plan {
	var items []*ldapfilter.Node
	for _, a := range e.present(names...) {
		items = append(items, cond("withinDays", a, p["days"]))
	}
	attrs := append(e.present("cn", "uid", "sAMAccountName"), e.present(names...)...)
	return &Plan{Query: anyOf(items), Attrs: strings.Join(attrs, ", ")}
}
