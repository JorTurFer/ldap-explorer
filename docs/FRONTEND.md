# Working on the front end

The interface is plain HTML, CSS and native ES modules, served by the Go binary. There is no build step, no bundler, no framework and no npm dependency, and that is on purpose: the program must stay one self-contained binary. This page is how to change it comfortably.

## The loop

```sh
make front-dev    # serves the UI from front/ on disk: edit a file, refresh the browser, no rebuild
make test-js      # unit tests of the logic that has no DOM (node --test)
make test-e2e     # browser tests: real Chrome, the real binary, a real OpenLDAP
make test-docker  # everything, in a container, exactly as CI does it
make screenshots  # regenerate docs/img after a visible change
```

`make front-dev` needs a connection to try things against: any LDAP server you can reach, or start one the way the tests do (`internal/slapdtest`).

## Layout

| Path | What lives there |
|---|---|
| `front/index.html` | The page: header, the tree column, the tabs and their panes, and the dialogs. Markup only. |
| `front/css/` | `tokens.css` (colours, type, spacing, radii and the themes), `base.css` (reset and plain controls), `layout.css` (header, columns, tabs), `components.css` (everything else). `style.css` only imports them. |
| `front/js/app.js` | The entry point: it calls each module's `init…()` and nothing else. |
| `front/js/*.js` | One module per concern (`tree`, `entry`, `search`, `builder`, `results`, `recipes`, `explore`, `server`, `vault`...). |
| `front/js/ui/` | Reusable pieces: `copy.js`, `toast.js`, `download.js`. |
| `front/js/*-model.js`, `table.js`, `decode.js`, `diff.js`, `nav.js`, `labels.js`, `friendly-errors.js`, `entry-model.js`, `trail.js`, `activity-model.js`, `tree-model.js` | **Pure logic with no DOM access**, unit-tested with Node. |
| `front/test/` | The Node unit tests. Not embedded in the binary. |
| `e2e/` | The browser tests (a separate Go module, so the app's `go.mod` does not carry the browser-automation dependency). |

## Rules the tests enforce

- **Strict Content-Security-Policy**: `script-src 'self'; style-src 'self'`. No inline `<script>`, `<style>`, `style=""` or `on…=""`; no `setAttribute("style")`, `innerHTML` or `eval`. Use CSS classes, `el()` and `addEventListener`.
- **Nothing external**: no CDN, font or image from the internet. A static test scans every file.
- **Every import and every `@import` resolves**, and `index.html` has balanced tags.
- **Every visible control has an accessible name** (a linked `<label>`, `aria-label`, visible text or `title`), in every tab and dialog.
- **Nothing is clipped**: the builder and the Server tab are checked for overflow, and the Search button must not move when lists above it refresh.
- **No JavaScript error or console error** during any browser test (the API answering 400, 423 or 429 on purpose is the only exception).
- **Everything is in English**: UI text, error messages, comments.

## The look: design tokens

`css/tokens.css` is the only place with raw colours, font stacks, spacing and radii. Components use the variables (`var(--accent)`, `var(--space-3)`, `var(--radius-m)`...), so changing the look of the whole program means editing that file. There are three themes: the light defaults, the dark values under `prefers-color-scheme`, and an explicit `data-theme` that the Theme button sets and the server remembers.

## Small conventions

- **Build the DOM with `el(tag, props, ...children)`** (`js/dom.js`). `class` sets the class, `on…` adds a listener, `data-…`, `aria-…` and `role` become attributes, everything else is a property. Strings become text, never HTML.
- **Modules do not import each other in circles.** They talk through the tiny event bus in `js/events.js`; the catalogue of events is the comment at the top of that file. Add yours there.
- **Anything that shows a value a person might need elsewhere gets a copy button**: `copyBtn(text, { title, what })` or `withCopy(node, text)` from `ui/copy.js`. It confirms with a toast.
- **Requests**: always call the server through `api()`, `apiText()` or `apiBytes()` (never `fetch` directly): they report to the status bar, tag the request for progress and make it cancellable. A new kind of request that takes time should get a label in `describeRequest()` (`activity-model.js`); anything not listed there stays out of the bar.
- **Errors**: show `friendlyError(e.message)` (a sentence, with the technical text kept in brackets).
- **Layout that can move** (a list that refreshes, a label that changes) must not change the size of what is above a button the user is about to press: give it a fixed width.
- **Logic goes in a pure module** (`something-model.js`) and gets a Node test; the DOM module only wires it. If you cannot write the test without a DOM, the logic is in the wrong place.

## Adding a tab

1. `index.html`: a `<button data-tab="name">` in `.tabs` and a `<div id="pane-name" class="pane">`.
2. A module `js/name.js` exporting `initName()`; call it from `app.js`. Listen for `connected` and `disconnected` on the bus to load and to clear.
3. Pure logic in `js/name-model.js` with a test in `front/test/`.
4. A browser test in `e2e/ui_test.go`, and a step in `e2e/screenshots_test.go` if the tab deserves a picture in the manual.
5. A section in `docs/USAGE.md` (the manual) in the same commit.

## Adding a feature that needs the server

1. A Go handler in its own file, registered in `newHandler` (`main.go`). It is read-only; it never returns a secret.
2. A Go test that runs it against the real OpenLDAP from `internal/slapdtest` (add what it needs to `internal/slapdtest/seed.ldif`).
3. The JS above. Use `api()` (JSON), `apiText()` (text, e.g. LDIF) or `apiBytes()` (a binary value).
4. Update the manual and, if it changes what is supported, the lists at the end of it.

## Why not a framework?

The interface is small, the CSP forbids the inline code most toolchains emit, and a build step would turn "one binary you compile on your own machine" into "install Node first". Native modules, a 25-line DOM helper and an event bus are enough, and every part of it can be read in one sitting.
