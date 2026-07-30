package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appversion "xendfile/internal/version"
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
	mutations := map[string]func(*pairRequest){
		"sender ID":          func(r *pairRequest) { r.SenderID = "other-device" },
		"sender certificate": func(r *pairRequest) { r.SenderFingerprint = strings.Repeat("e", 64) },
		"nonce":              func(r *pairRequest) { r.Nonce = strings.Repeat("f", 32) },
		"return token":       func(r *pairRequest) { r.ReturnToken = strings.Repeat("0", 64) },
	}
	for name, mutate := range mutations {
		changed := request
		mutate(&changed)
		if proof == pairProof("abcd-1234-5678-90ef", fingerprint, changed) {
			t.Fatalf("proof did not bind the %s", name)
		}
	}
	if proof == pairProof("abcd-1234-5678-90ef", strings.Repeat("f", 64), request) {
		t.Fatal("proof did not bind the recipient certificate")
	}
}

func TestOldestSupportedProtocolContract(t *testing.T) {
	if protocolVersion != 2 || appversion.MinimumCompatibleVersion != "0.2.0" {
		t.Fatalf("unexpected compatibility floor: protocol=%d version=%q", protocolVersion, appversion.MinimumCompatibleVersion)
	}
	if legacyUIHeader != "X-UniDrop-UI" || legacySenderID != "X-UniDrop-Sender-ID" || legacyPairDomain != "unidrop-pair-v1" {
		t.Fatal("the v0.3 wire identifiers changed during the Xendfile rename")
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

func TestLegacyEnvironmentOverridesRemainCompatible(t *testing.T) {
	legacyConfig := filepath.Join(t.TempDir(), "legacy-config")
	t.Setenv("XENDFILE_CONFIG_DIR", "")
	t.Setenv("UNIDROP_CONFIG_DIR", legacyConfig)
	got, err := configDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if got != legacyConfig {
		t.Fatalf("legacy config override = %q, want %q", got, legacyConfig)
	}

	currentConfig := filepath.Join(t.TempDir(), "current-config")
	t.Setenv("XENDFILE_CONFIG_DIR", currentConfig)
	if got, err = configDirectory(); err != nil || got != currentConfig {
		t.Fatalf("current config override = %q, %v; want %q", got, err, currentConfig)
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

	payload := []byte("hello securely from Xendfile\n")
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

func TestLocalMutationGuardsRejectBrowserAndRemoteAbuse(t *testing.T) {
	a := testApp(t, t.TempDir(), "Local")
	tests := []struct {
		name   string
		method string
		host   string
		header string
		body   string
		want   int
	}{
		{name: "missing UI header", method: http.MethodPost, host: "127.0.0.1:43337", body: `{"mode":"off"}`, want: http.StatusForbidden},
		{name: "non-loopback host", method: http.MethodPost, host: "attacker.example", header: "1", body: `{"mode":"off"}`, want: http.StatusForbidden},
		{name: "wrong method", method: http.MethodGet, host: "127.0.0.1:43337", header: "1", want: http.StatusMethodNotAllowed},
		{name: "unknown JSON field", method: http.MethodPost, host: "127.0.0.1:43337", header: "1", body: `{"mode":"off","admin":true}`, want: http.StatusBadRequest},
		{name: "trailing JSON object", method: http.MethodPost, host: "127.0.0.1:43337", header: "1", body: `{"mode":"off"}{"mode":"trusted"}`, want: http.StatusBadRequest},
		{name: "oversized JSON", method: http.MethodPost, host: "127.0.0.1:43337", header: "1", body: `{"mode":"` + strings.Repeat("a", 4096) + `"}`, want: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "http://"+test.host+"/api/receive-mode", strings.NewReader(test.body))
			request.Host = test.host
			if test.header != "" {
				request.Header.Set("X-UniDrop-UI", test.header)
			}
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			a.localMux().ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.want, response.Body.String())
			}
			if a.receiveMode != receiveModeAsk {
				t.Fatalf("rejected request changed receive mode to %q", a.receiveMode)
			}
		})
	}
}

func TestCommandBridgeRejectsWrongHostTokenAndMethod(t *testing.T) {
	a := testApp(t, t.TempDir(), "Local")
	tests := []struct {
		name   string
		method string
		host   string
		token  string
		want   int
	}{
		{name: "remote host", method: http.MethodPost, host: "192.0.2.10:43337", token: a.controlToken, want: http.StatusForbidden},
		{name: "wrong token", method: http.MethodPost, host: "127.0.0.1:43337", token: strings.Repeat("0", 64), want: http.StatusUnauthorized},
		{name: "wrong method", method: http.MethodGet, host: "127.0.0.1:43337", token: a.controlToken, want: http.StatusMethodNotAllowed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "http://"+test.host+"/api/cli/shutdown", nil)
			request.Host = test.host
			request.Header.Set("Authorization", "Bearer "+test.token)
			response := httptest.NewRecorder()
			a.localMux().ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.want, response.Body.String())
			}
			select {
			case <-a.shutdown:
				t.Fatal("rejected command stopped the app")
			default:
			}
		})
	}
}

func TestPairingRejectsTamperingReplayAndBruteForce(t *testing.T) {
	receiver := testApp(t, filepath.Join(t.TempDir(), "receiver"), "Receiver")
	request := pairRequest{
		SenderID: "sender-device", SenderName: "Sender",
		SenderFingerprint: strings.Repeat("a", 64), Nonce: strings.Repeat("b", 32),
		ReturnToken: strings.Repeat("c", 64),
	}
	request.Proof = pairProof(receiver.pairingCode, receiver.fingerprint, request)

	tampered := request
	tampered.ReturnToken = strings.Repeat("d", 64)
	response := servePublicPair(receiver, tampered, "192.0.2.10:4000")
	if response.Code != http.StatusUnauthorized || len(receiver.trusted) != 0 {
		t.Fatalf("tampered pairing returned %d and trust=%d", response.Code, len(receiver.trusted))
	}

	originalCode := receiver.pairingCode
	response = servePublicPair(receiver, request, "192.0.2.11:4000")
	if response.Code != http.StatusOK {
		t.Fatalf("valid pairing returned %d: %s", response.Code, response.Body.String())
	}
	if receiver.pairingCode == originalCode {
		t.Fatal("successful pairing did not rotate its one-time code")
	}
	var paired pairResponse
	if err := json.NewDecoder(response.Body).Decode(&paired); err != nil {
		t.Fatal(err)
	}
	trusted := receiver.trusted[request.SenderID]
	if trusted == nil || trusted.IncomingTokenHash != hashToken(paired.Token) || trusted.IncomingTokenHash == paired.Token {
		t.Fatal("receiver did not retain only the returned token hash")
	}
	if len(paired.Token) != 64 || trusted.OutgoingToken != request.ReturnToken {
		t.Fatal("pairing did not establish independent 256-bit directional tokens")
	}

	response = servePublicPair(receiver, request, "192.0.2.12:4000")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("replayed one-time pairing proof returned %d", response.Code)
	}

	limited := testApp(t, filepath.Join(t.TempDir(), "limited"), "Limited")
	bad := request
	bad.Proof = strings.Repeat("0", 64)
	for attempt := 1; attempt <= 9; attempt++ {
		response = servePublicPair(limited, bad, "198.51.100.8:4000")
		want := http.StatusUnauthorized
		if attempt == 9 {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf("attempt %d returned %d, want %d", attempt, response.Code, want)
		}
	}
}

func TestOfferAuthorizationIsolationAndFloodLimits(t *testing.T) {
	a := testApp(t, t.TempDir(), "Receiver")
	firstID, firstToken := authorizeTestPeer(a, "first-device", "First")
	secondID, secondToken := authorizeTestPeer(a, "second-device", "Second")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/offers", strings.NewReader(`{"file":"forged.txt","bytes":1}`))
	request.Header.Set("X-UniDrop-Sender-ID", firstID)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("0", 64))
	response := httptest.NewRecorder()
	a.publicMux().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong peer token created an offer with %d", response.Code)
	}

	offerID := strings.Repeat("e", 32)
	a.offers[offerID] = &IncomingOffer{
		ID: offerID, SenderID: firstID, SenderName: "First", File: "photo.jpg", Bytes: 4,
		Status: "accepted", Created: time.Now().UTC().Format(time.RFC3339),
		Expires: time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/offers/"+offerID, nil)
	request.Header.Set("X-UniDrop-Sender-ID", secondID)
	request.Header.Set("Authorization", "Bearer "+secondToken)
	response = httptest.NewRecorder()
	a.publicMux().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("other peer read offer status with %d", response.Code)
	}
	if err := a.beginReceiveOffer(offerID, secondID, "photo.jpg", 4); err == nil {
		t.Fatal("other peer consumed an accepted offer")
	}

	a.offers = make(map[string]*IncomingOffer)
	for index := 0; index < 10; index++ {
		id := randomHex(16)
		a.offers[id] = &IncomingOffer{
			ID: id, SenderID: firstID, File: "pending.txt", Bytes: 1, Status: "pending",
			Created: time.Now().UTC().Format(time.RFC3339), Expires: time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
		}
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/offers", strings.NewReader(`{"file":"eleventh.txt","bytes":1}`))
	request.Header.Set("X-UniDrop-Sender-ID", firstID)
	request.Header.Set("Authorization", "Bearer "+firstToken)
	response = httptest.NewRecorder()
	a.publicMux().ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("eleventh pending offer returned %d: %s", response.Code, response.Body.String())
	}
}

func TestFailedAndOversizedReceivesLeaveNoFiles(t *testing.T) {
	a := testApp(t, t.TempDir(), "Receiver")
	senderID, token := authorizeTestPeer(a, "sender-device", "Sender")
	offerID := strings.Repeat("f", 32)
	a.offers[offerID] = &IncomingOffer{
		ID: offerID, SenderID: senderID, SenderName: "Sender", File: "partial.txt", Bytes: 10,
		Status: "accepted", Created: time.Now().UTC().Format(time.RFC3339),
		Expires: time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/files?name=partial.txt&offer="+offerID, strings.NewReader("short"))
	request.ContentLength = 10
	authorizeTestRequest(request, senderID, token)
	response := httptest.NewRecorder()
	a.publicMux().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || a.offers[offerID].Status != "failed" {
		t.Fatalf("partial receive returned %d with offer %q", response.Code, a.offers[offerID].Status)
	}
	assertDirectoryEmpty(t, a.downloadDir)

	a.maxBytes = 4
	request = httptest.NewRequest(http.MethodPost, "/api/v1/files?name=large.txt&offer="+strings.Repeat("0", 32), strings.NewReader("large"))
	request.ContentLength = 5
	authorizeTestRequest(request, senderID, token)
	response = httptest.NewRecorder()
	a.publicMux().ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized receive returned %d", response.Code)
	}
	assertDirectoryEmpty(t, a.downloadDir)

	request = httptest.NewRequest(http.MethodPost, "/api/v1/files?name=%00&offer="+strings.Repeat("0", 32), strings.NewReader("x"))
	request.ContentLength = 1
	authorizeTestRequest(request, senderID, token)
	response = httptest.NewRecorder()
	a.publicMux().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unsafe filename returned %d", response.Code)
	}
	assertDirectoryEmpty(t, a.downloadDir)
}

func TestTLS13PinningAndProxyIsolation(t *testing.T) {
	a := testApp(t, t.TempDir(), "Peer")
	startTestApp(t, a)
	endpoint := "https://" + a.publicListen.Addr().String() + "/api/v1/info"

	response, err := pinnedClient(a.fingerprint).Get(endpoint)
	if err != nil {
		t.Fatalf("correct certificate pin failed: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("pinned request returned %d", response.StatusCode)
	}
	if _, err := pinnedClient(strings.Repeat("0", 64)).Get(endpoint); err == nil {
		t.Fatal("wrong certificate pin was accepted")
	}

	tls12Only := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, // test client intentionally probes the server floor
		MaxVersion:         tls.VersionTLS12,
	}}}
	if _, err := tls12Only.Get(endpoint); err == nil {
		t.Fatal("TLS 1.2 connection was accepted")
	}

	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	if transportForTLS(&tls.Config{}).Proxy != nil {
		t.Fatal("peer transport inherited an environment proxy")
	}
}

func TestChangedFingerprintBlocksTrustedPeer(t *testing.T) {
	a := testApp(t, t.TempDir(), "Sender")
	peerID := "trusted-device"
	a.trusted[peerID] = &TrustedPeer{
		ID: peerID, Name: "Trusted", Fingerprint: strings.Repeat("a", 64),
		OutgoingToken: randomHex(32), IncomingTokenHash: hashToken(randomHex(32)),
	}
	a.discovered[peerID] = &DiscoveredPeer{
		ID: peerID, Name: "Impostor", Fingerprint: strings.Repeat("b", 64),
		Address: "127.0.0.1:43338", LastSeen: time.Now(),
	}
	if _, err := a.readyPeer(peerID); err == nil {
		t.Fatal("changed peer certificate remained ready for transfer")
	}

	request := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	response := httptest.NewRecorder()
	a.localMux().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("peer list returned %d", response.Code)
	}
	var peers []peerView
	if err := json.NewDecoder(response.Body).Decode(&peers); err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 || peers[0].Trusted {
		t.Fatalf("changed fingerprint was presented as trusted: %+v", peers)
	}
}

func TestUniqueDestinationPreservesExistingFiles(t *testing.T) {
	directory := t.TempDir()
	existing := filepath.Join(directory, "report.txt")
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination, err := uniqueDestination(directory, "report.txt")
	if err != nil {
		t.Fatal(err)
	}
	if destination == existing || filepath.Base(destination) != "report (1).txt" {
		t.Fatalf("unique destination = %q", destination)
	}
	contents, err := os.ReadFile(existing)
	if err != nil || string(contents) != "keep" {
		t.Fatalf("existing file changed: contents=%q err=%v", contents, err)
	}
}

func TestSecretFilesArePrivateRegularFiles(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX file permission bits are not enforced on Windows")
	}
	a := testApp(t, t.TempDir(), "Private")
	paths := []string{
		a.configDir,
		a.statePath,
		filepath.Join(a.configDir, "control-token"),
		filepath.Join(a.configDir, "device-key.pem"),
		filepath.Join(a.configDir, "device-cert.pem"),
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("secret path is a symlink: %s", path)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("secret path permissions are %o: %s", info.Mode().Perm(), path)
		}
	}
}

func testApp(t *testing.T, root, name string) *App {
	t.Helper()
	config := filepath.Join(root, "config")
	downloads := filepath.Join(root, "downloads")
	oldConfig, hadConfig := os.LookupEnv("XENDFILE_CONFIG_DIR")
	oldDownloads, hadDownloads := os.LookupEnv("XENDFILE_DOWNLOAD_DIR")
	if err := os.Setenv("XENDFILE_CONFIG_DIR", config); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("XENDFILE_DOWNLOAD_DIR", downloads); err != nil {
		t.Fatal(err)
	}
	a, err := newApp("127.0.0.1:0")
	if hadConfig {
		_ = os.Setenv("XENDFILE_CONFIG_DIR", oldConfig)
	} else {
		_ = os.Unsetenv("XENDFILE_CONFIG_DIR")
	}
	if hadDownloads {
		_ = os.Setenv("XENDFILE_DOWNLOAD_DIR", oldDownloads)
	} else {
		_ = os.Unsetenv("XENDFILE_DOWNLOAD_DIR")
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

func servePublicPair(app *App, value pairRequest, remoteAddress string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(value)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/pair", bytes.NewReader(body))
	request.RemoteAddr = remoteAddress
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	app.publicMux().ServeHTTP(response, request)
	return response
}

func authorizeTestPeer(app *App, id, name string) (string, string) {
	token := randomHex(32)
	app.trusted[id] = &TrustedPeer{
		ID: id, Name: name, Fingerprint: strings.Repeat("a", 64),
		IncomingTokenHash: hashToken(token), OutgoingToken: randomHex(32),
	}
	return id, token
}

func authorizeTestRequest(request *http.Request, senderID, token string) {
	request.Header.Set("X-UniDrop-Sender-ID", senderID)
	request.Header.Set("Authorization", "Bearer "+token)
}

func assertDirectoryEmpty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("rejected receive left files behind: %v", entries)
	}
}
