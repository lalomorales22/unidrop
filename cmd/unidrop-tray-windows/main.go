// UniDrop's Windows shell owns the notification-area experience while the
// console-capable core continues to own discovery, trust, and file transfer.
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	appversion "unidrop/internal/version"
)

var appVersion = appversion.Current

const defaultUIURL = "http://127.0.0.1:43337/"

type coreSummary struct {
	Nearby         int    `json:"nearby"`
	Pending        int    `json:"pending"`
	ReceiveMode    string `json:"receive_mode"`
	DiscoveryState string `json:"discovery_status"`
	Available      bool   `json:"-"`
}

type coreClient struct {
	baseURL   string
	configDir string
	http      *http.Client
}

func newCoreClient(baseURL string) (*coreClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, errors.New("the UniDrop tray UI must use an HTTP loopback address")
	}
	loopback := parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost" || parsed.Hostname() == "::1"
	cleanPath := parsed.Path == "" || parsed.Path == "/"
	if parsed.Scheme != "http" || !loopback || !cleanPath || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("the UniDrop tray UI must use an HTTP loopback address")
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("find config directory: %w", err)
	}
	configDir = filepath.Join(configDir, "UniDrop")
	if override := strings.TrimSpace(os.Getenv("UNIDROP_CONFIG_DIR")); override != "" {
		configDir = override
	}
	return &coreClient{
		baseURL:   strings.TrimRight(baseURL, "/") + "/",
		configDir: configDir,
		http:      &http.Client{Timeout: 2 * time.Second},
	}, nil
}

func (c *coreClient) summary() (coreSummary, error) {
	var summary coreSummary
	request, err := http.NewRequest(http.MethodGet, c.baseURL+"api/summary", nil)
	if err != nil {
		return summary, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return summary, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return summary, responseError(response)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&summary); err != nil {
		return summary, err
	}
	summary.Available = true
	return summary, nil
}

func (c *coreClient) setReceiveMode(mode string) error {
	if mode != "ask" && mode != "trusted" && mode != "off" {
		return errors.New("invalid receive mode")
	}
	return c.post("api/cli/receive-mode", map[string]string{"mode": mode})
}

func (c *coreClient) openDownloads() error {
	return c.post("api/cli/open-downloads", nil)
}

func (c *coreClient) shutdown() error {
	return c.post("api/cli/shutdown", nil)
}

func (c *coreClient) post(path string, body any) error {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	token, err := c.controlToken()
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, c.baseURL+strings.TrimLeft(path, "/"), bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return responseError(response)
	}
	return nil
}

func (c *coreClient) controlToken() (string, error) {
	data, err := os.ReadFile(filepath.Join(c.configDir, "control-token"))
	if err != nil {
		return "", fmt.Errorf("read UniDrop control token: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if len(token) != 64 {
		return "", errors.New("invalid UniDrop control token")
	}
	if _, err := hex.DecodeString(token); err != nil {
		return "", errors.New("invalid UniDrop control token")
	}
	return token, nil
}

func responseError(response *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	var payload struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(data, &payload)
	if strings.TrimSpace(payload.Error) != "" {
		return errors.New(payload.Error)
	}
	return fmt.Errorf("UniDrop returned %s", response.Status)
}

type iconState int

const (
	iconOffline iconState = iota
	iconNormal
	iconNearby
	iconAttention
)

func statusPresentation(summary coreSummary) (tooltip string, state iconState) {
	if !summary.Available {
		return "UniDrop - reconnecting", iconOffline
	}
	if summary.Pending > 0 {
		return fmt.Sprintf("UniDrop - %d request%s waiting", summary.Pending, pluralSuffix(summary.Pending)), iconAttention
	}
	if summary.Nearby > 0 {
		return fmt.Sprintf("UniDrop - %d nearby", summary.Nearby), iconNearby
	}
	if summary.ReceiveMode == "off" {
		return "UniDrop - receiving paused", iconNormal
	}
	return "UniDrop - searching nearby", iconNormal
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func summaryLabel(summary coreSummary) string {
	if !summary.Available {
		return "UniDrop - reconnecting"
	}
	return fmt.Sprintf("UniDrop - %d nearby - %d waiting", summary.Nearby, summary.Pending)
}

func waitForCore(client *coreClient, timeout time.Duration) coreSummary {
	deadline := time.Now().Add(timeout)
	for {
		if summary, err := client.summary(); err == nil {
			return summary
		}
		if time.Now().After(deadline) {
			return coreSummary{}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func setupLog() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	logDir := filepath.Join(configDir, "UniDrop")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(logDir, "tray.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err == nil {
		log.SetOutput(file)
	}
}

func main() {
	baseURL := flag.String("ui", defaultUIURL, "UniDrop loopback control-panel URL")
	showVersion := flag.Bool("version", false, "show version and exit")
	openOnStart := flag.Bool("open", false, "open the control panel after starting")
	flag.Parse()
	if *showVersion {
		fmt.Printf("UniDrop Windows Tray %s\n", appVersion)
		return
	}
	setupLog()
	if err := runTray(*baseURL, *openOnStart); err != nil {
		log.Printf("UniDrop Windows tray stopped: %v", err)
	}
}
