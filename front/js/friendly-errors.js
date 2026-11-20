// Turns the server's technical errors into something a person can act on, keeping the
// technical text in brackets so nothing is hidden. Pure: unit-tested with `node --test`.

const RULES = [
  [/^cancelled$/i, "Cancelled. Nothing was changed; run it again whenever you like."],
  [/invalid credentials|result code 49/i, "The server did not accept that user and password."],
  [/empty password not allowed/i, "A user was given but no password. Type the password, or clear the user to connect anonymously."],
  [/connection refused|no such host|i\/o timeout|network is unreachable|no route to host|dial tcp|context deadline exceeded/i,
    "Could not reach the server. Check the address, the port and your network or VPN."],
  [/x509: certificate signed by unknown authority|certificate is not trusted|unknown authority/i,
    "The server's certificate is not trusted by this computer. Install its authority, or (only for a lab server) tick “Skip certificate verification”."],
  [/x509: certificate has expired|certificate is expired/i, "The server's certificate has expired."],
  [/x509:.*(doesn't contain any ip sans|valid for|not valid for)/i, "The address you typed is not one of the names in the server's certificate."],
  [/tls: |remote error: tls/i, "The secure connection could not be set up. Check that the port matches ldaps:// or StartTLS."],
  [/no such object|result code 32/i, "That entry does not exist, or you are not allowed to see it."],
  [/insufficient access|result code 50/i, "You do not have permission to read that."],
  [/size limit exceeded|result code 4\b/i, "The server stopped the search at its own limit. Narrow the search to see the rest."],
  [/time limit exceeded|result code 3\b/i, "The server took too long to answer. Narrow the search."],
  [/unwilling to perform|result code 53/i, "The server refused to do that."],
  [/invalid dn syntax|result code 34/i, "That is not a valid DN (the full address of an entry)."],
  [/bad search filter|invalid filter/i, "The filter is not valid. Use the builder, or check the parentheses of the typed filter."],
  [/the vault is locked|vault_locked/i, "The vault is locked."],
];

// "Friendly explanation (technical text)"; the technical text alone when we have nothing better.
export function friendlyError(message) {
  const text = String(message || "").trim();
  if (!text) return "Something went wrong.";
  const rule = RULES.find(([re]) => re.test(text));
  if (!rule) return text;
  return `${rule[1]} (${text})`;
}
