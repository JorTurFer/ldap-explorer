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
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"ldap-explorer/internal/slapdtest"
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

// reload loads the app again from scratch. Navigating to the same URL only changes the
// #fragment, which the browser treats as "stay on this page": go through about:blank.
func reload(t *testing.T, ctx context.Context, appURL string) {
	t.Helper()
	run(t, ctx, chromedp.Navigate("about:blank"), chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	var status string
	js(t, ctx, `document.getElementById('status').textContent`, &status)
	if status != "Not connected" {
		t.Fatalf("the page was not reloaded (status %q)", status)
	}
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
	run(t, ctx, chromedp.Click(`button[data-tab="search"]`), chromedp.WaitReady("#sBase"), rawMode())
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

	reload(t, ctx, appURL) // fresh page, same server
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

	run(t, ctx, chromedp.Click(`button[data-tab="search"]`), chromedp.WaitReady("#sBase"), rawMode(),
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
		matches, _ := filepath.Glob(filepath.Join(downloads, "ldap-explorer-*.csv"))
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

	// Layout: every control of every row stays inside the builder box (no clipping).
	var inside bool
	js(t, ctx, `(() => { const g = document.querySelector('#builder .bgroup').getBoundingClientRect();
		return [...document.querySelectorAll('#builder .bcond > *')].every(e => e.getBoundingClientRect().right <= g.right + 1); })()`, &inside)
	if !inside {
		t.Error("a condition row overflows the builder box: its operator, value or remove button is clipped")
	}
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

// ---------- recipes and explorer ----------

func TestUIRecipes(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	run(t, ctx, chromedp.Click(`button[data-tab="recipes"]`), chromedp.WaitVisible("#recipe-find-person"))

	// Grouped by category.
	var cats []string
	js(t, ctx, `[...document.querySelectorAll('#pane-recipes h3')].map(h => h.textContent)`, &cats)
	if strings.Join(cats, ",") != "People,Groups,Accounts,Explore" {
		t.Errorf("categories: %v", cats)
	}

	// A recipe this server cannot answer is shown greyed out, with the reason. Which ones
	// depend on the build (Homebrew's OpenLDAP 2.7 defines the ppolicy lock attribute), so
	// look for any: "expired accounts" needs attributes no stock OpenLDAP schema has.
	var unavailable struct {
		Found    bool   `json:"found"`
		Reason   string `json:"reason"`
		Disabled bool   `json:"disabled"`
	}
	js(t, ctx, `(() => { const c = document.querySelector('#pane-recipes .recipe.unavailable');
		if (!c) return { found: false, reason: '', disabled: false };
		return { found: true, reason: c.querySelector('.reason').textContent, disabled: c.querySelector('button.primary').disabled }; })()`, &unavailable)
	if !unavailable.Found {
		t.Fatal("this server cannot answer every recipe: at least one should be greyed out")
	}
	if !strings.HasPrefix(unavailable.Reason, "Not available on this server: this server's schema has no ") || !unavailable.Disabled {
		t.Errorf("unavailable recipe: reason %q, button disabled=%v", unavailable.Reason, unavailable.Disabled)
	}

	// Parameters are validated by the server and the message is shown on the card.
	run(t, ctx, chromedp.Click(`#recipe-find-person button.primary`))
	waitText(t, ctx, "#recipe-find-person .err", "is required")

	// Object class and attribute parameters suggest names from the schema.
	var lists [2]string
	js(t, ctx, `[document.querySelector('#recipe-by-class [data-param="class"]').getAttribute('list'),
		document.querySelector('#recipe-with-attribute [data-param="attribute"]').getAttribute('list')]`, &lists)
	if lists != [2]string{"classList", "attrList"} {
		t.Errorf("suggestion lists: %v", lists)
	}

	// Use a recipe: it opens in the builder (so it can be tweaked) and runs.
	run(t, ctx, set(`#recipe-find-person [data-param="text"]`, "ali"), chromedp.Click(`#recipe-find-person button.primary`))
	waitJS(t, ctx, "search tab", `document.getElementById('pane-search').classList.contains('on')`)
	waitText(t, ctx, "#sNote", "Find a person")
	waitText(t, ctx, "#bFilter", "(|(cn=*ali*)")
	waitText(t, ctx, "#sOut", "1 result(s)")
	waitText(t, ctx, "#sOut", "uid=alice,")
	var conditions int
	js(t, ctx, `document.querySelectorAll('#builder .bcond').length`, &conditions)
	if conditions < 4 {
		t.Errorf("the recipe should fill the builder with its conditions, got %d rows", conditions)
	}

	// Running a search by hand clears the recipe note.
	run(t, ctx, chromedp.Click("#btnSearch"))
	waitJS(t, ctx, "note hidden", `document.getElementById('sNote').classList.contains('hidden')`)

	// Groups of a user: the DN matches `member` and the login matches `memberUid`.
	run(t, ctx, chromedp.Click(`button[data-tab="recipes"]`),
		set(`#recipe-user-groups [data-param="dn"]`, slapdtest.AliceDN),
		set(`#recipe-user-groups [data-param="uid"]`, "alice"),
		chromedp.Click(`#recipe-user-groups button.primary`))
	waitText(t, ctx, "#sOut", "2 result(s)")
	waitText(t, ctx, "#sOut", "cn=admins,")
	waitText(t, ctx, "#sOut", "cn=staff,")
}

func TestUIExplore(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	run(t, ctx, chromedp.Click(`button[data-tab="explore"]`), chromedp.WaitVisible("#xBase"))

	var base string
	js(t, ctx, `document.getElementById('xBase').value`, &base)
	if base != slapdtest.BaseDN {
		t.Errorf("the explorer should start at the first root, got %q", base)
	}

	people := "ou=people," + slapdtest.BaseDN
	run(t, ctx, set("#xBase", people), set("#xScope", "one"), chromedp.Click("#btnExplore"))
	waitText(t, ctx, "#xSummary", "Looked at 3 entries")
	waitText(t, ctx, "#xList-class", "inetOrgPerson")
	waitText(t, ctx, "#xList-class", "3 (100%)")
	waitText(t, ctx, "#xList-attr", "mail")
	waitText(t, ctx, "#xList-attr", "2 (67%)") // alice and bob have mail, zoe does not

	// Click a kind of entry: it becomes a search and runs.
	run(t, ctx, chromedp.Click(`#xList-class li:first-child a`))
	waitJS(t, ctx, "search tab", `document.getElementById('pane-search').classList.contains('on')`)
	waitText(t, ctx, "#sNote", "Entries of class inetOrgPerson")
	waitText(t, ctx, "#bFilter", "(objectClass=inetOrgPerson)")
	waitText(t, ctx, "#sOut", "3 result(s)")
	var sbase string
	js(t, ctx, `document.getElementById('sBase').value`, &sbase)
	if sbase != people {
		t.Errorf("the search should use the explored base, got %q", sbase)
	}

	// Selecting a node in the tree points the explorer at it.
	run(t, ctx, chromedp.Click(`button[data-tab="explore"]`))
	root := `.node[title="` + slapdtest.BaseDN + `"]`
	run(t, ctx, chromedp.Click(root+" .tw"))
	groups := `.node[title="ou=groups,` + slapdtest.BaseDN + `"]`
	run(t, ctx, chromedp.WaitVisible(groups), chromedp.Click(groups))
	waitJS(t, ctx, "explorer base follows the tree", `document.getElementById('xBase').value === `+q("ou=groups,"+slapdtest.BaseDN))
}

// ---------- saved queries ----------

func savedOptions(t *testing.T, ctx context.Context) []string {
	t.Helper()
	var o []string
	js(t, ctx, `[...document.querySelectorAll('#qSaved option')].map(o => o.value).filter(Boolean)`, &o)
	return o
}

func TestUISavedQueries(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	people := "ou=people," + slapdtest.BaseDN
	run(t, ctx, chromedp.Click(`button[data-tab="search"]`), chromedp.WaitVisible("#qSave"),
		set("#sBase", people), set("#sScope", "one"), set("#sAttrs", "uid, mail"), set("#sLimit", "50"))

	// Nothing to save yet, and a name is required.
	run(t, ctx, chromedp.Click("#qSave"))
	waitText(t, ctx, "#qMsg", "Add at least one condition first.")
	setAttr(t, ctx, 0, "mail")
	setVal(t, ctx, 0, "example")
	run(t, ctx, chromedp.Click("#qSave"))
	waitText(t, ctx, "#qMsg", "give the query a name")

	// Save a builder query.
	run(t, ctx, set("#qName", "People with mail"), chromedp.Click("#qSave"))
	waitText(t, ctx, "#qMsg", "Saved.")
	if got := savedOptions(t, ctx); strings.Join(got, ",") != "People with mail" {
		t.Fatalf("saved options: %v", got)
	}

	// Save a raw one.
	run(t, ctx, chromedp.Click("#modeRaw"), set("#sFilter", "(uid=zoe)"), set("#qName", "Only zoe"), chromedp.Click("#qSave"))
	waitText(t, ctx, "#qMsg", "Saved.")
	if got := savedOptions(t, ctx); strings.Join(got, ",") != "Only zoe,People with mail" {
		t.Fatalf("saved options (sorted): %v", got)
	}

	// Change everything, then load the builder query back: it is all restored.
	run(t, ctx, set("#sBase", "dc=other"), set("#sScope", "base"), set("#sAttrs", "cn"), set("#sLimit", "7"), set("#sFilter", "(x=y)"))
	run(t, ctx, set("#qSaved", "People with mail"), chromedp.Evaluate(`document.getElementById('qSaved').dispatchEvent(new Event('change'))`, nil))
	waitText(t, ctx, "#sNote", `Saved query "People with mail" loaded`)
	waitJS(t, ctx, "builder mode with the condition back", `!document.getElementById('builderBox').classList.contains('hidden') &&
		document.querySelector('#builder .battr').value === 'mail' && document.getElementById('bFilter').textContent === '(mail=*example*)'`)
	var form [4]string
	js(t, ctx, `[document.getElementById('sBase').value, document.getElementById('sScope').value, document.getElementById('sAttrs').value, document.getElementById('sLimit').value]`, &form)
	if form != [4]string{people, "one", "uid, mail", "50"} {
		t.Errorf("restored form: %v", form)
	}
	run(t, ctx, chromedp.Click("#btnSearch"))
	waitText(t, ctx, "#sOut", "2 result(s)")

	// The raw one comes back in text mode.
	run(t, ctx, set("#qSaved", "Only zoe"), chromedp.Evaluate(`document.getElementById('qSaved').dispatchEvent(new Event('change'))`, nil))
	waitJS(t, ctx, "text mode", `!document.getElementById('rawBox').classList.contains('hidden') && document.getElementById('sFilter').value === '(uid=zoe)'`)

	// They are stored on the server: still there after reloading the page and reconnecting.
	reload(t, ctx, appURL)
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	waitJS(t, ctx, "saved queries after reload", `document.querySelectorAll('#qSaved option').length === 3`)

	// Delete one.
	run(t, ctx, chromedp.Click(`button[data-tab="search"]`), set("#qSaved", "Only zoe"),
		chromedp.Evaluate(`document.getElementById('qSaved').dispatchEvent(new Event('change'))`, nil), chromedp.Click("#qDelete"))
	waitText(t, ctx, "#qMsg", "Deleted.")
	if got := savedOptions(t, ctx); strings.Join(got, ",") != "People with mail" {
		t.Errorf("after delete: %v", got)
	}
}

// ---------- navigation ----------

// nodeHidden reports whether the tree node for dn is hidden by the quick filter.
func nodeHidden(t *testing.T, ctx context.Context, dn string) bool {
	t.Helper()
	var hidden bool
	js(t, ctx, fmt.Sprintf(`(() => { const n = document.querySelector('.node[title=%s]'); return !n || n.closest('li').classList.contains('hidden'); })()`, q(dn)), &hidden)
	return hidden
}

func TestUINavigation(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	base := slapdtest.BaseDN
	people, bulk := "ou=people,"+base, "ou=bulk,"+base

	root := `.node[title="` + base + `"]`
	run(t, ctx, chromedp.WaitVisible(root), chromedp.Click(root+" .tw"), chromedp.WaitVisible(`.node[title="`+people+`"]`))

	// Quick filter: only matching nodes (and their ancestors) stay.
	run(t, ctx, set("#treeFilter", "peop"), chromedp.Evaluate(`document.getElementById('treeFilter').dispatchEvent(new Event('input'))`, nil))
	waitJS(t, ctx, "bulk hidden", `document.querySelector('.node[title="`+bulk+`"]').closest('li').classList.contains('hidden')`)
	if nodeHidden(t, ctx, people) || nodeHidden(t, ctx, base) {
		t.Error("the matching node and its ancestor must stay visible")
	}
	waitJS(t, ctx, "hint", `!document.getElementById('treeHint').classList.contains('hidden')`)
	// Escape clears it.
	run(t, ctx, chromedp.Focus("#treeFilter"), chromedp.SendKeys("#treeFilter", "\x1b"))
	waitJS(t, ctx, "filter cleared", `document.getElementById('treeFilter').value === '' && !document.querySelector('.node[title="`+bulk+`"]').closest('li').classList.contains('hidden')`)

	// Go to a DN typed in any case, in a branch that is not open yet: the tree opens it.
	run(t, ctx, set("#treeFilter", strings.ToUpper(slapdtest.AliceDN)), chromedp.SendKeys("#treeFilter", "\r"))
	waitText(t, ctx, "#pane-entry .dn", slapdtest.AliceDN)
	waitJS(t, ctx, "alice selected in the tree", `document.querySelector('.node.sel') && document.querySelector('.node.sel').title === `+q(slapdtest.AliceDN))

	// Breadcrumb: every ancestor in the DN is a link; the entry itself is not.
	var crumbs []string
	js(t, ctx, `[...document.querySelectorAll('#pane-entry .dn .crumb')].map(a => a.textContent)`, &crumbs)
	if strings.Join(crumbs, "|") != "ou=people|dc=example|dc=org" {
		t.Errorf("breadcrumb links: %v", crumbs)
	}
	run(t, ctx, chromedp.Click(`#pane-entry .dn .crumb`)) // ou=people
	waitText(t, ctx, "#pane-entry .dn", "ou=people,"+base)
	waitJS(t, ctx, "people selected", `document.querySelector('.node.sel').title === `+q(people))

	// DN-valued attributes are links: a group's member opens that person.
	admins := "cn=admins,ou=groups," + base
	run(t, ctx, set("#treeFilter", admins), chromedp.SendKeys("#treeFilter", "\r"))
	waitText(t, ctx, "#pane-entry .dn", admins)
	waitJS(t, ctx, "member link", `!!document.querySelector('#pane-entry a.dnlink')`)
	var link string
	js(t, ctx, `document.querySelector('#pane-entry a.dnlink').textContent`, &link)
	if link != slapdtest.AliceDN {
		t.Errorf("member link text: %q", link)
	}
	run(t, ctx, chromedp.Click(`#pane-entry a.dnlink`))
	waitText(t, ctx, "#pane-entry .dn", slapdtest.AliceDN)
	waitJS(t, ctx, "alice selected after the link", `document.querySelector('.node.sel').title === `+q(slapdtest.AliceDN))
	// ...and ordinary attributes are not links.
	var plain bool
	js(t, ctx, `![...document.querySelectorAll('#pane-entry table.attrs tr')].some(tr => tr.querySelector('td.k').textContent === 'mail' && tr.querySelector('a'))`, &plain)
	if !plain {
		t.Error("mail must not be a link")
	}

	// A DN that does not exist is reported, not ignored.
	run(t, ctx, set("#treeFilter", "uid=nobody,"+people), chromedp.SendKeys("#treeFilter", "\r"))
	waitText(t, ctx, "#pane-entry .err", "No Such Object")
}

// ---------- passwords: the vault and several users ----------

const vaultPassphrase = "correct horse battery"

// fillConnection fills the connection dialog (it must be open) without submitting it.
func fillConnection(t *testing.T, ctx context.Context, name, url, bind, password, store string) {
	t.Helper()
	run(t, ctx, set("#cName", name), set("#cURL", url), set("#cBind", bind), set("#cPass", password), set("#cStore", store),
		chromedp.Evaluate(`document.getElementById('cStore').dispatchEvent(new Event('change'))`, nil))
}

func unlockDialogOpen(t *testing.T, ctx context.Context) bool {
	t.Helper()
	var open bool
	js(t, ctx, `document.getElementById('unlockDlg').open`, &open)
	return open
}

// answerVault types a passphrase in the vault dialog (and its confirmation) and submits it.
func answerVault(t *testing.T, ctx context.Context, pass, confirm string) {
	t.Helper()
	run(t, ctx, set("#uPass", pass), set("#uConfirm", confirm), chromedp.Click("#uOk"))
}

func quickOptions(t *testing.T, ctx context.Context) []string {
	t.Helper()
	var o []string
	js(t, ctx, `[...document.querySelectorAll('#quickConn option')].map(o => o.textContent)`, &o)
	return o
}

func TestUIPasswordVault(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))

	// Before any vault exists the button says so, and the selector explains each choice.
	waitText(t, ctx, "#btnVault", "Passwords")
	run(t, ctx, chromedp.Click("#btnConn"), chromedp.WaitVisible("#cStore"))
	fillConnection(t, ctx, "lab", l.URL, slapdtest.AliceDN, slapdtest.AlicePW, "vault")
	waitText(t, ctx, "#cStoreHint", "does not exist yet")
	var opts []struct {
		Value, Label string
		Disabled     bool
	}
	js(t, ctx, `[...document.querySelectorAll('#cStore option')].map(o => ({value: o.value, label: o.textContent, disabled: o.disabled}))`, &opts)
	if len(opts) != 3 {
		t.Fatalf("storage options: %+v", opts)
	}
	// The keychain option is disabled exactly when the machine has no usable keychain, and says so.
	if opts[1].Disabled != strings.Contains(opts[1].Label, "not available") {
		t.Errorf("keychain option inconsistent: %+v", opts[1])
	}

	// Saving the profile needs the vault: it is created right there.
	run(t, ctx, chromedp.Click("#btnSaveProfile"))
	waitJS(t, ctx, "create dialog", `document.getElementById('unlockDlg').open`)
	waitText(t, ctx, "#unlockTitle", "Create the vault")
	answerVault(t, ctx, "short", "short")
	waitText(t, ctx, "#uErr", "at least 8")
	answerVault(t, ctx, vaultPassphrase, "something different")
	waitText(t, ctx, "#uErr", "different")
	answerVault(t, ctx, vaultPassphrase, vaultPassphrase)
	waitJS(t, ctx, "dialog closed", `!document.getElementById('unlockDlg').open`)
	waitText(t, ctx, "#status", "Profile saved") // the save went through after the vault was created
	waitText(t, ctx, "#btnVault", "vault unlocked")
	run(t, ctx, chromedp.Click("#btnCancel"))

	// Quick switch: connect as that profile without typing the password.
	waitJS(t, ctx, "quick switch lists the profile", `[...document.querySelectorAll('#quickConn option')].some(o => o.value === 'lab')`)
	if got := quickOptions(t, ctx)[1]; !strings.HasPrefix(got, "lab — alice @ 127.0.0.1:") {
		t.Errorf("quick switch label: %q", got)
	}
	run(t, ctx, set("#quickConn", "lab"), chromedp.Evaluate(`document.getElementById('quickConn').dispatchEvent(new Event('change'))`, nil))
	waitText(t, ctx, "#status", "Connected to "+l.URL+" as "+slapdtest.AliceDN)
	run(t, ctx, chromedp.Click("#btnDisc"))
	waitText(t, ctx, "#status", "Not connected")

	// The Passwords panel shows the state and can lock the vault.
	run(t, ctx, chromedp.Click("#btnVault"), chromedp.WaitVisible("#mList"))
	waitText(t, ctx, "#mStatus", "The vault is unlocked")
	waitText(t, ctx, `#mList tr[data-profile="lab"] .state`, "saved")
	run(t, ctx, chromedp.Click("#mLock"))
	waitText(t, ctx, "#mStatus", "The vault is locked")
	waitText(t, ctx, `#mList tr[data-profile="lab"] .state`, "vault locked")
	waitText(t, ctx, "#btnVault", "vault locked")
	run(t, ctx, chromedp.Click("#mClose"))

	// Locked: connecting asks for the passphrase, a wrong one is refused, the right one connects.
	run(t, ctx, set("#quickConn", "lab"), chromedp.Evaluate(`document.getElementById('quickConn').dispatchEvent(new Event('change'))`, nil))
	waitJS(t, ctx, "unlock dialog", `document.getElementById('unlockDlg').open`)
	waitText(t, ctx, "#unlockTitle", "Unlock the vault")
	var confirmHidden bool
	js(t, ctx, `document.getElementById('uConfirm').classList.contains('hidden')`, &confirmHidden)
	if !confirmHidden {
		t.Error("unlocking asks for the passphrase once, not twice")
	}
	answerVault(t, ctx, "not the passphrase", "")
	waitText(t, ctx, "#uErr", "wrong passphrase")
	if !unlockDialogOpen(t, ctx) {
		t.Fatal("the dialog must stay open after a wrong passphrase")
	}
	answerVault(t, ctx, vaultPassphrase, "")
	waitText(t, ctx, "#status", "Connected to "+l.URL+" as "+slapdtest.AliceDN) // retried by itself
	waitText(t, ctx, "#btnVault", "vault unlocked")

	// Forget the password: the profile no longer connects on its own.
	run(t, ctx, chromedp.Click("#btnVault"), chromedp.WaitVisible("#mList"))
	run(t, ctx, chromedp.Click(`#mList tr[data-profile="lab"] button.forget`))
	waitText(t, ctx, `#mList tr[data-profile="lab"] .state`, "not saved")
	run(t, ctx, chromedp.Click("#mClose"))
	run(t, ctx, chromedp.Click("#btnDisc"))
	waitText(t, ctx, "#status", "Not connected")
	run(t, ctx, set("#quickConn", "lab"), chromedp.Evaluate(`document.getElementById('quickConn').dispatchEvent(new Event('change'))`, nil))
	waitJS(t, ctx, "connection dialog asks for the password", `document.getElementById('dlg').open`)
	waitText(t, ctx, "#cErr", "Enter the password for this user")
	var form [2]string
	js(t, ctx, `[document.getElementById('cBind').value, document.getElementById('cStore').value]`, &form)
	if form != [2]string{slapdtest.AliceDN, "none"} {
		t.Errorf("the dialog should be prefilled for that user: %v", form)
	}
}

func TestUIChangePassphraseAndSeveralUsers(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))

	// First user: creates the vault while saving.
	run(t, ctx, chromedp.Click("#btnConn"), chromedp.WaitVisible("#cStore"))
	fillConnection(t, ctx, "alice@lab", l.URL, slapdtest.AliceDN, slapdtest.AlicePW, "vault")
	run(t, ctx, chromedp.Click("#btnSaveProfile"))
	waitJS(t, ctx, "create dialog", `document.getElementById('unlockDlg').open`)
	answerVault(t, ctx, vaultPassphrase, vaultPassphrase)
	waitText(t, ctx, "#status", "Profile saved")

	// Second user on the same server: "Add another user" keeps the server settings.
	run(t, ctx, chromedp.Click("#btnDupProfile"))
	var kept [3]string
	js(t, ctx, `[document.getElementById('cName').value, document.getElementById('cURL').value, document.getElementById('cStore').value]`, &kept)
	if kept != [3]string{"", l.URL, "vault"} {
		t.Errorf("Add another user must keep the URL and the storage and clear the name: %v", kept)
	}
	waitText(t, ctx, "#cErr", "Same server")
	run(t, ctx, set("#cName", "bob@lab"), set("#cBind", slapdtest.BobDN), set("#cPass", slapdtest.BobPW), chromedp.Click("#btnSaveProfile"))
	waitText(t, ctx, "#status", "Profile saved")
	if unlockDialogOpen(t, ctx) {
		t.Error("the vault is already unlocked: no prompt needed")
	}

	// A third one, on the same server over TLS, kept in nothing (typed each time).
	run(t, ctx, chromedp.Click("#btnDupProfile"), set("#cName", "alice@lab-tls"), set("#cURL", l.LDAPSURL), set("#cBind", slapdtest.AliceDN),
		set("#cStore", "none"), chromedp.Click("#cSkip"), chromedp.Click("#btnSaveProfile"))
	waitJS(t, ctx, "three profiles", `document.querySelectorAll('#quickConn option').length === 4`)
	run(t, ctx, chromedp.Click("#btnCancel"))

	// Switch between them with one click; the status shows who is connected.
	for _, who := range []struct{ profile, dn string }{{"alice@lab", slapdtest.AliceDN}, {"bob@lab", slapdtest.BobDN}, {"alice@lab", slapdtest.AliceDN}} {
		run(t, ctx, set("#quickConn", who.profile), chromedp.Evaluate(`document.getElementById('quickConn').dispatchEvent(new Event('change'))`, nil))
		waitText(t, ctx, "#status", "Connected to "+l.URL+" as "+who.dn)
		var selected string
		js(t, ctx, `document.getElementById('quickConn').value`, &selected)
		if selected != who.profile {
			t.Errorf("the quick switch should show the current profile: %q", selected)
		}
	}
	// The one that keeps nothing asks for a password instead of failing.
	run(t, ctx, set("#quickConn", "alice@lab-tls"), chromedp.Evaluate(`document.getElementById('quickConn').dispatchEvent(new Event('change'))`, nil))
	waitJS(t, ctx, "dialog for the typed password", `document.getElementById('dlg').open`)
	run(t, ctx, set("#cPass", slapdtest.AlicePW), chromedp.Click("#btnDoConn"))
	waitText(t, ctx, "#status", "Connected to "+l.LDAPSURL+" as "+slapdtest.AliceDN)

	// The panel lists everyone, each with its own state.
	run(t, ctx, chromedp.Click("#btnVault"), chromedp.WaitVisible("#mList"))
	waitText(t, ctx, `#mList tr[data-profile="alice@lab"] .state`, "saved")
	waitText(t, ctx, `#mList tr[data-profile="bob@lab"] .state`, "saved")
	waitText(t, ctx, `#mList tr[data-profile="alice@lab-tls"] .state`, "not saved")

	// Changing the passphrase: checks, then the old one stops working.
	run(t, ctx, chromedp.Click("#mChange"), chromedp.WaitVisible("#mOld"),
		set("#mOld", vaultPassphrase), set("#mNew", "short"), set("#mNew2", "short"), chromedp.Click("#changeForm button[type=submit]"))
	waitText(t, ctx, "#mErr", "at least 8")
	run(t, ctx, set("#mNew", "a brand new passphrase"), set("#mNew2", "a brand new passphrase"), set("#mOld", "wrong wrong wrong"), chromedp.Click("#changeForm button[type=submit]"))
	waitText(t, ctx, "#mErr", "wrong passphrase")
	run(t, ctx, set("#mOld", vaultPassphrase), chromedp.Click("#changeForm button[type=submit]"))
	waitJS(t, ctx, "change form closed", `document.getElementById('changeForm').classList.contains('hidden')`)
	run(t, ctx, chromedp.Click("#mLock"))
	waitText(t, ctx, "#mStatus", "The vault is locked")
	run(t, ctx, chromedp.Click("#mUnlock"))
	waitJS(t, ctx, "unlock dialog", `document.getElementById('unlockDlg').open`)
	answerVault(t, ctx, vaultPassphrase, "") // the old passphrase no longer works
	waitText(t, ctx, "#uErr", "wrong passphrase")
	answerVault(t, ctx, "a brand new passphrase", "")
	waitJS(t, ctx, "dialog closed", `!document.getElementById('unlockDlg').open`)
	waitText(t, ctx, "#mStatus", "The vault is unlocked")

	// Layout: nothing overflows the window, even with the new header controls.
	var overflow bool
	js(t, ctx, `document.documentElement.scrollWidth > document.documentElement.clientWidth + 1`, &overflow)
	if overflow {
		t.Error("the page has a horizontal scrollbar")
	}
}

// ---------- copying ----------

// allowClipboard lets the page read and write the clipboard, so a test can check what was copied.
func allowClipboard(t *testing.T, ctx context.Context, appURL string) {
	t.Helper()
	origin := strings.SplitN(appURL, "/#", 2)[0]
	run(t, ctx, cdpbrowser.GrantPermissions([]cdpbrowser.PermissionType{
		cdpbrowser.PermissionTypeClipboardReadWrite, cdpbrowser.PermissionTypeClipboardSanitizedWrite}).WithOrigin(origin))
}

func clipboard(t *testing.T, ctx context.Context) string {
	t.Helper()
	var s string
	run(t, ctx, chromedp.Evaluate(`navigator.clipboard.readText()`, &s, func(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
		return p.WithAwaitPromise(true)
	}))
	return s
}

// clickCopy clicks a copy button and checks the clipboard and the confirmation.
func clickCopy(t *testing.T, ctx context.Context, button, wantClipboard, wantToast string) {
	t.Helper()
	run(t, ctx, chromedp.Click(button))
	waitJS(t, ctx, "clipboard "+wantClipboard, fmt.Sprintf(`navigator.clipboard.readText().then(t => t === %s)`, q(wantClipboard)))
	if got := clipboard(t, ctx); got != wantClipboard {
		t.Errorf("%s: clipboard %q, want %q", button, got, wantClipboard)
	}
	if wantToast != "" {
		waitText(t, ctx, "#toasts", wantToast)
	}
}

func TestUICopy(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	allowClipboard(t, ctx, appURL)
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")

	root := `.node[title="` + slapdtest.BaseDN + `"]`
	run(t, ctx, chromedp.WaitVisible(root), chromedp.Click(root+" .tw"))
	people := "ou=people," + slapdtest.BaseDN
	run(t, ctx, chromedp.WaitVisible(`.node[title="`+people+`"]`), chromedp.Click(`.node[title="`+people+`"] .tw`))
	aliceNode := `.node[title="` + slapdtest.AliceDN + `"]`
	run(t, ctx, chromedp.WaitVisible(aliceNode), chromedp.Click(aliceNode))
	waitText(t, ctx, "#pane-entry .dn", slapdtest.AliceDN)

	// An attribute: its name and its value.
	clickCopy(t, ctx, `tr[data-attr="mail"] button[title^="Copy the attribute name"]`, "mail", "Copied mail")
	clickCopy(t, ctx, `tr[data-attr="mail"] button[title="Copy the value"]`, "alice@example.org", "Copied the value of mail")

	// The DN of the entry, from the header and from the tree.
	clickCopy(t, ctx, `#pane-entry .dn button[title="Copy the DN"]`, slapdtest.AliceDN, "Copied the DN")
	clickCopy(t, ctx, `.node[title="`+slapdtest.AliceDN+`"] .copy-btn`, slapdtest.AliceDN, "")

	// Several values: the first, or all of them, one per line.
	bob := `.node[title="uid=bob,` + people + `"]`
	run(t, ctx, chromedp.Click(bob))
	waitText(t, ctx, "#pane-entry .dn", "uid=bob,")
	clickCopy(t, ctx, `tr[data-attr="telephoneNumber"] button[title="Copy all 2 values, one per line"]`,
		"+34 600 000 001\n+34 600 000 002", "Copied 2 values of telephoneNumber")
	clickCopy(t, ctx, `tr[data-attr="telephoneNumber"] button[title="Copy the value"]`, "+34 600 000 001", "")
	var label string
	js(t, ctx, `document.querySelector('tr[data-attr="telephoneNumber"] button[title="Copy the value"]').textContent`, &label)
	if label != "first value" {
		t.Errorf("with several values the button says so: %q", label)
	}

	// The confirmation goes away by itself.
	waitJS(t, ctx, "toast gone", `document.getElementById('toasts').children.length === 0`)

	// A table cell copies when clicked.
	run(t, ctx, chromedp.Click(`button[data-tab="search"]`), chromedp.WaitReady("#sBase"), rawMode(),
		set("#sBase", people), set("#sScope", "one"), set("#sFilter", "(uid=bob)"), set("#sAttrs", "uid, mail"),
		chromedp.Click("#btnSearch"))
	waitText(t, ctx, "#sOut", "1 result(s)")
	run(t, ctx, chromedp.Click("#viewTable"), chromedp.WaitVisible("#sOut table.rtable"))
	run(t, ctx, chromedp.Click(`#sOut table.rtable tbody td.copy-cell:nth-child(3)`)) // the mail cell
	waitJS(t, ctx, "cell copied", `navigator.clipboard.readText().then(t => t === 'bob@example.org')`)
	waitText(t, ctx, "#toasts", `Copied "bob@example.org"`)

	// The LDAP filter of the builder.
	run(t, ctx, chromedp.Click("#modeBuilder"))
	setAttr(t, ctx, 0, "uid")
	setOp(t, ctx, 0, "startsWith")
	setVal(t, ctx, 0, "al")
	waitText(t, ctx, "#bFilter", "(uid=al*)")
	clickCopy(t, ctx, "#bCopy", "(uid=al*)", "Copied the LDAP filter")
}

// ---------- theme ----------

func bodyColor(t *testing.T, ctx context.Context) string {
	t.Helper()
	var c string
	js(t, ctx, `getComputedStyle(document.body).backgroundColor`, &c)
	return c
}

func TestUITheme(t *testing.T) {
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnTheme"))
	waitText(t, ctx, "#btnTheme", "Theme: auto")
	attr := func() string {
		var a string
		js(t, ctx, `document.documentElement.getAttribute('data-theme') || ''`, &a)
		return a
	}
	auto := bodyColor(t, ctx)

	run(t, ctx, chromedp.Click("#btnTheme"))
	waitText(t, ctx, "#btnTheme", "Theme: light")
	if attr() != "light" {
		t.Errorf("data-theme: %q", attr())
	}
	light := bodyColor(t, ctx)
	run(t, ctx, chromedp.Click("#btnTheme"))
	waitText(t, ctx, "#btnTheme", "Theme: dark")
	if attr() != "dark" {
		t.Errorf("data-theme: %q", attr())
	}
	dark := bodyColor(t, ctx)
	if light == dark {
		t.Errorf("light and dark look the same: %s", light)
	}
	if light != "rgb(246, 246, 244)" || dark != "rgb(27, 27, 29)" {
		t.Errorf("tokens not applied: light=%s dark=%s", light, dark)
	}

	// It is remembered: reload the page and it is still dark.
	reload(t, ctx, appURL)
	waitText(t, ctx, "#btnTheme", "Theme: dark")
	if got := bodyColor(t, ctx); got != dark {
		t.Errorf("after reload: %s, want %s", got, dark)
	}
	run(t, ctx, chromedp.Click("#btnTheme"))
	waitText(t, ctx, "#btnTheme", "Theme: auto")
	if attr() != "" {
		t.Errorf("auto must not force a theme: %q", attr())
	}
	if got := bodyColor(t, ctx); got != auto {
		t.Errorf("auto goes back to the system look: %s, want %s", got, auto)
	}
}

// ---------- a builder anyone can use ----------

func TestUISimpleBuilderAndQuickFind(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))

	// Nothing to search before connecting.
	var disabled bool
	js(t, ctx, `document.getElementById('quickFind').disabled`, &disabled)
	if !disabled {
		t.Error("the quick find box needs a connection")
	}
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	run(t, ctx, chromedp.Click(`button[data-tab="search"]`), chromedp.WaitVisible("#builder"))

	// The options stay folded, with a sentence that says what they are.
	var open bool
	js(t, ctx, `document.getElementById('sOptions').open`, &open)
	if open {
		t.Error("the options start folded")
	}
	waitText(t, ctx, "#sOptSummary", "Looking in the whole branch below "+slapdtest.BaseDN+", showing all attributes, at most 500 results")
	run(t, ctx, chromedp.Click("#sOptions > summary"), chromedp.WaitVisible("#sLimit"),
		set("#sLimit", "50"), chromedp.Evaluate(`document.getElementById('sLimit').dispatchEvent(new Event('input'))`, nil))
	waitText(t, ctx, "#sOptSummary", "at most 50 results")

	// Friendly names in the suggestions...
	waitJS(t, ctx, "suggestions", `document.querySelectorAll('#attrList option').length > 50`)
	var label string
	js(t, ctx, `document.querySelector('#attrList option[value="mail"]').label`, &label)
	if label != "Email" {
		t.Errorf("the suggestion for mail should say Email: %q", label)
	}
	// ...and one-click chips for the common fields this server has.
	var chips []string
	js(t, ctx, `[...document.querySelectorAll('#bChips .chip')].map(c => c.textContent)`, &chips)
	if len(chips) < 5 || chips[0] != "Full name" || chips[1] != "Login" || chips[2] != "Email" {
		t.Fatalf("chips: %v", chips)
	}

	rows := func() int {
		var n int
		js(t, ctx, `document.querySelectorAll('#builder .bcond').length`, &n)
		return n
	}
	chip := func(name string) {
		i := slices.Index(chips, name)
		if i < 0 {
			t.Fatalf("no chip %q in %v", name, chips)
		}
		run(t, ctx, chromedp.Click(fmt.Sprintf("#bChips .chip:nth-child(%d)", i+1)))
	}
	chip("Email") // reuses the empty first row
	if rows() != 1 {
		t.Errorf("the first chip should fill the empty row, rows=%d", rows())
	}
	var first struct{ Attr, Op, Caption string }
	js(t, ctx, `({ attr: document.querySelector('#builder .battr').value, op: document.querySelector('#builder .bop').value,
		caption: document.querySelector('#builder .battr-label').textContent })`, &first)
	if first.Attr != "mail" || first.Op != "contains" || first.Caption != "Email" {
		t.Errorf("a chip fills attribute, operator and caption: %+v", first)
	}
	var focused string
	js(t, ctx, `document.activeElement.className`, &focused)
	if !strings.Contains(focused, "bval") {
		t.Errorf("after a chip the value box is ready to type in: %q", focused)
	}
	setVal(t, ctx, 0, "example")
	chip("Login")
	chip("Created")
	if rows() != 3 {
		t.Errorf("each further chip adds a row: %d", rows())
	}
	// a row without a value is explained, not silently searched
	waitText(t, ctx, "#bErr", "needs a value")
	setVal(t, ctx, 1, "al")
	waitJS(t, ctx, "three conditions in the filter", `/^\(&\(mail=\*example\*\)\(uid=\*al\*\)\(createTimestamp>=\d{14}Z\)\)$/.test(document.getElementById('bFilter').textContent)`)
	var op string
	js(t, ctx, `document.querySelectorAll('#builder .bop')[2].value`, &op)
	if op != "withinDays" {
		t.Errorf("a date chip starts as 'in the last N days': %q", op)
	}

	// Nothing is clipped, even with the chips.
	var inside bool
	js(t, ctx, `(() => { const g = document.querySelector('#builder .bgroup').getBoundingClientRect();
		return [...document.querySelectorAll('#builder .bcond > *, #bChips .chip')].every(e => e.getBoundingClientRect().right <= g.right + 1); })()`, &inside)
	if !inside {
		t.Error("something overflows the builder box")
	}

	// Quick find: one box in the header, Enter, results.
	run(t, ctx, chromedp.Click(`button[data-tab="explore"]`)) // from another tab
	run(t, ctx, chromedp.Focus("#quickFind"), chromedp.SendKeys("#quickFind", "bob\r"))
	waitJS(t, ctx, "search tab", `document.getElementById('pane-search').classList.contains('on')`)
	waitText(t, ctx, "#sNote", "Find any entry")
	waitText(t, ctx, "#sOut", "result(s)")
	waitText(t, ctx, "#sOut", "uid=bob,")
	waitJS(t, ctx, "the recipe is shown in the builder", `document.querySelectorAll('#builder .bcond').length >= 3`)
	// an empty search does nothing, and a miss says so clearly
	run(t, ctx, set("#quickFind", ""), chromedp.Focus("#quickFind"), chromedp.SendKeys("#quickFind", "zzzzzz\r"))
	waitText(t, ctx, "#sOut", "0 result(s)")
}
