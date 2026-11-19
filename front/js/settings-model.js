// Pure helpers for the theme setting. No DOM access: unit-tested with `node --test`.

export const THEMES = ["auto", "light", "dark"];

// auto -> light -> dark -> auto
export const nextTheme = (t) => THEMES[(THEMES.indexOf(t) + 1) % THEMES.length] || "auto";

export const themeLabel = (t) => `Theme: ${THEMES.includes(t) ? t : "auto"}`;

// The value of <html data-theme>: none for auto, so the system setting decides.
export const themeAttribute = (t) => (t === "light" || t === "dark" ? t : null);
