// A minimal event bus so modules do not import each other in circles.
//   connected      { profile, roots }
//   disconnected   {}
//   select         { dn }   a tree node was selected
//   search-from    { dn }   "search from here" on an entry
//   builder-to-raw { filter }  "Edit as text" in the query builder
//   run-search     {}       Enter pressed in the query builder
//   profiles-changed   [profile]  the list of saved profiles was (re)loaded
//   profiles-stale     {}       something changed a profile elsewhere: reload them
//   open-connect       { profile, message? }  open the connection dialog for that profile
//   vault-status       { keychain, vault }   the state of the stores changed
//   pin-changed        { dn }    the entry pinned for comparison changed (dn is null when unpinned)
//   tab-shown          { name }  a tab was brought to the front
//   (Back / Forward live in navigation.js: it listens to select, tab-shown and disconnected)
//   load-query     { title, query, attrs, scope, note?, base? }  a recipe or the explorer wants this search run

const bus = new EventTarget();
export const emit = (name, detail = {}) => bus.dispatchEvent(new CustomEvent(name, { detail }));
export const on = (name, fn) => bus.addEventListener(name, (e) => fn(e.detail));
