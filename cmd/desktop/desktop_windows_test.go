package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func testPairing() pairing {
	return pairing{
		URL:   "https://clip.example.test:8443",
		Token: strings.Repeat("a", 32),
		Key:   base64.StdEncoding.EncodeToString(make([]byte, 32)),
	}
}

func TestTrayIcon(t *testing.T) {
	for _, size := range []uintptr{16, 32} {
		icon, err := newTrayIcon(size, size)
		if err != nil {
			t.Fatalf("create %d px tray icon: %v", size, err)
		}
		if ok, _, err := destroyIcon.Call(icon); ok == 0 {
			t.Fatalf("destroy %d px tray icon: %v", size, err)
		}
	}
}

func TestClientConfig(t *testing.T) {
	p := testPairing()
	endpoint, g, err := p.clientConfig()
	if err != nil || endpoint != "wss://clip.example.test:8443/desktop" || g == nil {
		t.Fatal("valid config did not produce a desktop WebSocket endpoint and cipher")
	}
	for _, tc := range []struct {
		name   string
		change func(*pairing)
	}{
		{"insecure URL", func(p *pairing) { p.URL = "http://clip.example.test" }},
		{"URL credentials", func(p *pairing) { p.URL = "https://user:password@clip.example.test" }},
		{"URL path", func(p *pairing) { p.URL += "/clip" }},
		{"URL query", func(p *pairing) { p.URL += "?" }},
		{"invalid port", func(p *pairing) { p.URL = "https://clip.example.test:65536" }},
		{"short token", func(p *pairing) { p.Token = "short" }},
		{"invalid token", func(p *pairing) { p.Token += "\n" }},
		{"invalid key", func(p *pairing) { p.Key = "!" }},
		{"wrong key size", func(p *pairing) { p.Key = base64.StdEncoding.EncodeToString(make([]byte, 16)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testPairing()
			tc.change(&p)
			if _, _, err := p.clientConfig(); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestPairingStorage(t *testing.T) {
	name := filepath.Join(t.TempDir(), "Buffer", "pair.bin")
	if _, err := loadPairing(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing pairing was not detected")
	}
	p := testPairing()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal("could not encode test pairing")
	}
	if err := savePairing(name, b); err != nil {
		t.Fatalf("could not save DPAPI pairing: %v", err)
	}
	if err := savePairing(name, b); !errors.Is(err, os.ErrExist) {
		t.Fatal("existing pairing was not protected against overwrite")
	}
	if got, err := loadPairing(name); err != nil || got != p {
		t.Fatal("could not round-trip the DPAPI pairing")
	}
	if err := os.WriteFile(name, []byte("invalid DPAPI data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPairing(name); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid pairing was not distinguished from missing pairing")
	}
}

func TestClaimInstance(t *testing.T) {
	name := filepath.Join(t.TempDir(), "pair.bin")
	release, err := claimInstance(name)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, err := claimInstance(strings.ToUpper(name)); err == nil {
		duplicate()
		release()
		t.Fatal("a second instance claimed the same config")
	}
	release()
	release, err = claimInstance(name)
	if err != nil {
		t.Fatal("config could not be claimed again after exit")
	}
	release()
}

func TestClientReconnect(t *testing.T) {
	p := testPairing()
	var attempts atomic.Int32
	upgrader := websocket.Upgrader{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/desktop" || r.Header.Get("Authorization") != "Bearer "+p.Token {
			t.Error("unexpected desktop path or bearer authorization")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		attempt := attempts.Add(1)
		if attempt == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error("test relay could not upgrade desktop connection")
			return
		}
		defer conn.Close()
		if attempt > 2 {
			conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			conn.ReadMessage() // Keep the reconnected socket open until the client is canceled.
		}
	}))
	defer server.Close()
	p.URL = server.URL
	endpoint, g, err := p.clientConfig()
	if err != nil {
		t.Fatal(err)
	}
	dialer := websocket.Dialer{TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig, HandshakeTimeout: 2 * time.Second}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	statuses := make(chan connectionStatus, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runClient(ctx, endpoint, p.Token, g, &dialer, func(status connectionStatus) { statuses <- status })
	}()
	for _, want := range []connectionStatus{authRejected, connected, retrying, connected} {
		select {
		case got := <-statuses:
			if got != want {
				t.Fatalf("status: got %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("client did not stop with an active socket")
	}
}
