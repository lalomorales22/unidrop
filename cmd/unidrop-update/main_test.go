package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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

func TestUpdatePreferenceDefaultsAndRoundTripsPrivately(t *testing.T) {
	preferencePath := filepath.Join(t.TempDir(), "config", "update-preference.json")
	if mode, err := readUpdatePreference(preferencePath); err != nil || mode != preferenceNotifyOnly {
		t.Fatalf("default preference = %q, err=%v", mode, err)
	}
	for index, mode := range []string{preferenceAutomatic, preferenceDisabled, preferenceNotifyOnly} {
		if err := writeUpdatePreference(preferencePath, mode, fixedUpdateTime.Add(time.Duration(index)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		selected, err := readUpdatePreference(preferencePath)
		if err != nil || selected != mode {
			t.Fatalf("round-trip preference = %q, want %q, err=%v", selected, mode, err)
		}
		if runtime.GOOS != "windows" {
			info, statErr := os.Stat(preferencePath)
			if statErr != nil {
				t.Fatal(statErr)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("preference permissions = %o", info.Mode().Perm())
			}
		}
		if _, statErr := os.Lstat(preferencePath + ".previous"); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("completed preference write left previous generation: %v", statErr)
		}
	}
}

func TestInvalidUpdatePreferenceDoesNotReplacePriorValue(t *testing.T) {
	preferencePath := filepath.Join(t.TempDir(), "config", "update-preference.json")
	if err := writeUpdatePreference(preferencePath, preferenceDisabled, fixedUpdateTime); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(preferencePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeUpdatePreference(preferencePath, "install-everything", fixedUpdateTime.Add(time.Minute)); err == nil {
		t.Fatal("invalid preference was accepted")
	}
	after, err := os.ReadFile(preferencePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("invalid preference changed the prior value")
	}
}

func TestUpdatePreferenceStateAbuseCasesFailClosed(t *testing.T) {
	tests := map[string][]byte{
		"unknown field":     []byte(`{"schemaVersion":1,"mode":"automatic","updatedAt":"2026-07-30T16:00:00Z","surprise":true}`),
		"duplicate field":   []byte(`{"schemaVersion":1,"mode":"automatic","mode":"disabled","updatedAt":"2026-07-30T16:00:00Z"}`),
		"invalid mode":      []byte(`{"schemaVersion":1,"mode":"unattended","updatedAt":"2026-07-30T16:00:00Z"}`),
		"invalid timestamp": []byte(`{"schemaVersion":1,"mode":"disabled","updatedAt":"tomorrow"}`),
		"oversized":         []byte(strings.Repeat("x", int(maxLocalStateBytes)+1)),
	}
	for name, encoded := range tests {
		t.Run(name, func(t *testing.T) {
			preferencePath := filepath.Join(t.TempDir(), "update-preference.json")
			if err := os.WriteFile(preferencePath, encoded, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readUpdatePreference(preferencePath); err == nil {
				t.Fatal("unsafe preference state was accepted")
			}
		})
	}
}

func TestInterruptedPreferenceWriteRestoresPreviousGeneration(t *testing.T) {
	root := t.TempDir()
	preferencePath := filepath.Join(root, "update-preference.json")
	previous := updatePreferenceFile{
		SchemaVersion: 1,
		Mode:          preferenceAutomatic,
		UpdatedAt:     fixedUpdateTime.Format(time.RFC3339),
	}
	writeTestJSON(t, preferencePath+".previous", previous)
	mode, err := readUpdatePreference(preferencePath)
	if err != nil || mode != preferenceAutomatic {
		t.Fatalf("recovered preference = %q, err=%v", mode, err)
	}
	if _, err := os.Stat(preferencePath); err != nil {
		t.Fatal("previous preference generation was not restored", err)
	}
	if _, err := os.Lstat(preferencePath + ".previous"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previous generation remains after recovery: %v", err)
	}
}

func TestUpdatePreferenceRejectsSymlinkAndPublicPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation and POSIX modes are not portable to Windows")
	}
	root := t.TempDir()
	realPreference := filepath.Join(root, "real-preference.json")
	writeTestJSON(t, realPreference, updatePreferenceFile{
		SchemaVersion: 1, Mode: preferenceNotifyOnly, UpdatedAt: fixedUpdateTime.Format(time.RFC3339),
	})
	link := filepath.Join(root, "preference-link.json")
	if err := os.Symlink(realPreference, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readUpdatePreference(link); err == nil {
		t.Fatal("symlink preference was accepted")
	}
	if err := os.Chmod(realPreference, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readUpdatePreference(realPreference); err == nil {
		t.Fatal("publicly readable preference was accepted")
	}
}

func TestVerifiedReplacementKeepsLastWorkingBackup(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "unidrop")
	staged := filepath.Join(root, "staged-update")
	journal := filepath.Join(root, "state", "replacement.json")
	oldBytes := []byte("working release\n")
	newBytes := []byte("verified update\n")
	writeTestFile(t, target, oldBytes, 0o755)
	writeTestFile(t, staged, newBytes, 0o600)
	newSize, newDigest := testArtifactMetadata(newBytes)
	backup, err := replaceVerifiedArtifact(staged, target, journal, newSize, newDigest, func(installed string) error {
		actual, readErr := os.ReadFile(installed)
		if readErr != nil {
			return readErr
		}
		if string(actual) != string(newBytes) {
			return errors.New("health check saw unexpected bytes")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if backup != target+".last-working" {
		t.Fatalf("backup path = %q", backup)
	}
	assertFileBytes(t, target, newBytes)
	assertFileBytes(t, backup, oldBytes)
	assertReplacementStateCleared(t, target, journal)
}

func TestFailedReplacementHealthCheckRollsBackAndPreservesStableBackup(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "unidrop")
	staged := filepath.Join(root, "staged-update")
	journal := filepath.Join(root, "state", "replacement.json")
	oldBytes := []byte("working release\n")
	olderBytes := []byte("older fallback\n")
	newBytes := []byte("bad update\n")
	writeTestFile(t, target, oldBytes, 0o755)
	writeTestFile(t, target+".last-working", olderBytes, 0o755)
	writeTestFile(t, staged, newBytes, 0o600)
	newSize, newDigest := testArtifactMetadata(newBytes)
	_, err := replaceVerifiedArtifact(staged, target, journal, newSize, newDigest, func(string) error {
		return errors.New("process did not become healthy")
	})
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("health-check error = %v", err)
	}
	assertFileBytes(t, target, oldBytes)
	assertFileBytes(t, target+".last-working", olderBytes)
	assertReplacementStateCleared(t, target, journal)
}

func TestReplacementRejectsChangedStagedArtifactWithoutTouchingInstallation(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "unidrop")
	staged := filepath.Join(root, "staged-update")
	journal := filepath.Join(root, "state", "replacement.json")
	oldBytes := []byte("working release\n")
	signedBytes := []byte("signed update\n")
	writeTestFile(t, target, oldBytes, 0o755)
	writeTestFile(t, staged, []byte("tampered update\n"), 0o600)
	signedSize, signedDigest := testArtifactMetadata(signedBytes)
	if _, err := replaceVerifiedArtifact(staged, target, journal, signedSize, signedDigest, func(string) error { return nil }); err == nil {
		t.Fatal("changed staged artifact was accepted")
	}
	assertFileBytes(t, target, oldBytes)
	assertAbsent(t, journal)
	assertAbsent(t, target+".previous.pending")
	assertAbsent(t, target+".last-working")
}

func TestReplacementRejectsHardLinkedStagedAndInstalledPaths(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "unidrop")
	staged := filepath.Join(root, "staged-update")
	journal := filepath.Join(root, "state", "replacement.json")
	installedBytes := []byte("working release\n")
	writeTestFile(t, target, installedBytes, 0o755)
	if err := os.Link(target, staged); err != nil {
		t.Skipf("hard links are unavailable: %v", err)
	}
	size, digest := testArtifactMetadata(installedBytes)
	if _, err := replaceVerifiedArtifact(staged, target, journal, size, digest, func(string) error { return nil }); err == nil {
		t.Fatal("hard-linked staged and installed paths were accepted")
	}
	assertFileBytes(t, target, installedBytes)
	assertFileBytes(t, staged, installedBytes)
	assertAbsent(t, journal)
}

func TestInterruptedReplacementRecoveryIsPhaseAwareAndIdempotent(t *testing.T) {
	tests := []struct {
		phase          string
		targetBytes    []byte
		pendingBytes   []byte
		candidateBytes []byte
		wantTarget     []byte
		wantBackup     []byte
	}{
		{phase: replacementPrepared, targetBytes: []byte("old\n"), candidateBytes: []byte("new\n"), wantTarget: []byte("old\n")},
		{phase: replacementTargetMoved, pendingBytes: []byte("old\n"), candidateBytes: []byte("new\n"), wantTarget: []byte("old\n")},
		{phase: replacementReplaced, targetBytes: []byte("new\n"), pendingBytes: []byte("old\n"), wantTarget: []byte("old\n")},
		{phase: replacementHealthy, targetBytes: []byte("new\n"), pendingBytes: []byte("old\n"), wantTarget: []byte("new\n"), wantBackup: []byte("old\n")},
	}
	for _, test := range tests {
		t.Run(test.phase, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "unidrop")
			candidate := filepath.Join(root, ".unidrop-candidate-test")
			pending := target + ".previous.pending"
			backup := target + ".last-working"
			journalPath := filepath.Join(root, "state", "replacement.json")
			if test.targetBytes != nil {
				writeTestFile(t, target, test.targetBytes, 0o755)
			}
			if test.pendingBytes != nil {
				writeTestFile(t, pending, test.pendingBytes, 0o755)
			}
			if test.candidateBytes != nil {
				writeTestFile(t, candidate, test.candidateBytes, 0o755)
			}
			oldSize, oldDigest := testArtifactMetadata([]byte("old\n"))
			newSize, newDigest := testArtifactMetadata([]byte("new\n"))
			journal := replacementJournal{
				SchemaVersion: 1, Phase: test.phase,
				TargetPath: target, CandidatePath: candidate, PendingBackupPath: pending, BackupPath: backup,
				PreviousSize: oldSize, PreviousSHA256: oldDigest, CandidateSize: newSize, CandidateSHA256: newDigest,
			}
			if err := writeReplacementJournal(journalPath, &journal); err != nil {
				t.Fatal(err)
			}
			if err := recoverReplacement(journalPath); err != nil {
				t.Fatal(err)
			}
			if err := recoverReplacement(journalPath); err != nil {
				t.Fatalf("second recovery was not idempotent: %v", err)
			}
			assertFileBytes(t, target, test.wantTarget)
			if test.wantBackup == nil {
				assertAbsent(t, backup)
			} else {
				assertFileBytes(t, backup, test.wantBackup)
			}
			assertReplacementStateCleared(t, target, journalPath)
		})
	}
}

func TestReplacementRecoveryRejectsTamperedStateWithoutTouchingInstalledFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "unidrop")
	journalPath := filepath.Join(root, "state", "replacement.json")
	oldBytes := []byte("working release\n")
	newBytes := []byte("new release\n")
	writeTestFile(t, target, oldBytes, 0o755)
	oldSize, oldDigest := testArtifactMetadata(oldBytes)
	newSize, newDigest := testArtifactMetadata(newBytes)
	journal := replacementJournal{
		SchemaVersion: 1, Phase: replacementPrepared,
		TargetPath: target, CandidatePath: journalPath, PendingBackupPath: target + ".previous.pending", BackupPath: target + ".last-working",
		PreviousSize: oldSize, PreviousSHA256: oldDigest, CandidateSize: newSize, CandidateSHA256: newDigest,
		UpdatedAt: fixedUpdateTime.Format(time.RFC3339Nano),
	}
	writeTestJSON(t, journalPath, journal)
	if err := recoverReplacement(journalPath); err == nil {
		t.Fatal("colliding journal state was accepted")
	}
	assertFileBytes(t, target, oldBytes)
}

func TestReplacementRecoveryRejectsChangedPendingBackupBeforeRemovingTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "unidrop")
	pending := target + ".previous.pending"
	candidate := filepath.Join(root, ".unidrop-candidate-test")
	journalPath := filepath.Join(root, "state", "replacement.json")
	oldBytes := []byte("old release\n")
	newBytes := []byte("new release\n")
	writeTestFile(t, target, newBytes, 0o755)
	writeTestFile(t, pending, []byte("tampered backup\n"), 0o755)
	oldSize, oldDigest := testArtifactMetadata(oldBytes)
	newSize, newDigest := testArtifactMetadata(newBytes)
	journal := replacementJournal{
		SchemaVersion: 1, Phase: replacementReplaced,
		TargetPath: target, CandidatePath: candidate, PendingBackupPath: pending, BackupPath: target + ".last-working",
		PreviousSize: oldSize, PreviousSHA256: oldDigest, CandidateSize: newSize, CandidateSHA256: newDigest,
	}
	if err := writeReplacementJournal(journalPath, &journal); err != nil {
		t.Fatal(err)
	}
	if err := recoverReplacement(journalPath); err == nil {
		t.Fatal("changed pending backup was accepted")
	}
	assertFileBytes(t, target, newBytes)
	assertFileBytes(t, pending, []byte("tampered backup\n"))
}

func TestReplacementJournalRejectsSymlinkAndPublicPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation and POSIX modes are not portable to Windows")
	}
	root := t.TempDir()
	realJournal := filepath.Join(root, "real-journal.json")
	writeTestJSON(t, realJournal, map[string]any{"not": "trusted"})
	link := filepath.Join(root, "journal-link.json")
	if err := os.Symlink(realJournal, link); err != nil {
		t.Fatal(err)
	}
	if err := recoverReplacement(link); err == nil {
		t.Fatal("symlink replacement journal was accepted")
	}
	if err := os.Chmod(realJournal, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := recoverReplacement(realJournal); err == nil {
		t.Fatal("publicly readable replacement journal was accepted")
	}
}

func writeTestFile(t *testing.T, filePath string, contents []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, contents, mode); err != nil {
		t.Fatal(err)
	}
}

func writeTestJSON(t *testing.T, filePath string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filePath, append(encoded, '\n'), 0o600)
}

func testArtifactMetadata(contents []byte) (int64, string) {
	digest := sha256.Sum256(contents)
	return int64(len(contents)), hex.EncodeToString(digest[:])
}

func assertFileBytes(t *testing.T, filePath string, want []byte) {
	t.Helper()
	actual, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(want) {
		t.Fatalf("%s = %q, want %q", filePath, actual, want)
	}
}

func assertAbsent(t *testing.T, filePath string) {
	t.Helper()
	if _, err := os.Lstat(filePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be absent, err=%v", filePath, err)
	}
}

func assertReplacementStateCleared(t *testing.T, target, journal string) {
	t.Helper()
	assertAbsent(t, target+".previous.pending")
	assertAbsent(t, journal)
	assertAbsent(t, journal+".previous")
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".unidrop-candidate-") {
			t.Fatalf("replacement candidate remains: %s", entry.Name())
		}
	}
}
