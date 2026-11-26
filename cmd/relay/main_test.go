package main

import (
	"buffer/internal/delivery"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
		if err = c.WriteJSON(receipt{"another-message", true}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-result:
			t.Fatal("accepted wrong receipt")
		case <-time.After(25 * time.Millisecond):
		}
		if err = c.WriteJSON(receipt{msg.ID, want == 204}); err != nil {
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
	go delivery.Receive(c, g, func(value string) error { written <- value; return nil })
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
