// Pure helpers for the search history list. No DOM: unit-tested with `node --test`.
import { relativeTime } from "./decode.js";

// How a history item reads in the list: the filter (shortened) and how long ago.
export function historyLabel(item, now = new Date(), max = 60) {
  const text = (item.summary || item.filter || "(no filter)").replace(/\s+/g, " ").trim();
  const short = text.length > max ? text.slice(0, max - 1) + "…" : text;
  const when = item.at ? new Date(item.at) : null;
  return when && !Number.isNaN(when.getTime()) ? `${short} · ${relativeTime(when, now)}` : short;
}

// Where it searched, for the tooltip: "ou=people,dc=x (one level)".
export function historyTitle(item) {
  const scope = { sub: "whole branch", one: "one level", base: "that entry only" }[item.scope] || "whole branch";
  return `${item.base || "(whole directory)"} · ${scope} · ${item.mode === "raw" ? "typed filter" : "builder"}`;
}
