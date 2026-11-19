// A minimal event bus so modules do not import each other in circles.
//   connected      { profile, roots }
//   disconnected   {}
//   select         { dn }   a tree node was selected
//   search-from    { dn }   "search from here" on an entry

const bus = new EventTarget();
export const emit = (name, detail = {}) => bus.dispatchEvent(new CustomEvent(name, { detail }));
export const on = (name, fn) => bus.addEventListener(name, (e) => fn(e.detail));
