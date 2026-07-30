#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const defaultRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const root = process.env.XENDFILE_FLATPAK_ROOT ? path.resolve(process.env.XENDFILE_FLATPAK_ROOT) : defaultRoot;
const appID = 'io.github.lalomorales22.xendfile';
const manifestPath = path.join(root, `${appID}.json`);
const desktopPath = path.join(root, 'linux', `${appID}.desktop`);
const metainfoPath = path.join(root, 'linux', `${appID}.metainfo.xml`);
const wrapperPath = path.join(root, 'linux', 'xendfile-flatpak-wrapper.sh');

function fail(message) {
  console.error(`Flatpak validation failed: ${message}`);
  process.exit(1);
}

function requireValue(condition, message) {
  if (!condition) fail(message);
}

const encoded = fs.readFileSync(manifestPath, 'utf8');
let manifest;
try {
  manifest = JSON.parse(encoded);
} catch (error) {
  fail(`manifest is not strict JSON: ${error.message}`);
}
requireValue(encoded === `${JSON.stringify(manifest, null, 4)}\n`, 'manifest formatting must be canonical four-space JSON with a final newline');
requireValue(path.basename(manifestPath) === `${manifest.id}.json`, 'manifest filename must match its application ID');
requireValue(manifest.id === appID, 'unexpected application ID');
requireValue(manifest.runtime === 'org.freedesktop.Platform', 'unexpected runtime');
requireValue(manifest['runtime-version'] === '25.08', 'runtime must remain on the documented development branch');
requireValue(manifest.sdk === 'org.freedesktop.Sdk', 'unexpected SDK');
requireValue(JSON.stringify(manifest['sdk-extensions']) === JSON.stringify(['org.freedesktop.Sdk.Extension.golang']), 'only the Go SDK extension is allowed');
requireValue(manifest.command === 'xendfile-flatpak', 'unexpected application command');
requireValue(typeof manifest['x-comment'] === 'string' && manifest['x-comment'].includes('not a Flathub submission'), 'development-only boundary must be explicit');

const expectedPermissions = [
  '--share=network',
  '--filesystem=xdg-download/Xendfile:create',
  '--talk-name=org.kde.StatusNotifierWatcher',
  '--talk-name=org.freedesktop.StatusNotifierWatcher',
  '--own-name=org.kde.StatusNotifierItem-*',
  '--own-name=org.freedesktop.StatusNotifierItem-*',
];
requireValue(JSON.stringify(manifest['finish-args']) === JSON.stringify(expectedPermissions), 'sandbox permissions changed without review');
for (const forbidden of ['--filesystem=home', '--filesystem=host', '--socket=session-bus', '--socket=system-bus', '--device=all']) {
  requireValue(!manifest['finish-args'].includes(forbidden), `broad permission ${forbidden} is forbidden`);
}

const buildEnvironment = manifest['build-options']?.env;
requireValue(manifest['build-options']?.['append-path'] === '/usr/lib/sdk/golang/bin', 'Go SDK extension is not on PATH');
requireValue(buildEnvironment?.CGO_ENABLED === '0', 'Flatpak build must disable cgo');
requireValue(buildEnvironment?.GOPROXY === 'off' && buildEnvironment?.GOSUMDB === 'off', 'Flatpak build must be offline');
requireValue(buildEnvironment?.GOFLAGS === '-buildvcs=false', 'Flatpak build must not depend on Git metadata');

requireValue(Array.isArray(manifest.modules) && manifest.modules.length === 1, 'manifest must contain one auditable source module');
const module = manifest.modules[0];
requireValue(module.name === 'xendfile' && module.buildsystem === 'simple', 'unexpected build module');
requireValue(Array.isArray(module.sources) && module.sources.length === 1, 'build must have one local source');
const source = module.sources[0];
requireValue(source.type === 'dir' && source.path === '.', 'development build must use the local source tree');
for (const skipped of ['.git', '.flatpak-builder', 'build-flatpak', 'dist', 'site/dist']) {
  requireValue(source.skip?.includes(skipped), `local source must exclude ${skipped}`);
}

const commands = module['build-commands']?.join('\n') ?? '';
for (const required of [
  'go1.26.5',
  'go test -mod=vendor ./...',
  'go build -mod=vendor -trimpath',
  './cmd/xendfile-tray',
  './cmd/xendfile-update',
  'install -Dm644 LICENSE /app/share/licenses/xendfile/LICENSE',
  'desktop-file-validate',
  'appstreamcli validate --no-net',
]) {
  requireValue(commands.includes(required), `build commands are missing ${required}`);
}

const desktop = fs.readFileSync(desktopPath, 'utf8');
requireValue(desktop.startsWith('[Desktop Entry]\n'), 'desktop file header is invalid');
requireValue(desktop.includes('\nExec=xendfile-flatpak\n'), 'desktop file command does not use the wrapper');
requireValue(desktop.includes(`\nIcon=${appID}\n`), 'desktop icon does not match the application ID');
requireValue(desktop.endsWith('\n'), 'desktop file needs a final newline');

const metainfo = fs.readFileSync(metainfoPath, 'utf8');
requireValue(metainfo.includes(`<id>${appID}</id>`), 'Metainfo ID does not match the manifest');
requireValue(metainfo.includes(`<launchable type="desktop-id">${appID}.desktop</launchable>`), 'Metainfo launchable does not match the desktop file');
requireValue(metainfo.includes('<project_license>Apache-2.0</project_license>'), 'Metainfo must declare the approved Apache-2.0 client license');
requireValue(metainfo.includes('__VERSION__') && metainfo.includes('__RELEASE_DATE__'), 'Metainfo release placeholders are missing');

const wrapper = fs.readFileSync(wrapperPath, 'utf8');
requireValue(wrapper.startsWith('#!/bin/sh\nset -eu\n'), 'wrapper must be a fail-closed POSIX shell script');
requireValue(wrapper.includes('CORE=/app/bin/xendfile') && wrapper.includes('TRAY=/app/bin/xendfile-tray'), 'wrapper does not use packaged binaries');
requireValue(wrapper.includes('trap cleanup EXIT HUP INT TERM'), 'wrapper does not clean up the core');

console.log(`Verified development Flatpak manifest for ${appID}`);
