// The places you have been (an entry, or one of the tabs), so Back and Forward work across the
// whole program: after following links from entry to entry, after going from a search to an
// entry and back. Pure (no DOM): unit-tested with `node --test`.
//
// A place is { tab: "entry", dn } for an entry, or { tab: "search" } (and so on) for a tab.

import { normDN } from "./nav.js";

const LIMIT = 100;

export const TAB_NAMES = { entry: "Entry", search: "Search", recipes: "Recipes", explore: "Explore", server: "Server" };

export const samePlace = (a, b) => !!a && !!b && a.tab === b.tab && (a.tab !== "entry" || normDN(a.dn || "") === normDN(b.dn || ""));

// What to call a place in a tooltip.
export const placeLabel = (p) => (!p ? "" : p.tab === "entry" && p.dn ? p.dn : `the ${TAB_NAMES[p.tab] || p.tab} tab`);

export function createTrail() {
  return { items: [], at: -1 };
}

// Going to the place you are already on does nothing; going somewhere new forgets what was
// "forward" (like a browser) and keeps the last LIMIT places.
export function visit(trail, place) {
  if (trail.at >= 0 && samePlace(trail.items[trail.at], place)) return trail;
  const items = [...trail.items.slice(0, trail.at + 1), place].slice(-LIMIT);
  return { items, at: items.length - 1 };
}

export const canBack = (trail) => trail.at > 0;
export const canForward = (trail) => trail.at >= 0 && trail.at < trail.items.length - 1;

// The trail after going back / forward (unchanged when it cannot).
export const back = (trail) => (canBack(trail) ? { items: trail.items, at: trail.at - 1 } : trail);
export const forward = (trail) => (canForward(trail) ? { items: trail.items, at: trail.at + 1 } : trail);
export const current = (trail) => (trail.at >= 0 ? trail.items[trail.at] : null);
export const peekBack = (trail) => (canBack(trail) ? trail.items[trail.at - 1] : null);
export const peekForward = (trail) => (canForward(trail) ? trail.items[trail.at + 1] : null);
