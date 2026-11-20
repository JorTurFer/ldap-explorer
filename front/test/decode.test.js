import test from "node:test";
import assert from "node:assert/strict";
import { parseGeneralizedTime, relativeTime, formatUTC, describeTime, formatBytes, summaryBytes, isPhotoAttr, bytesToBase64 } from "../js/decode.js";

const iso = (d) => d && d.toISOString();

test("GeneralizedTime in its usual forms", () => {
  assert.equal(iso(parseGeneralizedTime("20261119120000Z")), "2026-11-19T12:00:00.000Z");
  assert.equal(iso(parseGeneralizedTime("20261119120000")), "2026-11-19T12:00:00.000Z"); // no zone means UTC
  assert.equal(iso(parseGeneralizedTime("202611191200Z")), "2026-11-19T12:00:00.000Z");
  assert.equal(iso(parseGeneralizedTime("2026111912Z")), "2026-11-19T12:00:00.000Z");
  assert.equal(iso(parseGeneralizedTime("20261119")), "2026-11-19T00:00:00.000Z");
  assert.equal(iso(parseGeneralizedTime("20261119120000.250Z")), "2026-11-19T12:00:00.250Z");
  assert.equal(iso(parseGeneralizedTime("20261119120000,5Z")), "2026-11-19T12:00:00.500Z");
  assert.equal(iso(parseGeneralizedTime(" 20261119120000Z ")), "2026-11-19T12:00:00.000Z");
});

test("GeneralizedTime with an offset", () => {
  assert.equal(iso(parseGeneralizedTime("20261119120000+0200")), "2026-11-19T10:00:00.000Z"); // 2 h ahead of UTC
  assert.equal(iso(parseGeneralizedTime("20261119120000-0500")), "2026-11-19T17:00:00.000Z");
  assert.equal(iso(parseGeneralizedTime("20261119120000+02")), "2026-11-19T10:00:00.000Z");
  assert.equal(iso(parseGeneralizedTime("20261119120000+0530")), "2026-11-19T06:30:00.000Z");
});

test("things that are not a time", () => {
  for (const bad of ["", "hello", "2026-11-19", "20261319120000Z", "20261100120000Z", "20260231120000Z", "20261119250000Z", "20261119126000Z", "123", null, undefined]) {
    assert.equal(parseGeneralizedTime(bad), null, String(bad));
  }
});

const now = new Date("2026-11-19T12:00:00Z");
test("relative time reads like people talk", () => {
  const at = (s) => new Date(new Date(now).getTime() + s * 1000);
  assert.equal(relativeTime(at(0), now), "just now");
  assert.equal(relativeTime(at(-30), now), "just now");
  assert.equal(relativeTime(at(-120), now), "2 minutes ago");
  assert.equal(relativeTime(at(-3600), now), "1 hour ago");
  assert.equal(relativeTime(at(-5 * 3600), now), "5 hours ago");
  assert.equal(relativeTime(at(-3 * 86400), now), "3 days ago");
  assert.equal(relativeTime(at(-40 * 86400), now), "1 month ago");
  assert.equal(relativeTime(at(-400 * 86400), now), "1 year ago");
  assert.equal(relativeTime(at(2 * 3600), now), "in 2 hours");
  assert.equal(relativeTime(at(86400), now), "in 1 day");
});

test("describeTime gives the date and how long ago", () => {
  assert.equal(formatUTC(new Date("2026-11-16T09:05:07Z")), "2026-11-16 09:05:07 UTC");
  assert.equal(describeTime("20261116090507Z", now), "2026-11-16 09:05:07 UTC · 3 days ago");
  assert.equal(describeTime("not a time", now), "");
});

test("sizes and summaries", () => {
  assert.equal(formatBytes(12), "12 bytes");
  assert.equal(formatBytes(1536), "1.5 KB");
  assert.equal(formatBytes(3 * 1024 * 1024), "3.0 MB");
  assert.equal(summaryBytes("(binary, 676 bytes) ffd8ffe0"), 676);
  assert.equal(summaryBytes("alice"), null);
  assert.equal(summaryBytes(undefined), null);
});

test("which attributes are pictures", () => {
  assert.equal(isPhotoAttr("jpegPhoto"), true);
  assert.equal(isPhotoAttr("THUMBNAILPHOTO"), true);
  assert.equal(isPhotoAttr("userCertificate"), false);
  assert.equal(isPhotoAttr(""), false);
});

test("base64 of bytes, including big arrays", () => {
  assert.equal(bytesToBase64(new Uint8Array([0xff, 0xd8, 0xff])), "/9j/");
  assert.equal(bytesToBase64(new Uint8Array(0)), "");
  const big = new Uint8Array(200000).map((_, i) => i % 251);
  assert.equal(Buffer.from(bytesToBase64(big), "base64").equals(Buffer.from(big)), true);
});
