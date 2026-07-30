package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoreClientRequiresLoopback(t *testing.T) {
	for _, value := range []string{
		"https://127.0.0.1:43337/",
		"http://example.com:43337/",
		"http://user@127.0.0.1:43337/",
		"http://127.0.0.1:43337/path",
	} {
		if _, err := newCoreClient(value); err == nil {
			t.Fatalf("accepted unsafe tray URL %q", value)
		}
	}
	if _, err := newCoreClient("http://127.0.0.1:43337/"); err != nil {
		t.Fatalf("rejected loopback tray URL: %v", err)
	}
}

func TestAuthenticatedCoreBridge(t *testing.T) {
	token := strings.Repeat("a", 64)
	var receivedMode string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/summary":
			_ = json.NewEncoder(w).Encode(coreSummary{Nearby: 2, Pending: 1, ReceiveMode: "ask"})
		case "/api/cli/receive-mode":
			if r.Header.Get("Authorization") != "Bearer "+token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			receivedMode = body["mode"]
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := newCoreClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.configDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(client.configDir, "control-token"), []byte(token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	summary, err := client.summary()
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Available || summary.Nearby != 2 || summary.Pending != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if err := client.setReceiveMode("trusted"); err != nil {
		t.Fatal(err)
	}
	if receivedMode != "trusted" {
		t.Fatalf("receive mode = %q", receivedMode)
	}
	if err := client.setReceiveMode("invalid"); err == nil {
		t.Fatal("invalid receive mode was accepted")
	}
}

func TestStatusPresentation(t *testing.T) {
	tests := []struct {
		summary coreSummary
		want    string
		state   iconState
	}{
		{coreSummary{}, "reconnecting", iconOffline},
		{coreSummary{Available: true}, "searching nearby", iconNormal},
		{coreSummary{Available: true, Nearby: 3}, "3 nearby", iconNearby},
		{coreSummary{Available: true, Pending: 2}, "2 requests waiting", iconAttention},
	}
	for _, test := range tests {
		tooltip, state := statusPresentation(test.summary)
		if !strings.Contains(tooltip, test.want) || state != test.state {
			t.Errorf("presentation(%+v) = %q/%v", test.summary, tooltip, state)
		}
	}
}

func TestGeneratedIconsContainOpaquePixels(t *testing.T) {
	for state := iconOffline; state <= iconAttention; state++ {
		pixels := make([]byte, 32*32*4)
		drawStateIcon(pixels, 32, state)
		opaque := 0
		for index := 3; index < len(pixels); index += 4 {
			if pixels[index] != 0 {
				opaque++
			}
		}
		if opaque < 100 {
			t.Fatalf("state %d generated only %d opaque pixels", state, opaque)
		}
	}
}
