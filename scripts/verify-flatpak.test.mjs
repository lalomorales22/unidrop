import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { spawnSync } from 'node:child_process';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const verifier = path.join(root, 'scripts', 'verify-flatpak.mjs');
const appID = 'io.github.lalomorales22.xendfile';

function fixture(t) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'xendfile-flatpak-test-'));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  fs.mkdirSync(path.join(directory, 'linux'), { recursive: true });
  for (const source of [
    `${appID}.json`,
    `linux/${appID}.desktop`,
    `linux/${appID}.metainfo.xml`,
    'linux/xendfile-flatpak-wrapper.sh',
  ]) {
    fs.copyFileSync(path.join(root, source), path.join(directory, source));
  }
  return directory;
}

function runVerifier(directory) {
  return spawnSync(process.execPath, [verifier], {
    encoding: 'utf8',
    env: { ...process.env, XENDFILE_FLATPAK_ROOT: directory },
  });
}

function mutateManifest(directory, mutate) {
  const manifestPath = path.join(directory, `${appID}.json`);
  const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
  mutate(manifest);
  fs.writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 4)}\n`);
}

test('accepts the reviewed development manifest fixture', (t) => {
  const result = runVerifier(fixture(t));
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /Verified development Flatpak manifest/);
});

test('rejects broad host filesystem access', (t) => {
  const directory = fixture(t);
  mutateManifest(directory, (manifest) => {
    manifest['finish-args'][1] = '--filesystem=home';
  });
  const result = runVerifier(directory);
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /sandbox permissions changed without review/);
});

test('rejects online Go dependency resolution', (t) => {
  const directory = fixture(t);
  mutateManifest(directory, (manifest) => {
    manifest['build-options'].env.GOPROXY = 'https://proxy.golang.org';
  });
  const result = runVerifier(directory);
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /build must be offline/);
});

test('rejects a remote or unpinned application source', (t) => {
  const directory = fixture(t);
  mutateManifest(directory, (manifest) => {
    manifest.modules[0].sources[0] = {
      type: 'git',
      url: 'https://github.com/lalomorales22/xendfile',
      branch: 'main',
    };
  });
  const result = runVerifier(directory);
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /development build must use the local source tree/);
});

test('rejects application identity drift across metadata', (t) => {
  const directory = fixture(t);
  const metainfoPath = path.join(directory, 'linux', `${appID}.metainfo.xml`);
  const metainfo = fs.readFileSync(metainfoPath, 'utf8').replace(`<id>${appID}</id>`, '<id>com.example.Impostor</id>');
  fs.writeFileSync(metainfoPath, metainfo);
  const result = runVerifier(directory);
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /Metainfo ID does not match/);
});
