// How the tree asks for a branch: one page at a time, so opening a node with hundreds of
// thousands of children does not read them all. Pure: unit-tested with `node --test`.

export const PAGE_SIZE = 200;

// The URL of the first page of a branch, or of the page that follows a cursor.
// `light` asks for the names only (no "has children?" information): much cheaper on a server
// that works that out for every entry.
export const childrenURL = (dn, cursor = "", light = false) =>
  cursor
    ? `/api/children?dn=${encodeURIComponent(dn)}&cursor=${encodeURIComponent(cursor)}`
    : `/api/children?dn=${encodeURIComponent(dn)}&size=${PAGE_SIZE}${light ? "&light=1" : ""}`;

// Did the directory take too long to answer? (then "load the names only" is worth offering)
export const isTimeout = (message) => /timed out|time limit exceeded|result code 3\b|result code 201|timeout/i.test(message || "");

export const loadedText = (n) => `${n.toLocaleString("en-US")} loaded, more below`;

