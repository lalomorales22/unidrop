package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSanitizeFilename(t *testing.T) {
	tests := map[string]string{
		"photo.jpg":                     "photo.jpg",
		"../../private.txt":             "private.txt",
		`..\..\windows.ini`:             "windows.ini",
		" report:final.pdf ":            "reportfinal.pdf",
		"CON.txt":                       "_CON.txt",
		"\x00\x01":                      "",
		strings.Repeat("a", 300) + ".x": strings.Repeat("a", 220),
	}
	for input, want := range tests {
		if got := sanitizeFilename(input); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPairProofBindsEveryIdentityValue(t *testing.T) {
	request := pairRequest{
		SenderID: "sender-device", SenderFingerprint: strings.Repeat("a", 64),
		Nonce: strings.Repeat("b", 32), ReturnToken: strings.Repeat("c", 64),
	}
	fingerprint := strings.Repeat("d", 64)
	proof := pairProof("abcd-1234-5678-90ef", fingerprint, request)
	if proof != pairProof("abcd1234567890ef", fingerprint, request) {
		t.Fatal("formatted and unformatted pairing keys should be equivalent")
	}
	changed := request
	changed.ReturnToken = strings.Repeat("e", 64)
	if proof == pairProof("abcd-1234-5678-90ef", fingerprint, changed) {
		t.Fatal("proof did not bind the return token")
	}
	if proof == pairProof("abcd-1234-5678-90ef", strings.Repeat("f", 64), request) {
		t.Fatal("proof did not bind the recipient certificate")
	}
}

func TestPairAndTransferEndToEnd(t *testing.T) {
	root := t.TempDir()
	a := testApp(t, filepath.Join(root, "a"), "Alice")
	b := testApp(t, filepath.Join(root, "b"), "Bob")
	startTestApp(t, a)
	startTestApp(t, b)

	a.mu.Lock()
	a.discovered[b.identity.ID] = &DiscoveredPeer{
		ID: b.identity.ID, Name: b.identity.Name, OS: "test",
		Address: b.publicListen.Addr().String(), Fingerprint: b.fingerprint, LastSeen: time.Now(),
	}
	a.mu.Unlock()
	originalCode := b.pairingCode
	if err := a.pairWithPeer(b.identity.ID, originalCode); err != nil {
		t.Fatalf("pair devices: %v", err)
	}
	if b.pairingCode == originalCode {
		t.Fatal("receiver pairing key did not rotate after use")
	}
	if a.trusted[b.identity.ID] == nil || b.trusted[a.identity.ID] == nil {
		t.Fatal("pairing should establish mutual trust")
	}

	payload := []byte("hello securely from UniDrop\n")
	request := httptest.NewRequest(http.MethodPost, "/api/send?peer="+b.identity.ID+"&filename=../hello.txt", bytes.NewReader(payload))
	request.Host = "127.0.0.1:43337"
	request.Header.Set("X-UniDrop-UI", "1")
	request.Header.Set("Content-Type", "application/octet-stream")
	response := httptest.NewRecorder()
	a.localMux().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("send returned %d: %s", response.Code, response.Body.String())
	}
	received, err := os.ReadFile(filepath.Join(b.downloadDir, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(received, payload) {
		t.Fatalf("received bytes differ: %q", received)
	}
	if len(a.transfers) != 1 || a.transfers[0].Status != "complete" {
		t.Fatal("sender transfer was not marked complete")
	}
	if len(b.transfers) != 1 || b.transfers[0].Status != "complete" {
		t.Fatal("receiver transfer was not marked complete")
	}
}

func TestReceiveRejectsUnpairedSender(t *testing.T) {
	a := testApp(t, t.TempDir(), "Receiver")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/files?name=bad.txt", strings.NewReader("bad"))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
	request.Header.Set("X-UniDrop-Sender-ID", "unknown-device")
	response := httptest.NewRecorder()
	a.publicMux().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unpaired receive returned %d", response.Code)
	}
}

func testApp(t *testing.T, root, name string) *App {
	t.Helper()
	config := filepath.Join(root, "config")
	downloads := filepath.Join(root, "downloads")
	oldConfig, hadConfig := os.LookupEnv("UNIDROP_CONFIG_DIR")
	oldDownloads, hadDownloads := os.LookupEnv("UNIDROP_DOWNLOAD_DIR")
	if err := os.Setenv("UNIDROP_CONFIG_DIR", config); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("UNIDROP_DOWNLOAD_DIR", downloads); err != nil {
		t.Fatal(err)
	}
	a, err := newApp("127.0.0.1:0")
	if hadConfig {
		_ = os.Setenv("UNIDROP_CONFIG_DIR", oldConfig)
	} else {
		_ = os.Unsetenv("UNIDROP_CONFIG_DIR")
	}
	if hadDownloads {
		_ = os.Setenv("UNIDROP_DOWNLOAD_DIR", oldDownloads)
	} else {
		_ = os.Unsetenv("UNIDROP_DOWNLOAD_DIR")
	}
	if err != nil {
		t.Fatal(err)
	}
	a.identity.Name = name
	return a
}

func startTestApp(t *testing.T, app *App) {
	t.Helper()
	if err := app.start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = app.uiServer.Shutdown(ctx)
		_ = app.publicServer.Shutdown(ctx)
	})
}
