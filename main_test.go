package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appversion "unidrop/internal/version"
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

func TestOldestSupportedProtocolContract(t *testing.T) {
	if protocolVersion != 2 || appversion.MinimumCompatibleVersion != "0.2.0" {
		t.Fatalf("unexpected compatibility floor: protocol=%d version=%q", protocolVersion, appversion.MinimumCompatibleVersion)
	}
	a := testApp(t, t.TempDir(), "Compatibility Peer")
	request := httptest.NewRequest(http.MethodGet, "/api/v1/info", nil)
	response := httptest.NewRecorder()
	a.publicMux().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("v2 info route returned %d: %s", response.Code, response.Body.String())
	}
	var info struct {
		Protocol                 int    `json:"protocol"`
		Version                  string `json:"version"`
		MinimumCompatibleVersion string `json:"minimum_compatible_version"`
	}
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if info.Protocol != 2 || info.Version != appversion.Current || info.MinimumCompatibleVersion != "0.2.0" {
		t.Fatalf("unexpected public compatibility metadata: %+v", info)
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
	done := make(chan struct{})
	go func() {
		a.localMux().ServeHTTP(response, request)
		close(done)
	}()
	offerID := waitForPendingOffer(t, b)
	action := httptest.NewRequest(http.MethodPost, "/api/offer-action", strings.NewReader(`{"id":"`+offerID+`","action":"accept"}`))
	action.Host = "127.0.0.1:43337"
	action.Header.Set("X-UniDrop-UI", "1")
	action.Header.Set("Content-Type", "application/json")
	actionResponse := httptest.NewRecorder()
	b.localMux().ServeHTTP(actionResponse, action)
	if actionResponse.Code != http.StatusOK {
		t.Fatalf("accept offer returned %d: %s", actionResponse.Code, actionResponse.Body.String())
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("send did not finish after receiver approval")
	}
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

func TestFriendlyTargetAndSendArgumentParsing(t *testing.T) {
	if got := cliDeviceName("Mini Brain"); got != "mini-brain.local" {
		t.Fatalf("friendly command name = %q", got)
	}
	target, paths, err := parseSendArguments([]string{"photo.jpg", "notes.txt", "Mini Brain.local"})
	if err != nil {
		t.Fatal(err)
	}
	if target != "Mini Brain.local" || len(paths) != 2 || paths[0] != "photo.jpg" || paths[1] != "notes.txt" {
		t.Fatalf("unexpected shorthand parse: target=%q paths=%q", target, paths)
	}
	target, paths, err = parseSendArguments([]string{"--to", "minibrain.local", "photo.jpg"})
	if err != nil || target != "minibrain.local" || len(paths) != 1 {
		t.Fatalf("unexpected --to parse: target=%q paths=%q err=%v", target, paths, err)
	}

	a := testApp(t, filepath.Join(t.TempDir(), "a"), "Mega Brain")
	b := testApp(t, filepath.Join(t.TempDir(), "b"), "Mini Brain")
	a.mu.Lock()
	a.discovered[b.identity.ID] = &DiscoveredPeer{
		ID: b.identity.ID, Name: b.identity.Name, OS: "test",
		Address: "127.0.0.1:43338", Fingerprint: b.fingerprint, LastSeen: time.Now(),
	}
	a.trusted[b.identity.ID] = &TrustedPeer{
		ID: b.identity.ID, Name: b.identity.Name, Fingerprint: b.fingerprint,
		OutgoingToken: strings.Repeat("a", 64), IncomingTokenHash: strings.Repeat("b", 64),
	}
	a.mu.Unlock()
	peer, err := a.resolveTarget("mini-brain.local")
	if err != nil {
		t.Fatal(err)
	}
	if peer.Trusted.ID != b.identity.ID {
		t.Fatalf("friendly target resolved to %q, want %q", peer.Trusted.ID, b.identity.ID)
	}
}

func TestAcceptedOfferBindsMetadataAndCanBeConsumedOnce(t *testing.T) {
	a := testApp(t, t.TempDir(), "Receiver")
	id := strings.Repeat("a", 32)
	a.offers[id] = &IncomingOffer{
		ID: id, SenderID: "sender", SenderName: "Sender", File: "photo.jpg", Bytes: 123,
		Status: "accepted", Created: time.Now().UTC().Format(time.RFC3339),
		Expires: time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
	}
	if err := a.beginReceiveOffer(id, "sender", "changed.jpg", 123); err == nil {
		t.Fatal("accepted offer allowed a changed filename")
	}
	if err := a.beginReceiveOffer(id, "sender", "photo.jpg", 124); err == nil {
		t.Fatal("accepted offer allowed a changed size")
	}
	if err := a.beginReceiveOffer(id, "sender", "photo.jpg", 123); err != nil {
		t.Fatalf("consume accepted offer: %v", err)
	}
	if err := a.beginReceiveOffer(id, "sender", "photo.jpg", 123); err == nil {
		t.Fatal("accepted offer was consumed twice")
	}
}

func TestReceivingOffDeclinesPendingOffers(t *testing.T) {
	a := testApp(t, t.TempDir(), "Receiver")
	id := strings.Repeat("b", 32)
	a.offers[id] = &IncomingOffer{
		ID: id, SenderID: "sender", File: "notes.txt", Status: "pending",
		Created: time.Now().UTC().Format(time.RFC3339), Expires: time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
	}
	request := httptest.NewRequest(http.MethodPost, "/api/receive-mode", strings.NewReader(`{"mode":"off"}`))
	request.Host = "127.0.0.1:43337"
	request.Header.Set("X-UniDrop-UI", "1")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	a.localMux().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("disable receiving returned %d: %s", response.Code, response.Body.String())
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.receiveMode != receiveModeOff || a.offers[id].Status != "declined" {
		t.Fatalf("receive off left mode=%q offer=%q", a.receiveMode, a.offers[id].Status)
	}
}

func TestCLIControlRequiresPrivateToken(t *testing.T) {
	a := testApp(t, t.TempDir(), "Local")
	request := httptest.NewRequest(http.MethodGet, "/api/cli/peers", nil)
	request.Host = "127.0.0.1:43337"
	response := httptest.NewRecorder()
	a.localMux().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated command bridge returned %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/cli/peers", nil)
	request.Host = "127.0.0.1:43337"
	request.Header.Set("Authorization", "Bearer "+a.controlToken)
	response = httptest.NewRecorder()
	a.localMux().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated command bridge returned %d: %s", response.Code, response.Body.String())
	}
}

func TestMenuSummaryReportsNearbyDevicesAndApprovals(t *testing.T) {
	a := testApp(t, t.TempDir(), "Menu Mac")
	a.discovery = "active"
	a.discovered[strings.Repeat("c", 32)] = &DiscoveredPeer{LastSeen: time.Now()}
	a.offers[strings.Repeat("d", 32)] = &IncomingOffer{
		Status: "pending", Expires: time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
	}
	request := httptest.NewRequest(http.MethodGet, "/api/summary", nil)
	response := httptest.NewRecorder()
	a.localMux().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("menu summary returned %d: %s", response.Code, response.Body.String())
	}
	var summary struct {
		Nearby    int    `json:"nearby"`
		Pending   int    `json:"pending"`
		Discovery string `json:"discovery_status"`
	}
	if err := json.NewDecoder(response.Body).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	if summary.Nearby != 1 || summary.Pending != 1 || summary.Discovery != "active" {
		t.Fatalf("unexpected menu summary: %+v", summary)
	}
}

func TestManualPeerAddressPersistsAndIsActivelyRechecked(t *testing.T) {
	root := t.TempDir()
	a := testApp(t, filepath.Join(root, "a"), "Linux sender")
	b := testApp(t, filepath.Join(root, "b"), "Mac receiver")
	startTestApp(t, b)
	address := b.publicListen.Addr().String()

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:43337/api/add-peer", strings.NewReader(`{"address":"`+address+`"}`))
	request.Header.Set("X-UniDrop-UI", "1")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	a.localMux().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("add manual peer returned %d: %s", response.Code, response.Body.String())
	}

	stateBytes, err := os.ReadFile(a.statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state savedState
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.ManualAddresses) != 1 || state.ManualAddresses[0] != address {
		t.Fatalf("manual address was not persisted: %q", state.ManualAddresses)
	}

	a.mu.Lock()
	peer := a.discovered[b.identity.ID]
	peer.LastSeen = time.Now().Add(-peerLifetime - time.Second)
	a.mu.Unlock()
	if peerIsOnline(peer, time.Now()) {
		t.Fatal("stale manual peer should be offline before its direct probe")
	}
	a.probeManualPeers(context.Background())

	a.mu.RLock()
	refreshed := a.discovered[b.identity.ID]
	_, persisted := a.manualPeers[address]
	a.mu.RUnlock()
	if !persisted || !refreshed.Manual || !peerIsOnline(refreshed, time.Now()) {
		t.Fatalf("manual peer was not refreshed: persisted=%v peer=%+v", persisted, refreshed)
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

func TestAuthorizedTrayShutdown(t *testing.T) {
	token := strings.Repeat("a", 64)
	app := &App{controlToken: token, shutdown: make(chan struct{})}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:43337/api/cli/shutdown", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	app.localMux().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("shutdown returned %d: %s", recorder.Code, recorder.Body.String())
	}
	select {
	case <-app.shutdown:
	default:
		t.Fatal("authorized shutdown did not stop the app")
	}

	unauthorized := &App{controlToken: token, shutdown: make(chan struct{})}
	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:43337/api/cli/shutdown", nil)
	recorder = httptest.NewRecorder()
	unauthorized.localMux().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized shutdown returned %d", recorder.Code)
	}
	select {
	case <-unauthorized.shutdown:
		t.Fatal("unauthorized shutdown stopped the app")
	default:
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

func waitForPendingOffer(t *testing.T, app *App) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		app.mu.RLock()
		for id, offer := range app.offers {
			if offer.Status == "pending" {
				app.mu.RUnlock()
				return id
			}
		}
		app.mu.RUnlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("receiver did not publish a pending offer")
	return ""
}
