// The Recipes tab: ready-made queries for the connected server. The server only
// marks a recipe available when its schema can answer it; the rest are shown
// greyed out with the reason.

import { $, el } from "./dom.js";
import { api } from "./api.js";
import { emit, on } from "./events.js";
import { friendlyError } from "./friendly-errors.js";

const CATEGORIES = ["People", "Groups", "Accounts", "Explore"];

export function initRecipes() {
  on("connected", load);
  on("disconnected", () => {
    $("pane-recipes").replaceChildren(el("div", { class: "empty", textContent: "Connect to a server to see the recipes." }));
  });
}

async function load() {
  const pane = $("pane-recipes");
  pane.replaceChildren(el("div", { class: "muted", textContent: "Loading…" }));
  try {
    render(await api("/api/recipes"));
  } catch (e) {
    pane.replaceChildren(el("div", { class: "err", textContent: e.message }));
  }
}

function render(list) {
  const root = el("div", { class: "recipes" });
  for (const cat of CATEGORIES) {
    const items = list.filter((r) => r.category === cat);
    if (items.length === 0) continue;
    root.append(el("h3", { textContent: cat }), ...items.map(card));
  }
  $("pane-recipes").replaceChildren(root);
}

function card(r) {
  const err = el("div", { class: "err" });
  const inputs = new Map();

  const rows = (r.params || []).flatMap((p) => {
    const input = el("input", { type: p.kind === "days" ? "number" : "text", placeholder: p.placeholder || "", value: p.default || "",
      autocomplete: "off", spellcheck: false, disabled: !r.available });
    if (p.kind === "days") { input.min = "1"; input.step = "1"; }
    if (p.kind === "class") input.setAttribute("list", "classList");
    if (p.kind === "attribute") input.setAttribute("list", "attrList");
    input.dataset.param = p.name;
    input.id = `rp-${r.id}-${p.name}`;
    input.addEventListener("keydown", (ev) => { if (ev.key === "Enter") use(); });
    inputs.set(p.name, input);
    return [el("label", { htmlFor: input.id, textContent: p.label + (p.required ? "" : " (optional)") }), input, ...(p.help ? [el("div", { class: "help", textContent: p.help })] : [])];
  });

  async function use() {
    err.textContent = "";
    const params = {};
    for (const [name, input] of inputs) params[name] = input.value;
    try {
      const plan = await api("/api/recipes/" + encodeURIComponent(r.id), { body: { params } });
      emit("load-query", plan);
    } catch (e) { err.textContent = friendlyError(e.message); }
  }

  return el("div", { class: "recipe" + (r.available ? "" : " unavailable"), id: "recipe-" + r.id },
    el("div", { class: "rtitle", textContent: r.title }),
    el("div", { class: "rdesc", textContent: r.description }),
    ...(r.available ? [] : [el("div", { class: "reason", textContent: "Not available on this server: " + r.reason })]),
    ...(rows.length ? [el("div", { class: "rparams" }, ...rows)] : []),
    el("button", { class: "primary", textContent: "Use this recipe", disabled: !r.available, onclick: use }),
    err);
}
