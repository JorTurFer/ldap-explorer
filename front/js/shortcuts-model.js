// Keyboard shortcuts: the list, and the logic that turns key presses (including two-key
// sequences such as "g then s") into actions. Pure: unit-tested with `node --test`.

export const SHORTCUTS = [
  { keys: ["/"], action: "quickfind", help: "Find anyone or anything (go to the search box)" },
  { keys: ["t"], action: "treefilter", help: "Filter the tree, or go to a DN" },
  { keys: ["g", "e"], action: "tab:entry", help: "Go to Entry" },
  { keys: ["g", "s"], action: "tab:search", help: "Go to Search" },
  { keys: ["g", "r"], action: "tab:recipes", help: "Go to Recipes" },
  { keys: ["g", "x"], action: "tab:explore", help: "Go to Explore" },
  { keys: ["g", "v"], action: "tab:server", help: "Go to Server and schema" },
  { keys: ["g", "b"], action: "bookmarks", help: "Open the bookmarks" },
  { keys: ["g", "p"], action: "passwords", help: "Open the Passwords panel" },
  { keys: ["["], action: "back", help: "Back to the previous entry" },
  { keys: ["]"], action: "forward", help: "Forward to the next entry" },
  { keys: ["?"], action: "help", help: "Show this list" },
];

const SEQUENCE_MS = 1200; // the second key of a sequence must come within this time

// One key press. `pending` is what was typed before ({ keys, at }) or null.
// Returns { action, pending }: the action to run (or null) and what is still pending.
export function step(pending, key, now, shortcuts = SHORTCUTS) {
  const live = pending && now - pending.at <= SEQUENCE_MS ? pending.keys : [];
  for (const candidate of [[...live, key], [key]]) {
    const exact = shortcuts.find((s) => s.keys.length === candidate.length && s.keys.every((k, i) => k === candidate[i]));
    if (exact) return { action: exact.action, pending: null };
    const prefix = shortcuts.some((s) => s.keys.length > candidate.length && candidate.every((k, i) => s.keys[i] === k));
    if (prefix) return { action: null, pending: { keys: candidate, at: now } };
    if (live.length === 0) break; // nothing to retry without the pending keys
  }
  return { action: null, pending: null };
}

// Typing in a box must never trigger a shortcut.
export const isTypingTarget = (tag, editable = false) => editable || ["INPUT", "TEXTAREA", "SELECT"].includes((tag || "").toUpperCase());

export const describeKeys = (keys) => keys.join(" then ");
