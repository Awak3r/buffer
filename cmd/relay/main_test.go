package main

import (
	"buffer/internal/delivery"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestDelivery(t *testing.T) {
	const token = "test-secret"
	s := httptest.NewServer(&relay{token: token})
	defer s.Close()
	data := bytes.Repeat([]byte{42}, 100)
	post := func(auth string) (int, error) {
		req, _ := http.NewRequest("POST", s.URL+"/clip", bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+auth)
		res, err := s.Client().Do(req)
		if err != nil {
			return 0, err
		}
		defer res.Body.Close()
		io.Copy(io.Discard, res.Body)
		return res.StatusCode, nil
	}
	if code, e := post("wrong"); code != 401 || e != nil {
		t.Fatalf("unauthorized: %d %v", code, e)
	}
	if code, e := post(token); code != 503 || e != nil {
		t.Fatalf("offline: %d %v", code, e)
	}
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.URL, "http")+"/desktop", http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(6 * time.Second))
	for _, want := range []int{204, 422} {
		result := make(chan int, 1)
		go func() { code, _ := post(token); result <- code }()
		_, raw, err := c.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var msg packet
		if json.Unmarshal(raw, &msg) != nil || msg.ID == "" || !bytes.Equal(msg.Data, data) {
			t.Fatal("wrong payload")
		}
		if code, e := post(token); code != 409 || e != nil {
			t.Fatalf("busy: %d %v", code, e)
		}
		if err = c.WriteJSON(receipt{ID: "another-message", OK: true}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-result:
			t.Fatal("accepted wrong receipt")
		case <-time.After(25 * time.Millisecond):
		}
		if err = c.WriteJSON(receipt{ID: msg.ID, OK: want == 204}); err != nil {
			t.Fatal(err)
		}
		if code := <-result; code != want {
			t.Fatalf("HTTP %d, want %d", code, want)
		}
	}
}

// TestAndroidPayload uses an AES-GCM packet produced by Android's Pair.seal on the JVM.
func TestAndroidPayload(t *testing.T) {
	body, err := base64.StdEncoding.DecodeString("76Lmsn8DGb9eX+T0Ltd+2TzJbOackQsOk5jfkp+YsHnJ8XHPseUcYIsF+IhdfDOQGDNg23oWy2jLwp6N6HiqCN6CyNaoNCDsTiRqr47G0i4=")
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewTLSServer(&relay{token: "test"})
	defer s.Close()
	client := s.Client()
	d := websocket.Dialer{TLSClientConfig: client.Transport.(*http.Transport).TLSClientConfig}
	c, _, err := d.Dial("wss"+strings.TrimPrefix(s.URL, "https")+"/desktop", http.Header{"Authorization": []string{"Bearer test"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	b, _ := aes.NewCipher(make([]byte, 32))
	g, _ := cipher.NewGCM(b)
	written := make(chan string, 1)
	go delivery.Receive(c, g, func(value string) error { written <- value; return nil }, nil, nil)
	for _, expected := range []int{204, 422} {
		req, _ := http.NewRequest("POST", s.URL+"/clip", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer test")
		res, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if res.StatusCode != expected {
			t.Fatalf("HTTP %d, want %d", res.StatusCode, expected)
		}
		if expected == 204 {
			select {
			case v := <-written:
				if v != "Привет с телефона\nСтрока 2 📋" {
					t.Fatal("text mismatch")
				}
			default:
				t.Fatal("early acknowledgement")
			}
		}
		body[len(body)-1] ^= 1
	}
}

func TestPhoneNameAnnouncement(t *testing.T) {
	const token = "test"
	const phoneAAD = "buffer/phone-name/v1"
	const phoneName = "Тестовый телефон 📱"
	const clipboardText = "Desktop clipboard\n📋"
	// Java's AES/GCM/NoPadding with a zero AES-256 key, nonce 000102030405060708090a0b,
	// and phoneAAD over the UTF-8 phoneName, packed as nonce || ciphertext || tag.
	fixture, err := base64.StdEncoding.DecodeString("AAECAwQFBgcICQoLWGnj59F8XnsSxoJXLIgOtXS9KkoKTTQmehpg2zPlsk8caC8Uun6eqpNlPbFLqFkXeu4IzQ==")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := aes.NewCipher(make([]byte, 32))
	g, _ := cipher.NewGCM(b)
	seal := func(plain, aad string) []byte {
		nonce := make([]byte, g.NonceSize())
		rand.Read(nonce)
		return g.Seal(nonce, nonce, []byte(plain), []byte(aad))
	}

	for _, peer := range []struct {
		name      string
		supported bool
	}{
		{"unsupported peer", false},
		{"supporting peer", true},
	} {
		t.Run(peer.name, func(t *testing.T) {
			r := &relay{token: token}
			s := httptest.NewServer(r)
			defer s.Close()
			client := s.Client()
			client.Timeout = 10 * time.Second
			c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.URL, "http")+"/desktop", http.Header{"Authorization": []string{"Bearer " + token}})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			// The upgrade response can arrive before the relay registers the peer.
			for deadline := time.Now().Add(2 * time.Second); ; {
				r.mu.Lock()
				connected := r.pc != nil
				r.mu.Unlock()
				if connected {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("desktop peer was not registered")
				}
				time.Sleep(time.Millisecond)
			}

			type call struct{ kind, value string }
			var mu sync.Mutex
			var calls []call
			record := func(kind, value string) error {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, call{kind, value})
				return nil
			}
			write := func(value string) error { return record("clipboard write", value) }
			read := func() (string, error) { return clipboardText, record("clipboard read", "") }
			var recordPhone func(string) error
			if peer.supported {
				recordPhone = func(value string) error { return record("phone name", value) }
			}
			go delivery.Receive(c, g, write, read, recordPhone)

			post := func(t *testing.T, path string, body []byte, wantStatus int, wantCalls ...call) []byte {
				t.Helper()
				req, err := http.NewRequest("POST", s.URL+path, bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Authorization", "Bearer "+token)
				res, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				data, err := io.ReadAll(res.Body)
				if err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				gotCalls := calls
				calls = nil
				mu.Unlock()
				if res.StatusCode != wantStatus || !slices.Equal(gotCalls, wantCalls) {
					t.Fatalf("POST %s: HTTP %d, callbacks %v; want HTTP %d, callbacks %v", path, res.StatusCode, gotCalls, wantStatus, wantCalls)
				}
				return data
			}

			if !peer.supported {
				post(t, "/clip", fixture, http.StatusUnprocessableEntity)
			} else {
				post(t, "/clip", fixture, http.StatusNoContent, call{"phone name", phoneName})
				maxName := strings.Repeat("📱", 64)
				post(t, "/clip", seal(maxName, phoneAAD), http.StatusNoContent, call{"phone name", maxName})

				t.Run("tampered tag", func(t *testing.T) {
					tampered := bytes.Clone(fixture)
					tampered[len(tampered)-1] ^= 1
					post(t, "/clip", tampered, http.StatusUnprocessableEntity)
				})
				for _, invalid := range []struct{ name, plain string }{
					{"invalid UTF-8", "Phone\xff"},
					{"blank name", "   "},
					{"too many code points", strings.Repeat("x", 65)},
					{"control character", "Test\tPhone"},
					{"format character", "Test\u200dPhone"},
					{"line separator", "Test\u2028Phone"},
					{"paragraph separator", "Test\u2029Phone"},
				} {
					t.Run(invalid.name, func(t *testing.T) {
						post(t, "/clip", seal(invalid.plain, phoneAAD), http.StatusUnprocessableEntity)
					})
				}
			}

			// The same plaintext without phone AAD is still clipboard text.
			post(t, "/clip", seal(phoneName, ""), http.StatusNoContent, call{"clipboard write", phoneName})
			proof := seal("", "buffer/pull/v1")
			data := post(t, "/pull", proof, http.StatusOK, call{"clipboard read", ""})
			if len(data) < g.NonceSize()+g.Overhead() {
				t.Fatal("short pull response")
			}
			plain, err := g.Open(nil, data[:g.NonceSize()], data[g.NonceSize():], proof)
			if err != nil || string(plain) != clipboardText {
				t.Fatalf("pull response = %q, %v; want %q", plain, err, clipboardText)
			}
		})
	}
}

func TestPull(t *testing.T) {
	s := httptest.NewServer(&relay{token: "test"})
	defer s.Close()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.URL, "http")+"/desktop", http.Header{"Authorization": []string{"Bearer test"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	b, _ := aes.NewCipher(make([]byte, 32))
	g, _ := cipher.NewGCM(b)
	reads := make(chan struct{}, 2)
	go delivery.Receive(c, g, nil, func() (string, error) { reads <- struct{}{}; return "Привет 📋", nil }, nil)
	nonce := make([]byte, 12)
	proof := g.Seal(nonce, nonce, nil, []byte("buffer/pull/v1"))
	for _, code := range []int{200, 422} {
		req, _ := http.NewRequest("POST", s.URL+"/pull", bytes.NewReader(proof))
		req.Header.Set("Authorization", "Bearer test")
		res, err := s.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil || res.StatusCode != code {
			t.Fatalf("status %d %v", res.StatusCode, err)
		}
		if code == 200 {
			if len(data) < 28 {
				t.Fatal("short response")
			}
			plain, e := g.Open(nil, data[:12], data[12:], proof)
			if e != nil || string(plain) != "Привет 📋" {
				t.Fatal("response mismatch")
			}
		}
		proof[len(proof)-1] ^= 1
	}
	if len(reads) != 1 {
		t.Fatal("invalid proof triggered clipboard read")
	}
}
