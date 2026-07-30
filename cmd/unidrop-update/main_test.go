package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var fixedUpdateTime = time.Date(2026, 7, 30, 16, 0, 0, 0, time.UTC)

type updateFixture struct {
	t               *testing.T
	publicKey       ed25519.PublicKey
	privateKey      ed25519.PrivateKey
	artifact        []byte
	artifactHandler http.HandlerFunc
	manifest        releaseManifest
	manifestBytes   []byte
	signatureBytes  []byte
	server          *httptest.Server
}

func newUpdateFixture(t *testing.T) *updateFixture {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &updateFixture{
		t: t, publicKey: publicKey, privateKey: privateKey,
		artifact: []byte("verified UniDrop update bytes\n"),
	}
	fixture.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			_, _ = w.Write(fixture.manifestBytes)
		case "/manifest.sig.json":
			_, _ = w.Write(fixture.signatureBytes)
		case "/unidrop-linux-amd64":
			if fixture.artifactHandler != nil {
				fixture.artifactHandler(w, r)
				return
			}
			_, _ = w.Write(fixture.artifact)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fixture.server.Close)
	fixture.resetManifest()
	return fixture
}

func (fixture *updateFixture) resetManifest() {
	digest := sha256.Sum256(fixture.artifact)
	fixture.manifest = releaseManifest{
		SchemaVersion:            1,
		Product:                  "UniDrop",
		Version:                  "0.4.0",
		Tag:                      "v0.4.0",
		SourceCommit:             strings.Repeat("a", 40),
		PublishedAt:              fixedUpdateTime.Add(-time.Hour).Format(time.RFC3339),
		ExpiresAt:                fixedUpdateTime.Add(30 * 24 * time.Hour).Format(time.RFC3339),
		ProtocolVersion:          2,
		MinimumCompatibleVersion: "0.2.0",
		RevokedVersions:          []string{},
		Artifacts: []releaseArtifact{{
			Name:                     "unidrop-linux-amd64",
			URL:                      fixture.server.URL + "/unidrop-linux-amd64",
			Component:                "core",
			Platform:                 "linux",
			Architecture:             "amd64",
			ProtocolVersion:          2,
			MinimumCompatibleVersion: "0.2.0",
			Size:                     int64(len(fixture.artifact)),
			SHA256:                   hex.EncodeToString(digest[:]),
		}},
	}
	fixture.resign()
}

func (fixture *updateFixture) resign() {
	fixture.t.Helper()
	manifestBytes, err := json.MarshalIndent(fixture.manifest, "", "  ")
	if err != nil {
		fixture.t.Fatal(err)
	}
	fixture.setSignedManifest(append(manifestBytes, '\n'), fixture.privateKey)
}

func (fixture *updateFixture) setSignedManifest(manifest []byte, privateKey ed25519.PrivateKey) {
	fixture.t.Helper()
	fixture.manifestBytes = manifest
	digest := sha256.Sum256(manifest)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	keyDigest := sha256.Sum256(publicKey)
	envelope := signatureEnvelope{
		SchemaVersion:  1,
		Algorithm:      "Ed25519",
		KeyID:          hex.EncodeToString(keyDigest[:]),
		ManifestSHA256: hex.EncodeToString(digest[:]),
		Signature:      base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, manifest)),
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		fixture.t.Fatal(err)
	}
	fixture.signatureBytes = encoded
}

func (fixture *updateFixture) options() updateOptions {
	parsed, err := checkedHTTPSURL(fixture.server.URL)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return updateOptions{
		CurrentVersion:  "0.3.3",
		Platform:        "linux",
		Architecture:    "amd64",
		Component:       "core",
		ApprovedHosts:   map[string]struct{}{canonicalHost(parsed): {}},
		Now:             fixedUpdateTime,
		MaximumArtifact: 1 << 20,
	}
}

func (fixture *updateFixture) trustedKeys() map[string]ed25519.PublicKey {
	digest := sha256.Sum256(fixture.publicKey)
	return map[string]ed25519.PublicKey{hex.EncodeToString(digest[:]): fixture.publicKey}
}

func (fixture *updateFixture) stage(t *testing.T, options updateOptions) (*stageResult, error) {
	t.Helper()
	root := t.TempDir()
	return stageUpdate(context.Background(), fixture.server.Client(),
		fixture.server.URL+"/manifest.json", fixture.server.URL+"/manifest.sig.json",
		fixture.trustedKeys(), options, filepath.Join(root, "stage"), filepath.Join(root, "state", "update.json"))
}

func TestSignedUpdateStagesWithoutTouchingWorkingInstallation(t *testing.T) {
	fixture := newUpdateFixture(t)
	root := t.TempDir()
	working := filepath.Join(root, "working-unidrop")
	if err := os.WriteFile(working, []byte("current working version\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "state", "update.json")
	result, err := stageUpdate(context.Background(), fixture.server.Client(),
		fixture.server.URL+"/manifest.json", fixture.server.URL+"/manifest.sig.json",
		fixture.trustedKeys(), fixture.options(), filepath.Join(root, "stage"), statePath)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(staged) != string(fixture.artifact) {
		t.Fatal("staged artifact differs")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(result.Path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 != 0 {
			t.Fatal("staged artifact is executable before installation approval")
		}
	}
	current, err := os.ReadFile(working)
	if err != nil || string(current) != "current working version\n" {
		t.Fatal("staging changed the working installation")
	}
	if highest, err := readHighestAccepted(statePath); err != nil || highest != "0.4.0" {
		t.Fatalf("highest accepted version = %q, err=%v", highest, err)
	}
}

func TestUpdateManifestAbuseCasesFailClosed(t *testing.T) {
	tests := map[string]func(*updateFixture, *updateOptions){
		"unsigned envelope":  func(f *updateFixture, _ *updateOptions) { f.signatureBytes = []byte(`{}`) },
		"manifest tamper":    func(f *updateFixture, _ *updateOptions) { f.manifestBytes = append(f.manifestBytes, ' ') },
		"wrong platform":     func(f *updateFixture, _ *updateOptions) { f.manifest.Artifacts[0].Platform = "darwin"; f.resign() },
		"wrong architecture": func(f *updateFixture, _ *updateOptions) { f.manifest.Artifacts[0].Architecture = "arm64"; f.resign() },
		"protocol downgrade": func(f *updateFixture, _ *updateOptions) {
			f.manifest.ProtocolVersion = 1
			f.manifest.Artifacts[0].ProtocolVersion = 1
			f.resign()
		},
		"expired metadata": func(f *updateFixture, _ *updateOptions) {
			f.manifest.ExpiresAt = fixedUpdateTime.Add(-time.Minute).Format(time.RFC3339)
			f.resign()
		},
		"future metadata": func(f *updateFixture, _ *updateOptions) {
			f.manifest.PublishedAt = fixedUpdateTime.Add(time.Hour).Format(time.RFC3339)
			f.resign()
		},
		"excessive validity window": func(f *updateFixture, _ *updateOptions) {
			f.manifest.ExpiresAt = fixedUpdateTime.Add(maximumValidity + time.Hour).Format(time.RFC3339)
			f.resign()
		},
		"malformed compatibility floor": func(f *updateFixture, _ *updateOptions) {
			f.manifest.MinimumCompatibleVersion = "not-a-version"
			f.manifest.Artifacts[0].MinimumCompatibleVersion = "not-a-version"
			f.resign()
		},
		"lowered compatibility floor": func(f *updateFixture, _ *updateOptions) {
			f.manifest.MinimumCompatibleVersion = "0.1.0"
			f.manifest.Artifacts[0].MinimumCompatibleVersion = "0.1.0"
			f.resign()
		},
		"installed rollback": func(f *updateFixture, _ *updateOptions) {
			f.manifest.Version = "0.3.3"
			f.manifest.Tag = "v0.3.3"
			f.resign()
		},
		"accepted rollback": func(_ *updateFixture, o *updateOptions) { o.HighestVersion = "0.5.0" },
		"revoked release":   func(f *updateFixture, _ *updateOptions) { f.manifest.RevokedVersions = []string{"0.4.0"}; f.resign() },
		"duplicate revocation": func(f *updateFixture, _ *updateOptions) {
			f.manifest.RevokedVersions = []string{"0.2.0", "0.2.0"}
			f.resign()
		},
		"malformed revocation": func(f *updateFixture, _ *updateOptions) {
			f.manifest.RevokedVersions = []string{"v0.2.0"}
			f.resign()
		},
		"oversized artifact": func(f *updateFixture, _ *updateOptions) {
			f.manifest.Artifacts[0].Size = (1 << 20) + 1
			f.resign()
		},
		"unsafe artifact name": func(f *updateFixture, _ *updateOptions) {
			f.manifest.Artifacts[0].Name = "../unidrop"
			f.resign()
		},
		"unapproved URL": func(f *updateFixture, _ *updateOptions) {
			f.manifest.Artifacts[0].URL = "https://example.com/unidrop-linux-amd64"
			f.resign()
		},
		"wrong filename binding": func(f *updateFixture, _ *updateOptions) {
			f.manifest.Artifacts[0].URL = f.server.URL + "/other"
			f.resign()
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newUpdateFixture(t)
			options := fixture.options()
			mutate(fixture, &options)
			if _, err := fixture.stage(t, options); err == nil {
				t.Fatal("unsafe update was accepted")
			}
		})
	}
}

func TestOversizedMetadataIsRejectedWithoutChangingState(t *testing.T) {
	tests := map[string]func(*updateFixture){
		"manifest":  func(f *updateFixture) { f.manifestBytes = make([]byte, maxManifestBytes+1) },
		"signature": func(f *updateFixture) { f.signatureBytes = make([]byte, maxSignatureBytes+1) },
	}
	for name, configure := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newUpdateFixture(t)
			configure(fixture)
			root := t.TempDir()
			_, err := stageUpdate(context.Background(), fixture.server.Client(),
				fixture.server.URL+"/manifest.json", fixture.server.URL+"/manifest.sig.json",
				fixture.trustedKeys(), fixture.options(), filepath.Join(root, "stage"), filepath.Join(root, "state", "update.json"))
			if err == nil || !strings.Contains(err.Error(), "size limit") {
				t.Fatalf("oversized %s error = %v", name, err)
			}
			if entries, readErr := os.ReadDir(root); readErr != nil || len(entries) != 0 {
				t.Fatalf("oversized metadata changed local state: entries=%v err=%v", entries, readErr)
			}
		})
	}
}

func TestSignedDuplicateAndUnknownJSONFieldsAreRejected(t *testing.T) {
	fixture := newUpdateFixture(t)
	duplicate := strings.Replace(string(fixture.manifestBytes), `"schemaVersion": 1,`, `"schemaVersion": 1, "schemaVersion": 1,`, 1)
	fixture.setSignedManifest([]byte(duplicate), fixture.privateKey)
	if _, err := fixture.stage(t, fixture.options()); err == nil || !strings.Contains(err.Error(), "duplicate JSON field") {
		t.Fatalf("signed duplicate field error = %v", err)
	}

	fixture.resetManifest()
	unknown := strings.Replace(string(fixture.manifestBytes), `"schemaVersion": 1,`, `"schemaVersion": 1, "surprise": true,`, 1)
	fixture.setSignedManifest([]byte(unknown), fixture.privateKey)
	if _, err := fixture.stage(t, fixture.options()); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("signed unknown field error = %v", err)
	}
}

func TestUnknownSigningKeyIsRejected(t *testing.T) {
	fixture := newUpdateFixture(t)
	_, otherPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fixture.setSignedManifest(fixture.manifestBytes, otherPrivate)
	if _, err := fixture.stage(t, fixture.options()); err == nil || !strings.Contains(err.Error(), "untrusted key") {
		t.Fatalf("unknown key error = %v", err)
	}
}

func TestInitialMetadataHostMustBeApproved(t *testing.T) {
	fixture := newUpdateFixture(t)
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fixture.signatureBytes)
	}))
	defer other.Close()
	root := t.TempDir()
	_, err := stageUpdate(context.Background(), fixture.server.Client(),
		fixture.server.URL+"/manifest.json", other.URL+"/manifest.sig.json",
		fixture.trustedKeys(), fixture.options(), filepath.Join(root, "stage"), filepath.Join(root, "state"))
	if err == nil || !strings.Contains(err.Error(), "signature host is not approved") {
		t.Fatalf("metadata host error = %v", err)
	}
}

func TestCorruptAndPartialDownloadsAreRemoved(t *testing.T) {
	tests := map[string]func(*updateFixture){
		"corrupt": func(f *updateFixture) {
			f.artifactHandler = func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(strings.Repeat("x", len(f.artifact))))
			}
		},
		"partial": func(f *updateFixture) {
			f.artifactHandler = interruptedResponse(f.t, int64(len(f.artifact)), f.artifact[:5])
		},
	}
	for name, configure := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newUpdateFixture(t)
			configure(fixture)
			root := t.TempDir()
			stageDir := filepath.Join(root, "stage")
			statePath := filepath.Join(root, "state", "update.json")
			_, err := stageUpdate(context.Background(), fixture.server.Client(),
				fixture.server.URL+"/manifest.json", fixture.server.URL+"/manifest.sig.json",
				fixture.trustedKeys(), fixture.options(), stageDir, statePath)
			if err == nil {
				t.Fatal("bad download was accepted")
			}
			entries, readErr := os.ReadDir(stageDir)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("bad download left staged files: %v", entries)
			}
			if _, statErr := os.Stat(statePath); !os.IsNotExist(statErr) {
				t.Fatal("bad download advanced rollback state")
			}
		})
	}
}

func interruptedResponse(t *testing.T, declared int64, partial []byte) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("test server does not support hijacking")
			return
		}
		connection, buffer, err := hijacker.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = fmt.Fprintf(buffer, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nContent-Type: application/octet-stream\r\n\r\n", declared)
		_, _ = buffer.Write(partial)
		_ = buffer.Flush()
		_ = connection.Close()
	}
}

func TestRedirectToUnapprovedHostIsRejected(t *testing.T) {
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("should never be reached"))
	}))
	defer destination.Close()
	fixture := newUpdateFixture(t)
	fixture.artifactHandler = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/artifact", http.StatusFound)
	}
	if _, err := fixture.stage(t, fixture.options()); err == nil || !strings.Contains(err.Error(), "approved release hosts") {
		t.Fatalf("redirect error = %v", err)
	}
}

func TestOfflineCheckDoesNotAffectStartupState(t *testing.T) {
	fixture := newUpdateFixture(t)
	root := t.TempDir()
	client := fixture.server.Client()
	client.Timeout = 150 * time.Millisecond
	_, err := stageUpdate(context.Background(), client,
		"https://127.0.0.1:1/manifest.json", "https://127.0.0.1:1/manifest.sig.json",
		fixture.trustedKeys(), updateOptions{
			CurrentVersion: "0.3.3", Platform: "linux", Architecture: "amd64", Component: "core",
			ApprovedHosts: map[string]struct{}{"127.0.0.1:1": {}}, Now: fixedUpdateTime, MaximumArtifact: 1 << 20,
		}, filepath.Join(root, "stage"), filepath.Join(root, "state", "update.json"))
	if err == nil {
		t.Fatal("offline update unexpectedly succeeded")
	}
	if entries, readErr := os.ReadDir(root); readErr != nil || len(entries) != 0 {
		t.Fatalf("offline check changed local state: entries=%v err=%v", entries, readErr)
	}
}

func TestVersionParserIsStrictAndOrdered(t *testing.T) {
	for _, invalid := range []string{"1", "1.2", "1.2.3.4", "01.2.3", "1.2.x", "1.2.3-beta"} {
		if _, err := parseVersion(invalid); err == nil {
			t.Fatalf("accepted malformed version %q", invalid)
		}
	}
	if compareVersion("0.4.0", "0.3.9") != 1 || compareVersion("1.0.0", "1.0.0") != 0 || compareVersion("1.0.0", "2.0.0") != -1 {
		t.Fatal("semantic version ordering is wrong")
	}
}

func TestRollbackStateAdvancesAcrossMultipleWrites(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state", "update.json")
	if err := writeHighestAccepted(statePath, "0.4.0", fixedUpdateTime); err != nil {
		t.Fatal(err)
	}
	if err := writeHighestAccepted(statePath, "0.5.0", fixedUpdateTime.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if highest, err := readHighestAccepted(statePath); err != nil || highest != "0.5.0" {
		t.Fatalf("highest accepted version = %q, err=%v", highest, err)
	}
}

func TestStagingDirectoryRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require privileges on Windows")
	}
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realDirectory, link); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDirectory(link); err == nil {
		t.Fatal("symlink staging directory was accepted")
	}
}

func TestUpdateStateRejectsSymlinkAndPublicPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation and POSIX modes are not portable to Windows")
	}
	root := t.TempDir()
	realState := filepath.Join(root, "real-state.json")
	if err := os.WriteFile(realState, []byte(`{"highestAcceptedVersion":"0.4.0","acceptedAt":"2026-07-30T16:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "state-link.json")
	if err := os.Symlink(realState, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readHighestAccepted(link); err == nil {
		t.Fatal("symlink update state was accepted")
	}
	if err := os.Chmod(realState, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readHighestAccepted(realState); err == nil {
		t.Fatal("publicly readable update state was accepted")
	}
}
