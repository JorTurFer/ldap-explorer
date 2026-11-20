import test from "node:test";
import assert from "node:assert/strict";
import { friendlyError } from "../js/friendly-errors.js";

const cases = [
  ['bind: LDAP Result Code 49 "Invalid Credentials": ', /did not accept that user and password/],
  ["bind: ldap: empty password not allowed by the client", /no password/],
  ["dial tcp 10.1.2.3:636: connect: connection refused", /Could not reach the server/],
  ["dial tcp: lookup ldap.example: no such host", /Could not reach the server/],
  ["dial tcp 10.1.2.3:636: i/o timeout", /Could not reach the server/],
  ["LDAP Result Code 200 \"Network Error\": context deadline exceeded", /Could not reach the server/],
  ["tls: failed to verify certificate: x509: certificate signed by unknown authority", /not trusted by this computer/],
  ["x509: certificate has expired or is not yet valid", /expired/],
  ["x509: certificate is valid for ldap.example.org, not 10.0.0.1", /not one of the names/],
  ["remote error: tls: handshake failure", /secure connection could not be set up/],
  ['LDAP Result Code 32 "No Such Object": ', /does not exist, or you are not allowed/],
  ['LDAP Result Code 50 "Insufficient Access Rights": ', /do not have permission/],
  ['LDAP Result Code 4 "Size Limit Exceeded": ', /stopped the search at its own limit/],
  ['LDAP Result Code 3 "Time Limit Exceeded": ', /took too long/],
  ['LDAP Result Code 34 "Invalid DN Syntax": ', /not a valid DN/],
  ["invalid filter: LDAP Result Code 201 \"Filter Compile Error\": ldap: error parsing filter", /filter is not valid/],
];

test("technical errors get an explanation and keep the original text", () => {
  for (const [raw, expected] of cases) {
    const out = friendlyError(raw);
    assert.match(out, expected, raw);
    assert.ok(out.endsWith(`(${raw.trim()})`), `the original is kept: ${out}`);
  }
});

test("an error we do not know is shown as it is", () => {
  assert.equal(friendlyError("something exotic happened"), "something exotic happened");
  assert.equal(friendlyError("  padded  "), "padded");
});

test("nothing at all", () => {
  assert.equal(friendlyError(""), "Something went wrong.");
  assert.equal(friendlyError(undefined), "Something went wrong.");
  assert.equal(friendlyError(null), "Something went wrong.");
});

test("a size limit is not confused with a time limit or other codes", () => {
  assert.match(friendlyError('LDAP Result Code 4 "Size Limit Exceeded"'), /own limit/);
  assert.doesNotMatch(friendlyError('LDAP Result Code 49 "Invalid Credentials"'), /limit/);
  assert.doesNotMatch(friendlyError('LDAP Result Code 34 "Invalid DN Syntax"'), /own limit/);
});
