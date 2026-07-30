import { access, mkdir, readFile, rm, writeFile, copyFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(fileURLToPath(import.meta.url));
const dist = join(root, "dist");
const canonicalVersion = join(root, "..", "internal", "version", "VERSION");
const localVersion = join(root, "version.txt");
const versionPath = await access(canonicalVersion).then(() => canonicalVersion).catch(() => localVersion);
const version = (await readFile(versionPath, "utf8")).trim();
const mirroredVersion = (await readFile(localVersion, "utf8")).trim();
if (mirroredVersion !== version) throw new Error("site/version.txt must mirror internal/version/VERSION");
const html = (await readFile(join(root, "index.html"), "utf8")).replaceAll("{{VERSION}}", version);

await rm(dist, { recursive: true, force: true });
await mkdir(join(dist, "server"), { recursive: true });
await mkdir(join(dist, "client"), { recursive: true });
await mkdir(join(dist, ".openai"), { recursive: true });

const worker = `const page = ${JSON.stringify(html)};

const headers = {
  "content-type": "text/html; charset=utf-8",
  "cache-control": "public, max-age=300",
  "content-security-policy": "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'",
  "referrer-policy": "no-referrer",
  "permissions-policy": "camera=(), microphone=(), geolocation=(), payment=()",
  "x-content-type-options": "nosniff",
  "x-frame-options": "DENY"
};

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    if (url.pathname === "/" || url.pathname === "/index.html") {
      const body = page.replaceAll("{{ORIGIN}}", url.origin);
      return new Response(request.method === "HEAD" ? null : body, { status: 200, headers });
    }
    if (env?.ASSETS) return env.ASSETS.fetch(request);
    return new Response("Not found", { status: 404, headers: { "content-type": "text/plain; charset=utf-8" } });
  }
};
`;

await writeFile(join(dist, "server", "index.js"), worker);
await writeFile(join(dist, "client", "index.html"), html);
await copyFile(join(root, ".openai", "hosting.json"), join(dist, ".openai", "hosting.json"));

try {
  await copyFile(join(root, "public", "og.png"), join(dist, "client", "og.png"));
} catch (error) {
  if (error.code !== "ENOENT") throw error;
}

console.log("Built UniDrop landing page in dist/");
