import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const dist = join(root, "dist");
const versionRoot = join(root, "internal", "version");
const checksumOnly = process.argv.includes("--checksums-only");

async function textFile(name) {
  return (await readFile(join(versionRoot, name), "utf8")).trim();
}

async function sha256(path) {
  return createHash("sha256").update(await readFile(path)).digest("hex");
}

async function releaseFiles() {
  const entries = await readdir(dist);
  const files = [];
  for (const name of entries.sort()) {
    if (name === "SHA256SUMS") continue;
    const path = join(dist, name);
    if ((await stat(path)).isFile()) files.push({ name, path });
  }
  return files;
}

function componentFor(name) {
  let match = name.match(/^unidrop-(darwin|linux|windows)-(amd64|arm64)(?:\.exe)?$/);
  if (match) return { component: "core", platform: match[1], architecture: match[2] };
  match = name.match(/^unidrop-tray-(linux|windows)-(amd64|arm64)(?:\.exe)?$/);
  if (match) return { component: "tray", platform: match[1], architecture: match[2] };
  match = name.match(/^unidrop-menu-darwin-(amd64|arm64)$/);
  if (match) return { component: "menu", platform: "darwin", architecture: match[1] };
  match = name.match(/^unidrop-update-(darwin|linux|windows)-(amd64|arm64)(?:\.exe)?$/);
  if (match) return { component: "updater", platform: match[1], architecture: match[2] };
  if (/^unidrop-[0-9]+\.[0-9]+\.[0-9]+-macos-universal\.dmg$/.test(name)) {
    return { component: "package", platform: "darwin", architecture: "universal" };
  }
  if (name.endsWith(".spdx.json")) return { component: "sbom", platform: "all", architecture: "all" };
  return { component: "release-metadata", platform: "all", architecture: "all" };
}

function gitValue(...args) {
  return execFileSync("git", args, { cwd: root, encoding: "utf8" }).trim();
}

async function writeChecksums() {
  const files = await releaseFiles();
  const lines = [];
  for (const file of files) lines.push(`${await sha256(file.path)}  ${file.name}`);
  await writeFile(join(dist, "SHA256SUMS"), `${lines.join("\n")}\n`);
}

if (checksumOnly) {
  await writeChecksums();
  process.exit(0);
}

const version = await textFile("VERSION");
const protocol = Number(await textFile("PROTOCOL"));
const minimumCompatibleVersion = await textFile("MIN_COMPATIBLE_VERSION");
const repository = process.env.GITHUB_REPOSITORY || "lalomorales22/unidrop";
const tag = `v${version}`;
const releaseBaseURL = process.env.UNIDROP_RELEASE_BASE_URL ||
  `https://github.com/${repository}/releases/download/${tag}`;
const commit = process.env.GITHUB_SHA || gitValue("rev-parse", "HEAD");
const commitDate = process.env.SOURCE_DATE_EPOCH
  ? new Date(Number(process.env.SOURCE_DATE_EPOCH) * 1000).toISOString()
  : new Date(gitValue("show", "-s", "--format=%cI", "HEAD")).toISOString();
const expiresAt = new Date(new Date(commitDate).getTime() + 30 * 24 * 60 * 60 * 1000).toISOString();
const sbomName = `unidrop-${version}.spdx.json`;
const manifestName = `unidrop-${version}-manifest.json`;

await rm(join(dist, sbomName), { force: true });
await rm(join(dist, manifestName), { force: true });

const binaries = await releaseFiles();
const sbomFiles = [];
for (const file of binaries) {
  const id = file.name.replace(/[^A-Za-z0-9.-]/g, "-");
  sbomFiles.push({
    fileName: `dist/${file.name}`,
    SPDXID: `SPDXRef-File-${id}`,
    checksums: [{ algorithm: "SHA256", checksumValue: await sha256(file.path) }],
    licenseConcluded: "NOASSERTION",
    copyrightText: "NOASSERTION"
  });
}

const sbom = {
  spdxVersion: "SPDX-2.3",
  dataLicense: "CC0-1.0",
  SPDXID: "SPDXRef-DOCUMENT",
  name: `UniDrop ${version} release SBOM`,
  documentNamespace: `https://github.com/${repository}/releases/tag/${tag}/sbom/${commit}`,
  creationInfo: {
    created: commitDate,
    creators: ["Tool: UniDrop dependency-free release-metadata.mjs"]
  },
  packages: [
    {
      name: "UniDrop",
      SPDXID: "SPDXRef-Package-UniDrop",
      versionInfo: version,
      downloadLocation: `https://github.com/${repository}/tree/${commit}`,
      filesAnalyzed: false,
      licenseConcluded: "NOASSERTION",
      licenseDeclared: "NOASSERTION",
      copyrightText: "NOASSERTION"
    },
    {
      name: "github.com/godbus/dbus/v5",
      SPDXID: "SPDXRef-Package-godbus-dbus-v5",
      versionInfo: "v5.2.2",
      downloadLocation: "https://github.com/godbus/dbus/tree/v5.2.2",
      filesAnalyzed: false,
      licenseConcluded: "BSD-2-Clause",
      licenseDeclared: "BSD-2-Clause",
      copyrightText: "NOASSERTION"
    },
    {
      name: "golang.org/x/sys",
      SPDXID: "SPDXRef-Package-golang-x-sys",
      versionInfo: "v0.44.0",
      downloadLocation: "https://pkg.go.dev/golang.org/x/sys@v0.44.0",
      filesAnalyzed: false,
      licenseConcluded: "BSD-3-Clause",
      licenseDeclared: "BSD-3-Clause",
      copyrightText: "NOASSERTION"
    }
  ],
  files: sbomFiles,
  relationships: [
    { spdxElementId: "SPDXRef-DOCUMENT", relationshipType: "DESCRIBES", relatedSpdxElement: "SPDXRef-Package-UniDrop" },
    { spdxElementId: "SPDXRef-Package-UniDrop", relationshipType: "DEPENDS_ON", relatedSpdxElement: "SPDXRef-Package-godbus-dbus-v5" },
    { spdxElementId: "SPDXRef-Package-UniDrop", relationshipType: "DEPENDS_ON", relatedSpdxElement: "SPDXRef-Package-golang-x-sys" },
    ...sbomFiles.map((file) => ({
      spdxElementId: file.SPDXID,
      relationshipType: "GENERATED_FROM",
      relatedSpdxElement: "SPDXRef-Package-UniDrop"
    }))
  ]
};
await writeFile(join(dist, sbomName), `${JSON.stringify(sbom, null, 2)}\n`);

const artifacts = [];
for (const file of await releaseFiles()) {
  const metadata = componentFor(file.name);
  artifacts.push({
    name: file.name,
    url: `${releaseBaseURL}/${encodeURIComponent(file.name)}`,
    ...metadata,
    protocolVersion: protocol,
    minimumCompatibleVersion,
    size: (await stat(file.path)).size,
    sha256: await sha256(file.path)
  });
}

const manifest = {
  schemaVersion: 1,
  product: "UniDrop",
  version,
  tag,
  sourceCommit: commit,
  publishedAt: commitDate,
  expiresAt,
  protocolVersion: protocol,
  minimumCompatibleVersion,
  revokedVersions: [],
  artifacts
};
await writeFile(join(dist, manifestName), `${JSON.stringify(manifest, null, 2)}\n`);
await writeChecksums();

console.log(`Generated ${basename(join(dist, manifestName))}, ${sbomName}, and SHA256SUMS`);
