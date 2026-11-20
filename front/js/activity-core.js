// The requests in flight, shared by the API layer (which reports them) and the status bar
// (which shows them). It does not import the API, so there is no cycle.
import { createTracker, describeRequest } from "./activity-model.js";
import { emit } from "./events.js";

export const tracker = createTracker();
const controllers = new Map(); // id -> AbortController, for the requests the bar shows
const cancelled = new Set();
let seq = 0;

const newId = () => `${Date.now().toString(36)}${(seq++).toString(36)}${Math.random().toString(36).slice(2, 8)}`;

// Called by the API layer before it sends a request. `end(outcome)` when it is over:
// "ok", "error" or "cancelled".
export function beginRequest(method, path, body) {
  const id = newId();
  const desc = describeRequest(method, path, body);
  const ctl = new AbortController();
  if (!desc.quiet) {
    controllers.set(id, ctl);
    tracker.start(id, desc, performance.now());
    emit("activity-changed");
  }
  return {
    id, signal: ctl.signal, quiet: desc.quiet,
    wasCancelled: () => cancelled.has(id),
    end(outcome) {
      if (desc.quiet) return;
      tracker.finish(id, performance.now(), cancelled.has(id) ? "cancelled" : outcome);
      controllers.delete(id); cancelled.delete(id);
      emit("activity-changed");
    },
  };
}

// The Cancel button: abort everything the bar is showing. The server notices that the
// request is gone and stops asking the directory for more pages.
export function cancelAll() {
  for (const [id, ctl] of controllers) { cancelled.add(id); ctl.abort(); }
}
