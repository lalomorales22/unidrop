// UniDrop is a dependency-free, cross-platform local-network file sender.
// The complete application core and browser UI intentionally live in this file.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var appVersion = "0.1.0"

const (
	protocolVersion  = 1
	defaultUIPort    = 43337
	defaultPeerPort  = 43338
	discoveryAddress = "239.255.77.77:43339"
	maxRecent        = 60
	defaultMaxBytes  = int64(20 << 30) // 20 GiB
	peerLifetime     = 15 * time.Second
)

type Identity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type TrustedPeer struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Fingerprint       string `json:"fingerprint"`
	IncomingTokenHash string `json:"incoming_token_hash"`
	OutgoingToken     string `json:"outgoing_token"`
	PairedAt          string `json:"paired_at"`
}

type savedState struct {
	Identity    Identity                `json:"identity"`
	DownloadDir string                  `json:"download_dir"`
	Trusted     map[string]*TrustedPeer `json:"trusted_peers"`
}

type discoveryPacket struct {
	Version     int    `json:"version"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	OS          string `json:"os"`
	Port        int    `json:"port"`
	Fingerprint string `json:"fingerprint"`
}

type DiscoveredPeer struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	OS          string    `json:"os"`
	Address     string    `json:"address"`
	Fingerprint string    `json:"fingerprint"`
	LastSeen    time.Time `json:"last_seen"`
}

type peerView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	OS          string `json:"os"`
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint"`
	Trusted     bool   `json:"trusted"`
	Online      bool   `json:"online"`
}

type Transfer struct {
	ID        string `json:"id"`
	Direction string `json:"direction"`
	Peer      string `json:"peer"`
	File      string `json:"file"`
	Bytes     int64  `json:"bytes"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	Started   string `json:"started"`
	Finished  string `json:"finished,omitempty"`
}

type attemptWindow struct {
	Start time.Time
	Count int
}

type App struct {
	mu           sync.RWMutex
	identity     Identity
	trusted      map[string]*TrustedPeer
	discovered   map[string]*DiscoveredPeer
	transfers    []*Transfer
	attempts     map[string]*attemptWindow
	configDir    string
	downloadDir  string
	statePath    string
	cert         tls.Certificate
	fingerprint  string
	pairingCode  string
	peerPort     int
	uiAddress    string
	maxBytes     int64
	publicServer *http.Server
	uiServer     *http.Server
	publicListen net.Listener
	uiListen     net.Listener
}

type pairRequest struct {
	SenderID          string `json:"sender_id"`
	SenderName        string `json:"sender_name"`
	SenderFingerprint string `json:"sender_fingerprint"`
	Nonce             string `json:"nonce"`
	Proof             string `json:"proof"`
	ReturnToken       string `json:"return_token"`
}

type pairResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	Token       string `json:"token"`
}

func main() {
	listenAddress := flag.String("listen", fmt.Sprintf(":%d", defaultPeerPort), "LAN HTTPS listen address")
	uiAddress := flag.String("ui", fmt.Sprintf("127.0.0.1:%d", defaultUIPort), "local control-panel address")
	noOpen := flag.Bool("no-open", false, "do not open the control panel at startup")
	openOnly := flag.Bool("open", false, "open the running control panel and exit")
	showVersion := flag.Bool("version", false, "show version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("UniDrop %s (%s/%s)\n", appVersion, runtime.GOOS, runtime.GOARCH)
		return
	}
	uiURL := "http://" + *uiAddress + "/"
	if *openOnly {
		if !probeUI(uiURL) {
			log.Fatal("UniDrop is not running")
		}
		if err := openTarget(uiURL); err != nil {
			log.Fatal(err)
		}
		return
	}

	app, err := newApp(*uiAddress)
	if err != nil {
		log.Fatal(err)
	}
	if err := app.start(*listenAddress); err != nil {
		if probeUI(uiURL) {
			if !*noOpen {
				_ = openTarget(uiURL)
			}
			return
		}
		log.Fatal(err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go app.runDiscovery(ctx)
	if !*noOpen {
		go func() {
			time.Sleep(350 * time.Millisecond)
			_ = openTarget(uiURL)
		}()
	}
	log.Printf("UniDrop %s ready: %s (secure peer port %d)", appVersion, uiURL, app.peerPort)
	<-ctx.Done()
	shutdown, stop := context.WithTimeout(context.Background(), 4*time.Second)
	defer stop()
	_ = app.uiServer.Shutdown(shutdown)
	_ = app.publicServer.Shutdown(shutdown)
}

func newApp(uiAddress string) (*App, error) {
	configBase, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("find config directory: %w", err)
	}
	configDir := filepath.Join(configBase, "UniDrop")
	if override := strings.TrimSpace(os.Getenv("UNIDROP_CONFIG_DIR")); override != "" {
		configDir = override
	}
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return nil, fmt.Errorf("create config directory: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("find home directory: %w", err)
	}
	a := &App{
		trusted:     make(map[string]*TrustedPeer),
		discovered:  make(map[string]*DiscoveredPeer),
		attempts:    make(map[string]*attemptWindow),
		configDir:   configDir,
		downloadDir: filepath.Join(home, "Downloads", "UniDrop"),
		statePath:   filepath.Join(configDir, "state.json"),
		uiAddress:   uiAddress,
		maxBytes:    defaultMaxBytes,
	}
	if override := strings.TrimSpace(os.Getenv("UNIDROP_DOWNLOAD_DIR")); override != "" {
		a.downloadDir = override
	}
	if err := a.loadState(); err != nil {
		return nil, err
	}
	if a.identity.ID == "" {
		a.identity.ID = randomHex(16)
	}
	if a.identity.Name == "" {
		host, _ := os.Hostname()
		if strings.TrimSpace(host) == "" {
			host = "UniDrop device"
		}
		a.identity.Name = cleanDisplayName(host)
	}
	if err := os.MkdirAll(a.downloadDir, 0700); err != nil {
		return nil, fmt.Errorf("create download directory: %w", err)
	}
	cert, fingerprint, err := loadOrCreateCertificate(configDir, a.identity.Name)
	if err != nil {
		return nil, err
	}
	a.cert = cert
	a.fingerprint = fingerprint
	a.pairingCode = randomCode()
	if err := a.saveState(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *App) loadState() error {
	b, err := os.ReadFile(a.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	var state savedState
	if err := json.Unmarshal(b, &state); err != nil {
		return fmt.Errorf("parse %s: %w", a.statePath, err)
	}
	a.identity = state.Identity
	if state.DownloadDir != "" {
		a.downloadDir = state.DownloadDir
	}
	if state.Trusted != nil {
		a.trusted = state.Trusted
	}
	return nil
}

func (a *App) saveState() error {
	a.mu.RLock()
	state := savedState{Identity: a.identity, DownloadDir: a.downloadDir, Trusted: a.trusted}
	b, err := json.MarshalIndent(state, "", "  ")
	a.mu.RUnlock()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(a.configDir, "state-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0600); err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Windows does not replace an existing file with os.Rename. The state file
	// contains tokens, so keep the fallback narrowly scoped to this exact path.
	if err := os.Rename(tmpName, a.statePath); err != nil {
		if removeErr := os.Remove(a.statePath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(tmpName, a.statePath); err != nil {
			return err
		}
	}
	ok = true
	return nil
}

func loadOrCreateCertificate(dir, name string) (tls.Certificate, string, error) {
	certPath := filepath.Join(dir, "device-cert.pem")
	keyPath := filepath.Join(dir, "device-key.pem")
	if cert, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		return finishCertificate(cert)
	}
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "UniDrop " + name},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		return tls.Certificate{}, "", err
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	return finishCertificate(cert)
}

func finishCertificate(cert tls.Certificate) (tls.Certificate, string, error) {
	if len(cert.Certificate) == 0 {
		return tls.Certificate{}, "", errors.New("device certificate is empty")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return tls.Certificate{}, "", err
	}
	cert.Leaf = leaf
	sum := sha256.Sum256(leaf.Raw)
	return cert, hex.EncodeToString(sum[:]), nil
}

func (a *App) start(listenAddress string) error {
	uiListener, err := net.Listen("tcp", a.uiAddress)
	if err != nil {
		return fmt.Errorf("control panel: %w", err)
	}
	a.uiListen = uiListener
	publicListener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		_ = uiListener.Close()
		return fmt.Errorf("secure peer listener: %w", err)
	}
	a.publicListen = publicListener
	a.peerPort = publicListener.Addr().(*net.TCPAddr).Port

	a.uiServer = &http.Server{
		Handler:           a.localMux(),
		ReadHeaderTimeout: 8 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	a.publicServer = &http.Server{
		Handler:           a.publicMux(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{a.cert},
			MinVersion:   tls.VersionTLS13,
		},
	}
	go func() {
		if err := a.uiServer.Serve(uiListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("control panel stopped: %v", err)
		}
	}()
	go func() {
		tlsListener := tls.NewListener(publicListener, a.publicServer.TLSConfig)
		if err := a.publicServer.Serve(tlsListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("peer server stopped: %v", err)
		}
	}()
	return nil
}

func (a *App) localMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleUI)
	mux.HandleFunc("/api/info", a.handleLocalInfo)
	mux.HandleFunc("/api/peers", a.handlePeers)
	mux.HandleFunc("/api/transfers", a.handleTransfers)
	mux.HandleFunc("/api/pair", a.requireLocalWrite(a.handleLocalPair))
	mux.HandleFunc("/api/send", a.requireLocalWrite(a.handleLocalSend))
	mux.HandleFunc("/api/add-peer", a.requireLocalWrite(a.handleAddPeer))
	mux.HandleFunc("/api/open-downloads", a.requireLocalWrite(a.handleOpenDownloads))
	mux.HandleFunc("/api/rotate-code", a.requireLocalWrite(a.handleRotateCode))
	return securityHeaders(mux, true)
}

func (a *App) publicMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/info", a.handlePublicInfo)
	mux.HandleFunc("/api/v1/pair", a.handlePublicPair)
	mux.HandleFunc("/api/v1/files", a.handleReceive)
	return securityHeaders(mux, false)
}

func securityHeaders(next http.Handler, local bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if local {
			w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; img-src 'self' data:")
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) requireLocalWrite(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("X-UniDrop-UI") != "1" {
			http.Error(w, "local request header required", http.StatusForbidden)
			return
		}
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil || (host != "127.0.0.1" && host != "localhost" && host != "[::1]" && host != "::1") {
			http.Error(w, "local host required", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (a *App) handleUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, uiHTML)
}

func (a *App) handleLocalInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.mu.RLock()
	data := map[string]any{
		"version": appVersion, "id": a.identity.ID, "name": a.identity.Name,
		"os": runtime.GOOS, "fingerprint": a.fingerprint, "pairing_code": a.pairingCode,
		"download_dir": a.downloadDir, "max_bytes": a.maxBytes,
	}
	a.mu.RUnlock()
	writeJSON(w, http.StatusOK, data)
}

func (a *App) handlePublicInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"protocol": protocolVersion, "version": appVersion, "id": a.identity.ID,
		"name": a.identity.Name, "os": runtime.GOOS, "fingerprint": a.fingerprint,
	})
}

func (a *App) handlePeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	now := time.Now()
	a.mu.RLock()
	views := make([]peerView, 0, len(a.discovered))
	seen := make(map[string]bool)
	for id, peer := range a.discovered {
		online := now.Sub(peer.LastSeen) <= peerLifetime
		trusted := a.trusted[id]
		views = append(views, peerView{
			ID: id, Name: peer.Name, OS: peer.OS, Address: peer.Address,
			Fingerprint: peer.Fingerprint, Online: online,
			Trusted: trusted != nil && trusted.OutgoingToken != "" && hmac.Equal([]byte(trusted.Fingerprint), []byte(peer.Fingerprint)),
		})
		seen[id] = true
	}
	for id, peer := range a.trusted {
		if !seen[id] {
			views = append(views, peerView{ID: id, Name: peer.Name, Fingerprint: peer.Fingerprint, Trusted: true, Online: false})
		}
	}
	a.mu.RUnlock()
	writeJSON(w, http.StatusOK, views)
}

func (a *App) handleTransfers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.mu.RLock()
	items := make([]*Transfer, len(a.transfers))
	for i, transfer := range a.transfers {
		copyTransfer := *transfer
		items[i] = &copyTransfer
	}
	a.mu.RUnlock()
	writeJSON(w, http.StatusOK, items)
}

func (a *App) handleRotateCode(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.pairingCode = randomCode()
	code := a.pairingCode
	a.attempts = make(map[string]*attemptWindow)
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"pairing_code": code})
}

func (a *App) handleOpenDownloads(w http.ResponseWriter, r *http.Request) {
	if err := openTarget(a.downloadDir); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleAddPeer(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Address string `json:"address"`
	}
	if err := decodeJSON(r, &request, 4096); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	peer, err := a.inspectAddress(request.Address)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	a.recordDiscovered(peer)
	writeJSON(w, http.StatusOK, peer)
}

func (a *App) inspectAddress(raw string) (*DiscoveredPeer, error) {
	address := strings.TrimSpace(raw)
	address = strings.TrimPrefix(address, "https://")
	address = strings.TrimSuffix(address, "/")
	if !strings.Contains(address, ":") {
		address += fmt.Sprintf(":%d", defaultPeerPort)
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return nil, errors.New("enter an address such as 192.168.1.20:43338")
	}
	client := insecurePairClient(nil)
	resp, err := client.Get("https://" + address + "/api/v1/info")
	if err != nil {
		return nil, fmt.Errorf("connect to peer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("peer returned %s", resp.Status)
	}
	var info struct {
		Protocol    int    `json:"protocol"`
		ID          string `json:"id"`
		Name        string `json:"name"`
		OS          string `json:"os"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&info); err != nil {
		return nil, err
	}
	if info.Protocol != protocolVersion || !validID(info.ID) || !validFingerprint(info.Fingerprint) {
		return nil, errors.New("address is not a compatible UniDrop peer")
	}
	presented := responseFingerprint(resp)
	if !hmac.Equal([]byte(presented), []byte(info.Fingerprint)) {
		return nil, errors.New("peer certificate fingerprint does not match its identity response")
	}
	return &DiscoveredPeer{ID: info.ID, Name: cleanDisplayName(info.Name), OS: info.OS, Address: address, Fingerprint: info.Fingerprint, LastSeen: time.Now()}, nil
}

func (a *App) handleLocalPair(w http.ResponseWriter, r *http.Request) {
	var request struct {
		PeerID string `json:"peer_id"`
		Code   string `json:"code"`
	}
	if err := decodeJSON(r, &request, 4096); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := a.pairWithPeer(request.PeerID, request.Code); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) pairWithPeer(peerID, code string) error {
	code = normalizePairingCode(code)
	if len(code) != 16 {
		return errors.New("enter the four-part pairing key shown on the other device")
	}
	a.mu.RLock()
	discovered := a.discovered[peerID]
	if discovered != nil {
		peerCopy := *discovered
		discovered = &peerCopy
	}
	identity := a.identity
	localFingerprint := a.fingerprint
	a.mu.RUnlock()
	if discovered == nil || time.Since(discovered.LastSeen) > 5*time.Minute {
		return errors.New("peer is offline; discover or add it again")
	}
	nonce := randomHex(16)
	returnToken := randomHex(32)
	request := pairRequest{
		SenderID: identity.ID, SenderName: identity.Name, SenderFingerprint: localFingerprint,
		Nonce: nonce, ReturnToken: returnToken,
	}
	request.Proof = pairProof(code, discovered.Fingerprint, request)
	body, _ := json.Marshal(request)
	var presented string
	client := insecurePairClient(&presented)
	httpRequest, _ := http.NewRequest(http.MethodPost, "https://"+discovered.Address+"/api/v1/pair", strings.NewReader(string(body)))
	httpRequest.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("secure pairing connection: %w", err)
	}
	defer resp.Body.Close()
	if presented == "" || !hmac.Equal([]byte(presented), []byte(discovered.Fingerprint)) {
		return errors.New("the peer certificate changed; remove and rediscover the device")
	}
	if resp.StatusCode != http.StatusOK {
		return responseError(resp)
	}
	var paired pairResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&paired); err != nil {
		return err
	}
	if paired.ID != discovered.ID || !hmac.Equal([]byte(paired.Fingerprint), []byte(presented)) || len(paired.Token) < 32 {
		return errors.New("invalid pairing response")
	}
	a.mu.Lock()
	a.trusted[paired.ID] = &TrustedPeer{
		ID: paired.ID, Name: cleanDisplayName(paired.Name), Fingerprint: paired.Fingerprint,
		IncomingTokenHash: hashToken(returnToken), OutgoingToken: paired.Token,
		PairedAt: time.Now().UTC().Format(time.RFC3339),
	}
	a.mu.Unlock()
	return a.saveState()
}

func (a *App) handlePublicPair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	remoteIP := clientIP(r.RemoteAddr)
	if !a.allowPairAttempt(remoteIP) {
		writeError(w, http.StatusTooManyRequests, errors.New("too many pairing attempts; wait five minutes or rotate the code"))
		return
	}
	var request pairRequest
	if err := decodeJSON(r, &request, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !validID(request.SenderID) || !validFingerprint(request.SenderFingerprint) || len(request.Nonce) != 32 || len(request.ReturnToken) != 64 {
		writeError(w, http.StatusBadRequest, errors.New("invalid pairing request"))
		return
	}
	a.mu.RLock()
	code := a.pairingCode
	a.mu.RUnlock()
	expected := pairProof(code, a.fingerprint, request)
	provided, err := hex.DecodeString(request.Proof)
	if err != nil || !hmac.Equal(provided, mustDecodeHex(expected)) {
		writeError(w, http.StatusUnauthorized, errors.New("pairing code was not accepted"))
		return
	}
	token := randomHex(32)
	a.mu.Lock()
	a.trusted[request.SenderID] = &TrustedPeer{
		ID: request.SenderID, Name: cleanDisplayName(request.SenderName), Fingerprint: request.SenderFingerprint,
		IncomingTokenHash: hashToken(token), OutgoingToken: request.ReturnToken,
		PairedAt: time.Now().UTC().Format(time.RFC3339),
	}
	a.pairingCode = randomCode()
	a.attempts = make(map[string]*attemptWindow)
	a.mu.Unlock()
	if err := a.saveState(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, pairResponse{ID: a.identity.ID, Name: a.identity.Name, Fingerprint: a.fingerprint, Token: token})
}

func (a *App) allowPairAttempt(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	window := a.attempts[ip]
	if window == nil || now.Sub(window.Start) > 5*time.Minute {
		a.attempts[ip] = &attemptWindow{Start: now, Count: 1}
		return true
	}
	if window.Count >= 8 {
		return false
	}
	window.Count++
	return true
}

func pairProof(code, recipientFingerprint string, request pairRequest) string {
	returnHash := sha256.Sum256([]byte(request.ReturnToken))
	message := strings.Join([]string{
		"unidrop-pair-v1", recipientFingerprint, request.SenderID,
		request.SenderFingerprint, request.Nonce, hex.EncodeToString(returnHash[:]),
	}, "\n")
	mac := hmac.New(sha256.New, []byte(normalizePairingCode(code)))
	_, _ = mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *App) handleLocalSend(w http.ResponseWriter, r *http.Request) {
	peerID := r.URL.Query().Get("peer")
	fileName := sanitizeFilename(r.URL.Query().Get("filename"))
	if !validID(peerID) || fileName == "" {
		writeError(w, http.StatusBadRequest, errors.New("peer and filename are required"))
		return
	}
	if r.ContentLength < 0 {
		writeError(w, http.StatusLengthRequired, errors.New("file size is required"))
		return
	}
	if r.ContentLength > a.maxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, errors.New("file exceeds this device's size limit"))
		return
	}
	a.mu.RLock()
	trusted := a.trusted[peerID]
	discovered := a.discovered[peerID]
	if trusted != nil {
		trustedCopy := *trusted
		trusted = &trustedCopy
	}
	if discovered != nil {
		discoveredCopy := *discovered
		discovered = &discoveredCopy
	}
	a.mu.RUnlock()
	if trusted == nil || discovered == nil || trusted.OutgoingToken == "" || !hmac.Equal([]byte(trusted.Fingerprint), []byte(discovered.Fingerprint)) {
		writeError(w, http.StatusUnauthorized, errors.New("pair with this online peer before sending"))
		return
	}
	transfer := a.addTransfer("send", trusted.Name, fileName)
	requestURL := "https://" + discovered.Address + "/api/v1/files?name=" + url.QueryEscape(fileName)
	out, err := http.NewRequestWithContext(r.Context(), http.MethodPost, requestURL, http.MaxBytesReader(w, r.Body, a.maxBytes))
	if err != nil {
		a.finishTransfer(transfer, "failed", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out.ContentLength = r.ContentLength
	out.Header.Set("Content-Type", "application/octet-stream")
	out.Header.Set("Authorization", "Bearer "+trusted.OutgoingToken)
	out.Header.Set("X-UniDrop-Sender-ID", a.identity.ID)
	client := pinnedClient(trusted.Fingerprint)
	resp, err := client.Do(out)
	if err != nil {
		a.finishTransfer(transfer, "failed", err)
		writeError(w, http.StatusBadGateway, fmt.Errorf("send to %s: %w", trusted.Name, err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		err := responseError(resp)
		a.finishTransfer(transfer, "failed", err)
		writeError(w, http.StatusBadGateway, err)
		return
	}
	a.updateTransferBytes(transfer, r.ContentLength)
	a.finishTransfer(transfer, "complete", nil)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "bytes": r.ContentLength})
}

func (a *App) handleReceive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	senderID := r.Header.Get("X-UniDrop-Sender-ID")
	peer, ok := a.authenticate(senderID, r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, errors.New("this device is not paired"))
		return
	}
	if r.ContentLength < 0 {
		writeError(w, http.StatusLengthRequired, errors.New("file size is required"))
		return
	}
	if r.ContentLength > a.maxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, errors.New("file exceeds receiver size limit"))
		return
	}
	name := sanitizeFilename(r.URL.Query().Get("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("safe filename is required"))
		return
	}
	transfer := a.addTransfer("receive", peer.Name, name)
	tmp, err := os.CreateTemp(a.downloadDir, ".unidrop-*.part")
	if err != nil {
		a.finishTransfer(transfer, "failed", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	tmpPath := tmp.Name()
	complete := false
	defer func() {
		_ = tmp.Close()
		if !complete {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0600); err != nil {
		a.finishTransfer(transfer, "failed", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	written, err := io.CopyBuffer(tmp, io.LimitReader(r.Body, a.maxBytes+1), make([]byte, 256<<10))
	a.updateTransferBytes(transfer, written)
	if err == nil && written != r.ContentLength {
		err = errors.New("connection ended before the complete file arrived")
	}
	if err == nil && written > a.maxBytes {
		err = errors.New("file exceeds receiver size limit")
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		a.finishTransfer(transfer, "failed", err)
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.mu.Lock()
	finalPath, destinationErr := uniqueDestination(a.downloadDir, name)
	var renameErr error
	if destinationErr == nil {
		renameErr = os.Rename(tmpPath, finalPath)
	}
	a.mu.Unlock()
	if destinationErr != nil {
		a.finishTransfer(transfer, "failed", destinationErr)
		writeError(w, http.StatusInternalServerError, destinationErr)
		return
	}
	if renameErr != nil {
		a.finishTransfer(transfer, "failed", renameErr)
		writeError(w, http.StatusInternalServerError, renameErr)
		return
	}
	complete = true
	a.finishTransfer(transfer, "complete", nil)
	go notifyReceived(filepath.Base(finalPath), peer.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "file": filepath.Base(finalPath), "bytes": written})
}

func (a *App) authenticate(senderID, authorization string) (*TrustedPeer, bool) {
	if !strings.HasPrefix(authorization, "Bearer ") || !validID(senderID) {
		return nil, false
	}
	token := strings.TrimPrefix(authorization, "Bearer ")
	provided := hashToken(token)
	a.mu.RLock()
	peer := a.trusted[senderID]
	if peer != nil {
		copyPeer := *peer
		peer = &copyPeer
	}
	a.mu.RUnlock()
	if peer == nil || len(provided) != len(peer.IncomingTokenHash) || !hmac.Equal([]byte(provided), []byte(peer.IncomingTokenHash)) {
		return nil, false
	}
	return peer, true
}

func (a *App) addTransfer(direction, peer, file string) *Transfer {
	t := &Transfer{ID: randomHex(8), Direction: direction, Peer: peer, File: file, Status: "in progress", Started: time.Now().UTC().Format(time.RFC3339)}
	a.mu.Lock()
	a.transfers = append([]*Transfer{t}, a.transfers...)
	if len(a.transfers) > maxRecent {
		a.transfers = a.transfers[:maxRecent]
	}
	a.mu.Unlock()
	return t
}

func (a *App) finishTransfer(t *Transfer, status string, err error) {
	a.mu.Lock()
	t.Status = status
	t.Finished = time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		t.Error = err.Error()
	}
	a.mu.Unlock()
}

func (a *App) updateTransferBytes(t *Transfer, bytes int64) {
	a.mu.Lock()
	t.Bytes = bytes
	a.mu.Unlock()
}

func (a *App) runDiscovery(ctx context.Context) {
	group, err := net.ResolveUDPAddr("udp4", discoveryAddress)
	if err != nil {
		log.Printf("discovery disabled: %v", err)
		return
	}
	conn, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		log.Printf("multicast discovery unavailable (manual address still works): %v", err)
		return
	}
	defer conn.Close()
	_ = conn.SetReadBuffer(64 << 10)
	go a.announceLoop(ctx, group)
	buffer := make([]byte, 4096)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, source, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				select {
				case <-ctx.Done():
					return
				default:
					continue
				}
			}
			continue
		}
		var packet discoveryPacket
		if err := json.Unmarshal(buffer[:n], &packet); err != nil || packet.Version != protocolVersion || packet.ID == a.identity.ID {
			continue
		}
		if !validID(packet.ID) || !validFingerprint(packet.Fingerprint) || packet.Port < 1 || packet.Port > 65535 {
			continue
		}
		peer := &DiscoveredPeer{
			ID: packet.ID, Name: cleanDisplayName(packet.Name), OS: packet.OS,
			Address:     net.JoinHostPort(source.IP.String(), strconv.Itoa(packet.Port)),
			Fingerprint: packet.Fingerprint, LastSeen: time.Now(),
		}
		a.recordDiscovered(peer)
	}
}

func (a *App) recordDiscovered(peer *DiscoveredPeer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.discovered[peer.ID]; !exists && len(a.discovered) >= 256 {
		var oldestID string
		var oldestTime time.Time
		for id, existing := range a.discovered {
			if time.Since(existing.LastSeen) > 5*time.Minute {
				delete(a.discovered, id)
				continue
			}
			if oldestID == "" || existing.LastSeen.Before(oldestTime) {
				oldestID, oldestTime = id, existing.LastSeen
			}
		}
		if len(a.discovered) >= 256 && oldestID != "" {
			delete(a.discovered, oldestID)
		}
	}
	a.discovered[peer.ID] = peer
}

func (a *App) announceLoop(ctx context.Context, group *net.UDPAddr) {
	conn, err := net.DialUDP("udp4", nil, group)
	if err != nil {
		log.Printf("discovery announcements unavailable: %v", err)
		return
	}
	defer conn.Close()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	announce := func() {
		packet, _ := json.Marshal(discoveryPacket{
			Version: protocolVersion, ID: a.identity.ID, Name: a.identity.Name,
			OS: runtime.GOOS, Port: a.peerPort, Fingerprint: a.fingerprint,
		})
		_, _ = conn.Write(packet)
	}
	announce()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			announce()
		}
	}
}

func insecurePairClient(capture *string) *http.Client {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
		// Pairing is the only TOFU operation. The one-time HMAC proof is bound
		// to the presented certificate fingerprint before trust is persisted.
		InsecureSkipVerify: true, //nolint:gosec -- intentional certificate pin bootstrap
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("peer did not present a certificate")
			}
			if capture != nil {
				sum := sha256.Sum256(state.PeerCertificates[0].Raw)
				*capture = hex.EncodeToString(sum[:])
			}
			return nil
		},
	}
	return &http.Client{Transport: transportForTLS(tlsConfig), Timeout: 20 * time.Second}
}

func pinnedClient(fingerprint string) *http.Client {
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, //nolint:gosec -- exact certificate pin checked below
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("peer did not present a certificate")
			}
			sum := sha256.Sum256(state.PeerCertificates[0].Raw)
			actual := hex.EncodeToString(sum[:])
			if !hmac.Equal([]byte(actual), []byte(fingerprint)) {
				return errors.New("peer certificate does not match the paired identity")
			}
			return nil
		},
	}
	return &http.Client{Transport: transportForTLS(tlsConfig)}
}

func transportForTLS(config *tls.Config) *http.Transport {
	return &http.Transport{
		// Peer traffic must stay on the LAN and must never be sent to a proxy
		// inherited from the user's shell environment.
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:       config,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	}
}

func responseFingerprint(resp *http.Response) string {
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return ""
	}
	sum := sha256.Sum256(resp.TLS.PeerCertificates[0].Raw)
	return hex.EncodeToString(sum[:])
}

func responseError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &body) == nil && body.Error != "" {
		return errors.New(body.Error)
	}
	return fmt.Errorf("peer returned %s", resp.Status)
}

func decodeJSON(r *http.Request, target any, limit int64) error {
	if r.ContentLength > limit {
		return errors.New("JSON request is too large")
	}
	limited := &io.LimitedReader{R: r.Body, N: limit + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	var trailing any
	err := decoder.Decode(&trailing)
	if limited.N == 0 {
		return errors.New("JSON request is too large")
	}
	if !errors.Is(err, io.EOF) {
		return errors.New("JSON request must contain exactly one object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func randomHex(bytes int) string {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func randomCode() string {
	value := randomHex(8)
	return value[0:4] + "-" + value[4:8] + "-" + value[8:12] + "-" + value[12:16]
}

func normalizePairingCode(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "-", "")
	value = strings.ReplaceAll(value, " ", "")
	return value
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func mustDecodeHex(value string) []byte {
	b, _ := hex.DecodeString(value)
	return b
}

func validID(value string) bool {
	if len(value) < 8 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_') {
			return false
		}
	}
	return true
}

func validFingerprint(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func cleanDisplayName(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, char := range value {
		if char >= 32 && char != 127 {
			builder.WriteRune(char)
		}
		if builder.Len() >= 80 {
			break
		}
	}
	if builder.Len() == 0 {
		return "UniDrop device"
	}
	return builder.String()
}

func sanitizeFilename(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	value = filepath.Base(value)
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, char := range value {
		if char >= 32 && char != 127 && char != '/' && char != '\\' && char != ':' {
			builder.WriteRune(char)
		}
		if builder.Len() >= 220 {
			break
		}
	}
	name := strings.Trim(builder.String(), ". ")
	if name == "" || name == "." || name == ".." {
		return ""
	}
	ext := filepath.Ext(name)
	base := strings.ToUpper(strings.TrimSuffix(name, ext))
	reserved := base == "CON" || base == "PRN" || base == "AUX" || base == "NUL"
	if !reserved && len(base) == 4 {
		reserved = (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9'
	}
	if reserved {
		name = "_" + name
	}
	return name
}

func uniqueDestination(dir, name string) (string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	candidate := filepath.Join(dir, name)
	for index := 1; ; index++ {
		_, err := os.Stat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
		candidate = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, index, ext))
	}
}

func clientIP(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return remote
	}
	return host
}

func probeUI(base string) bool {
	client := &http.Client{Timeout: 700 * time.Millisecond}
	resp, err := client.Get(base + "api/info")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func openTarget(target string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", target)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	return command.Start()
}

func notifyReceived(file, sender string) {
	title := "UniDrop received " + file
	message := "From " + sender
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf("display notification %s with title %s", strconv.Quote(message), strconv.Quote(title))
		_ = exec.Command("osascript", "-e", script).Run()
	case "windows":
		// Windows receives the file even when toast support is unavailable.
		return
	default:
		if path, err := exec.LookPath("notify-send"); err == nil {
			_ = exec.Command(path, title, message).Run()
		}
	}
}

const uiHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>UniDrop</title>
<style>
:root{color-scheme:dark;--ink:#f5f7fb;--muted:#9ba7ba;--card:#111827cc;--line:#29344a;--blue:#5ba7ff;--green:#5ee2a0;--red:#ff7c8f}
*{box-sizing:border-box}body{margin:0;min-height:100vh;font:15px/1.45 system-ui,-apple-system,"Segoe UI",sans-serif;color:var(--ink);background:radial-gradient(circle at 12% 10%,#17305c 0,transparent 32%),radial-gradient(circle at 88% 8%,#27366b 0,transparent 29%),#070b13}
main{width:min(1040px,calc(100% - 28px));margin:0 auto;padding:28px 0 60px}.top{display:flex;align-items:center;justify-content:space-between;gap:16px;margin-bottom:22px}.brand{display:flex;align-items:center;gap:13px}.logo{width:44px;height:44px;border-radius:14px;display:grid;place-items:center;background:linear-gradient(145deg,#68c6ff,#675bff);box-shadow:0 10px 35px #488dff55;font-size:23px}.brand h1{margin:0;font-size:24px}.brand p{margin:1px 0 0;color:var(--muted);font-size:13px}.button,button{border:1px solid var(--line);background:#172033;color:var(--ink);border-radius:11px;padding:10px 14px;font:inherit;font-weight:650;cursor:pointer}button:hover{border-color:#536582}.primary{background:linear-gradient(135deg,#4f92ff,#7668ff);border:0}.ghost{background:transparent}.grid{display:grid;grid-template-columns:1.45fr .8fr;gap:18px}.card{background:var(--card);border:1px solid var(--line);border-radius:18px;padding:19px;box-shadow:0 18px 65px #0005;backdrop-filter:blur(12px)}h2{font-size:15px;margin:0 0 14px;color:#dce5f3}.code{font:700 20px/1.25 ui-monospace,SFMono-Regular,monospace;letter-spacing:1px;margin:13px 0 12px;white-space:nowrap}.muted{color:var(--muted)}.small{font-size:12px}.devices{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px;min-height:94px}.device{display:flex;align-items:center;gap:11px;text-align:left;width:100%;padding:13px;background:#0d1422;border:1px solid var(--line);border-radius:13px}.device.selected{border-color:var(--blue);box-shadow:0 0 0 2px #5ba7ff22}.device.offline{opacity:.55}.os{width:37px;height:37px;border-radius:10px;background:#202b40;display:grid;place-items:center;font-size:18px}.device strong,.device span{display:block;overflow:hidden;text-overflow:ellipsis}.status{color:var(--green);font-size:12px}.offline .status{color:var(--muted)}.drop{display:block;cursor:pointer;border:1.5px dashed #40506c;border-radius:16px;padding:27px;text-align:center;margin-top:13px;transition:.15s}.drop.drag{border-color:var(--blue);background:#5ba7ff12}.drop input{display:none}.drop strong{display:block;font-size:17px;margin-bottom:4px}.sendbar{display:flex;gap:9px;align-items:center;margin-top:12px}.sendbar input,.manual input{min-width:0;flex:1;border:1px solid var(--line);background:#09101d;color:var(--ink);border-radius:10px;padding:10px 12px;font:inherit}.progress{height:7px;background:#202a3c;border-radius:10px;overflow:hidden;margin-top:10px}.progress i{display:block;height:100%;width:0;background:linear-gradient(90deg,var(--blue),var(--green));transition:.1s}.manual{display:flex;gap:8px;margin-top:10px}.transfer{padding:10px 0;border-top:1px solid #202a3c;display:grid;grid-template-columns:1fr auto;gap:4px}.transfer:first-child{border-top:0}.transfer strong{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.good{color:var(--green)}.bad{color:var(--red)}.empty{padding:22px 8px;color:var(--muted);text-align:center}.wide{grid-column:1/-1}.toast{position:fixed;right:18px;bottom:18px;max-width:360px;padding:13px 16px;border-radius:12px;background:#202b40;border:1px solid #46546b;box-shadow:0 15px 50px #0008;display:none}.toast.bad{display:block;border-color:#7c3c4c}.toast.good{display:block;border-color:#39755b}@media(max-width:760px){.grid{grid-template-columns:1fr}.devices{grid-template-columns:1fr}.top{align-items:flex-start}.top>.button{display:none}}
</style>
</head>
<body><main>
<header class="top"><div class="brand"><div class="logo">⇄</div><div><h1>UniDrop</h1><p id="deviceName">Secure local file sharing</p></div></div><button class="button" onclick="openDownloads()">Open received files</button></header>
<div class="grid">
<section class="card"><h2>Nearby devices</h2><div id="devices" class="devices"><div class="empty">Looking on your local network…</div></div><div class="manual"><input id="manual" placeholder="Can't see it? Enter 192.168.1.20:43338"><button onclick="addPeer()">Add</button></div></section>
<aside class="card"><h2>Pair this device</h2><div class="muted small">Copy this one-time key to the sending device.</div><div id="code" class="code">----&nbsp;----&nbsp;----&nbsp;----</div><button class="ghost small" onclick="copyCode()">Copy key</button> <button class="ghost small" onclick="rotateCode()">Rotate</button><div class="muted small" style="margin-top:13px">TLS 1.3 • certificate pinning • local network only</div></aside>
<section class="card"><h2>Send files <span id="selectedLabel" class="muted">— choose a device</span></h2><label class="drop" id="drop"><input id="files" type="file" multiple><strong>Drop files here</strong><span class="muted">or click to choose files</span></label><div class="sendbar"><input id="pairCode" maxlength="19" autocomplete="off" placeholder="Other device's pairing key"><button class="primary" id="send" onclick="sendSelected()">Send</button></div><div class="progress"><i id="progress"></i></div><div id="queue" class="muted small" style="margin-top:7px"></div></section>
<aside class="card"><h2>Recent activity</h2><div id="transfers"><div class="empty">No transfers yet</div></div></aside>
</div></main><div id="toast" class="toast"></div>
<script>
let selected=null,chosen=[],peers=[];const $=id=>document.getElementById(id);
async function api(path,options={}){options.headers={...(options.headers||{}),'X-UniDrop-UI':'1'};const r=await fetch(path,options);let body={};try{body=await r.json()}catch{}if(!r.ok)throw new Error(body.error||r.statusText);return body}
function esc(s){return String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))}
function icon(os){return os==='darwin'?'●':os==='windows'?'⊞':os==='linux'?'◆':'◇'}
async function refresh(){try{const [info,p,t]=await Promise.all([api('/api/info'),api('/api/peers'),api('/api/transfers')]);peers=p;$('deviceName').textContent=info.name+' • '+info.os;$('code').textContent=info.pairing_code;renderPeers();renderTransfers(t)}catch(e){toast(e.message,false)}}
function renderPeers(){const online=peers.filter(p=>p.online);if(!online.length){$('devices').innerHTML='<div class="empty">No devices found yet. Make sure UniDrop is open on both machines.</div>';return}$('devices').innerHTML=online.map(p=>'<button class="device '+(selected===p.id?'selected':'')+'" data-peer="'+p.id+'"><span class="os">'+icon(p.os)+'</span><span><strong>'+esc(p.name)+'</strong><span class="status">'+(p.trusted?'Paired and ready':'Code required')+'</span></span></button>').join('');document.querySelectorAll('[data-peer]').forEach(b=>b.onclick=()=>choose(b.dataset.peer))}
function choose(id){selected=id;const p=peers.find(x=>x.id===id);$('selectedLabel').textContent=p?'— '+p.name:'';$('pairCode').style.display=p?.trusted?'none':'block';renderPeers()}
function renderTransfers(items){$('transfers').innerHTML=items.length?items.slice(0,6).map(t=>'<div class="transfer"><strong>'+(t.direction==='send'?'↑':'↓')+' '+esc(t.file)+'</strong><span class="'+(t.status==='complete'?'good':t.status==='failed'?'bad':'muted')+'">'+esc(t.status)+'</span><span class="muted small">'+esc(t.peer)+'</span><span class="muted small">'+size(t.bytes)+'</span></div>').join(''):'<div class="empty">No transfers yet</div>'}
function size(n){if(!n)return '0 B';const u=['B','KB','MB','GB','TB'];let i=0;while(n>=1024&&i<u.length-1){n/=1024;i++}return n.toFixed(i?1:0)+' '+u[i]}
const drop=$('drop'),input=$('files');drop.onclick=()=>input.click();input.onchange=()=>setFiles([...input.files]);['dragenter','dragover'].forEach(e=>drop.addEventListener(e,x=>{x.preventDefault();drop.classList.add('drag')}));['dragleave','drop'].forEach(e=>drop.addEventListener(e,x=>{x.preventDefault();drop.classList.remove('drag')}));drop.addEventListener('drop',e=>setFiles([...e.dataTransfer.files]));function setFiles(f){chosen=f;$('queue').textContent=f.length?f.length+' file'+(f.length===1?'':'s')+' • '+size(f.reduce((n,x)=>n+x.size,0)):''}
async function ensurePaired(){let p=peers.find(x=>x.id===selected);if(!p)throw new Error('Choose an online device');if(p.trusted)return;const code=$('pairCode').value.trim();if(!/^[0-9a-fA-F]{4}(-?[0-9a-fA-F]{4}){3}$/.test(code))throw new Error("Enter the pairing key shown on "+p.name);await api('/api/pair',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({peer_id:p.id,code})});await refresh();p=peers.find(x=>x.id===selected);if(!p?.trusted)throw new Error('Pairing did not complete')}
async function sendSelected(){try{if(!chosen.length)throw new Error('Choose at least one file');await ensurePaired();$('send').disabled=true;for(let i=0;i<chosen.length;i++){const file=chosen[i];$('queue').textContent='Sending '+(i+1)+' of '+chosen.length+': '+file.name;await upload(file,n=>{$('progress').style.width=((i+n/file.size)/chosen.length*100)+'%'})}toast('Files sent securely',true);chosen=[];input.value='';$('queue').textContent='';setTimeout(()=>$('progress').style.width='0',900);await refresh()}catch(e){toast(e.message,false)}finally{$('send').disabled=false}}
function upload(file,onProgress){return new Promise((resolve,reject)=>{const x=new XMLHttpRequest();x.open('POST','/api/send?peer='+encodeURIComponent(selected)+'&filename='+encodeURIComponent(file.name));x.setRequestHeader('X-UniDrop-UI','1');x.setRequestHeader('Content-Type','application/octet-stream');x.upload.onprogress=e=>{if(e.lengthComputable)onProgress(e.loaded)};x.onload=()=>{let b={};try{b=JSON.parse(x.responseText)}catch{};x.status>=200&&x.status<300?resolve(b):reject(new Error(b.error||x.statusText))};x.onerror=()=>reject(new Error('Network connection failed'));x.send(file)})}
async function addPeer(){try{const address=$('manual').value.trim();if(!address)throw new Error('Enter the other device address');await api('/api/add-peer',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({address})});$('manual').value='';await refresh();toast('Device added',true)}catch(e){toast(e.message,false)}}
async function rotateCode(){try{await api('/api/rotate-code',{method:'POST'});await refresh()}catch(e){toast(e.message,false)}}
async function copyCode(){try{await navigator.clipboard.writeText($('code').textContent);toast('Pairing key copied',true)}catch(e){toast('Copy the key manually',false)}}
async function openDownloads(){try{await api('/api/open-downloads',{method:'POST'})}catch(e){toast(e.message,false)}}
let toastTimer;function toast(message,ok){const t=$('toast');t.textContent=message;t.className='toast '+(ok?'good':'bad');clearTimeout(toastTimer);toastTimer=setTimeout(()=>t.className='toast',4500)}
refresh();setInterval(refresh,3000);
</script></body></html>`
