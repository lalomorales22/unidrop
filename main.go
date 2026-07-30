// Xendfile is a dependency-free, cross-platform local-network file sender.
// The complete application core and browser UI intentionally live in this file.
package main

import (
	"bytes"
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
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	appversion "xendfile/internal/version"
)

var appVersion = appversion.Current

var protocolVersion = appversion.Protocol

const (
	defaultUIPort    = 43337
	defaultPeerPort  = 43338
	discoveryAddress = "239.255.77.77:43339"
	legacyUIHeader   = "X-UniDrop-UI"
	legacySenderID   = "X-UniDrop-Sender-ID"
	legacyPairDomain = "unidrop-pair-v1"
	maxRecent        = 60
	defaultMaxBytes  = int64(20 << 30) // 20 GiB
	peerLifetime     = 30 * time.Second
	manualProbeEvery = 10 * time.Second
	offerLifetime    = 2 * time.Minute
	maxPendingOffers = 100
)

const (
	receiveModeAsk     = "ask"
	receiveModeTrusted = "trusted"
	receiveModeOff     = "off"
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
	Identity        Identity                `json:"identity"`
	DownloadDir     string                  `json:"download_dir"`
	Trusted         map[string]*TrustedPeer `json:"trusted_peers"`
	ReceiveMode     string                  `json:"receive_mode"`
	ManualAddresses []string                `json:"manual_addresses,omitempty"`
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
	Manual      bool      `json:"-"`
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

type IncomingOffer struct {
	ID         string `json:"id"`
	SenderID   string `json:"-"`
	SenderName string `json:"sender_name"`
	File       string `json:"file"`
	Bytes      int64  `json:"bytes"`
	Status     string `json:"status"`
	Created    string `json:"created"`
	Expires    string `json:"expires"`
}

type offerRequest struct {
	File  string `json:"file"`
	Bytes int64  `json:"bytes"`
}

type offerResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type cliSendRequest struct {
	Target string   `json:"target"`
	Paths  []string `json:"paths"`
}

type cliSendResult struct {
	Target string   `json:"target"`
	Files  []string `json:"files"`
	Bytes  int64    `json:"bytes"`
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
	manualPeers  map[string]struct{}
	transfers    []*Transfer
	offers       map[string]*IncomingOffer
	attempts     map[string]*attemptWindow
	configDir    string
	downloadDir  string
	statePath    string
	cert         tls.Certificate
	fingerprint  string
	pairingCode  string
	controlToken string
	receiveMode  string
	discovery    string
	discoveryErr string
	peerPort     int
	uiAddress    string
	maxBytes     int64
	publicServer *http.Server
	uiServer     *http.Server
	publicListen net.Listener
	uiListen     net.Listener
	shutdown     chan struct{}
	shutdownOnce sync.Once
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
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "send":
			os.Exit(runSendCLI(os.Args[2:]))
		case "peers":
			os.Exit(runPeersCLI())
		case "stop":
			os.Exit(runStopCLI())
		}
	}
	listenAddress := flag.String("listen", fmt.Sprintf(":%d", defaultPeerPort), "LAN HTTPS listen address")
	uiAddress := flag.String("ui", fmt.Sprintf("127.0.0.1:%d", defaultUIPort), "local control-panel address")
	noOpen := flag.Bool("no-open", false, "do not open the control panel at startup")
	openOnly := flag.Bool("open", false, "open the running control panel and exit")
	showVersion := flag.Bool("version", false, "show version and exit")
	exitOnStdinClose := flag.Bool("exit-on-stdin-close", false, "stop when the supervising native shell exits")
	flag.Parse()

	if *showVersion {
		fmt.Printf("Xendfile %s (%s/%s)\n", appVersion, runtime.GOOS, runtime.GOARCH)
		return
	}
	uiURL := "http://" + *uiAddress + "/"
	if *openOnly {
		if !probeUI(uiURL) && startManagedServices() {
			waitForUI(uiURL, 5*time.Second)
		}
		if probeUI(uiURL) {
			if err := openTarget(uiURL); err != nil {
				log.Fatal(err)
			}
			return
		}
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
	if *exitOnStdinClose {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			cancel()
		}()
	}
	go app.runDiscovery(ctx)
	go app.runManualPeerChecks(ctx)
	if *openOnly && runtime.GOOS == "linux" {
		startLinuxTray(uiURL)
	}
	if !*noOpen {
		go func() {
			time.Sleep(350 * time.Millisecond)
			_ = openTarget(uiURL)
		}()
	}
	log.Printf("Xendfile %s ready: %s (secure peer port %d)", appVersion, uiURL, app.peerPort)
	select {
	case <-ctx.Done():
	case <-app.shutdown:
	}
	shutdown, stop := context.WithTimeout(context.Background(), 4*time.Second)
	defer stop()
	_ = app.uiServer.Shutdown(shutdown)
	_ = app.publicServer.Shutdown(shutdown)
}

func runSendCLI(args []string) int {
	target, paths, err := parseSendArguments(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Xendfile:", err)
		fmt.Fprintln(os.Stderr, "Usage: xendfile send <file> [file...] <device-name.local>")
		fmt.Fprintln(os.Stderr, "   or: xendfile send --to <device> <file> [file...]")
		return 2
	}
	total := int64(0)
	for index, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Xendfile: resolve %s: %v\n", path, err)
			return 1
		}
		info, err := os.Stat(absolute)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Xendfile: inspect %s: %v\n", path, err)
			return 1
		}
		if !info.Mode().IsRegular() {
			fmt.Fprintf(os.Stderr, "Xendfile: %s is not a regular file (folder sending is coming next)\n", path)
			return 1
		}
		total += info.Size()
		paths[index] = absolute
	}
	request := cliSendRequest{Target: target, Paths: paths}
	body, _ := json.Marshal(request)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	fmt.Printf("Xendfile: asking %s to accept %d file(s), %s total...\n", target, len(paths), humanBytes(total))
	response, err := localControlRequest(ctx, http.MethodPost, "/api/cli/send", body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Xendfile:", err)
		return 1
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		fmt.Fprintln(os.Stderr, "Xendfile:", responseError(response))
		return 1
	}
	var result cliSendResult
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result); err != nil {
		fmt.Fprintln(os.Stderr, "Xendfile: invalid local service response:", err)
		return 1
	}
	fmt.Printf("Xendfile: sent %d file(s) to %s (%s).\n", len(result.Files), result.Target, humanBytes(result.Bytes))
	return 0
}

func runPeersCLI() int {
	response, err := localControlRequest(context.Background(), http.MethodGet, "/api/cli/peers", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Xendfile:", err)
		return 1
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "Xendfile:", responseError(response))
		return 1
	}
	var peers []peerView
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&peers); err != nil {
		fmt.Fprintln(os.Stderr, "Xendfile: invalid local service response:", err)
		return 1
	}
	sort.Slice(peers, func(i, j int) bool { return strings.ToLower(peers[i].Name) < strings.ToLower(peers[j].Name) })
	found := 0
	for _, peer := range peers {
		if !peer.Online {
			continue
		}
		state := "pair first"
		if peer.Trusted {
			state = "ready"
		}
		fmt.Printf("%-28s %-10s %s\n", cliDeviceName(peer.Name), state, peer.Address)
		found++
	}
	if found == 0 {
		fmt.Println("No Xendfile devices are currently visible.")
	}
	return 0
}

func runStopCLI() int {
	if stopManagedServices() {
		fmt.Println("Xendfile: stopped the background service and desktop shell.")
		return 0
	}
	response, err := localControlRequest(context.Background(), http.MethodPost, "/api/cli/shutdown", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Xendfile:", err)
		return 1
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "Xendfile:", responseError(response))
		return 1
	}
	if runtime.GOOS == "linux" {
		_ = exec.Command("pkill", "-TERM", "-x", "xendfile-tray").Run()
	} else if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill", "/IM", "xendfile-tray.exe", "/F").Run()
	}
	fmt.Println("Xendfile: stopped the background service and desktop shell.")
	return 0
}

func stopManagedServices() bool {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/io.github.lalomorales22.xendfile", os.Getuid())).Run() == nil
	case "linux":
		return exec.Command("systemctl", "--user", "stop", "xendfile-tray.service", "xendfile.service").Run() == nil
	default:
		return false
	}
}

func startManagedServices() bool {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		plist := filepath.Join(home, "Library", "LaunchAgents", "io.github.lalomorales22.xendfile.plist")
		return exec.Command("launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), plist).Run() == nil
	case "linux":
		return exec.Command("systemctl", "--user", "start", "xendfile.service", "xendfile-tray.service").Run() == nil
	case "windows":
		executable, err := os.Executable()
		if err != nil {
			return false
		}
		tray := filepath.Join(filepath.Dir(executable), "xendfile-tray.exe")
		if info, err := os.Stat(tray); err != nil || !info.Mode().IsRegular() {
			return false
		}
		return exec.Command(tray).Start() == nil
	default:
		return false
	}
}

func waitForUI(uiURL string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if probeUI(uiURL) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func startLinuxTray(uiURL string) {
	if exec.Command("pgrep", "-x", "xendfile-tray").Run() == nil {
		return
	}
	executable, err := os.Executable()
	if err != nil {
		return
	}
	tray := filepath.Join(filepath.Dir(executable), "xendfile-tray")
	if info, err := os.Stat(tray); err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return
	}
	_ = exec.Command(tray, "--ui", uiURL).Start()
}

func parseSendArguments(args []string) (string, []string, error) {
	if len(args) >= 3 && args[0] == "--to" {
		target := strings.TrimSpace(args[1])
		if target == "" {
			return "", nil, errors.New("device name is required")
		}
		return target, append([]string(nil), args[2:]...), nil
	}
	if len(args) < 2 {
		return "", nil, errors.New("at least one file and a destination device are required")
	}
	target := strings.TrimSpace(args[len(args)-1])
	if target == "" {
		return "", nil, errors.New("device name is required")
	}
	return target, append([]string(nil), args[:len(args)-1]...), nil
}

func localControlRequest(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	configDir, err := configDirectory()
	if err != nil {
		return nil, err
	}
	tokenBytes, err := os.ReadFile(filepath.Join(configDir, "control-token"))
	if err != nil {
		return nil, errors.New("the Xendfile service is not initialized; open Xendfile once and try again")
	}
	token := strings.TrimSpace(string(tokenBytes))
	if len(token) != 64 {
		return nil, errors.New("the local Xendfile control token is invalid")
	}
	base := environmentValue("XENDFILE_UI_URL", "UNIDROP_UI_URL")
	if base == "" {
		base = fmt.Sprintf("http://127.0.0.1:%d", defaultUIPort)
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "http" || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1") {
		return nil, errors.New("XENDFILE_UI_URL must be an HTTP loopback address")
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(base, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		return nil, errors.New("the Xendfile background service is not running")
	}
	return response, nil
}

func cliDeviceName(name string) string {
	name = normalizeDeviceTarget(name)
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return -1
	}, name)
	name = strings.Trim(name, "-")
	if name == "" {
		return "unknown.local"
	}
	return name + ".local"
}

func newApp(uiAddress string) (*App, error) {
	configDir, err := configDirectory()
	if err != nil {
		return nil, err
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
		manualPeers: make(map[string]struct{}),
		offers:      make(map[string]*IncomingOffer),
		attempts:    make(map[string]*attemptWindow),
		configDir:   configDir,
		downloadDir: filepath.Join(home, "Downloads", "Xendfile"),
		statePath:   filepath.Join(configDir, "state.json"),
		uiAddress:   uiAddress,
		maxBytes:    defaultMaxBytes,
		discovery:   "starting",
		shutdown:    make(chan struct{}),
	}
	if override := environmentValue("XENDFILE_DOWNLOAD_DIR", "UNIDROP_DOWNLOAD_DIR"); override != "" {
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
			host = "Xendfile device"
		}
		a.identity.Name = cleanDisplayName(host)
	}
	if !validReceiveMode(a.receiveMode) {
		a.receiveMode = receiveModeAsk
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
	a.controlToken, err = loadOrCreateControlToken(configDir)
	if err != nil {
		return nil, err
	}
	if err := a.saveState(); err != nil {
		return nil, err
	}
	return a, nil
}

func configDirectory() (string, error) {
	configBase, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config directory: %w", err)
	}
	configDir := filepath.Join(configBase, "Xendfile")
	if override := environmentValue("XENDFILE_CONFIG_DIR", "UNIDROP_CONFIG_DIR"); override != "" {
		configDir = override
	} else if _, err := os.Lstat(configDir); errors.Is(err, os.ErrNotExist) {
		// Preserve an existing pre-rename identity and trust store in place. A
		// symlink is never selected as an implicit compatibility directory.
		legacyDir := filepath.Join(configBase, "UniDrop")
		if info, legacyErr := os.Lstat(legacyDir); legacyErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			configDir = legacyDir
		}
	}
	return configDir, nil
}

func environmentValue(primary, legacy string) string {
	if value := strings.TrimSpace(os.Getenv(primary)); value != "" {
		return value
	}
	return strings.TrimSpace(os.Getenv(legacy))
}

func loadOrCreateControlToken(dir string) (string, error) {
	path := filepath.Join(dir, "control-token")
	readToken := func() (string, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		token := strings.TrimSpace(string(data))
		if len(token) != 64 {
			return "", errors.New("invalid local control token")
		}
		if _, err := hex.DecodeString(token); err != nil {
			return "", errors.New("invalid local control token")
		}
		_ = os.Chmod(path, 0600)
		return token, nil
	}
	if token, err := readToken(); err == nil {
		return token, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read local control token: %w", err)
	}
	token := randomHex(32)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return readToken()
	}
	if err != nil {
		return "", fmt.Errorf("create local control token: %w", err)
	}
	if _, err := io.WriteString(file, token+"\n"); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return token, nil
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
	a.receiveMode = state.ReceiveMode
	if state.DownloadDir != "" {
		a.downloadDir = state.DownloadDir
	}
	if state.Trusted != nil {
		a.trusted = state.Trusted
	}
	for _, address := range state.ManualAddresses {
		if normalized, err := normalizePeerAddress(address); err == nil {
			a.manualPeers[normalized] = struct{}{}
		}
	}
	return nil
}

func (a *App) saveState() error {
	a.mu.RLock()
	manualAddresses := make([]string, 0, len(a.manualPeers))
	for address := range a.manualPeers {
		manualAddresses = append(manualAddresses, address)
	}
	sort.Strings(manualAddresses)
	state := savedState{
		Identity: a.identity, DownloadDir: a.downloadDir, Trusted: a.trusted,
		ReceiveMode: a.receiveMode, ManualAddresses: manualAddresses,
	}
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
		Subject:               pkix.Name{CommonName: "Xendfile " + name},
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
	mux.HandleFunc("/api/summary", a.handleLocalSummary)
	mux.HandleFunc("/api/peers", a.handlePeers)
	mux.HandleFunc("/api/transfers", a.handleTransfers)
	mux.HandleFunc("/api/offers", a.handleLocalOffers)
	mux.HandleFunc("/api/pair", a.requireLocalWrite(a.handleLocalPair))
	mux.HandleFunc("/api/send", a.requireLocalWrite(a.handleLocalSend))
	mux.HandleFunc("/api/add-peer", a.requireLocalWrite(a.handleAddPeer))
	mux.HandleFunc("/api/open-downloads", a.requireLocalWrite(a.handleOpenDownloads))
	mux.HandleFunc("/api/rotate-code", a.requireLocalWrite(a.handleRotateCode))
	mux.HandleFunc("/api/offer-action", a.requireLocalWrite(a.handleOfferAction))
	mux.HandleFunc("/api/receive-mode", a.requireLocalWrite(a.handleReceiveMode))
	mux.HandleFunc("/api/cli/send", a.requireLocalControl(a.handleCLISend))
	mux.HandleFunc("/api/cli/peers", a.requireLocalControl(a.handlePeers))
	mux.HandleFunc("/api/cli/receive-mode", a.requireLocalControlWrite(a.handleReceiveMode))
	mux.HandleFunc("/api/cli/open-downloads", a.requireLocalControlWrite(a.handleOpenDownloads))
	mux.HandleFunc("/api/cli/shutdown", a.requireLocalControlWrite(a.handleShutdown))
	return securityHeaders(mux, true)
}

func (a *App) publicMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/info", a.handlePublicInfo)
	mux.HandleFunc("/api/v1/pair", a.handlePublicPair)
	mux.HandleFunc("/api/v1/offers", a.handleOfferCreate)
	mux.HandleFunc("/api/v1/offers/", a.handleOfferStatus)
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
		if r.Header.Get(legacyUIHeader) != "1" {
			http.Error(w, "local request header required", http.StatusForbidden)
			return
		}
		if !isLoopbackRequest(r) {
			http.Error(w, "local host required", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (a *App) requireLocalControl(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackRequest(r) {
			writeError(w, http.StatusForbidden, errors.New("local host required"))
			return
		}
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(provided) != len(a.controlToken) || !hmac.Equal([]byte(provided), []byte(a.controlToken)) {
			writeError(w, http.StatusUnauthorized, errors.New("local control authorization failed"))
			return
		}
		next(w, r)
	}
}

func (a *App) requireLocalControlWrite(next http.HandlerFunc) http.HandlerFunc {
	authorized := a.requireLocalControl(next)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		authorized(w, r)
	}
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
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
		"download_dir": a.downloadDir, "max_bytes": a.maxBytes, "receive_mode": a.receiveMode,
		"peer_port": a.peerPort, "lan_addresses": localLANAddresses(),
		"discovery_status": a.discovery, "discovery_error": a.discoveryErr,
	}
	a.mu.RUnlock()
	writeJSON(w, http.StatusOK, data)
}

func (a *App) handleLocalSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	now := time.Now()
	a.mu.RLock()
	nearby := 0
	for _, peer := range a.discovered {
		if peerIsOnline(peer, now) {
			nearby++
		}
	}
	pending := 0
	for _, offer := range a.offers {
		if offer.Status == "pending" && !offerExpired(offer, now) {
			pending++
		}
	}
	data := map[string]any{
		"nearby": nearby, "pending": pending, "receive_mode": a.receiveMode,
		"discovery_status": a.discovery,
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
		"minimum_compatible_version": appversion.MinimumCompatibleVersion,
		"name":                       a.identity.Name, "os": runtime.GOOS, "fingerprint": a.fingerprint,
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
		online := peerIsOnline(peer, now)
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

func (a *App) handleLocalOffers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.mu.Lock()
	a.cleanupOffersLocked(time.Now())
	items := make([]*IncomingOffer, 0, len(a.offers))
	for _, offer := range a.offers {
		if offer.Status != "pending" && offer.Status != "receiving" {
			continue
		}
		copyOffer := *offer
		items = append(items, &copyOffer)
	}
	a.mu.Unlock()
	sort.Slice(items, func(i, j int) bool { return items[i].Created > items[j].Created })
	writeJSON(w, http.StatusOK, items)
}

func (a *App) handleOfferAction(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	if err := decodeJSON(r, &request, 4096); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.Action != "accept" && request.Action != "decline" {
		writeError(w, http.StatusBadRequest, errors.New("action must be accept or decline"))
		return
	}
	a.mu.Lock()
	offer := a.offers[request.ID]
	if offer == nil || offer.Status != "pending" || offerExpired(offer, time.Now()) {
		a.mu.Unlock()
		writeError(w, http.StatusConflict, errors.New("this transfer request is no longer pending"))
		return
	}
	if request.Action == "accept" {
		offer.Status = "accepted"
	} else {
		offer.Status = "declined"
	}
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleReceiveMode(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Mode string `json:"mode"`
	}
	if err := decodeJSON(r, &request, 4096); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !validReceiveMode(request.Mode) {
		writeError(w, http.StatusBadRequest, errors.New("receive mode must be ask, trusted, or off"))
		return
	}
	a.mu.Lock()
	a.receiveMode = request.Mode
	if request.Mode == receiveModeOff {
		for _, offer := range a.offers {
			if offer.Status == "pending" {
				offer.Status = "declined"
			}
		}
	}
	a.mu.Unlock()
	if err := a.saveState(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"receive_mode": request.Mode})
}

func (a *App) handleOfferCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	senderID := r.Header.Get(legacySenderID)
	peer, ok := a.authenticate(senderID, r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, errors.New("this device is not paired"))
		return
	}
	var request offerRequest
	if err := decodeJSON(r, &request, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	request.File = sanitizeFilename(request.File)
	if request.File == "" || request.Bytes < 0 || request.Bytes > a.maxBytes {
		writeError(w, http.StatusBadRequest, errors.New("invalid file offer"))
		return
	}
	now := time.Now()
	a.mu.Lock()
	a.cleanupOffersLocked(now)
	if a.receiveMode == receiveModeOff {
		a.mu.Unlock()
		writeError(w, http.StatusForbidden, errors.New("the receiver has paused incoming files"))
		return
	}
	pendingForSender := 0
	activeOffers := 0
	for _, existing := range a.offers {
		if existing.SenderID == senderID && existing.Status == "pending" {
			pendingForSender++
		}
		if existing.Status == "pending" || existing.Status == "accepted" || existing.Status == "receiving" {
			activeOffers++
		}
	}
	if activeOffers >= maxPendingOffers || pendingForSender >= 10 {
		a.mu.Unlock()
		writeError(w, http.StatusTooManyRequests, errors.New("too many pending transfer requests"))
		return
	}
	status := "pending"
	if a.receiveMode == receiveModeTrusted {
		status = "accepted"
	}
	offer := &IncomingOffer{
		ID: randomHex(16), SenderID: senderID, SenderName: peer.Name,
		File: request.File, Bytes: request.Bytes, Status: status,
		Created: now.UTC().Format(time.RFC3339), Expires: now.Add(offerLifetime).UTC().Format(time.RFC3339),
	}
	a.offers[offer.ID] = offer
	a.mu.Unlock()
	if status == "pending" {
		go notifyMessage("Xendfile request from "+peer.Name, request.File+" • "+humanBytes(request.Bytes))
	}
	httpStatus := http.StatusCreated
	if status == "pending" {
		httpStatus = http.StatusAccepted
	}
	writeJSON(w, httpStatus, offerResponse{ID: offer.ID, Status: status})
}

func (a *App) handleOfferStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	senderID := r.Header.Get(legacySenderID)
	if _, ok := a.authenticate(senderID, r.Header.Get("Authorization")); !ok {
		writeError(w, http.StatusUnauthorized, errors.New("this device is not paired"))
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/offers/")
	if len(id) != 32 {
		http.NotFound(w, r)
		return
	}
	a.mu.Lock()
	offer := a.offers[id]
	if offer == nil || offer.SenderID != senderID {
		a.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	if (offer.Status == "pending" || offer.Status == "accepted") && offerExpired(offer, time.Now()) {
		offer.Status = "expired"
	}
	response := offerResponse{ID: offer.ID, Status: offer.Status}
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, response)
}

func (a *App) cleanupOffersLocked(now time.Time) {
	for id, offer := range a.offers {
		created, _ := time.Parse(time.RFC3339, offer.Created)
		if offerExpired(offer, now) || ((!created.IsZero() && now.Sub(created) > 10*time.Minute) && offer.Status != "receiving") {
			delete(a.offers, id)
		}
	}
}

func offerExpired(offer *IncomingOffer, now time.Time) bool {
	expires, err := time.Parse(time.RFC3339, offer.Expires)
	return err != nil || now.After(expires)
}

func validReceiveMode(mode string) bool {
	return mode == receiveModeAsk || mode == receiveModeTrusted || mode == receiveModeOff
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

func (a *App) handleShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	a.shutdownOnce.Do(func() { close(a.shutdown) })
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
	peer.Manual = true
	a.mu.Lock()
	a.manualPeers[peer.Address] = struct{}{}
	a.mu.Unlock()
	a.recordDiscovered(peer)
	if err := a.saveState(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, peer)
}

func (a *App) inspectAddress(raw string) (*DiscoveredPeer, error) {
	return a.inspectAddressContext(context.Background(), raw)
}

func (a *App) inspectAddressContext(ctx context.Context, raw string) (*DiscoveredPeer, error) {
	address, err := normalizePeerAddress(raw)
	if err != nil {
		return nil, err
	}
	client := insecurePairClient(nil)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+address+"/api/v1/info", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(request)
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
		return nil, errors.New("address is not a compatible Xendfile peer")
	}
	presented := responseFingerprint(resp)
	if !hmac.Equal([]byte(presented), []byte(info.Fingerprint)) {
		return nil, errors.New("peer certificate fingerprint does not match its identity response")
	}
	return &DiscoveredPeer{ID: info.ID, Name: cleanDisplayName(info.Name), OS: info.OS, Address: address, Fingerprint: info.Fingerprint, LastSeen: time.Now()}, nil
}

func normalizePeerAddress(raw string) (string, error) {
	address := strings.TrimSpace(raw)
	address = strings.TrimPrefix(address, "https://")
	address = strings.TrimSuffix(address, "/")
	if !strings.Contains(address, ":") {
		address += fmt.Sprintf(":%d", defaultPeerPort)
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return "", errors.New("enter an address such as 192.168.1.20:43338")
	}
	return address, nil
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
		legacyPairDomain, recipientFingerprint, request.SenderID,
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
	peer, err := a.readyPeer(peerID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	body := http.MaxBytesReader(w, r.Body, a.maxBytes)
	if err := a.sendStream(r.Context(), peer, fileName, r.ContentLength, body); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "bytes": r.ContentLength})
}

func (a *App) handleCLISend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request cliSendRequest
	if err := decodeJSON(r, &request, 512<<10); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(request.Target) == "" || len(request.Paths) == 0 || len(request.Paths) > 100 {
		writeError(w, http.StatusBadRequest, errors.New("a target and between 1 and 100 files are required"))
		return
	}
	peer, err := a.resolveTarget(request.Target)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	result := cliSendResult{Target: peer.Trusted.Name, Files: make([]string, 0, len(request.Paths))}
	for _, path := range request.Paths {
		if !filepath.IsAbs(path) {
			writeError(w, http.StatusBadRequest, errors.New("command bridge requires absolute file paths"))
			return
		}
		info, err := os.Stat(path)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("inspect %s: %w", path, err))
			return
		}
		if !info.Mode().IsRegular() {
			writeError(w, http.StatusBadRequest, fmt.Errorf("%s is not a regular file", path))
			return
		}
		if info.Size() > a.maxBytes {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Errorf("%s exceeds the file size limit", filepath.Base(path)))
			return
		}
		file, err := os.Open(path)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		err = a.sendStream(r.Context(), peer, filepath.Base(path), info.Size(), file)
		_ = file.Close()
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		result.Files = append(result.Files, filepath.Base(path))
		result.Bytes += info.Size()
	}
	writeJSON(w, http.StatusCreated, result)
}

type readyPeerConnection struct {
	Trusted    *TrustedPeer
	Discovered *DiscoveredPeer
}

func (a *App) readyPeer(peerID string) (*readyPeerConnection, error) {
	a.mu.RLock()
	trusted := a.trusted[peerID]
	discovered := a.discovered[peerID]
	if trusted != nil {
		copyTrusted := *trusted
		trusted = &copyTrusted
	}
	if discovered != nil {
		copyDiscovered := *discovered
		discovered = &copyDiscovered
	}
	a.mu.RUnlock()
	if trusted == nil || discovered == nil || time.Since(discovered.LastSeen) > 5*time.Minute || trusted.OutgoingToken == "" || !hmac.Equal([]byte(trusted.Fingerprint), []byte(discovered.Fingerprint)) {
		return nil, errors.New("pair with this online peer before sending")
	}
	return &readyPeerConnection{Trusted: trusted, Discovered: discovered}, nil
}

func (a *App) resolveTarget(target string) (*readyPeerConnection, error) {
	needle := normalizeDeviceTarget(target)
	if needle == "" {
		return nil, errors.New("device name is empty")
	}
	a.mu.RLock()
	matches := make([]string, 0, 2)
	for id, peer := range a.discovered {
		if !peerIsOnline(peer, time.Now()) {
			continue
		}
		host, _, _ := net.SplitHostPort(peer.Address)
		keys := []string{normalizeDeviceTarget(id), normalizeDeviceTarget(peer.Name), normalizeDeviceTarget(cliDeviceName(peer.Name)), normalizeDeviceTarget(host)}
		for _, key := range keys {
			if key == needle {
				matches = append(matches, id)
				break
			}
		}
	}
	a.mu.RUnlock()
	if len(matches) > 1 {
		return nil, fmt.Errorf("%q matches multiple devices; use 'xendfile peers' and choose a device address", target)
	}
	if len(matches) == 1 {
		return a.readyPeer(matches[0])
	}
	if strings.Contains(target, ".") || strings.Contains(target, ":") {
		peer, err := a.inspectAddress(target)
		if err == nil {
			a.recordDiscovered(peer)
			return a.readyPeer(peer.ID)
		}
	}
	return nil, fmt.Errorf("could not find %q; run 'xendfile peers' to list visible devices", target)
}

func normalizeDeviceTarget(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, ".")
	value = strings.TrimSuffix(value, ".local")
	value = strings.Join(strings.Fields(value), "-")
	return value
}

func (a *App) sendStream(ctx context.Context, peer *readyPeerConnection, fileName string, size int64, body io.Reader) error {
	fileName = sanitizeFilename(fileName)
	if fileName == "" || size < 0 || size > a.maxBytes {
		return errors.New("invalid file transfer")
	}
	transfer := a.addTransfer("send", peer.Trusted.Name, fileName)
	a.setTransferStatus(transfer, "waiting for approval")
	offerID, err := a.requestOffer(ctx, peer, fileName, size)
	if err != nil {
		a.finishTransfer(transfer, "failed", err)
		return err
	}
	a.setTransferStatus(transfer, "sending")
	requestURL := "https://" + peer.Discovered.Address + "/api/v1/files?name=" + url.QueryEscape(fileName) + "&offer=" + url.QueryEscape(offerID)
	out, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, body)
	if err != nil {
		a.finishTransfer(transfer, "failed", err)
		return err
	}
	out.ContentLength = size
	out.Header.Set("Content-Type", "application/octet-stream")
	a.authorizePeerRequest(out, peer.Trusted)
	resp, err := pinnedClient(peer.Trusted.Fingerprint).Do(out)
	if err != nil {
		err = fmt.Errorf("send to %s: %w", peer.Trusted.Name, err)
		a.finishTransfer(transfer, "failed", err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		err := responseError(resp)
		a.finishTransfer(transfer, "failed", err)
		return err
	}
	a.updateTransferBytes(transfer, size)
	a.finishTransfer(transfer, "complete", nil)
	return nil
}

func (a *App) requestOffer(ctx context.Context, peer *readyPeerConnection, fileName string, size int64) (string, error) {
	body, _ := json.Marshal(offerRequest{File: fileName, Bytes: size})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+peer.Discovered.Address+"/api/v1/offers", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	a.authorizePeerRequest(request, peer.Trusted)
	client := pinnedClient(peer.Trusted.Fingerprint)
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusCreated {
		err := responseError(response)
		_ = response.Body.Close()
		return "", err
	}
	var offer offerResponse
	err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&offer)
	_ = response.Body.Close()
	if err != nil || len(offer.ID) != 32 {
		return "", errors.New("receiver returned an invalid transfer offer")
	}
	if offer.Status == "accepted" {
		return offer.ID, nil
	}
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(offerLifetime)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout.C:
			return "", errors.New("receiver did not respond before the request expired")
		case <-ticker.C:
			statusRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+peer.Discovered.Address+"/api/v1/offers/"+offer.ID, nil)
			a.authorizePeerRequest(statusRequest, peer.Trusted)
			statusResponse, err := client.Do(statusRequest)
			if err != nil {
				return "", err
			}
			if statusResponse.StatusCode != http.StatusOK {
				err := responseError(statusResponse)
				_ = statusResponse.Body.Close()
				return "", err
			}
			err = json.NewDecoder(io.LimitReader(statusResponse.Body, 64<<10)).Decode(&offer)
			_ = statusResponse.Body.Close()
			if err != nil {
				return "", err
			}
			switch offer.Status {
			case "accepted":
				return offer.ID, nil
			case "declined":
				return "", errors.New("receiver declined the file")
			case "expired":
				return "", errors.New("receiver did not respond before the request expired")
			}
		}
	}
}

func (a *App) authorizePeerRequest(request *http.Request, peer *TrustedPeer) {
	request.Header.Set("Authorization", "Bearer "+peer.OutgoingToken)
	request.Header.Set(legacySenderID, a.identity.ID)
}

func (a *App) handleReceive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	senderID := r.Header.Get(legacySenderID)
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
	offerID := r.URL.Query().Get("offer")
	if err := a.beginReceiveOffer(offerID, senderID, name, r.ContentLength); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	offerComplete := false
	defer func() {
		if !offerComplete {
			a.updateOfferStatus(offerID, "failed")
		}
	}()
	transfer := a.addTransfer("receive", peer.Name, name)
	tmp, err := os.CreateTemp(a.downloadDir, ".xendfile-*.part")
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
	offerComplete = true
	a.updateOfferStatus(offerID, "complete")
	a.finishTransfer(transfer, "complete", nil)
	go notifyReceived(filepath.Base(finalPath), peer.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "file": filepath.Base(finalPath), "bytes": written})
}

func (a *App) beginReceiveOffer(id, senderID, file string, size int64) error {
	if len(id) != 32 {
		return errors.New("a valid accepted transfer offer is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	offer := a.offers[id]
	if offer == nil || offer.SenderID != senderID {
		return errors.New("transfer offer was not found")
	}
	if offerExpired(offer, time.Now()) {
		offer.Status = "expired"
		return errors.New("transfer offer expired")
	}
	if offer.Status != "accepted" {
		return fmt.Errorf("transfer offer is %s", offer.Status)
	}
	if offer.File != file || offer.Bytes != size {
		return errors.New("file does not match the accepted transfer offer")
	}
	offer.Status = "receiving"
	return nil
}

func (a *App) updateOfferStatus(id, status string) {
	a.mu.Lock()
	if offer := a.offers[id]; offer != nil {
		offer.Status = status
	}
	a.mu.Unlock()
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

func (a *App) setTransferStatus(t *Transfer, status string) {
	a.mu.Lock()
	t.Status = status
	a.mu.Unlock()
}

func (a *App) updateTransferBytes(t *Transfer, bytes int64) {
	a.mu.Lock()
	t.Bytes = bytes
	a.mu.Unlock()
}

func (a *App) runDiscovery(ctx context.Context) {
	for {
		err := a.discoverySession(ctx)
		if ctx.Err() != nil {
			return
		}
		a.setDiscoveryStatus("retrying", err)
		log.Printf("automatic discovery retrying: %v", err)
		timer := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (a *App) discoverySession(ctx context.Context) error {
	group, err := net.ResolveUDPAddr("udp4", discoveryAddress)
	if err != nil {
		return err
	}
	bindings, err := multicastIPv4Bindings()
	if err != nil {
		return err
	}
	listeners := make([]*net.UDPConn, 0, len(bindings))
	listenerNames := make([]string, 0, len(bindings))
	listenErrors := make([]string, 0)
	for _, binding := range bindings {
		conn, listenErr := net.ListenMulticastUDP("udp4", &binding.Interface, group)
		if listenErr != nil {
			listenErrors = append(listenErrors, binding.Interface.Name+": "+listenErr.Error())
			continue
		}
		_ = conn.SetReadBuffer(64 << 10)
		listeners = append(listeners, conn)
		listenerNames = append(listenerNames, binding.Interface.Name)
	}
	if len(listeners) == 0 {
		return fmt.Errorf("join multicast on LAN interfaces: %s", strings.Join(listenErrors, "; "))
	}
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	sessionContext, cancel := context.WithCancel(ctx)
	defer cancel()
	announceErrors := make(chan error, 1)
	go func() { announceErrors <- a.announceLoop(sessionContext, group) }()
	datagrams := make(chan discoveryDatagram, 64)
	for _, listener := range listeners {
		go readDiscoveryDatagrams(sessionContext, listener, datagrams)
	}
	a.setDiscoveryStatus("active", nil)
	log.Printf("automatic discovery listening on %s", strings.Join(listenerNames, ", "))
	for {
		var datagram discoveryDatagram
		select {
		case <-ctx.Done():
			return ctx.Err()
		case announceErr := <-announceErrors:
			return announceErr
		case datagram = <-datagrams:
			if datagram.Err != nil {
				return datagram.Err
			}
		}
		var packet discoveryPacket
		if err := json.Unmarshal(datagram.Data, &packet); err != nil || packet.Version != protocolVersion || packet.ID == a.identity.ID {
			continue
		}
		if !validID(packet.ID) || !validFingerprint(packet.Fingerprint) || packet.Port < 1 || packet.Port > 65535 {
			continue
		}
		peer := &DiscoveredPeer{
			ID: packet.ID, Name: cleanDisplayName(packet.Name), OS: packet.OS,
			Address:     net.JoinHostPort(datagram.Source.IP.String(), strconv.Itoa(packet.Port)),
			Fingerprint: packet.Fingerprint, LastSeen: time.Now(),
		}
		a.recordDiscovered(peer)
	}
}

type multicastBinding struct {
	Interface net.Interface
	IPv4      []net.IP
}

type discoveryDatagram struct {
	Data   []byte
	Source *net.UDPAddr
	Err    error
}

func multicastIPv4Bindings() ([]multicastBinding, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list network interfaces: %w", err)
	}
	bindings := make([]multicastBinding, 0, len(interfaces))
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagMulticast == 0 || networkInterface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, addressErr := networkInterface.Addrs()
		if addressErr != nil {
			continue
		}
		ipv4 := make([]net.IP, 0, len(addresses))
		for _, address := range addresses {
			var ip net.IP
			switch value := address.(type) {
			case *net.IPNet:
				ip = value.IP
			case *net.IPAddr:
				ip = value.IP
			}
			if ip4 := ip.To4(); ip4 != nil && !ip4.IsLoopback() && !ip4.IsUnspecified() {
				ipv4 = append(ipv4, append(net.IP(nil), ip4...))
			}
		}
		if len(ipv4) > 0 {
			bindings = append(bindings, multicastBinding{Interface: networkInterface, IPv4: ipv4})
		}
	}
	if len(bindings) == 0 {
		return nil, errors.New("no active multicast-capable IPv4 LAN interface")
	}
	return bindings, nil
}

func readDiscoveryDatagrams(ctx context.Context, conn *net.UDPConn, output chan<- discoveryDatagram) {
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
			select {
			case output <- discoveryDatagram{Err: err}:
			case <-ctx.Done():
			}
			return
		}
		data := append([]byte(nil), buffer[:n]...)
		select {
		case output <- discoveryDatagram{Data: data, Source: source}:
		case <-ctx.Done():
			return
		}
	}
}

func (a *App) setDiscoveryStatus(status string, err error) {
	a.mu.Lock()
	a.discovery = status
	if err == nil {
		a.discoveryErr = ""
	} else {
		a.discoveryErr = err.Error()
	}
	a.mu.Unlock()
}

func (a *App) recordDiscovered(peer *DiscoveredPeer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, manual := a.manualPeers[peer.Address]; manual {
		peer.Manual = true
	}
	if existing := a.discovered[peer.ID]; existing != nil && existing.Manual {
		peer.Manual = true
	}
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

func peerIsOnline(peer *DiscoveredPeer, now time.Time) bool {
	return peer != nil && !peer.LastSeen.IsZero() && now.Sub(peer.LastSeen) <= peerLifetime
}

func (a *App) runManualPeerChecks(ctx context.Context) {
	for {
		a.probeManualPeers(ctx)
		timer := time.NewTimer(manualProbeEvery)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (a *App) probeManualPeers(ctx context.Context) {
	a.mu.RLock()
	addresses := make([]string, 0, len(a.manualPeers))
	for address := range a.manualPeers {
		addresses = append(addresses, address)
	}
	a.mu.RUnlock()
	for _, address := range addresses {
		if ctx.Err() != nil {
			return
		}
		probeContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		peer, err := a.inspectAddressContext(probeContext, address)
		cancel()
		if err != nil {
			continue
		}
		peer.Manual = true
		a.recordDiscovered(peer)
	}
}

func (a *App) announceLoop(ctx context.Context, group *net.UDPAddr) error {
	conn, err := net.DialUDP("udp4", nil, group)
	if err != nil {
		return err
	}
	defer conn.Close()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	announce := func() error {
		packet, _ := json.Marshal(discoveryPacket{
			Version: protocolVersion, ID: a.identity.ID, Name: a.identity.Name,
			OS: runtime.GOOS, Port: a.peerPort, Fingerprint: a.fingerprint,
		})
		_, err := conn.Write(packet)
		return err
	}
	if err := announce(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := announce(); err != nil {
				return err
			}
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
		return "Xendfile device"
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

func localLANAddresses() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return []string{}
	}
	seen := make(map[string]bool)
	addresses := make([]string, 0, 4)
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagLoopback != 0 {
			continue
		}
		assigned, err := networkInterface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range assigned {
			ip, _, err := net.ParseCIDR(address.String())
			if err != nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.To4() == nil {
				continue
			}
			value := ip.String()
			if !seen[value] {
				seen[value] = true
				addresses = append(addresses, value)
			}
		}
	}
	sort.Strings(addresses)
	return addresses
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
	notifyMessage("Xendfile received "+file, "From "+sender)
}

func notifyMessage(title, message string) {
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

func humanBytes(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(bytes)
	unit := "B"
	for _, candidate := range units {
		value /= 1024
		unit = candidate
		if value < 1024 {
			break
		}
	}
	return fmt.Sprintf("%.1f %s", value, unit)
}

const uiHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Xendfile</title>
<style>
:root{color-scheme:dark;--ink:#f7f7fa;--muted:#888d99;--card:#0a0c11e8;--line:#22252e;--blue:#7b8cff;--violet:#9a6cff;--green:#52d99a;--red:#ff7088}
*{box-sizing:border-box}html{background:#020304}body{margin:0;min-height:100vh;font:15px/1.45 system-ui,-apple-system,"Segoe UI",sans-serif;color:var(--ink);background:radial-gradient(circle at 14% 0,#161329 0,transparent 34%),radial-gradient(circle at 92% 4%,#101b2a 0,transparent 31%),#020304}
main{width:min(1040px,calc(100% - 28px));margin:0 auto;padding:28px 0 60px}.top{display:flex;align-items:center;justify-content:space-between;gap:16px;margin-bottom:22px}.brand{display:flex;align-items:center;gap:13px}.logo{width:44px;height:44px;border-radius:14px;display:grid;place-items:center;background:linear-gradient(145deg,#4f66ff,#9a5cff);box-shadow:0 10px 35px #675dff35;font-size:23px}.brand h1{margin:0;font-size:24px;letter-spacing:-.5px}.brand p{margin:1px 0 0;color:var(--muted);font-size:13px}.button,button{border:1px solid var(--line);background:#12151c;color:var(--ink);border-radius:11px;padding:10px 14px;font:inherit;font-weight:650;cursor:pointer;transition:.16s ease}button:hover{border-color:#4a4f60;background:#171a23}.primary{background:linear-gradient(135deg,#526dff,#8b5dff);border:0;box-shadow:0 7px 24px #675dff25}.ghost{background:transparent}.grid{display:grid;grid-template-columns:1.45fr .8fr;gap:18px}.card{background:var(--card);border:1px solid var(--line);border-radius:18px;padding:19px;box-shadow:0 18px 65px #0008;backdrop-filter:blur(18px)}h2{font-size:15px;margin:0 0 14px;color:#e8e9ee}.code{font:700 20px/1.25 ui-monospace,SFMono-Regular,monospace;letter-spacing:1px;margin:13px 0 12px;white-space:nowrap}.muted{color:var(--muted)}.small{font-size:12px}.devices{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px;min-height:94px}.device{display:flex;align-items:center;gap:11px;text-align:left;width:100%;padding:13px;background:#080a0f;border:1px solid var(--line);border-radius:13px}.device.selected{border-color:var(--blue);box-shadow:0 0 0 2px #7b8cff1f}.device.offline{opacity:.55}.os{width:37px;height:37px;border-radius:10px;background:#181b24;display:grid;place-items:center;font-size:18px}.device strong,.device span{display:block;overflow:hidden;text-overflow:ellipsis}.status{color:var(--green);font-size:12px}.offline .status{color:var(--muted)}.drop{display:block;cursor:pointer;border:1.5px dashed #343844;border-radius:16px;padding:27px;text-align:center;margin-top:13px;transition:.15s;background:#07090d}.drop:hover,.drop.drag{border-color:var(--blue);background:#7b8cff0d}.drop input{display:none}.drop strong{display:block;font-size:17px;margin-bottom:4px}.sendbar{display:flex;gap:9px;align-items:center;margin-top:12px}.sendbar input,.manual input{min-width:0;flex:1;border:1px solid var(--line);background:#05070a;color:var(--ink);border-radius:10px;padding:10px 12px;font:inherit}.progress{height:5px;background:#171921;border-radius:10px;overflow:hidden;margin-top:10px}.progress i{display:block;height:100%;width:0;background:linear-gradient(90deg,var(--blue),var(--green));transition:.1s}.manual{display:flex;gap:8px;margin-top:10px}.transfer{padding:10px 0;border-top:1px solid #1a1d24;display:grid;grid-template-columns:1fr auto;gap:4px}.transfer:first-child{border-top:0}.transfer strong{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.good{color:var(--green)}.bad{color:var(--red)}.empty{padding:22px 8px;color:var(--muted);text-align:center}.empty strong,.empty span{display:block}.wide{grid-column:1/-1}.toast{position:fixed;right:18px;bottom:18px;z-index:10;max-width:360px;padding:13px 16px;border-radius:12px;background:#151821;border:1px solid #353946;box-shadow:0 15px 50px #000b;display:none}.toast.bad{display:block;border-color:#713442}.toast.good{display:block;border-color:#2c6850}@media(max-width:760px){.grid{grid-template-columns:1fr}.devices{grid-template-columns:1fr}.top{align-items:flex-start}.top>.button{display:none}}
.sectionhead{display:flex;align-items:center;justify-content:space-between;gap:12px}.sectionhead h2{margin:0}.mode{border:1px solid var(--line);background:#05070a;color:var(--ink);border-radius:10px;padding:8px 10px;font:inherit}.offerlist{display:grid;gap:10px;margin-top:14px}.offer{display:flex;align-items:center;gap:13px;background:#07090d;border:1px solid #2a2e39;border-radius:14px;padding:13px}.offericon{width:40px;height:40px;flex:0 0 auto;border-radius:11px;display:grid;place-items:center;background:#171b2a;color:var(--blue);font-size:20px}.offermain{min-width:0;flex:1}.offermain strong,.offermain span{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.offeractions{display:flex;gap:7px}.decline{color:var(--red)}.live{display:flex;align-items:center;gap:7px;color:var(--muted);font-size:12px}.live i{width:7px;height:7px;border-radius:50%;background:var(--green);box-shadow:0 0 12px #52d99a99}.fallback{margin-top:10px;border-top:1px solid #1b1e25;padding-top:10px}.fallback summary{cursor:pointer;color:var(--muted);font-size:12px;list-style:none}.fallback summary::-webkit-details-marker{display:none}.fallback summary:before{content:'＋';margin-right:6px}.fallback[open] summary:before{content:'−'}.addresshint{margin-top:7px;color:#666b76;font-size:11px}.radar{width:36px;height:36px;margin:0 auto 9px;border:1px solid #4e5680;border-radius:50%;position:relative}.radar:after{content:'';position:absolute;inset:7px;border:1px solid #30364e;border-radius:50%;animation:pulse 1.8s infinite}.shellbar{display:none}.pairaction{white-space:nowrap}@keyframes pulse{0%{transform:scale(.65);opacity:.3}60%{transform:scale(1.35);opacity:1}100%{transform:scale(1.55);opacity:0}}
body.compact{background:#020304}body.compact main{width:100%;padding:12px}body.compact .top{margin:1px 2px 12px}body.compact .logo{width:36px;height:36px;border-radius:11px;font-size:18px}body.compact .brand h1{font-size:19px}body.compact .top>.button{display:none}body.compact .grid{display:flex;flex-direction:column;gap:9px}body.compact .card{padding:14px;border-radius:15px;box-shadow:none;background:#080a0ef2}body.compact #incomingCard{order:1}body.compact #nearbyCard{order:2}body.compact #sendCard{order:3}body.compact #activityCard{order:4}body.compact #pairCard{order:5}body.compact .devices{grid-template-columns:1fr;min-height:74px}body.compact .empty{padding:16px 6px}body.compact .drop{padding:18px;margin-top:9px}body.compact .drop strong{font-size:15px}body.compact .code{font-size:17px}body.compact .shellbar{display:flex;order:6;gap:8px;padding:2px}body.compact .shellbar button{flex:1;color:var(--muted);font-size:12px;background:#080a0e}body.compact .toast{position:fixed;left:12px;right:12px;bottom:12px;max-width:none}body.compact #pairCard .paircopy{font-size:11px}@media(max-width:560px){.sectionhead,.offer{align-items:stretch;flex-direction:column}.offericon{display:none}.offeractions button{flex:1}.mode{width:100%}}
</style>
</head>
<body><main>
<header class="top"><div class="brand"><div class="logo">⇄</div><div><h1>Xendfile</h1><p id="deviceName">Secure local file sharing</p></div></div><button class="button" onclick="openDownloads()">Open received files</button></header>
<div class="grid">
<section class="card" id="nearbyCard"><div class="sectionhead"><h2>Nearby devices</h2><span class="live"><i></i><span id="discoveryLabel">Searching automatically</span></span></div><div id="devices" class="devices"><div class="empty"><span class="radar"></span><strong>Searching nearby…</strong><span class="small">Xendfile scans this network automatically.</span></div></div><details class="fallback"><summary>Connect by address instead</summary><div class="manual"><input id="manual" placeholder="Other device address, e.g. 192.168.0.25:43338"><button onclick="addPeer()">Add</button></div><div class="addresshint" id="addressHint"></div></details></section>
<aside class="card" id="pairCard"><h2>Pair this device</h2><div class="muted small paircopy">Use this one-time key on the sending device.</div><div id="code" class="code">----&nbsp;----&nbsp;----&nbsp;----</div><button class="ghost small" onclick="copyCode()">Copy key</button> <button class="ghost small" onclick="rotateCode()">Rotate</button><div class="muted small" style="margin-top:13px">TLS 1.3 • certificate pinning • local network only</div></aside>
<section class="card wide" id="incomingCard"><div class="sectionhead"><h2>Incoming requests</h2><select id="receiveMode" class="mode" onchange="setReceiveMode()" aria-label="Receive mode"><option value="ask">Ask every time</option><option value="trusted">Auto-accept paired devices</option><option value="off">Receiving paused</option></select></div><div id="offers" class="offerlist"><div class="empty">No one is waiting to send you a file</div></div></section>
<section class="card" id="sendCard"><h2>Send files <span id="selectedLabel" class="muted">— choose a device</span></h2><label class="drop" id="drop"><input id="files" type="file" multiple><strong>Drop files here</strong><span class="muted">or click to choose files</span></label><div class="sendbar"><input id="pairCode" maxlength="19" autocomplete="off" placeholder="Other device's pairing key"><button class="ghost pairaction" id="pairButton" onclick="pairNow()">Pair</button><button class="primary" id="send" onclick="sendSelected()">Send</button></div><div class="progress"><i id="progress"></i></div><div id="queue" class="muted small" style="margin-top:7px"></div></section>
<aside class="card" id="activityCard"><h2>Recent activity</h2><div id="transfers"><div class="empty">No transfers yet</div></div></aside>
<footer class="shellbar"><button onclick="shellAction('open')">Open full window</button><button onclick="shellAction('quit')">Quit Xendfile</button></footer>
</div></main><div id="toast" class="toast"></div>
<script>
const compact=new URLSearchParams(location.search).get('compact')==='1';document.body.classList.toggle('compact',compact);let selected=null,chosen=[],peers=[];const $=id=>document.getElementById(id);
async function api(path,options={}){options.headers={...(options.headers||{}),'X-UniDrop-UI':'1'};const r=await fetch(path,options);let body={};try{body=await r.json()}catch{}if(!r.ok)throw new Error(body.error||r.statusText);return body}
function esc(s){return String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))}
function icon(os){return os==='darwin'?'●':os==='windows'?'⊞':os==='linux'?'◆':'◇'}
async function refresh(){try{const [info,p,t,o]=await Promise.all([api('/api/info'),api('/api/peers'),api('/api/transfers'),api('/api/offers')]);peers=p;const online=p.filter(x=>x.online);$('deviceName').textContent=info.name+' • '+(online.length?online.length+' nearby':'scanning nearby');$('code').textContent=info.pairing_code;$('receiveMode').value=info.receive_mode;$('discoveryLabel').textContent=online.length?online.length+' found':info.discovery_status==='retrying'?'Retrying search':'Searching automatically';const addresses=(info.lan_addresses||[]).map(x=>x+':'+info.peer_port);$('addressHint').textContent=addresses.length?'This device: '+addresses.join(' • '):'Automatic search is on. Manual address is only a fallback.';renderPeers();renderTransfers(t);renderOffers(o)}catch(e){toast(e.message,false)}}
function renderPeers(){const online=peers.filter(p=>p.online);if(!online.length){$('devices').innerHTML='<div class="empty"><span class="radar"></span><strong>Searching nearby…</strong><span class="small">Keep Xendfile open on the other computer.</span></div>';return}$('devices').innerHTML=online.map(p=>'<button class="device '+(selected===p.id?'selected':'')+'" data-peer="'+p.id+'"><span class="os">'+icon(p.os)+'</span><span><strong>'+esc(p.name)+'</strong><span class="status">'+(p.trusted?'Paired and ready':'Tap to pair')+'</span></span></button>').join('');document.querySelectorAll('[data-peer]').forEach(b=>b.onclick=()=>choose(b.dataset.peer))}
function choose(id){selected=id;const p=peers.find(x=>x.id===id);$('selectedLabel').textContent=p?'— '+p.name:'';const needsPair=!p?.trusted;$('pairCode').style.display=needsPair?'block':'none';$('pairButton').style.display=needsPair?'block':'none';renderPeers()}
function renderTransfers(items){$('transfers').innerHTML=items.length?items.slice(0,6).map(t=>'<div class="transfer"><strong>'+(t.direction==='send'?'↑':'↓')+' '+esc(t.file)+'</strong><span class="'+(t.status==='complete'?'good':t.status==='failed'?'bad':'muted')+'">'+esc(t.status)+'</span><span class="muted small">'+esc(t.peer)+'</span><span class="muted small">'+size(t.bytes)+'</span></div>').join(''):'<div class="empty">No transfers yet</div>'}
function renderOffers(items){$('offers').innerHTML=items.length?items.map(o=>'<div class="offer"><span class="offericon">↓</span><span class="offermain"><strong>'+esc(o.file)+'</strong><span class="muted">From '+esc(o.sender_name)+' • '+size(o.bytes)+'</span></span>'+(o.status==='pending'?'<span class="offeractions"><button class="ghost decline" data-offer-action="decline" data-offer="'+o.id+'">Decline</button><button class="primary" data-offer-action="accept" data-offer="'+o.id+'">Accept</button></span>':'<span class="status">Receiving…</span>')+'</div>').join(''):'<div class="empty">No one is waiting to send you a file</div>';document.querySelectorAll('[data-offer-action]').forEach(b=>b.onclick=()=>actOffer(b.dataset.offer,b.dataset.offerAction))}
function size(n){if(!n)return '0 B';const u=['B','KB','MB','GB','TB'];let i=0;while(n>=1024&&i<u.length-1){n/=1024;i++}return n.toFixed(i?1:0)+' '+u[i]}
const drop=$('drop'),input=$('files');drop.onclick=()=>input.click();input.onchange=()=>setFiles([...input.files]);['dragenter','dragover'].forEach(e=>drop.addEventListener(e,x=>{x.preventDefault();drop.classList.add('drag')}));['dragleave','drop'].forEach(e=>drop.addEventListener(e,x=>{x.preventDefault();drop.classList.remove('drag')}));drop.addEventListener('drop',e=>setFiles([...e.dataTransfer.files]));function setFiles(f){chosen=f;$('queue').textContent=f.length?f.length+' file'+(f.length===1?'':'s')+' • '+size(f.reduce((n,x)=>n+x.size,0)):''}
async function pairSelected(announce=true){let p=peers.find(x=>x.id===selected);if(!p)throw new Error('Choose a nearby device first');if(p.trusted)return;const code=$('pairCode').value.trim();if(!/^[0-9a-fA-F]{4}(-?[0-9a-fA-F]{4}){3}$/.test(code))throw new Error("Enter the pairing key shown on "+p.name);await api('/api/pair',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({peer_id:p.id,code})});await refresh();p=peers.find(x=>x.id===selected);if(!p?.trusted)throw new Error('Pairing did not complete');if(announce)toast('Paired securely with '+p.name,true)}
async function pairNow(){try{await pairSelected(true)}catch(e){toast(e.message,false)}}
async function ensurePaired(){const p=peers.find(x=>x.id===selected);if(!p)throw new Error('Choose an online device');if(!p.trusted)await pairSelected(false)}
async function sendSelected(){try{if(!chosen.length)throw new Error('Choose at least one file');await ensurePaired();$('send').disabled=true;for(let i=0;i<chosen.length;i++){const file=chosen[i];$('queue').textContent='Sending '+(i+1)+' of '+chosen.length+': '+file.name;await upload(file,n=>{$('progress').style.width=((i+n/file.size)/chosen.length*100)+'%'})}toast('Files sent securely',true);chosen=[];input.value='';$('queue').textContent='';setTimeout(()=>$('progress').style.width='0',900);await refresh()}catch(e){toast(e.message,false)}finally{$('send').disabled=false}}
function upload(file,onProgress){return new Promise((resolve,reject)=>{const x=new XMLHttpRequest();x.open('POST','/api/send?peer='+encodeURIComponent(selected)+'&filename='+encodeURIComponent(file.name));x.setRequestHeader('X-UniDrop-UI','1');x.setRequestHeader('Content-Type','application/octet-stream');x.upload.onprogress=e=>{if(e.lengthComputable)onProgress(e.loaded)};x.onload=()=>{let b={};try{b=JSON.parse(x.responseText)}catch{};x.status>=200&&x.status<300?resolve(b):reject(new Error(b.error||x.statusText))};x.onerror=()=>reject(new Error('Network connection failed'));x.send(file)})}
async function addPeer(){try{const address=$('manual').value.trim();if(!address)throw new Error('Enter the other device address');await api('/api/add-peer',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({address})});$('manual').value='';await refresh();toast('Device added',true)}catch(e){toast(e.message,false)}}
async function rotateCode(){try{await api('/api/rotate-code',{method:'POST'});await refresh()}catch(e){toast(e.message,false)}}
async function actOffer(id,action){try{await api('/api/offer-action',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({id,action})});toast(action==='accept'?'Transfer accepted':'Transfer declined',action==='accept');await refresh()}catch(e){toast(e.message,false)}}
async function setReceiveMode(){try{const mode=$('receiveMode').value;await api('/api/receive-mode',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({mode})});toast(mode==='off'?'Incoming files paused':mode==='ask'?'Approval required for every file':'Paired devices will be accepted automatically',true);await refresh()}catch(e){toast(e.message,false)}}
async function copyCode(){try{await navigator.clipboard.writeText($('code').textContent);toast('Pairing key copied',true)}catch(e){toast('Copy the key manually',false)}}
async function openDownloads(){try{await api('/api/open-downloads',{method:'POST'})}catch(e){toast(e.message,false)}}
function shellAction(action){if(window.webkit?.messageHandlers?.xendfile){window.webkit.messageHandlers.xendfile.postMessage(action);return}if(action==='open')location.href='/' ;else toast('Quit from the Xendfile menu-bar app',false)}
let toastTimer;function toast(message,ok){const t=$('toast');t.textContent=message;t.className='toast '+(ok?'good':'bad');clearTimeout(toastTimer);toastTimer=setTimeout(()=>t.className='toast',4500)}
refresh();setInterval(refresh,compact?1800:3000);
</script></body></html>`
