package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JorTurFer/ldap-explorer/internal/slapdtest"
	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"
	kb "github.com/chromedp/chromedp/kb"
)

// ---------- helpers ----------

// Act is a step that returns no value (chromedp v0.24 made actions generic).
type Act = chromedp.Action[chromedp.Void]

// eval runs JavaScript and ignores its result.
func eval(expr string, opts ...chromedp.EvaluateOption) Act {
	return evalInto(expr, nil, opts...)
}

// evalInto runs JavaScript and decodes its JSON result into out (when out is not nil).
func evalInto(expr string, out any, opts ...chromedp.EvaluateOption) Act {
	return func(ctx context.Context, t *chromedp.Target) (chromedp.Void, error) {
		raw, err := chromedp.Evaluate[json.RawMessage](expr, opts...)(ctx, t)
		if err != nil || out == nil || len(raw) == 0 {
			return chromedp.Void{}, err
		}
		return chromedp.Void{}, json.Unmarshal(raw, out)
	}
}

func run(t *testing.T, ctx context.Context, actions ...Act) {
	t.Helper()
	if err := chromedp.Do(ctx, actions...); err != nil {
		t.Fatal(err)
	}
}

// waitJS waits until the JavaScript expression is truthy.
func waitJS(t *testing.T, ctx context.Context, what, expr string) {
	t.Helper()
	if err := chromedp.Do(ctx, chromedp.Poll[chromedp.Void](expr, chromedp.WithPollingTimeout(20*time.Second))); err != nil {
		t.Fatalf("timed out waiting for %s: %v\n--- what the page showed ---\n%s", what, err, visibleText(ctx))
	}
}

// visibleText is the text of the active tab and of the status line, to explain a timeout
// (a failure that only happens on one CI platform must come with something to look at).
// It uses a fresh timeout because the test's own context may be the one that expired.
func visibleText(ctx context.Context) string {
	var s string
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := chromedp.Do(c, evalInto(`(document.getElementById('status')||{}).textContent + '\n' + ((document.querySelector('.pane.on')||{}).innerText || '').slice(0, 700)`, &s)); err != nil {
		return "(the page could not be read: " + err.Error() + ")"
	}
	return s
}

func js(t *testing.T, ctx context.Context, expr string, out any) {
	t.Helper()
	run(t, ctx, evalInto(expr, out))
}

// set sets an input/select value from JS (chromedp.SetValue fails on empty strings).
func set(sel, val string) Act {
	return eval(fmt.Sprintf(`(() => { const e = document.querySelector(%s); e.value = %s; return true; })()`, q(sel), q(val)))
}

// jsAsync is js for an expression that returns a Promise: it waits for it.
func jsAsync(t *testing.T, ctx context.Context, expr string, out any) {
	t.Helper()
	run(t, ctx, evalInto(expr, out, chromedp.EvalAwaitPromise))
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
func rawMode() Act { return chromedp.Click("#modeRaw") }

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

	// Invalid filter: rejected with a readable message. (The history list refreshes after the
	// previous search; wait for it so nothing moves under the click.)
	waitJS(t, ctx, "history settled", `document.querySelectorAll('#hSel option').length >= 2`)
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
	run(t, ctx, eval(`document.getElementById('cProfile').dispatchEvent(new Event('change'))`))
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
		allowDownloads(downloads))

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
	run(t, ctx, set(".bgroup .bmode", "any"), eval(`document.querySelector('.bgroup .bmode').dispatchEvent(new Event('change'))`))
	waitText(t, ctx, "#bFilter", "(|(mail=*example*)(uid=b*))")
	waitText(t, ctx, "#bExplain", "At least one of these must be true:")
	run(t, ctx, set(".bgroup .bmode", "none"), eval(`document.querySelector('.bgroup .bmode').dispatchEvent(new Event('change'))`))
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
	run(t, ctx, set("#qSaved", "People with mail"), eval(`document.getElementById('qSaved').dispatchEvent(new Event('change'))`))
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
	run(t, ctx, set("#qSaved", "Only zoe"), eval(`document.getElementById('qSaved').dispatchEvent(new Event('change'))`))
	waitJS(t, ctx, "text mode", `!document.getElementById('rawBox').classList.contains('hidden') && document.getElementById('sFilter').value === '(uid=zoe)'`)

	// They are stored on the server: still there after reloading the page and reconnecting.
	reload(t, ctx, appURL)
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	waitJS(t, ctx, "saved queries after reload", `document.querySelectorAll('#qSaved option').length === 3`)

	// Delete one.
	run(t, ctx, chromedp.Click(`button[data-tab="search"]`), set("#qSaved", "Only zoe"),
		eval(`document.getElementById('qSaved').dispatchEvent(new Event('change'))`), chromedp.Click("#qDelete"))
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
	run(t, ctx, set("#treeFilter", "peop"), eval(`document.getElementById('treeFilter').dispatchEvent(new Event('input'))`))
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
		eval(`document.getElementById('cStore').dispatchEvent(new Event('change'))`))
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
	run(t, ctx, set("#quickConn", "lab"), eval(`document.getElementById('quickConn').dispatchEvent(new Event('change'))`))
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
	run(t, ctx, set("#quickConn", "lab"), eval(`document.getElementById('quickConn').dispatchEvent(new Event('change'))`))
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
	run(t, ctx, set("#quickConn", "lab"), eval(`document.getElementById('quickConn').dispatchEvent(new Event('change'))`))
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
		run(t, ctx, set("#quickConn", who.profile), eval(`document.getElementById('quickConn').dispatchEvent(new Event('change'))`))
		waitText(t, ctx, "#status", "Connected to "+l.URL+" as "+who.dn)
		var selected string
		js(t, ctx, `document.getElementById('quickConn').value`, &selected)
		if selected != who.profile {
			t.Errorf("the quick switch should show the current profile: %q", selected)
		}
	}
	// The one that keeps nothing asks for a password instead of failing.
	run(t, ctx, set("#quickConn", "alice@lab-tls"), eval(`document.getElementById('quickConn').dispatchEvent(new Event('change'))`))
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
	run(t, ctx, chromedp.Func(func(ctx context.Context, _ *chromedp.Target) error {
		for _, name := range []string{"clipboard-read", "clipboard-write"} {
			if _, err := chromedp.CallBrowser(ctx, cdpbrowser.SetPermission, cdpbrowser.SetPermissionParams{
				Permission: &cdpbrowser.PermissionDescriptor{Name: name},
				Setting:    cdpbrowser.PermissionSettingGranted,
				Origin:     origin,
			}); err != nil {
				return err
			}
		}
		return nil
	}))
}

// allowDownloads makes the browser save downloads into dir without asking.
func allowDownloads(dir string) Act {
	return chromedp.Func(func(ctx context.Context, _ *chromedp.Target) error {
		_, err := chromedp.CallBrowser(ctx, cdpbrowser.SetDownloadBehavior, cdpbrowser.SetDownloadBehaviorParams{
			Behavior: cdpbrowser.SetDownloadBehaviorBehaviorAllow, DownloadPath: dir})
		return err
	})
}

func clipboard(t *testing.T, ctx context.Context) string {
	t.Helper()
	var s string
	// Windows stores line breaks as CRLF in the clipboard (what any app pasting from it expects).
	run(t, ctx, evalInto(`navigator.clipboard.readText().then(t => t.replace(/\r\n/g, "\n"))`, &s, chromedp.EvalAwaitPromise))
	return s
}

// clickCopy clicks a copy button and checks the clipboard and the confirmation.
func clickCopy(t *testing.T, ctx context.Context, button, wantClipboard, wantToast string) {
	t.Helper()
	run(t, ctx, chromedp.Click(button))
	waitJS(t, ctx, "clipboard "+wantClipboard, fmt.Sprintf(`navigator.clipboard.readText().then(t => t.replace(/\r\n/g, "\n") === %s)`, q(wantClipboard)))
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
		set("#sLimit", "50"), eval(`document.getElementById('sLimit').dispatchEvent(new Event('input'))`))
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

// ---------- exporting ----------

// waitDownload waits for one finished download matching the pattern and returns its content.
func waitDownload(t *testing.T, dir, pattern string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		matches, _ := filepath.Glob(filepath.Join(dir, pattern))
		if len(matches) == 1 {
			if b, err := os.ReadFile(matches[0]); err == nil {
				return string(b)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no download matching %s in %s", pattern, dir)
	return ""
}

func TestUIExport(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	downloads := t.TempDir()
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"),
		allowDownloads(downloads))
	allowClipboard(t, ctx, appURL)
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")

	// An entry: copy it as LDIF or JSON, or save it as a file.
	run(t, ctx, set("#treeFilter", slapdtest.AliceDN), chromedp.SendKeys("#treeFilter", "\r"))
	waitText(t, ctx, "#pane-entry .dn", slapdtest.AliceDN)
	run(t, ctx, chromedp.Click("#eCopyLdif"))
	waitText(t, ctx, "#toasts", "Copied the entry as LDIF")
	ldifText := clipboard(t, ctx)
	for _, want := range []string{"version: 1", "dn: " + slapdtest.AliceDN, "mail: alice@example.org", "jpegPhoto:: /9j/"} {
		if !strings.Contains(ldifText, want) {
			t.Errorf("the LDIF should contain %q:\n%s", want, ldifText)
		}
	}
	run(t, ctx, chromedp.Click("#eDlLdif"))
	if got := waitDownload(t, downloads, "alice.ldif"); got != ldifText {
		t.Error("the downloaded LDIF should be what Copy as LDIF copies")
	}
	run(t, ctx, chromedp.Click("#eCopyJson"))
	waitText(t, ctx, "#toasts", "Copied the entry as JSON")
	var entry struct {
		DN         string              `json:"dn"`
		Attributes map[string][]string `json:"attributes"`
	}
	if err := json.Unmarshal([]byte(clipboard(t, ctx)), &entry); err != nil {
		t.Fatalf("the JSON on the clipboard is not JSON: %v", err)
	}
	if entry.DN != slapdtest.AliceDN || len(entry.Attributes["mail"]) != 1 || entry.Attributes["mail"][0] != "alice@example.org" {
		t.Errorf("JSON entry: %+v", entry)
	}

	// Search results: LDIF with the exact values, or JSON.
	people := "ou=people," + slapdtest.BaseDN
	run(t, ctx, chromedp.Click(`button[data-tab="search"]`), chromedp.WaitReady("#sBase"), rawMode(),
		set("#sBase", people), set("#sScope", "one"), set("#sFilter", "(objectClass=inetOrgPerson)"), set("#sAttrs", "uid mail"),
		chromedp.Click("#btnSearch"))
	waitText(t, ctx, "#sOut", "3 result(s)")
	run(t, ctx, chromedp.Click("#btnLdif"))
	text := waitDownload(t, downloads, "ldap-explorer-*.ldif")
	if n := strings.Count(text, "\ndn: uid="); n != 3 {
		t.Errorf("LDIF export: %d entries\n%s", n, text)
	}
	if strings.Contains(text, "objectClass") || !strings.Contains(text, "mail: bob@example.org") {
		t.Errorf("only the requested attributes: %s", text)
	}
	run(t, ctx, chromedp.Click("#btnJson"))
	var all []struct{ DN string }
	if err := json.Unmarshal([]byte(waitDownload(t, downloads, "ldap-explorer-*.json")), &all); err != nil || len(all) != 3 {
		t.Errorf("JSON export: %v %+v", err, all)
	}

	// Nothing to export: the buttons are off.
	run(t, ctx, set("#sFilter", "(uid=nobody)"), chromedp.Click("#btnSearch"))
	waitText(t, ctx, "#sOut", "0 result(s)")
	var off bool
	js(t, ctx, `['btnCsv','btnJson','btnLdif'].every(id => document.getElementById(id).disabled)`, &off)
	if !off {
		t.Error("with no results the export buttons must be disabled")
	}
}

// ---------- binary and decoded values ----------

func TestUIBinaryAndTimeValues(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	downloads := t.TempDir()
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"),
		allowDownloads(downloads))
	allowClipboard(t, ctx, appURL)
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	run(t, ctx, set("#treeFilter", slapdtest.AliceDN), chromedp.SendKeys("#treeFilter", "\r"))
	waitText(t, ctx, "#pane-entry .dn", slapdtest.AliceDN)

	// The photo is drawn (a decoded 24x24 image, not a broken one) and sized.
	row := `tr[data-attr="jpegPhoto"]`
	waitText(t, ctx, row+" td.v", "(binary,")
	waitJS(t, ctx, "photo drawn", `(() => { const i = document.querySelector('`+row+` img.photo'); return !!i && i.complete && i.naturalWidth === 24 && i.naturalHeight === 24; })()`)
	var size string
	js(t, ctx, `document.querySelector('`+row+` button.download').textContent`, &size)
	if !strings.HasPrefix(size, "download (") || !strings.Contains(size, "bytes)") {
		t.Errorf("the download button says how big it is: %q", size)
	}

	// Copy the exact bytes as base64.
	run(t, ctx, chromedp.Click(row+` button[title="Copy the value as base64"]`))
	waitText(t, ctx, "#toasts", "as base64")
	var decoded struct {
		Magic string `json:"magic"`
		Len   int    `json:"len"`
	}
	jsAsync(t, ctx, `navigator.clipboard.readText().then(t => { const b = atob(t); return { magic: [...b.slice(0, 2)].map(c => c.charCodeAt(0).toString(16)).join(''), len: b.length }; })`, &decoded)
	if decoded.Magic != "ffd8" || decoded.Len < 600 {
		t.Errorf("the clipboard should hold the JPEG: %+v", decoded)
	}

	// Download it: the file has the same size.
	run(t, ctx, chromedp.Click(row+` button.download`))
	file := waitDownload(t, downloads, "jpegPhoto.jpg")
	if len(file) != decoded.Len || !strings.HasPrefix(file, "\xff\xd8") {
		t.Errorf("the downloaded photo has %d bytes, the clipboard %d", len(file), decoded.Len)
	}

	// A text attribute has no download button, and its copy button copies the value as before.
	var hasDownload bool
	js(t, ctx, `!!document.querySelector('tr[data-attr="mail"] button.download')`, &hasDownload)
	if hasDownload {
		t.Error("only binary values can be downloaded")
	}

	// Dates are shown in plain words next to the raw value.
	run(t, ctx, chromedp.Click("#chkOp"))
	waitJS(t, ctx, "decoded time", `/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} UTC · (just now|\d+ (minute|hour|day)s? ago)$/.test((document.querySelector('tr[data-attr="createTimestamp"] .decoded')||{}).textContent || '')`)
	var raw string
	js(t, ctx, `document.querySelector('tr[data-attr="createTimestamp"] td.v div').firstChild.textContent`, &raw)
	if !regexp.MustCompile(`^\d{14}Z$`).MatchString(raw) {
		t.Errorf("the raw value stays visible: %q", raw)
	}
	var nonTime bool
	js(t, ctx, `!document.querySelector('tr[data-attr="mail"] .decoded') && !document.querySelector('tr[data-attr="entryUUID"] .decoded')`, &nonTime)
	if !nonTime {
		t.Error("only date attributes get a decoded line")
	}
}

// ---------- server information and the schema browser ----------

func TestUIServerAndSchema(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	allowClipboard(t, ctx, appURL)
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	run(t, ctx, chromedp.Click(`button[data-tab="server"]`), chromedp.WaitVisible("#srvInfo"))

	// What the server is, in plain words, with names for the technical identifiers.
	waitText(t, ctx, "#srvInfo", "OpenLDAP")
	waitText(t, ctx, "#srvInfo", "Paged results")
	waitText(t, ctx, "#srvInfo", "StartTLS")
	waitText(t, ctx, "#srvInfo", slapdtest.BaseDN)
	clickCopy(t, ctx, `#srvInfo .nc .copy-btn`, slapdtest.BaseDN, "Copied the DN")
	run(t, ctx, chromedp.Click(`#srvInfo .oidline .copy-btn`))
	waitText(t, ctx, "#toasts", "Copied the OID")
	if got := clipboard(t, ctx); !regexp.MustCompile(`^\d+(\.\d+)+$`).MatchString(got) {
		t.Errorf("an OID was expected on the clipboard: %q", got)
	}

	// The schema list, filtered: the exact name comes first.
	waitJS(t, ctx, "attributes listed", `document.querySelectorAll('#scList .scitem').length > 50`)
	waitText(t, ctx, "#scCount", "attributes")
	run(t, ctx, set("#scFilter", "mail"), eval(`document.getElementById('scFilter').dispatchEvent(new Event('input'))`))
	waitJS(t, ctx, "mail first", `document.querySelector('#scList .scitem').dataset.name === 'mail'`)
	run(t, ctx, chromedp.Click(`#scList .scitem[data-name="mail"] a`))
	waitText(t, ctx, "#scDetail", "0.9.2342.19200300.100.1.3")
	waitText(t, ctx, "#scDetail", "IA5 String")
	waitText(t, ctx, "#scDetail", "caseIgnoreIA5SubstringsMatch")
	waitText(t, ctx, "#scDetail", "ordering (before/after): not supported")
	waitText(t, ctx, "#scDetail", "an ordinary attribute")
	waitText(t, ctx, "#scDetail", "rfc822Mailbox") // its other name

	// Follow it to a class that uses it, and from there up the inheritance chain.
	run(t, ctx, chromedp.Click(`#scDetail .scref:nth-of-type(1)`)) // first class listed under "Used by"
	waitJS(t, ctx, "switched to classes", `document.getElementById('scClasses').classList.contains('on')`)
	run(t, ctx, set("#scFilter", "inetOrgPerson"), eval(`document.getElementById('scFilter').dispatchEvent(new Event('input'))`))
	run(t, ctx, chromedp.Click(`#scList .scitem[data-name="inetOrgPerson"] a`))
	waitText(t, ctx, "#scDetail h3", "inetOrgPerson")
	waitText(t, ctx, "#scDetail", "structural")
	waitText(t, ctx, "#scDetail", "organizationalPerson → ")
	waitText(t, ctx, "#scDetail", "person → ")
	var required []string
	js(t, ctx, `[...document.querySelectorAll('#scDetail .kvrow')].find(r => r.firstChild.textContent === 'Required attributes').querySelectorAll('.scref').length > 0
		? [...[...document.querySelectorAll('#scDetail .kvrow')].find(r => r.firstChild.textContent === 'Required attributes').querySelectorAll('.scref')].map(a => a.textContent) : []`, &required)
	if !slices.Contains(required, "sn") || !slices.Contains(required, "cn") || !slices.Contains(required, "objectClass") {
		t.Errorf("the required attributes include the inherited ones: %v", required)
	}

	// An attribute found by its alias (surname is another name for sn).
	run(t, ctx, set("#scFilter", ""), chromedp.Click("#scAttrs"), set("#scFilter", "surname"), eval(`document.getElementById('scFilter').dispatchEvent(new Event('input'))`))
	run(t, ctx, chromedp.Click(`#scList .scitem[data-name="sn"] a`))
	waitText(t, ctx, "#scDetail", "surname")
	clickCopy(t, ctx, `#scDetail h3 .copy-btn`, "sn", "Copied the name")

	// "Find entries that have it" runs a search.
	run(t, ctx, chromedp.Click(`#scDetail button.find`))
	waitJS(t, ctx, "search tab", `document.getElementById('pane-search').classList.contains('on')`)
	waitText(t, ctx, "#sNote", "Entries that have sn")
	waitText(t, ctx, "#bFilter", "(sn=*)")
	waitText(t, ctx, "#sOut", "result(s)")

	// A class: find its entries.
	run(t, ctx, chromedp.Click(`button[data-tab="server"]`), chromedp.Click("#scClasses"),
		set("#scFilter", "posixGroup"), eval(`document.getElementById('scFilter').dispatchEvent(new Event('input'))`))
	run(t, ctx, chromedp.Click(`#scList .scitem[data-name="posixGroup"] a`), chromedp.Click(`#scDetail button.find`))
	waitText(t, ctx, "#bFilter", "(objectClass=posixGroup)")
	waitText(t, ctx, "#sOut", "1 result(s)")
	waitText(t, ctx, "#sOut", "cn=staff,")

	// Nothing matches: the list says zero rather than staying stale.
	run(t, ctx, chromedp.Click(`button[data-tab="server"]`), set("#scFilter", "zzzzzzzz"), eval(`document.getElementById('scFilter').dispatchEvent(new Event('input'))`))
	waitText(t, ctx, "#scCount", "0 object classes")

	var overflow bool
	js(t, ctx, `document.documentElement.scrollWidth > document.documentElement.clientWidth + 1`, &overflow)
	if overflow {
		t.Error("the Server tab makes the page scroll sideways")
	}
}

// ---------- bookmarks and history ----------

func TestUIBookmarksAndHistory(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	bobDN := "uid=bob,ou=people," + slapdtest.BaseDN
	goToDN := func(dn string) {
		run(t, ctx, set("#treeFilter", dn), chromedp.SendKeys("#treeFilter", "\r"))
		waitText(t, ctx, "#pane-entry .dn", dn)
	}

	// Bookmark two entries with the star.
	goToDN(slapdtest.AliceDN)
	waitText(t, ctx, "#eStar", "☆ Bookmark")
	run(t, ctx, chromedp.Click("#eStar"))
	waitText(t, ctx, "#eStar", "★ Bookmarked")
	waitText(t, ctx, "#toasts", "Bookmarked")
	goToDN(bobDN)
	waitText(t, ctx, "#eStar", "☆ Bookmark") // a different entry: not starred
	run(t, ctx, chromedp.Click("#eStar"))
	waitText(t, ctx, "#bmCount", "2")

	// The list: labels from the RDN, a click opens the entry (and the star is on again).
	run(t, ctx, chromedp.Click("#bookmarks > summary"), chromedp.WaitVisible("#bmList"))
	var labels []string
	js(t, ctx, `[...document.querySelectorAll('#bmList .bm .dnlink')].map(a => a.textContent)`, &labels)
	if strings.Join(labels, ",") != "alice,bob" {
		t.Errorf("bookmark labels: %v", labels)
	}
	run(t, ctx, chromedp.Click(`#bmList .bm[data-dn="`+slapdtest.AliceDN+`"] .dnlink`))
	waitText(t, ctx, "#pane-entry .dn", slapdtest.AliceDN)
	waitText(t, ctx, "#eStar", "★ Bookmarked")

	// Removing one from the list updates the star of the entry being shown.
	goToDN(bobDN)
	waitText(t, ctx, "#eStar", "★ Bookmarked")
	run(t, ctx, chromedp.Click(`#bmList .bm[data-dn="`+bobDN+`"] .bm-remove`))
	waitText(t, ctx, "#bmCount", "1")
	waitText(t, ctx, "#eStar", "☆ Bookmark")

	// They are kept: reload the page, reconnect, and alice is still there.
	reload(t, ctx, appURL)
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	waitText(t, ctx, "#bmCount", "1")

	// History: every search is remembered, newest first, a repeat moves to the top.
	people := "ou=people," + slapdtest.BaseDN
	search := func(filter string) {
		run(t, ctx, chromedp.Click(`button[data-tab="search"]`), chromedp.WaitReady("#sBase"), rawMode(),
			set("#sBase", people), set("#sScope", "one"), set("#sFilter", filter), chromedp.Click("#btnSearch"))
		waitText(t, ctx, "#sOut", "result(s)")
	}
	options := func() []string {
		var o []string
		js(t, ctx, `[...document.querySelectorAll('#hSel option')].map(o => o.textContent)`, &o)
		return o
	}
	waitText(t, ctx, "#hSel", "no recent searches")
	search("(uid=bob)")
	search("(uid=alice)")
	waitJS(t, ctx, "two items", `document.querySelectorAll('#hSel option').length === 3`)
	if o := options(); !strings.HasPrefix(o[1], "(uid=alice) · ") || !strings.HasPrefix(o[2], "(uid=bob) · ") {
		t.Errorf("newest first: %v", o)
	}
	search("(uid=bob)")
	waitJS(t, ctx, "bob on top, no duplicate", `document.querySelectorAll('#hSel option').length === 3 && document.querySelectorAll('#hSel option')[1].textContent.startsWith('(uid=bob)')`)

	// Picking one puts it back in the form (it does not run it).
	run(t, ctx, set("#sFilter", "(x=y)"), set("#sBase", "dc=other"),
		set("#hSel", "1"), eval(`document.getElementById('hSel').dispatchEvent(new Event('change'))`))
	waitJS(t, ctx, "form restored", `document.getElementById('sFilter').value === '(uid=alice)' && document.getElementById('sBase').value === `+q(people))
	waitText(t, ctx, "#sNote", "recent search was loaded")

	// A builder search is remembered too, and "clear" empties the list.
	run(t, ctx, chromedp.Click("#modeBuilder"))
	setAttr(t, ctx, 0, "mail")
	setVal(t, ctx, 0, "example")
	waitText(t, ctx, "#bFilter", "(mail=*example*)")
	run(t, ctx, chromedp.Click("#btnSearch"))
	waitJS(t, ctx, "builder search in the history", `document.querySelectorAll('#hSel option')[1].textContent.startsWith('(mail=*example*)')`)
	run(t, ctx, chromedp.Click("#hClear"))
	waitText(t, ctx, "#hSel", "no recent searches")
	var disabled bool
	js(t, ctx, `document.getElementById('hSel').disabled && document.getElementById('hClear').disabled`, &disabled)
	if !disabled {
		t.Error("with no history both controls are off")
	}
}

// ---------- groups and comparing entries ----------

func TestUIGroupsAndCompare(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	allowClipboard(t, ctx, appURL)
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	people := "ou=people," + slapdtest.BaseDN
	bobDN, zoeDN := "uid=bob,"+people, "uid=zoe,"+people
	goToDN := func(dn string) {
		run(t, ctx, set("#treeFilter", dn), chromedp.SendKeys("#treeFilter", "\r"))
		waitText(t, ctx, "#pane-entry .dn", dn)
	}
	groupLinks := func() []string {
		var g []string
		js(t, ctx, `[...document.querySelectorAll('#eGroupsBox a.grp')].map(a => a.textContent)`, &g)
		return g
	}

	// Groups: by DN (member) and by login (memberUid).
	goToDN(slapdtest.AliceDN)
	run(t, ctx, chromedp.Click("#eGroups"))
	waitText(t, ctx, "#eGroupsBox", "Member of:")
	waitJS(t, ctx, "groups listed", `document.querySelectorAll('#eGroupsBox a.grp').length === 2`)
	if got := strings.Join(groupLinks(), ","); got != "admins,staff" {
		t.Errorf("alice's groups: %s", got)
	}
	run(t, ctx, chromedp.Click(`#eGroupsBox a.grp`)) // admins
	waitText(t, ctx, "#pane-entry .dn", "cn=admins,ou=groups,")
	goToDN(bobDN)
	run(t, ctx, chromedp.Click("#eGroups"))
	waitJS(t, ctx, "bob in one group", `document.querySelectorAll('#eGroupsBox a.grp').length === 1`)
	goToDN(zoeDN)
	run(t, ctx, chromedp.Click("#eGroups"))
	waitText(t, ctx, "#eGroupsBox", "no group")

	// Compare: pin alice, open bob, compare.
	goToDN(slapdtest.AliceDN)
	waitText(t, ctx, "#ePin", "Pin to compare")
	run(t, ctx, chromedp.Click("#ePin"))
	waitText(t, ctx, "#ePin", "Unpin")
	var hidden bool
	js(t, ctx, `document.getElementById('eCompare').classList.contains('hidden')`, &hidden)
	if !hidden {
		t.Error("an entry cannot be compared with itself")
	}
	goToDN(bobDN)
	waitText(t, ctx, "#ePin", "Pin this one instead")
	waitText(t, ctx, "#eCompare", "Compare with alice")
	run(t, ctx, chromedp.Click("#eCompare"))
	waitJS(t, ctx, "compare dialog", `document.getElementById('compareDlg').open`)
	waitText(t, ctx, "#cmpSummary", "differ")
	waitText(t, ctx, "#cmpSummary", "only in alice")
	waitText(t, ctx, "#cmpSummary", "only in bob")
	waitText(t, ctx, "#cmpA", slapdtest.AliceDN)
	waitText(t, ctx, "#cmpB", bobDN)
	statusOf := func(attr string) string {
		var s string
		js(t, ctx, fmt.Sprintf(`(document.querySelector('#cmpTable tr[data-attr=%q] .cmp-status')||{textContent:''}).textContent`, attr), &s)
		return s
	}
	if statusOf("jpegPhoto") != "only in alice" || statusOf("telephoneNumber") != "only in bob" || statusOf("mail") != "differs" {
		t.Errorf("statuses: photo=%q phone=%q mail=%q", statusOf("jpegPhoto"), statusOf("telephoneNumber"), statusOf("mail"))
	}
	if statusOf("objectClass") != "" {
		t.Error("what is the same is hidden by default")
	}
	run(t, ctx, chromedp.Click("#cmpOnly"))
	waitJS(t, ctx, "identical rows shown", `!!document.querySelector('#cmpTable tr[data-attr="objectClass"]')`)
	if statusOf("objectClass") != "same" {
		t.Errorf("objectClass: %q", statusOf("objectClass"))
	}
	clickCopy(t, ctx, "#cmpA .copy-btn", slapdtest.AliceDN, "Copied the DN")
	run(t, ctx, chromedp.Click("#cmpClose"))
	waitJS(t, ctx, "closed", `!document.getElementById('compareDlg').open`)

	// Pin this one instead; unpin.
	run(t, ctx, chromedp.Click("#ePin"))
	waitText(t, ctx, "#ePin", "Unpin")
	run(t, ctx, chromedp.Click("#ePin"))
	waitText(t, ctx, "#ePin", "Pin to compare")
}

// ---------- keyboard shortcuts ----------

func TestUIKeyboardShortcuts(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	active := func() string {
		var id string
		js(t, ctx, `document.activeElement.id || document.activeElement.tagName`, &id)
		return id
	}
	activeTab := func() string {
		var tab string
		js(t, ctx, `document.querySelector('.pane.on').id`, &tab)
		return tab
	}

	// ? shows the list (any time), Escape closes it.
	run(t, ctx, chromedp.KeyEvent("?"))
	waitJS(t, ctx, "help open", `document.getElementById('helpDlg').open`)
	waitText(t, ctx, "#helpTable", "Go to Search")
	waitText(t, ctx, "#helpTable", "Find anyone or anything")
	var rows int
	js(t, ctx, `document.querySelectorAll('#helpTable tr').length`, &rows)
	if rows < 8 {
		t.Errorf("the help should list every shortcut: %d rows", rows)
	}
	run(t, ctx, chromedp.KeyEvent(kb.Escape))
	waitJS(t, ctx, "help closed", `!document.getElementById('helpDlg').open`)
	run(t, ctx, chromedp.Click("#btnHelp"))
	waitJS(t, ctx, "button opens it too", `document.getElementById('helpDlg').open`)
	run(t, ctx, chromedp.Click("#helpClose"))

	// Not connected: the search box is off, so "/" has nothing to focus.
	run(t, ctx, chromedp.KeyEvent("/"))
	if active() == "quickFind" {
		t.Error("a disabled box cannot take the focus")
	}
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	run(t, ctx, eval(`document.activeElement.blur()`))

	run(t, ctx, chromedp.KeyEvent("/"))
	waitJS(t, ctx, "quick find focused", `document.activeElement.id === 'quickFind'`)
	// typing inside a box is never a shortcut: "gs" stays text and the tab does not change
	run(t, ctx, chromedp.SendKeys("#quickFind", "gs"))
	var typed string
	js(t, ctx, `document.getElementById('quickFind').value`, &typed)
	if typed != "gs" || activeTab() != "pane-entry" {
		t.Errorf("typing in the box: value=%q tab=%s", typed, activeTab())
	}
	run(t, ctx, chromedp.KeyEvent(kb.Escape)) // clears and leaves the box
	waitJS(t, ctx, "box cleared", `document.getElementById('quickFind').value === '' && document.activeElement.id !== 'quickFind'`)

	// Two-key sequences: g then a letter.
	for _, c := range []struct{ keys, pane string }{{"gs", "pane-search"}, {"gr", "pane-recipes"}, {"gx", "pane-explore"}, {"gv", "pane-server"}, {"ge", "pane-entry"}} {
		run(t, ctx, chromedp.KeyEvent(c.keys))
		waitJS(t, ctx, c.keys+" -> "+c.pane, `document.querySelector('.pane.on').id === `+q(c.pane))
	}
	run(t, ctx, chromedp.KeyEvent("t"))
	waitJS(t, ctx, "tree filter focused", `document.activeElement.id === 'treeFilter'`)
	run(t, ctx, eval(`document.activeElement.blur()`))
	run(t, ctx, chromedp.KeyEvent("gb"))
	waitJS(t, ctx, "bookmarks open", `document.getElementById('bookmarks').open`)

	// An unknown key does nothing, and a dialog takes the keys away from the shortcuts.
	run(t, ctx, chromedp.KeyEvent("z"))
	run(t, ctx, chromedp.KeyEvent("gp"))
	waitJS(t, ctx, "passwords panel", `document.getElementById('manageDlg').open`)
	run(t, ctx, chromedp.KeyEvent("gs"))
	if activeTab() == "pane-search" {
		t.Error("while a dialog is open the shortcuts are off")
	}
}

// ---------- accessibility and the welcome screen ----------

// unnamedControls lists the visible controls that a screen reader could not name: no label,
// aria-label, visible button text or title.
func unnamedControls(t *testing.T, ctx context.Context) []string {
	t.Helper()
	var list []string
	js(t, ctx, `(() => {
	  const name = (el) => {
	    if (el.getAttribute('aria-label')) return el.getAttribute('aria-label');
	    const lb = el.getAttribute('aria-labelledby');
	    if (lb && document.getElementById(lb)) return document.getElementById(lb).textContent.trim();
	    if (el.id) { const l = document.querySelector('label[for="' + el.id + '"]'); if (l) return l.textContent.trim(); }
	    const wrap = el.closest('label'); if (wrap) return wrap.textContent.trim();
	    if (el.tagName === 'BUTTON') return (el.textContent || '').trim() || el.title || '';
	    return el.title || '';
	  };
	  const visible = (el) => !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
	  return [...document.querySelectorAll('button, input:not([type=hidden]), select, textarea')]
	    .filter(visible).filter((e) => !name(e)).map((e) => e.tagName.toLowerCase() + '#' + e.id + '.' + e.className);
	})()`, &list)
	return list
}

func TestUIEveryControlHasAName(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	if bad := unnamedControls(t, ctx); len(bad) != 0 {
		t.Errorf("before connecting: unnamed controls %v", bad)
	}
	// the connection dialog
	run(t, ctx, chromedp.Click("#btnConn"), chromedp.WaitVisible("#cURL"))
	if bad := unnamedControls(t, ctx); len(bad) != 0 {
		t.Errorf("connection dialog: unnamed controls %v", bad)
	}
	run(t, ctx, chromedp.Click("#btnCancel"))
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	waitJS(t, ctx, "recipes loaded", `document.querySelectorAll('#pane-recipes .recipe').length > 5`)
	waitJS(t, ctx, "suggestions", `document.querySelectorAll('#attrList option').length > 50`)
	for _, tab := range []string{"entry", "search", "recipes", "explore", "server"} {
		run(t, ctx, chromedp.Click(`button[data-tab="`+tab+`"]`))
		if tab == "search" {
			run(t, ctx, chromedp.Click("#sOptions > summary"), chromedp.WaitVisible("#sLimit"))
		}
		if bad := unnamedControls(t, ctx); len(bad) != 0 {
			t.Errorf("%s tab: unnamed controls %v", tab, bad)
		}
	}
	for _, dlg := range []struct{ open, id string }{{"#btnVault", "manageDlg"}, {"#btnHelp", "helpDlg"}} {
		run(t, ctx, chromedp.Click(dlg.open))
		waitJS(t, ctx, dlg.id, `document.getElementById('`+dlg.id+`').open`)
		if bad := unnamedControls(t, ctx); len(bad) != 0 {
			t.Errorf("%s: unnamed controls %v", dlg.id, bad)
		}
		run(t, ctx, eval(`document.getElementById('`+dlg.id+`').close()`))
	}
}

func TestUIWelcomeScreen(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#welcomeConnect"))
	waitText(t, ctx, "#pane-entry", "Welcome to ldap-explorer")
	waitText(t, ctx, "#pane-entry", "Connect to your server.")
	// the button does what the Connect button does
	run(t, ctx, chromedp.Click("#welcomeConnect"))
	waitJS(t, ctx, "dialog open", `document.getElementById('dlg').open`)
	run(t, ctx, chromedp.Click("#btnCancel"))
	// once connected the entry tab explains how to start, then shows the first entry
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	waitText(t, ctx, "#pane-entry .dn", slapdtest.BaseDN)
	// disconnecting brings the welcome back
	run(t, ctx, chromedp.Click("#btnDisc"))
	waitText(t, ctx, "#pane-entry", "Welcome to ldap-explorer")
}

func TestUIErrorsAreExplained(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	// wrong password: a sentence first, the technical text kept
	connect(t, ctx, l.URL, slapdtest.AliceDN, "wrong")
	waitText(t, ctx, "#cErr", "The server did not accept that user and password.")
	waitText(t, ctx, "#cErr", "Invalid Credentials")
	// nothing listens there
	run(t, ctx, set("#cURL", "ldap://127.0.0.1:1"), set("#cPass", slapdtest.AlicePW), chromedp.Click("#btnDoConn"))
	waitText(t, ctx, "#cErr", "Could not reach the server.")
	// an entry that does not exist
	run(t, ctx, set("#cURL", l.URL), chromedp.Click("#btnDoConn"))
	waitText(t, ctx, "#status", "Connected to")
	run(t, ctx, set("#treeFilter", "uid=nobody,ou=people,"+slapdtest.BaseDN), chromedp.SendKeys("#treeFilter", "\r"))
	waitText(t, ctx, "#pane-entry .err", "That entry does not exist, or you are not allowed to see it.")
}

// The Search button must not jump when the lists above it are refreshed (a click would miss).
func TestUISearchButtonStaysPut(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	run(t, ctx, chromedp.Click(`button[data-tab="search"]`), chromedp.WaitReady("#sBase"), rawMode(),
		set("#sBase", "ou=people,"+slapdtest.BaseDN), set("#sScope", "one"))
	// Relative to the saved-queries row, so that the page scrolling (clicking scrolls the
	// button into view in small windows) does not count as the button moving.
	top := func() float64 {
		var y float64
		js(t, ctx, `document.getElementById('btnSearch').getBoundingClientRect().top - document.querySelector('#pane-search .saved').getBoundingClientRect().top`, &y)
		return y
	}
	before := top()
	for _, f := range []string{"(uid=bob)", "(uid=alice)", "(objectClass=inetOrgPerson)", "(uid=a-much-longer-filter-to-make-the-history-text-long-" + strings.Repeat("x", 80) + ")"} {
		run(t, ctx, set("#sFilter", f), chromedp.Click("#btnSearch"))
		waitText(t, ctx, "#sOut", "result(s)")
		waitJS(t, ctx, "history refreshed", `document.querySelectorAll('#hSel option').length >= 2`)
		if got := top(); got != before {
			t.Fatalf("the Search button moved from %v to %v after searching %q", before, got, f)
		}
	}
}

// ---------- copying in the remaining places ----------

func TestUICopyEverywhereElse(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	allowClipboard(t, ctx, appURL)
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	people := "ou=people," + slapdtest.BaseDN

	// Explore: the name of a kind of entry or of an attribute.
	run(t, ctx, chromedp.Click(`button[data-tab="explore"]`), set("#xBase", people), set("#xScope", "one"), chromedp.Click("#btnExplore"))
	waitText(t, ctx, "#xSummary", "Looked at 3 entries")
	clickCopy(t, ctx, `#xList-class li:first-child .copy-btn`, "inetOrgPerson", "Copied inetOrgPerson")

	// Compare: a value copies when clicked.
	run(t, ctx, chromedp.Click(`button[data-tab="entry"]`), set("#treeFilter", slapdtest.AliceDN), chromedp.SendKeys("#treeFilter", "\r"))
	waitText(t, ctx, "#pane-entry .dn", slapdtest.AliceDN)
	run(t, ctx, chromedp.Click("#ePin"), set("#treeFilter", "uid=bob,"+people), chromedp.SendKeys("#treeFilter", "\r"))
	waitText(t, ctx, "#eCompare", "Compare with alice")
	run(t, ctx, chromedp.Click("#eCompare"))
	waitJS(t, ctx, "compare", `document.getElementById('compareDlg').open`)
	run(t, ctx, chromedp.Click(`#cmpTable tr[data-attr="mail"] td:nth-child(3) .copy-cell`))
	waitJS(t, ctx, "copied", `navigator.clipboard.readText().then(t => t === 'bob@example.org')`)
	waitText(t, ctx, "#toasts", `Copied "bob@example.org"`)
	run(t, ctx, chromedp.Click("#cmpClose"))

	// Passwords panel: the user and the server of a profile.
	run(t, ctx, chromedp.Click("#btnConn"), chromedp.WaitVisible("#cStore"))
	fillConnection(t, ctx, "lab", l.URL, slapdtest.AliceDN, slapdtest.AlicePW, "none")
	run(t, ctx, chromedp.Click("#btnSaveProfile"))
	waitText(t, ctx, "#status", "Profile saved")
	run(t, ctx, chromedp.Click("#btnCancel"), chromedp.Click("#btnVault"), chromedp.WaitVisible("#mList"))
	waitText(t, ctx, `#mList tr[data-profile="lab"]`, slapdtest.AliceDN)
	clickCopy(t, ctx, `#mList tr[data-profile="lab"] td:nth-child(2) .copy-btn`, slapdtest.AliceDN, "Copied the user DN")
	clickCopy(t, ctx, `#mList tr[data-profile="lab"] td:nth-child(3) .copy-btn`, l.URL, "Copied the server address")
}

// ---------- operational attributes in their own section; links, Back and Forward ----------

func TestUIOperationalSection(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	run(t, ctx, set("#treeFilter", slapdtest.AliceDN), chromedp.SendKeys("#treeFilter", "\r"))
	waitText(t, ctx, "#pane-entry .dn", slapdtest.AliceDN)

	// Hidden: the entry says so and offers them in one click.
	waitText(t, ctx, "#pane-entry", "Operational attributes (created, changed, unique id")
	run(t, ctx, chromedp.Click("#showOp"))
	waitJS(t, ctx, "the section", `!!document.getElementById('opTitle')`)
	if !jsBool(t, ctx, `document.getElementById('chkOp').checked`) {
		t.Error("the top-bar checkbox did not follow")
	}
	// They are in their own table, after the ordinary ones, each explained in words.
	if !jsBool(t, ctx, `(() => { const [user, op] = document.querySelectorAll('#pane-entry table.attrs');
		const names = (t) => [...t.querySelectorAll('td.k')].map(td => td.textContent);
		return !!op && names(user).includes('mail') && !names(user).includes('entryUUID') && names(op).includes('entryUUID') && !names(op).includes('mail'); })()`) {
		t.Error("ordinary and operational attributes are not in separate tables")
	}
	waitText(t, ctx, `#pane-entry tr[data-attr="modifyTimestamp"]`, "last changed")
	// Operational values still have their copy buttons.
	allowClipboard(t, ctx, appURL)
	run(t, ctx, chromedp.Click(`#pane-entry tr[data-attr="entryUUID"] .copy-btn`))
	waitText(t, ctx, "#toasts", "Copied")
}

func TestUILinksAndBack(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	admins := "cn=admins,ou=groups," + slapdtest.BaseDN
	run(t, ctx, set("#treeFilter", admins), chromedp.SendKeys("#treeFilter", "\r"))
	waitText(t, ctx, "#pane-entry .dn", admins)
	// (Opening the connection selected the root first, so Back leads there.)
	if !jsBool(t, ctx, `!document.getElementById('navBack').disabled && document.getElementById('navForward').disabled`) {
		t.Error("expected Back enabled (the root was shown first) and Forward disabled")
	}

	// Follow the member link: Back returns, Forward goes again.
	run(t, ctx, chromedp.Click(`#pane-entry tr[data-attr="member"] a.dnlink`))
	waitText(t, ctx, "#pane-entry .dn", slapdtest.AliceDN)
	waitJS(t, ctx, "Back enabled", `!document.getElementById('navBack').disabled && document.getElementById('navBack').title.includes('cn=admins')`)
	run(t, ctx, chromedp.Click("#navBack"))
	waitText(t, ctx, "#pane-entry .dn", admins)
	waitJS(t, ctx, "Forward enabled", `!document.getElementById('navForward').disabled && document.getElementById('navBack').title.includes('dc=example,dc=org')`)
	run(t, ctx, chromedp.Click("#navForward"))
	waitText(t, ctx, "#pane-entry .dn", slapdtest.AliceDN)

	// The same with the keyboard: [ and ].
	run(t, ctx, eval(`document.activeElement.blur()`), chromedp.KeyEvent("["))
	waitText(t, ctx, "#pane-entry .dn", admins)
	run(t, ctx, chromedp.KeyEvent("]"))
	waitText(t, ctx, "#pane-entry .dn", slapdtest.AliceDN)

	// Going back and then somewhere new drops what was forward.
	run(t, ctx, chromedp.Click("#navBack"))
	waitText(t, ctx, "#pane-entry .dn", admins)
	run(t, ctx, set("#treeFilter", slapdtest.BobDN), chromedp.SendKeys("#treeFilter", "\r"))
	waitText(t, ctx, "#pane-entry .dn", slapdtest.BobDN)
	if jsBool(t, ctx, `!document.getElementById('navForward').disabled`) {
		t.Error("Forward should be disabled after opening something new")
	}
}

func jsBool(t *testing.T, ctx context.Context, expr string) bool {
	t.Helper()
	var b bool
	js(t, ctx, expr, &b)
	return b
}

// ---------- the status bar: what is running, for how long, cancel ----------

// slowSearches makes the search requests of the page take `ms` milliseconds (or never answer,
// until aborted, when ms is 0), so the bar has something to show. It also records the headers.
func slowSearches(t *testing.T, ctx context.Context, ms int) {
	t.Helper()
	run(t, ctx, eval(fmt.Sprintf(`(() => {
		const real = (window.__realFetch ||= window.fetch.bind(window)); // always the original, so delays never add up
		window.__searchHeaders = [];
		window.fetch = (url, init = {}) => {
			if (String(url) !== '/api/search') return real(url, init);
			window.__searchHeaders.push(init.headers || {});
			return new Promise((resolve, reject) => {
				const abort = () => reject(new DOMException('aborted', 'AbortError'));
				if (init.signal) init.signal.addEventListener('abort', abort);
				if (%d > 0) setTimeout(() => real(url, init).then(resolve, reject), %d);
			});
		};
		return true; })()`, ms, ms)))
}

func TestUIStatusBar(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	waitText(t, ctx, "#actText", "Ready")

	run(t, ctx, chromedp.Click(`button[data-tab="search"]`), chromedp.WaitReady("#sBase"), rawMode(),
		set("#sBase", "ou=people,"+slapdtest.BaseDN), set("#sScope", "one"), set("#sFilter", "(uid=bob)"))

	// A slow search: the bar says what it is, how long it has taken, and then how it went.
	slowSearches(t, ctx, 1500)
	run(t, ctx, chromedp.Click("#btnSearch"))
	waitJS(t, ctx, "busy bar", `document.getElementById('activity').classList.contains('busy')`)
	waitText(t, ctx, "#actText", "Searching in ou=people for (uid=bob)")
	waitJS(t, ctx, "elapsed time", `/\d\.\d s/.test(document.getElementById('actDetail').textContent)`)
	if !jsBool(t, ctx, `!document.getElementById('actProgress').hidden && !document.getElementById('actCancel').hidden`) {
		t.Error("the bar should show the progress and the Cancel button while it works")
	}
	waitText(t, ctx, "#sOut", "result(s)")
	waitJS(t, ctx, "done", `document.getElementById('activity').classList.contains('done') && document.getElementById('actText').textContent.startsWith('Done in ')`)
	if !jsBool(t, ctx, `document.getElementById('actCancel').hidden && document.getElementById('actProgress').hidden`) {
		t.Error("Cancel and the progress bar should go away when it is over")
	}
	// The request carried the tag the server uses to report progress.
	if !jsBool(t, ctx, `window.__searchHeaders.length === 1 && /^[A-Za-z0-9_-]{1,64}$/.test(window.__searchHeaders[0]['X-Op'])`) {
		t.Error("the search request should carry an X-Op tag")
	}

	// A search that never answers: the user can cancel it, and the bar says so.
	slowSearches(t, ctx, 0)
	run(t, ctx, chromedp.Click("#btnSearch"))
	waitJS(t, ctx, "busy again", `document.getElementById('activity').classList.contains('busy')`)
	run(t, ctx, chromedp.Click("#actCancel"))
	waitText(t, ctx, "#sOut", "Cancelled. Nothing was changed")
	waitJS(t, ctx, "cancelled", `document.getElementById('actText').textContent.startsWith('Cancelled after ')`)

	// The app's own housekeeping never lights the bar: only a quick request, nothing to show.
	run(t, ctx, chromedp.Click(`button[data-tab="server"]`))
	if !jsBool(t, ctx, `document.getElementById('activity').getBoundingClientRect().height === 32 && document.getElementById('activity').getBoundingClientRect().bottom <= window.innerHeight`) {
		t.Error("the status bar should be fully visible at the bottom, with a fixed height")
	}
}

// ---------- a big branch loads page by page ----------

func TestUITreeLoadsBigBranchesOnDemand(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	connect(t, ctx, l.URL, slapdtest.BobDN, slapdtest.BobPW) // bob has no size limit
	waitText(t, ctx, "#status", "Connected to")
	bulk := `.node[title="ou=bulk,` + slapdtest.BaseDN + `"]`
	children := func() int {
		var n int
		js(t, ctx, `[...document.querySelectorAll('#tree .node')].filter(r => /^uid=u\d{4},ou=bulk,/.test(r.title)).length`, &n)
		return n
	}
	run(t, ctx, chromedp.WaitVisible(`.node[title="`+slapdtest.BaseDN+`"]`), chromedp.Click(`.node[title="`+slapdtest.BaseDN+`"] .tw`))
	run(t, ctx, chromedp.WaitVisible(bulk), chromedp.Click(bulk+" .tw"))

	// Only the first page, and a row that says there is more.
	waitJS(t, ctx, "first page", `document.querySelector('.more-row') !== null`)
	if n := children(); n != 200 {
		t.Fatalf("the first page has %d entries, want 200", n)
	}
	waitText(t, ctx, ".more-row", "200 loaded, more below")

	// Load more, by hand.
	run(t, ctx, chromedp.Click(".more-row .more-next"))
	waitJS(t, ctx, "second page", `document.querySelector('.more-row') !== null && document.querySelector('.more-row').textContent.includes('400 loaded')`)

	// Scrolling to the end loads the rest by itself.
	run(t, ctx, eval(`document.querySelector('.more-row').scrollIntoView()`))
	waitJS(t, ctx, "the whole branch", `document.querySelector('.more-row') === null`)
	if n := children(); n != slapdtest.BulkCount {
		t.Errorf("after scrolling: %d entries, want %d", n, slapdtest.BulkCount)
	}

	// Going to an entry that is on a page not loaded yet loads pages until it is found.
	run(t, ctx, chromedp.Click("#btnRefresh"))
	waitJS(t, ctx, "tree rebuilt", `document.querySelectorAll('#tree .node').length > 0 && !document.querySelector('.more-row')`)
	last := "uid=u0599,ou=bulk," + slapdtest.BaseDN
	run(t, ctx, set("#treeFilter", last), chromedp.SendKeys("#treeFilter", "\r"))
	waitText(t, ctx, "#pane-entry .dn", last)
	waitJS(t, ctx, "revealed in the tree", `(() => { const n = document.querySelector('#tree .node.sel'); return !!n && n.title === `+q(last)+`; })()`)

	// "Load all" fetches the rest of a branch in one go.
	run(t, ctx, chromedp.Click("#btnRefresh"))
	waitJS(t, ctx, "tree rebuilt", `document.querySelectorAll('#tree .node').length > 0`)
	run(t, ctx, chromedp.Click(`.node[title="`+slapdtest.BaseDN+`"] .tw`))
	run(t, ctx, chromedp.WaitVisible(bulk), chromedp.Click(bulk+" .tw"))
	waitJS(t, ctx, "first page again", `document.querySelector('.more-row .more-all') !== null`)
	run(t, ctx, chromedp.Click(".more-row .more-all"))
	waitJS(t, ctx, "all loaded", `document.querySelector('.more-row') === null`)
	if n := children(); n != slapdtest.BulkCount {
		t.Errorf("Load all: %d entries, want %d", n, slapdtest.BulkCount)
	}
}

// ---------- issues #2 and #3: aligned fields, and the options when the schema has no rules ----------

func TestUIBuilderFieldsAlignedAndGuessedOptions(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	// a window wide enough for a condition to fit on one line: the fields are compared on it
	run(t, ctx, chromedp.EmulateViewport(1500, 900), chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	// Make the server's schema look like Active Directory's for one field: no matching rules
	// published (the API then says rulesUnknown and allows what the type of the field suggests).
	run(t, ctx, eval(`(() => {
		const real = window.fetch.bind(window);
		window.fetch = async (url, init) => {
			const r = await real(url, init);
			if (String(url) !== '/api/schema') return r;
			const d = await r.json();
			const a = d.attributes.find((x) => x.name === 'description');
			a.rulesUnknown = true; a.canEqual = true; a.canSubstring = true; a.canOrder = false;
			return new Response(JSON.stringify(d), { status: 200, headers: { 'Content-Type': 'application/json' } });
		};
		return true; })()`))
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	run(t, ctx, chromedp.Click(`button[data-tab="search"]`), chromedp.WaitReady("#bChips"))
	waitJS(t, ctx, "chips", `document.querySelectorAll('#bChips .chip').length > 3`)

	// One row with a friendly name under the field (Email), one without (an unknown field).
	run(t, ctx, chromedp.Click(`#bChips .chip:nth-child(3)`))
	run(t, ctx, chromedp.Click(`#builder .badd`)) // + Condition
	waitJS(t, ctx, "two rows", `document.querySelectorAll('#builder .bcond').length === 2`)
	run(t, ctx, set("#builder .bcond:nth-child(2) .battr", "description"),
		eval(`document.querySelector('#builder .bcond:nth-child(2) .battr').dispatchEvent(new Event('change'))`))
	waitJS(t, ctx, "the guess marker", `!!document.querySelector('#builder .bcond .guess')`)

	// Every control of a row starts at the same height, with and without the friendly name.
	if !jsBool(t, ctx, `[...document.querySelectorAll('#builder .bcond')].every((row) => {
		const tops = ['input.battr', 'select.bop', '.bval', '.bremove'].map((s) => row.querySelector(s)).filter(Boolean).map((e) => Math.round(e.getBoundingClientRect().top));
		return tops.length >= 3 && Math.max(...tops) - Math.min(...tops) <= 1; })`) {
		var tops []string
		js(t, ctx, `[...document.querySelectorAll('#builder .bcond')].map((row) => ['input.battr', 'select.bop', '.bval', '.bremove'].map((s) => { const e = row.querySelector(s); return s + '=' + (e ? Math.round(e.getBoundingClientRect().top) : '-'); }).join(' '))`, &tops)
		t.Errorf("the fields of a condition are not aligned: %v", tops)
	}

	// The field without published rules gets contains / equals, and says why.
	var ops []string
	js(t, ctx, `[...document.querySelectorAll('#builder .bcond:nth-child(2) select.bop option')].map(o => o.textContent)`, &ops)
	if !slices.Contains(ops, "contains") || !slices.Contains(ops, "is") {
		t.Errorf("options for a field whose rules are unknown: %v", ops)
	}
	if !strings.Contains(textOf(t, ctx, "#builder .bcond:nth-child(2) .guess"), "ⓘ") ||
		!jsBool(t, ctx, `document.querySelector('#builder .bcond:nth-child(2) .guess').title.includes('does not say how this field can be compared')`) {
		t.Error("the guess marker should explain itself")
	}
	if jsBool(t, ctx, `!!document.querySelector('#builder .bcond:nth-child(1) .guess')`) {
		t.Error("a field with published rules should not carry the marker")
	}
}

// ---------- issue #4: Back and Forward for the whole program ----------

func TestUINavigationAcrossTabs(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	if !jsBool(t, ctx, `document.getElementById('navBack').disabled && document.getElementById('navForward').disabled`) {
		t.Error("Back and Forward start disabled")
	}
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	waitTab := func(name string) {
		t.Helper()
		waitJS(t, ctx, "the "+name+" tab", `document.querySelector('.tabs button.on').dataset.tab === `+q(name))
	}

	// An entry, then other tabs, then another entry.
	run(t, ctx, set("#treeFilter", slapdtest.AliceDN), chromedp.SendKeys("#treeFilter", "\r"))
	waitText(t, ctx, "#pane-entry .dn", slapdtest.AliceDN)
	run(t, ctx, chromedp.Click(`button[data-tab="search"]`))
	run(t, ctx, chromedp.Click(`button[data-tab="server"]`))
	waitTab("server")
	run(t, ctx, chromedp.Click(`button[data-tab="entry"]`))
	run(t, ctx, set("#treeFilter", slapdtest.BobDN), chromedp.SendKeys("#treeFilter", "\r"))
	waitText(t, ctx, "#pane-entry .dn", slapdtest.BobDN)

	// Back goes through them in reverse: the Entry tab (alice), Server, Search, alice.
	for i, want := range []string{"entry", "server", "search", "entry"} {
		run(t, ctx, chromedp.Click("#navBack"))
		waitTab(want)
		if i == 0 { // the entry is loaded asynchronously: let it finish before moving on
			waitText(t, ctx, "#pane-entry .dn", slapdtest.AliceDN)
		}
	}
	waitText(t, ctx, "#pane-entry .dn", slapdtest.AliceDN) // and the first place is alice again
	if !jsBool(t, ctx, `document.getElementById('navBack').title.includes('dc=example,dc=org')`) {
		t.Error("Back should now lead to the root entry that was shown first")
	}

	// Forward again; the keyboard (Alt+Right / Alt+Left) and a mouse's back button do the same.
	run(t, ctx, chromedp.Click("#navForward"))
	waitTab("search")
	run(t, ctx, eval(`document.activeElement.blur()`), chromedp.KeyEvent(kb.ArrowRight, chromedp.KeyModifiers(chromedp.ModifierAlt)))
	waitTab("server")
	run(t, ctx, chromedp.KeyEvent(kb.ArrowLeft, chromedp.KeyModifiers(chromedp.ModifierAlt)))
	waitTab("search")
	run(t, ctx, eval(`document.dispatchEvent(new MouseEvent('mouseup', { button: 3, bubbles: true, cancelable: true }))`))
	waitJS(t, ctx, "back with the mouse", `document.querySelector('.tabs button.on').dataset.tab === 'entry'`)
	run(t, ctx, eval(`document.dispatchEvent(new MouseEvent('mouseup', { button: 4, bubbles: true, cancelable: true }))`))
	waitTab("search")

	// Disconnecting forgets the trail.
	run(t, ctx, chromedp.Click("#btnDisc"))
	waitJS(t, ctx, "trail cleared", `document.getElementById('navBack').disabled && document.getElementById('navForward').disabled`)
}

// ---------- issue #5: every request in an expandable list ----------

func TestUIRequestsList(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	if !jsBool(t, ctx, `document.getElementById('activityPanel').hidden && document.getElementById('actToggle').getAttribute('aria-expanded') === 'false'`) {
		t.Fatal("the list starts collapsed")
	}

	// Two searches at the same time, both slow.
	run(t, ctx, chromedp.Click(`button[data-tab="search"]`), chromedp.WaitReady("#sBase"), rawMode(),
		set("#sBase", "ou=people,"+slapdtest.BaseDN), set("#sScope", "one"))
	slowSearches(t, ctx, 3000)
	run(t, ctx, set("#sFilter", "(uid=alice)"), chromedp.Click("#btnSearch"))
	run(t, ctx, set("#sFilter", "(uid=bob)"), chromedp.Click("#btnSearch"))

	// The bar says how many are running; clicking the bar opens the list with both.
	waitJS(t, ctx, "two running", `document.getElementById('actToggle').textContent.includes('2 running')`)
	run(t, ctx, chromedp.Click("#actText")) // the bar itself is the button
	waitJS(t, ctx, "the list", `!document.getElementById('activityPanel').hidden && document.getElementById('actToggle').getAttribute('aria-expanded') === 'true'`)
	waitJS(t, ctx, "both rows", `document.querySelectorAll('#actRunning li').length === 2`)
	var labels []string
	js(t, ctx, `[...document.querySelectorAll('#actRunning .act-row-text')].map(e => e.textContent)`, &labels)
	if len(labels) != 2 || !strings.Contains(labels[0], "(uid=alice)") || !strings.Contains(labels[1], "(uid=bob)") {
		t.Errorf("running rows (oldest first): %v", labels)
	}
	if !jsBool(t, ctx, `[...document.querySelectorAll('#actRunning li')].every(li => li.querySelector('button') && /\d\.\d s/.test(li.querySelector('.act-row-detail').textContent))`) {
		t.Error("each running row has its time and its own Cancel button")
	}

	// Cancel one from its row: it moves to the list of finished ones, the other keeps going.
	run(t, ctx, chromedp.Click(`#actRunning li:first-child button`))
	waitJS(t, ctx, "one left", `document.querySelectorAll('#actRunning li').length === 1`)
	waitJS(t, ctx, "cancelled in the history", `[...document.querySelectorAll('#actHistory li')].filter(li => li.querySelector('.act-row-text').textContent.startsWith('Searching')).some(li => li.querySelector('.act-row-detail').textContent.startsWith('Cancelled after '))`)
	// When the other one finishes, both are in the history, newest first, each with the time it started.
	waitJS(t, ctx, "finished", `document.querySelectorAll('#actRunning li').length === 0 && [...document.querySelectorAll('#actHistory li')].filter(li => li.querySelector('.act-row-text').textContent.startsWith('Searching')).length === 2`)
	var rows []string
	js(t, ctx, `[...document.querySelectorAll('#actHistory li')].filter(li => li.querySelector('.act-row-text').textContent.startsWith('Searching')).map(li => li.querySelector('.act-row-detail').textContent.split(' ')[0] + '|' + li.querySelector('.act-row-when').textContent)`, &rows)
	for _, r := range rows {
		if !regexp.MustCompile(`^(Done|Cancelled)\|\d\d:\d\d:\d\d$`).MatchString(r) {
			t.Errorf("history row %q", r)
		}
	}
	if len(rows) == 2 && !strings.HasPrefix(rows[0], "Done") {
		t.Errorf("the newest finished one comes first: %v", rows)
	}

	// Collapse with the button; Clear list empties the history.
	run(t, ctx, chromedp.Click("#actToggle"))
	waitJS(t, ctx, "collapsed", `document.getElementById('activityPanel').hidden && document.getElementById('actToggle').getAttribute('aria-expanded') === 'false'`)
	run(t, ctx, chromedp.Click("#actToggle"), chromedp.Click("#actClear"))
	waitJS(t, ctx, "cleared", `document.querySelectorAll('#actHistory li').length === 0 && !document.getElementById('actEmpty').hidden`)
}

// ---------- issue #6: quick requests are in the list too ----------

func TestUIRequestsListHasQuickPagesToo(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	connect(t, ctx, l.URL, slapdtest.BobDN, slapdtest.BobPW)
	waitText(t, ctx, "#status", "Connected to")
	bulk := `.node[title="ou=bulk,` + slapdtest.BaseDN + `"]`
	run(t, ctx, chromedp.WaitVisible(`.node[title="`+slapdtest.BaseDN+`"]`), chromedp.Click(`.node[title="`+slapdtest.BaseDN+`"] .tw`))
	run(t, ctx, chromedp.WaitVisible(bulk), chromedp.Click(bulk+" .tw"))
	waitText(t, ctx, ".more-row", "200 loaded")
	// two more pages, one after the other: each one is over in a few milliseconds
	run(t, ctx, chromedp.Click(".more-row .more-next"))
	waitText(t, ctx, ".more-row", "400 loaded")
	run(t, ctx, chromedp.Click(".more-row .more-next"))
	waitJS(t, ctx, "the whole branch", `document.querySelector('.more-row') === null`)

	run(t, ctx, chromedp.Click("#actToggle"))
	waitJS(t, ctx, "the list", `!document.getElementById('activityPanel').hidden`)
	var n int
	js(t, ctx, `[...document.querySelectorAll('#actHistory .act-row-text')].filter(e => e.textContent === 'Loading the entries under ou=bulk').length`, &n)
	if n != 3 {
		var all []string
		js(t, ctx, `[...document.querySelectorAll('#actHistory .act-row-text')].map(e => e.textContent)`, &all)
		t.Errorf("the 3 pages of ou=bulk should all be listed, found %d in %v", n, all)
	}
}

// ---------- issue #7: a branch so big that the directory times out ----------

func TestUITreeTimeoutOffersNamesOnly(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	// The directory "times out" on the first two attempts to list ou=bulk (the error go-ldap gives),
	// then answers. Every children request is recorded.
	run(t, ctx, eval(`(() => {
		const real = window.fetch.bind(window);
		window.__children = []; let failures = 2;
		window.fetch = async (url, init) => {
			const u = String(url);
			if (u.startsWith('/api/children') && decodeURIComponent(u).includes('dn=ou=bulk,') && !u.includes('cursor=')) {
				window.__children.push(u);
				if (failures-- > 0) return new Response(JSON.stringify({ error: 'LDAP Result Code 201 "Network Error": ldap: connection timed out' }), { status: 400, headers: { 'Content-Type': 'application/json' } });
			}
			return real(url, init);
		};
		return true; })()`))
	run(t, ctx, chromedp.Click("#chkHints")) // the full listing (with "has children?") is what can time out
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	bulk := `.node[title="ou=bulk,` + slapdtest.BaseDN + `"]`
	run(t, ctx, chromedp.WaitVisible(`.node[title="`+slapdtest.BaseDN+`"]`), chromedp.Click(`.node[title="`+slapdtest.BaseDN+`"] .tw`))
	run(t, ctx, chromedp.WaitVisible(bulk), chromedp.Click(bulk+" .tw"))

	// It says what happened, in plain words, and what can be done.
	waitText(t, ctx, ".more-row .err", "did not answer in time")
	if !jsBool(t, ctx, `!!document.querySelector('.more-row .more-retry') && document.querySelector('.more-row .more-retry').textContent === 'Try again' && !!document.querySelector('.more-row .more-light')`) {
		t.Fatal("the row should offer Try again and Load names only")
	}
	// Try again: it times out once more (the second failure).
	run(t, ctx, chromedp.Click(".more-row .more-retry"))
	waitJS(t, ctx, "second attempt", `window.__children.length === 2`)
	waitText(t, ctx, ".more-row .err", "did not answer in time")

	// Names only: a light listing, the entries appear, and they all show the arrow.
	run(t, ctx, chromedp.Click(".more-row .more-light"))
	waitJS(t, ctx, "the names", `[...document.querySelectorAll('#tree .node')].filter(r => /^uid=u\d{4},ou=bulk,/.test(r.title)).length === 200`)
	if !jsBool(t, ctx, `window.__children.length === 3 && !window.__children[0].includes('light=1') && window.__children[2].includes('light=1')`) {
		var urls []string
		js(t, ctx, `window.__children`, &urls)
		t.Errorf("requests: %v", urls)
	}
	waitText(t, ctx, "#tree", "Names only (the server was slow)")
	if !jsBool(t, ctx, `[...document.querySelectorAll('#tree .node')].filter(r => /^uid=u\d{4},ou=bulk,/.test(r.title)).every(r => r.querySelector('.tw').textContent === '▶')`) {
		t.Error("in a names-only listing every entry shows the arrow")
	}
	// The branch is remembered as heavy: collapsing and opening it again does not retry the full listing.
	run(t, ctx, chromedp.Click(bulk+" .tw"), chromedp.Click(bulk+" .tw"))
	if !jsBool(t, ctx, `window.__children.length === 3`) {
		t.Error("an already loaded branch is not asked for again")
	}
}

// ---------- the tree loads the names only unless asked to mark the entries without children ----------

func TestUITreeLoadsNamesOnlyByDefault(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))
	run(t, ctx, eval(`(() => {
		const real = window.fetch.bind(window);
		window.__children = [];
		window.fetch = (url, init) => { if (String(url).startsWith('/api/children')) window.__children.push(String(url)); return real(url, init); };
		return true; })()`))
	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to")
	if jsBool(t, ctx, `document.getElementById('chkHints').checked`) {
		t.Fatal("the option starts off")
	}
	root := `.node[title="` + slapdtest.BaseDN + `"]`
	empty := `.node[title="ou=empty,` + slapdtest.BaseDN + `"]`
	run(t, ctx, chromedp.WaitVisible(root), chromedp.Click(root+" .tw"), chromedp.WaitVisible(empty))
	// Names only: the empty branch looks like any other, with the arrow.
	if !jsBool(t, ctx, `document.querySelector('`+empty+` .tw').textContent === '▶' && window.__children.every(u => u.includes('light=1'))`) {
		t.Error("by default the tree asks for the names only, so every entry shows the arrow")
	}
	// Marking the entries without children asks for the full listing, and the empty branch loses its arrow.
	run(t, ctx, chromedp.Click("#chkHints"))
	waitJS(t, ctx, "the tree starts again", `document.querySelectorAll('#tree .node').length === 1`)
	run(t, ctx, chromedp.WaitVisible(root), chromedp.Click(root+" .tw"), chromedp.WaitVisible(empty))
	waitJS(t, ctx, "the empty branch is a leaf", `document.querySelector('`+empty+` .tw').textContent === ''`)
	if !jsBool(t, ctx, `!window.__children[window.__children.length - 1].includes('light=1')`) {
		t.Error("with the option on the listing is the full one")
	}
	// It is remembered (the server keeps it): a reload of the page starts with it on.
	reload(t, ctx, appURL)
	waitJS(t, ctx, "remembered", `document.getElementById('chkHints').checked`)
}
