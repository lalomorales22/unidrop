import { createHash } from "node:crypto";
import { readFile, readdir, stat } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const dist = join(root, "dist");
const versionRoot = join(root, "internal", "version");
const version = (await readFile(join(versionRoot, "VERSION"), "utf8")).trim();
const protocol = Number((await readFile(join(versionRoot, "PROTOCOL"), "utf8")).trim());
const minimumCompatibleVersion = (await readFile(join(versionRoot, "MIN_COMPATIBLE_VERSION"), "utf8")).trim();

function fail(message) {
  throw new Error(`release verification failed: ${message}`);
}

async function sha256(path) {
  return createHash("sha256").update(await readFile(path)).digest("hex");
}

const checksumText = await readFile(join(dist, "SHA256SUMS"), "utf8");
const checksums = new Map();
for (const line of checksumText.trim().split("\n")) {
  const match = line.match(/^([a-f0-9]{64})  (.+)$/);
  if (!match) fail(`malformed checksum line: ${line}`);
  if (checksums.has(match[2])) fail(`duplicate checksum entry: ${match[2]}`);
  checksums.set(match[2], match[1]);
}

for (const name of (await readdir(dist)).sort()) {
  if (name === "SHA256SUMS") continue;
  const path = join(dist, name);
  if (!(await stat(path)).isFile()) continue;
  if (!checksums.has(name)) fail(`${name} is missing from SHA256SUMS`);
  if (checksums.get(name) !== await sha256(path)) fail(`${name} has a checksum mismatch`);
}

const manifestName = `unidrop-${version}-manifest.json`;
const manifest = JSON.parse(await readFile(join(dist, manifestName), "utf8"));
if (manifest.version !== version || manifest.tag !== `v${version}`) fail("manifest version does not match the canonical version");
if (!Number.isFinite(Date.parse(manifest.publishedAt)) || !Number.isFinite(Date.parse(manifest.expiresAt))) fail("manifest validity window is invalid");
if (Date.parse(manifest.expiresAt) <= Date.parse(manifest.publishedAt)) fail("manifest expires before it is published");
if (!Array.isArray(manifest.revokedVersions)) fail("manifest revoked-version list is missing");
if (manifest.protocolVersion !== protocol) fail("manifest protocol version does not match");
if (manifest.minimumCompatibleVersion !== minimumCompatibleVersion) fail("manifest minimum compatible version does not match");
if (!/^[a-f0-9]{40}$/.test(manifest.sourceCommit)) fail("manifest source commit is invalid");
if (!Array.isArray(manifest.artifacts) || manifest.artifacts.length < 11) fail("manifest is missing release artifacts");
for (const platform of ["darwin", "linux", "windows"]) {
  for (const architecture of ["amd64", "arm64"]) {
    if (!manifest.artifacts.some((artifact) => artifact.component === "updater" && artifact.platform === platform && artifact.architecture === architecture)) {
      fail(`manifest is missing updater for ${platform}/${architecture}`);
    }
  }
}

const artifactNames = new Set();
for (const artifact of manifest.artifacts) {
  if (artifactNames.has(artifact.name)) fail(`duplicate manifest artifact: ${artifact.name}`);
  artifactNames.add(artifact.name);
  const path = join(dist, artifact.name);
  const info = await stat(path);
  if (artifact.size !== info.size) fail(`${artifact.name} size does not match`);
  if (artifact.sha256 !== await sha256(path)) fail(`${artifact.name} hash does not match`);
  if (artifact.protocolVersion !== protocol || artifact.minimumCompatibleVersion !== minimumCompatibleVersion) {
    fail(`${artifact.name} compatibility metadata does not match`);
  }
  if (!artifact.url.endsWith(`/${encodeURIComponent(artifact.name)}`)) fail(`${artifact.name} URL is invalid`);
}

const sbom = JSON.parse(await readFile(join(dist, `unidrop-${version}.spdx.json`), "utf8"));
if (sbom.spdxVersion !== "SPDX-2.3" || sbom.dataLicense !== "CC0-1.0") fail("SBOM header is invalid");
if (!sbom.packages?.some((item) => item.name === "UniDrop" && item.versionInfo === version)) fail("SBOM is missing the UniDrop package");
if (!sbom.packages?.some((item) => item.name === "github.com/godbus/dbus/v5" && item.versionInfo === "v5.2.2")) fail("SBOM is missing godbus");
if (!sbom.packages?.some((item) => item.name === "golang.org/x/sys" && item.versionInfo === "v0.44.0")) fail("SBOM is missing x/sys");

console.log(`Verified ${checksums.size} release files for UniDrop ${version}`);
