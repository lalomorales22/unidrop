import assert from "node:assert/strict";
import { access, readFile } from "node:fs/promises";
import test from "node:test";

const html = await readFile(new URL("../index.html", import.meta.url), "utf8");
const mirroredVersion = (await readFile(new URL("../version.txt", import.meta.url), "utf8")).trim();
const snapshot = JSON.parse(await readFile(new URL("../source-snapshot.json", import.meta.url), "utf8"));
const canonicalURL = new URL("../../internal/version/VERSION", import.meta.url);
const version = await access(canonicalURL)
  .then(() => readFile(canonicalURL, "utf8").then((value) => value.trim()))
  .catch(() => mirroredVersion);

test("contains one download path for every supported desktop platform", () => {
  for (const platform of ["mac", "windows", "linux"]) {
    assert.match(html, new RegExp(`data-platform="${platform}"`));
  }
  assert.equal((html.match(/data-download/g) || []).length, 3);
});

test("contains product, security, installation, and accessibility essentials", () => {
  for (const phrase of ["TLS 1.3", "No cloud", "How it works", "Install in minutes", "View source on GitHub", "Release facts", "Client privacy notice"]) {
    assert.ok(html.includes(phrase), `missing ${phrase}`);
  }
  assert.ok(html.includes("prefers-reduced-motion"));
  assert.ok(html.includes("aria-label"));
});

test("detects platform and architecture while preserving manual choices", () => {
  assert.match(html, /getHighEntropyValues\(\["architecture", "bitness"\]\)/);
  assert.match(html, /ARM64/);
  assert.match(html, /AMD64/);
  assert.match(html, /All platform and architecture choices remain available/);
  assert.equal((html.match(/data-download/g) || []).length, 3);
});

test("labels the unsigned source alpha honestly and links release resources", () => {
  for (const phrase of ["source-based alpha", "does not yet carry the planned UniDrop publisher signature", "Release notes", "Installation guide", "Source code"]) {
    assert.ok(html.includes(phrase), `missing ${phrase}`);
  }
  assert.match(html, /docs\/RELEASE_NOTES\.md">Release notes/);
  assert.match(html, /href="#privacy">Client privacy notice/);
  assert.match(html, /blob\/main\/PRIVACY\.md/);
});

test("binds every alpha download to one immutable verified source snapshot", async () => {
  assert.match(snapshot.commit, /^[a-f0-9]{40}$/);
  assert.equal(snapshot.shortCommit, snapshot.commit.slice(0, 7));
  assert.equal(snapshot.url, `https://github.com/lalomorales22/unidrop/archive/${snapshot.commit}.zip`);
  assert.match(snapshot.sha256, /^[a-f0-9]{64}$/);
  assert.ok(Number.isSafeInteger(snapshot.size) && snapshot.size > 0);
  assert.doesNotMatch(html, /archive\/refs\/heads\/main\.zip/);
  const built = await readFile(new URL("../dist/client/index.html", import.meta.url), "utf8");
  assert.equal((built.match(new RegExp(snapshot.url.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "g")) || []).length, 3);
  for (const value of [snapshot.commit, snapshot.shortCommit, String(snapshot.size), snapshot.displaySize, snapshot.sha256]) {
    assert.ok(built.includes(value), `built site is missing snapshot value ${value}`);
  }
  assert.doesNotMatch(built, /\{\{(?:VERSION|SOURCE_SNAPSHOT_[A-Z_]+)\}\}/);
  assert.doesNotMatch(built, /releases\/download|\.dmg\b|\.msix\b|\.AppImage\b/);
});

test("has no third-party scripts, trackers, or remote font dependencies", () => {
  assert.doesNotMatch(html, /<script[^>]+src=/i);
  assert.doesNotMatch(html, /google-analytics|googletagmanager|fonts\.googleapis/i);
});

test("build injects the canonical product version", async () => {
  assert.equal(mirroredVersion, version);
  const built = await readFile(new URL("../dist/client/index.html", import.meta.url), "utf8");
  assert.ok(built.includes(`v${version}`));
  assert.doesNotMatch(built, /\{\{VERSION\}\}/);
});
