// The entries you have visited, so Back and Forward work after following links from entry to
// entry. Pure (no DOM): unit-tested with `node --test`.

import { normDN } from "./nav.js";

const LIMIT = 100;

export function createTrail() {
  return { items: [], at: -1 };
}

// Visiting the entry you are already on does nothing; visiting a new one forgets what was
// "forward" (like a browser) and keeps the last LIMIT entries.
export function visit(trail, dn) {
  if (trail.at >= 0 && normDN(trail.items[trail.at]) === normDN(dn)) return trail;
  const items = [...trail.items.slice(0, trail.at + 1), dn].slice(-LIMIT);
  return { items, at: items.length - 1 };
}

export const canBack = (trail) => trail.at > 0;
export const canForward = (trail) => trail.at >= 0 && trail.at < trail.items.length - 1;

// The trail after going back / forward (unchanged when it cannot), and the entry to show.
export const back = (trail) => (canBack(trail) ? { items: trail.items, at: trail.at - 1 } : trail);
export const forward = (trail) => (canForward(trail) ? { items: trail.items, at: trail.at + 1 } : trail);
export const current = (trail) => (trail.at >= 0 ? trail.items[trail.at] : null);
export const peekBack = (trail) => (canBack(trail) ? trail.items[trail.at - 1] : null);
export const peekForward = (trail) => (canForward(trail) ? trail.items[trail.at + 1] : null);
