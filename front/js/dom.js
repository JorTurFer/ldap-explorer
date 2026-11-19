// Tiny DOM helpers.

export const $ = (id) => document.getElementById(id);

// el("div", { class: "x", onclick: fn, textContent: "t" }, child1, child2)
export const el = (tag, props = {}, ...kids) => {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k === "class") e.className = v;
    else if (k.startsWith("on")) e.addEventListener(k.slice(2), v);
    else if (k.startsWith("data-")) e.setAttribute(k, v); // a property named data-x would not be an attribute
    else e[k] = v;
  }
  for (const k of kids) if (k != null) e.append(k);
  return e;
};
