// Entry point: wires the modules together.
import { api } from "./api.js";
import { setStatus } from "./status.js";
import { initTabs } from "./tabs.js";
import { initProfiles, loadProfiles } from "./profiles.js";
import { initConnection } from "./connection.js";
import { initTree } from "./tree.js";
import { initEntry } from "./entry.js";
import { initSearch } from "./search.js";
import { initBuilder } from "./builder.js";
import { initRecipes } from "./recipes.js";
import { initExplore } from "./explore.js";
import { initSavedQueries } from "./savedqueries.js";

initTabs();
initProfiles();
initConnection();
initTree();
initEntry();
initBuilder();
initSearch();
initRecipes();
initExplore();
initSavedQueries();

loadProfiles();
// Heartbeat: if the tab is closed the server notices and exits.
const ping = () => api("/api/ping", { method: "POST" }).catch(() => setStatus("The ldap-explorer process is not responding", true));
ping(); setInterval(ping, 10000);
