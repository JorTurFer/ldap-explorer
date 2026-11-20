// How the tree asks for a branch: one page at a time, so opening a node with hundreds of
// thousands of children does not read them all. Pure: unit-tested with `node --test`.

export const PAGE_SIZE = 200;

// The URL of the first page of a branch, or of the page that follows a cursor.
export const childrenURL = (dn, cursor = "") =>
  cursor
    ? `/api/children?dn=${encodeURIComponent(dn)}&cursor=${encodeURIComponent(cursor)}`
    : `/api/children?dn=${encodeURIComponent(dn)}&size=${PAGE_SIZE}`;

export const loadedText = (n) => `${n.toLocaleString("en-US")} loaded, more below`;

