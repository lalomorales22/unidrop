import { createReadStream } from "node:fs";
import { access, readFile, stat } from "node:fs/promises";
import { createServer } from "node:http";
import { extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL(".", import.meta.url));
const host = "127.0.0.1";
const port = Number(process.env.PORT || 4173);
const types = { ".png": "image/png", ".ico": "image/x-icon" };
const canonicalVersion = join(root, "..", "internal", "version", "VERSION");
const localVersion = join(root, "version.txt");
const versionPath = await access(canonicalVersion).then(() => canonicalVersion).catch(() => localVersion);
const version = (await readFile(versionPath, "utf8")).trim();

const server = createServer(async (request, response) => {
  const url = new URL(request.url || "/", `http://${host}:${port}`);
  if (url.pathname === "/" || url.pathname === "/index.html") {
    const html = (await readFile(join(root, "index.html"), "utf8"))
      .replaceAll("{{ORIGIN}}", url.origin)
      .replaceAll("{{VERSION}}", version);
    response.writeHead(200, { "content-type": "text/html; charset=utf-8", "cache-control": "no-store" });
    response.end(html);
    return;
  }
  const relative = normalize(url.pathname).replace(/^[/\\]+/, "");
  const asset = join(root, "public", relative);
  try {
    const info = await stat(asset);
    if (!info.isFile()) throw new Error("not a file");
    response.writeHead(200, { "content-type": types[extname(asset)] || "application/octet-stream" });
    createReadStream(asset).pipe(response);
  } catch {
    response.writeHead(404, { "content-type": "text/plain; charset=utf-8" });
    response.end("Not found");
  }
});

server.listen(port, host, () => {
  console.log(`Local: http://${host}:${port}/`);
});
