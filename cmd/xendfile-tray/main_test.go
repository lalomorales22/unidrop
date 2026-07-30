package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestLegacyConfigOverrideRemainsCompatible(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "legacy")
	t.Setenv("XENDFILE_CONFIG_DIR", "")
	t.Setenv("UNIDROP_CONFIG_DIR", legacy)
	if got := compatibleConfigDirectory(t.TempDir()); got != legacy {
		t.Fatalf("config directory = %q, want %q", got, legacy)
	}
}

func TestDBusWireSignatures(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"icon pixmaps", []iconPixmap{}, "a(iiay)"},
		{"tooltip", toolTip{}, "(sa(iiay)ss)"},
		{"menu layout", menuLayout{}, "(ia{sv}av)"},
		{"menu property groups", []menuPropertyGroup{}, "a(ia{sv})"},
		{"menu events", []menuEvent{}, "a(isvu)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := dbus.SignatureOf(test.value).String(); got != test.want {
				t.Fatalf("signature=%q want %q", got, test.want)
			}
		})
	}
}

func TestCoreClientSummaryAndAuthorizedActions(t *testing.T) {
	token := strings.Repeat("a", 64)
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "control-token"), []byte(token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	actions := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/summary" {
			_ = json.NewEncoder(w).Encode(coreSummary{Nearby: 2, Pending: 1, ReceiveMode: "ask", DiscoveryState: "active"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		actions <- r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := &coreClient{baseURL: server.URL + "/", configDir: configDir, http: server.Client()}
	summary, err := client.summary()
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Available || summary.Nearby != 2 || summary.Pending != 1 || summary.ReceiveMode != "ask" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if err := client.setReceiveMode("trusted"); err != nil {
		t.Fatal(err)
	}
	if err := client.openDownloads(); err != nil {
		t.Fatal(err)
	}
	if err := client.shutdown(); err != nil {
		t.Fatal(err)
	}
	want := []string{"/api/cli/receive-mode", "/api/cli/open-downloads", "/api/cli/shutdown"}
	for _, path := range want {
		select {
		case got := <-actions:
			if got != path {
				t.Fatalf("action=%q want %q", got, path)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s", path)
		}
	}
}

func TestCoreClientRejectsNonLoopbackControlURL(t *testing.T) {
	for _, address := range []string{"https://127.0.0.1:43337", "http://192.168.1.10:43337", "http://example.com"} {
		if _, err := newCoreClient(address); err == nil {
			t.Fatalf("accepted unsafe control URL %q", address)
		}
	}
}

func TestMenuLayoutReflectsLiveSummary(t *testing.T) {
	app := &trayApp{
		summary:  coreSummary{Available: true, Nearby: 3, Pending: 2, ReceiveMode: "trusted"},
		revision: 7,
		done:     make(chan struct{}),
	}
	revision, layout, err := app.menuLayout(0, -1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if revision != 7 || len(layout.Children) != len(app.menuItems()) {
		t.Fatalf("revision=%d children=%d", revision, len(layout.Children))
	}
	status := app.menuProperties(menuStatus, nil)["label"].Value().(string)
	if !strings.Contains(status, "3 nearby") || !strings.Contains(status, "2 waiting") {
		t.Fatalf("status label=%q", status)
	}
	trusted := app.menuProperties(menuModeTrusted, nil)["toggle-state"].Value().(int32)
	ask := app.menuProperties(menuModeAsk, nil)["toggle-state"].Value().(int32)
	if trusted != 1 || ask != 0 {
		t.Fatalf("radio state ask=%d trusted=%d", ask, trusted)
	}
	filtered := app.menuProperties(menuOpen, []string{"label"})
	if len(filtered) != 1 || filtered["label"].Value().(string) != "Open Xendfile — 2 waiting" {
		t.Fatalf("filtered properties=%v", filtered)
	}
}

func TestGeneratedIconsAreCompleteARGBPixmaps(t *testing.T) {
	for _, state := range []iconState{iconOffline, iconNormal, iconNearby, iconAttention} {
		icon := makeIcon(22, state)
		if icon.Width != 22 || icon.Height != 22 || len(icon.Data) != 22*22*4 {
			t.Fatalf("state=%d invalid pixmap dimensions", state)
		}
		opaque := false
		for index := 0; index < len(icon.Data); index += 4 {
			if icon.Data[index] == 255 {
				opaque = true
				break
			}
		}
		if !opaque {
			t.Fatalf("state=%d icon is fully transparent", state)
		}
	}
}
