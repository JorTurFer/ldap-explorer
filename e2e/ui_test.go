package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"
	"ldapnav/internal/slapdtest"
)

// ---------- helpers ----------

func run(t *testing.T, ctx context.Context, actions ...chromedp.Action) {
	t.Helper()
	if err := chromedp.Run(ctx, actions...); err != nil {
		t.Fatal(err)
	}
}

// waitJS waits until the JavaScript expression is truthy.
func waitJS(t *testing.T, ctx context.Context, what, expr string) {
	t.Helper()
	if err := chromedp.Run(ctx, chromedp.Poll(expr, nil, chromedp.WithPollingTimeout(20*time.Second))); err != nil {
		t.Fatalf("timed out waiting for %s: %v", what, err)
	}
}

func js(t *testing.T, ctx context.Context, expr string, out any) {
	t.Helper()
	run(t, ctx, chromedp.Evaluate(expr, out))
}

// set sets an input/select value from JS (chromedp.SetValue fails on empty strings).
func set(sel, val string) chromedp.Action {
	return chromedp.Evaluate(fmt.Sprintf(`(() => { const e = document.querySelector(%s); e.value = %s; return true; })()`, q(sel), q(val)), nil)
}

func q(s string) string { b, _ := json.Marshal(s); return string(b) }

// waitText waits until the element matching sel contains text.
func waitText(t *testing.T, ctx context.Context, sel, text string) {
	t.Helper()
	waitJS(t, ctx, fmt.Sprintf("%q to contain %q", sel, text),
		fmt.Sprintf(`!!document.querySelector(%s) && document.querySelector(%s).textContent.includes(%s)`, q(sel), q(sel), q(text)))
}

func textOf(t *testing.T, ctx context.Context, sel string) string {
	t.Helper()
	var s string
	js(t, ctx, fmt.Sprintf(`(document.querySelector(%s)||{textContent:''}).textContent`, q(sel)), &s)
	return s
}

// attrValue returns the text of the value cell of an attribute in the entry pane.
func attrValue(t *testing.T, ctx context.Context, name string) (text string, found bool) {
	t.Helper()
	var r struct {
		Found bool   `json:"found"`
		Text  string `json:"text"`
	}
	js(t, ctx, fmt.Sprintf(`(() => { for (const tr of document.querySelectorAll('#pane-entry table.attrs tr')) {
		if (tr.querySelector('td.k').textContent === %s) return {found:true, text: tr.querySelector('td.v').textContent}; }
		return {found:false, text:''}; })()`, q(name)), &r)
	return r.Text, r.Found
}

// rawMode switches the Search tab to the "Advanced (text)" filter input.
func rawMode() chromedp.Action { return chromedp.Click("#modeRaw") }

func connect(t *testing.T, ctx context.Context, url, bindDN, password string) {
	t.Helper()
	run(t, ctx,
		chromedp.Click("#btnConn"),
		chromedp.WaitVisible("#cURL"),
		set("#cURL", url),
		set("#cBind", bindDN),
		set("#cPass", password),
		chromedp.Click("#btnDoConn"),
	)
}

// ---------- tests ----------

func TestUIConnectBrowseAndSearch(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))

	if got := textOf(t, ctx, "#status"); got != "Not connected" {
		t.Fatalf("initial status: %q", got)
	}

	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to "+l.URL+" as "+slapdtest.AliceDN)

	// The first root is detected from the RootDSE and selected automatically.
	waitText(t, ctx, "#pane-entry .dn", slapdtest.BaseDN)

	// The root is selected but collapsed: expand it, then ou=people, and open alice.
	root := `.node[title="` + slapdtest.BaseDN + `"]`
	run(t, ctx, chromedp.WaitVisible(root), chromedp.Click(root+" .tw"))
	people := `.node[title="ou=people,` + slapdtest.BaseDN + `"]`
	run(t, ctx, chromedp.WaitVisible(people), chromedp.Click(people+" .tw"))
	alice := `.node[title="` + slapdtest.AliceDN + `"]`
	run(t, ctx, chromedp.WaitVisible(alice), chromedp.Click(alice))
	waitText(t, ctx, "#pane-entry .dn", slapdtest.AliceDN)

	if mail, ok := attrValue(t, ctx, "mail"); !ok || mail != "alice@example.org" {
		t.Errorf("mail: %q (found=%v)", mail, ok)
	}
	if photo, ok := attrValue(t, ctx, "jpegPhoto"); !ok || !strings.Contains(photo, "(binary,") {
		t.Errorf("jpegPhoto should be summarised as binary, got %q (found=%v)", photo, ok)
	}
	var first string
	js(t, ctx, `document.querySelector('#pane-entry table.attrs tr td.k').textContent`, &first)
	if first != "objectClass" {
		t.Errorf("objectClass should be the first attribute, got %q", first)
	}

	// Operational attributes toggle.
	if _, ok := attrValue(t, ctx, "entryUUID"); ok {
		t.Error("entryUUID shown without the operational toggle")
	}
	run(t, ctx, chromedp.Click("#chkOp"))
	waitJS(t, ctx, "entryUUID", `[...document.querySelectorAll('#pane-entry table.attrs td.k')].some(td=>td.textContent==='entryUUID')`)

	// Search: filter and attribute list.
	run(t, ctx, chromedp.Click(`button[data-tab="search"]`), chromedp.WaitVisible("#sBase"), rawMode())
	var base string
	js(t, ctx, `document.getElementById('sBase').value`, &base)
	if base != slapdtest.BaseDN {
		t.Errorf("search base should default to the first root, got %q", base)
	}
	run(t, ctx,
		set("#sFilter", "(uid=alice)"),
		set("#sAttrs", "mail, sn"),
		chromedp.Click("#btnSearch"),
	)
	waitText(t, ctx, "#sOut", "1 result(s)")
	waitText(t, ctx, "#sOut", slapdtest.AliceDN)
	var cells int
	js(t, ctx, `document.querySelectorAll('#sOut table.attrs tr').length`, &cells)
	if cells != 2 {
		t.Errorf("requested 2 attributes, result shows %d rows", cells)
	}

	// A subtree search returning several entries, with Enter to submit.
	run(t, ctx,
		set("#sFilter", "(objectClass=inetOrgPerson)"),
		set("#sAttrs", "uid"),
		set("#sLimit", "10"),
	)
	run(t, ctx, chromedp.SendKeys("#sFilter", "\r"))
	waitText(t, ctx, "#sOut", "10 result(s)")
	waitText(t, ctx, "#sOut", "Size Limit Exceeded") // 10 < number of matches: warning, not an error

	// Invalid filter: rejected with a readable message.
	run(t, ctx, set("#sFilter", "(uid="), chromedp.Click("#btnSearch"))
	waitText(t, ctx, "#sOut .err", "invalid filter")

	// "view" on a result opens it in the Entry tab.
	run(t, ctx, set("#sFilter", "(uid=bob)"), chromedp.Click("#btnSearch"))
	waitText(t, ctx, "#sOut", "1 result(s)")
	run(t, ctx, chromedp.Click("#sOut summary a.jump"))
	waitText(t, ctx, "#pane-entry .dn", "uid=bob,")

	// "search from here" sets the base and switches to the Search tab.
	run(t, ctx, chromedp.Click("#pane-entry a.jump"))
	waitJS(t, ctx, "base = bob", `document.getElementById('sBase').value.startsWith('uid=bob,')`)

	// Disconnect.
	run(t, ctx, chromedp.Click("#btnDisc"))
	waitText(t, ctx, "#status", "Not connected")
	waitText(t, ctx, "#tree", "Not connected.")
}

func TestUIWrongPassword(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))

	connect(t, ctx, l.URL, slapdtest.AliceDN, "definitely-wrong")
	waitText(t, ctx, "#cErr", "bind")
	var dialogOpen bool
	js(t, ctx, `document.getElementById('dlg').open`, &dialogOpen)
	if !dialogOpen {
		t.Error("the dialog should stay open after a failed connection")
	}
	if got := textOf(t, ctx, "#status"); got != "Not connected" {
		t.Errorf("status after a failed connect: %q", got)
	}

	// A bind DN without a password is refused by the client with a clear message.
	run(t, ctx, set("#cPass", ""), chromedp.Click("#btnDoConn"))
	waitText(t, ctx, "#cErr", "empty password")
}

func TestUIAnonymousAndNoBaseDetection(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))

	connect(t, ctx, l.URL, "", "")
	waitText(t, ctx, "#status", "(anonymous)")
	waitText(t, ctx, "#tree", slapdtest.BaseDN)
}

func TestUIProfiles(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))

	// Saving without a name is refused.
	run(t, ctx, chromedp.Click("#btnConn"), chromedp.WaitVisible("#cURL"),
		set("#cURL", l.URL), chromedp.Click("#btnSaveProfile"))
	waitText(t, ctx, "#cErr", "Give the profile a name")

	// Save, then reload the page: the profile is still there and fills the form.
	run(t, ctx,
		set("#cName", "lab"),
		set("#cBind", slapdtest.AliceDN),
		set("#cBase", "ou=people,"+slapdtest.BaseDN),
		set("#cPass", "must-not-be-saved"),
		chromedp.Click("#btnSaveProfile"),
	)
	waitText(t, ctx, "#status", "Profile saved")

	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn")) // fresh page, same server
	run(t, ctx, chromedp.Click("#btnConn"), chromedp.WaitVisible("#cProfile"))
	waitJS(t, ctx, "profile option", `[...document.querySelectorAll('#cProfile option')].some(o=>o.value==='lab')`)
	run(t, ctx, set("#cProfile", "lab"))
	// SetValue does not fire "change" the way a user selection does.
	run(t, ctx, chromedp.Evaluate(`document.getElementById('cProfile').dispatchEvent(new Event('change'))`, nil))
	waitJS(t, ctx, "form filled", `document.getElementById('cURL').value === `+q(l.URL))
	var bind, pass string
	js(t, ctx, `document.getElementById('cBind').value`, &bind)
	js(t, ctx, `document.getElementById('cPass').value`, &pass)
	if bind != slapdtest.AliceDN || pass != "" {
		t.Errorf("form after loading the profile: bind=%q password=%q", bind, pass)
	}

	// Delete it.
	run(t, ctx, chromedp.Click("#btnDelProfile"))
	waitJS(t, ctx, "profile gone", `![...document.querySelectorAll('#cProfile option')].some(o=>o.value==='lab')`)
}

// headers returns the text of the results table header cells.
func headers(t *testing.T, ctx context.Context) []string {
	t.Helper()
	var h []string
	js(t, ctx, `[...document.querySelectorAll('#sOut table.rtable thead th')].map(th => th.textContent)`, &h)
	return h
}

// column returns the text of one column of the results table body (0 = DN).
func column(t *testing.T, ctx context.Context, n int) []string {
	t.Helper()
	var c []string
	js(t, ctx, fmt.Sprintf(`[...document.querySelectorAll('#sOut table.rtable tbody tr')].map(tr => tr.children[%d].textContent)`, n), &c)
	return c
}

func TestUISearchTableAndCSV(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	downloads := t.TempDir()
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"),
		cdpbrowser.SetDownloadBehavior(cdpbrowser.SetDownloadBehaviorBehaviorAllow).WithDownloadPath(downloads))

	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")

	run(t, ctx, chromedp.Click(`button[data-tab="search"]`), chromedp.WaitVisible("#sBase"), rawMode(),
		set("#sBase", "ou=people,"+slapdtest.BaseDN), set("#sScope", "one"),
		set("#sFilter", "(objectClass=inetOrgPerson)"), set("#sAttrs", "uid, mail, description, telephoneNumber"),
		chromedp.Click("#btnSearch"))
	waitText(t, ctx, "#sOut", "3 result(s)")

	// The list is the default view; the table keeps the requested columns in order.
	waitJS(t, ctx, "list view", `document.querySelectorAll('#sOut details.result').length === 3`)
	run(t, ctx, chromedp.Click("#viewTable"), chromedp.WaitVisible("#sOut table.rtable"))
	if got, want := strings.Join(headers(t, ctx), "|"), "DN|uid|mail|description|telephoneNumber"; got != want {
		t.Fatalf("headers: %q, want %q", got, want)
	}
	if rows := column(t, ctx, 1); len(rows) != 3 {
		t.Fatalf("rows: %v", rows)
	}

	// Sorting: first click ascending, second descending, with the arrow shown.
	run(t, ctx, chromedp.Click(`#sOut table.rtable thead th:nth-child(2)`))
	if got := strings.Join(column(t, ctx, 1), ","); got != "alice,bob,zoe" {
		t.Errorf("sorted ascending: %s", got)
	}
	run(t, ctx, chromedp.Click(`#sOut table.rtable thead th:nth-child(2)`))
	if got := strings.Join(column(t, ctx, 1), ","); got != "zoe,bob,alice" {
		t.Errorf("sorted descending: %s", got)
	}
	if got := headers(t, ctx)[1]; got != "uid ▼" {
		t.Errorf("sort indicator: %q", got)
	}

	// Hide the mail column with the column chooser.
	run(t, ctx, chromedp.Click("#colChooser summary"),
		chromedp.Click(`#colChooser label:nth-child(2) input`)) // columns: uid, mail, description, telephoneNumber
	if got, want := strings.Join(headers(t, ctx), "|"), "DN|uid ▼|description|telephoneNumber"; got != want {
		t.Errorf("headers after hiding mail: %q, want %q", got, want)
	}

	// A DN opens the entry.
	run(t, ctx, chromedp.Click(`#sOut table.rtable tbody tr:first-child td a`))
	waitText(t, ctx, "#pane-entry .dn", "uid=zoe,")
	run(t, ctx, chromedp.Click(`button[data-tab="search"]`))

	// The view is remembered for the next search.
	run(t, ctx, set("#sFilter", "(uid=bob)"), chromedp.Click("#btnSearch"))
	waitText(t, ctx, "#sOut", "1 result(s)")
	waitJS(t, ctx, "table view remembered", `!!document.querySelector('#sOut table.rtable')`)

	// CSV export of the current columns and order.
	run(t, ctx, set("#sFilter", "(objectClass=inetOrgPerson)"), chromedp.Click("#btnSearch"))
	waitText(t, ctx, "#sOut", "3 result(s)")
	// A new search starts with all requested columns and no sorting: click uid twice for descending.
	run(t, ctx, chromedp.Click(`#sOut table.rtable thead th:nth-child(2)`),
		chromedp.Click(`#sOut table.rtable thead th:nth-child(2)`))
	run(t, ctx, chromedp.Click("#btnCsv"))

	var file string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && file == "" {
		matches, _ := filepath.Glob(filepath.Join(downloads, "ldapnav-*.csv"))
		if len(matches) == 1 {
			file = matches[0]
		}
		time.Sleep(100 * time.Millisecond)
	}
	if file == "" {
		t.Fatal("the CSV file was not downloaded")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	csv := string(raw)
	if !strings.HasPrefix(csv, "\uFEFF") {
		t.Error("the CSV should start with a UTF-8 BOM so Excel detects the encoding")
	}
	lines := strings.Split(strings.TrimSuffix(strings.TrimPrefix(csv, "\uFEFF"), "\r\n"), "\r\n")
	// All four requested columns (the new search shows them again), sorted by uid descending.
	if len(lines) != 4 {
		t.Fatalf("CSV lines: %q", lines)
	}
	if want := "dn,uid,mail,description,telephoneNumber"; lines[0] != want {
		t.Errorf("CSV header: %q, want %q", lines[0], want)
	}
	if !strings.HasPrefix(lines[1], `"uid=zoe,ou=people,`+slapdtest.BaseDN+`",zoe,`) {
		t.Errorf("first CSV row should be zoe: %q", lines[1])
	}
	// bob: the formula in "description" is defused, both phone numbers are kept as is.
	if want := `"uid=bob,ou=people,` + slapdtest.BaseDN + `",bob,bob@example.org,'=1+1,+34 600 000 001; +34 600 000 002`; lines[2] != want {
		t.Errorf("bob's CSV row:\n got %q\nwant %q", lines[2], want)
	}
}

// ---------- query builder ----------

// cond sets part of the n-th condition row of the builder (0-based).
func setAttr(t *testing.T, ctx context.Context, n int, v string) {
	t.Helper()
	js(t, ctx, fmt.Sprintf(`(() => { const i = document.querySelectorAll('.bcond .battr')[%d]; i.value = %s;
		i.dispatchEvent(new Event('input')); i.dispatchEvent(new Event('change')); return true; })()`, n, q(v)), nil)
}

func setOp(t *testing.T, ctx context.Context, n int, v string) {
	t.Helper()
	js(t, ctx, fmt.Sprintf(`(() => { const s = document.querySelectorAll('.bcond .bop')[%d]; s.value = %s;
		s.dispatchEvent(new Event('change')); return true; })()`, n, q(v)), nil)
}

func setVal(t *testing.T, ctx context.Context, n int, v string) {
	t.Helper()
	js(t, ctx, fmt.Sprintf(`(() => { const i = document.querySelectorAll('.bcond .bval')[%d]; i.value = %s;
		i.dispatchEvent(new Event('input')); return true; })()`, n, q(v)), nil)
}

func opsOf(t *testing.T, ctx context.Context, n int) []string {
	t.Helper()
	var ops []string
	js(t, ctx, fmt.Sprintf(`[...document.querySelectorAll('.bcond .bop')[%d].options].map(o => o.value)`, n), &ops)
	return ops
}

func TestUIQueryBuilder(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	run(t, ctx, chromedp.Click(`button[data-tab="search"]`), chromedp.WaitVisible("#builder"),
		set("#sBase", "ou=people,"+slapdtest.BaseDN), set("#sScope", "one"))

	// The builder is the default; the schema fills the attribute suggestions.
	waitText(t, ctx, "#bExplain", "Choose an attribute to start.")
	waitJS(t, ctx, "attribute suggestions", `document.querySelectorAll('#attrList option').length > 50`)
	var order [2]int
	js(t, ctx, `(() => { const n = [...document.querySelectorAll('#attrList option')].map(o => o.value);
		return [n.indexOf('mail'), n.indexOf('createTimestamp')]; })()`, &order)
	if order[0] < 0 || order[1] < 0 || order[0] > order[1] {
		t.Errorf("user attributes should be suggested before operational ones: mail=%d createTimestamp=%d", order[0], order[1])
	}

	// One condition: preview, explanation and results.
	setAttr(t, ctx, 0, "mail")
	setVal(t, ctx, 0, "example")
	waitText(t, ctx, "#bFilter", "(mail=*example*)")
	waitText(t, ctx, "#bExplain", `mail contains "example"`)
	run(t, ctx, chromedp.Click("#btnSearch"))
	waitText(t, ctx, "#sOut", "2 result(s)")

	// A second condition, combined with "all of".
	run(t, ctx, chromedp.Click("#builder .bfoot .badd:first-child"))
	setAttr(t, ctx, 1, "uid")
	setOp(t, ctx, 1, "startsWith")
	setVal(t, ctx, 1, "b")
	waitText(t, ctx, "#bFilter", "(&(mail=*example*)(uid=b*))")
	waitText(t, ctx, "#bExplain", "All of these must be true:")
	run(t, ctx, chromedp.Click("#btnSearch"))
	waitText(t, ctx, "#sOut", "1 result(s)")
	waitText(t, ctx, "#sOut", "uid=bob,")

	// "any of" and "none of".
	run(t, ctx, set(".bgroup .bmode", "any"), chromedp.Evaluate(`document.querySelector('.bgroup .bmode').dispatchEvent(new Event('change'))`, nil))
	waitText(t, ctx, "#bFilter", "(|(mail=*example*)(uid=b*))")
	waitText(t, ctx, "#bExplain", "At least one of these must be true:")
	run(t, ctx, set(".bgroup .bmode", "none"), chromedp.Evaluate(`document.querySelector('.bgroup .bmode').dispatchEvent(new Event('change'))`, nil))
	waitText(t, ctx, "#bFilter", "(!(|(mail=*example*)(uid=b*)))")
	run(t, ctx, chromedp.Click("#btnSearch"))
	waitText(t, ctx, "#sOut", "uid=zoe,")

	// Special characters are escaped by the server, never interpreted.
	setAttr(t, ctx, 0, "cn")
	setVal(t, ctx, 0, "(Name) *")
	waitText(t, ctx, "#bFilter", `(cn=*\28Name\29 \2a*)`)

	// Operators follow the attribute type; dates get a date input and "last N days".
	setAttr(t, ctx, 1, "createTimestamp")
	if got, want := strings.Join(opsOf(t, ctx, 1), ","), "equals,notEquals,after,before,withinDays,present,notPresent"; got != want {
		t.Errorf("operators for a time attribute: %s, want %s", got, want)
	}
	setOp(t, ctx, 1, "after")
	var inputType string
	js(t, ctx, `document.querySelectorAll('.bcond .bval')[1].type`, &inputType)
	if inputType != "datetime-local" {
		t.Errorf("value input for a time attribute: %q", inputType)
	}
	setOp(t, ctx, 1, "withinDays")
	setVal(t, ctx, 1, "7")
	waitJS(t, ctx, "time filter", `/\(createTimestamp>=\d{14}Z\)/.test(document.getElementById('bFilter').textContent)`)
	setAttr(t, ctx, 1, "jpegPhoto")
	if got, want := strings.Join(opsOf(t, ctx, 1), ","), "present,notPresent"; got != want {
		t.Errorf("operators for a binary attribute: %s, want %s", got, want)
	}
	setAttr(t, ctx, 1, "gidNumber")
	if got := opsOf(t, ctx, 1); !slices.Contains(got, "gte") || slices.Contains(got, "contains") {
		t.Errorf("operators for an integer attribute: %v", got)
	}
	setAttr(t, ctx, 1, "objectClass")
	if got := opsOf(t, ctx, 1); slices.Contains(got, "contains") {
		t.Errorf("objectClass has no substring matching on the server: %v", got)
	}

	// An unknown attribute is explained, not silently searched.
	setAttr(t, ctx, 1, "colour")
	setOp(t, ctx, 1, "equals")
	setVal(t, ctx, 1, "red")
	waitText(t, ctx, "#bErr", "not an attribute of this server")
	run(t, ctx, chromedp.Click("#btnSearch"))
	waitText(t, ctx, "#sOut .err", "not an attribute of this server")

	// Remove the bad row, and move the filter to the text mode.
	run(t, ctx, chromedp.Click("#builder .bcond:nth-of-type(2) .bremove"))
	waitJS(t, ctx, "error gone", `document.getElementById('bErr').textContent === '' && document.getElementById('bFilter').textContent !== ''`)
	run(t, ctx, chromedp.Click("#bToRaw"))
	waitJS(t, ctx, "text mode with the filter", `!document.getElementById('rawBox').classList.contains('hidden') && document.getElementById('sFilter').value.startsWith('(')`)
}
