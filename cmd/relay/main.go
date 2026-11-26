package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type packet struct {
	ID   string `json:"id"`
	Data []byte `json:"data"`
}
type receipt struct {
	ID string `json:"id"`
	OK bool   `json:"ok"`
}
type peer struct {
	conn    *websocket.Conn
	busy    chan struct{}
	replies chan receipt
	done    chan struct{}
}
type relay struct {
	token string
	mu    sync.Mutex
	pc    *peer
}

func (s *relay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.token)) != 1 {
		http.Error(w, "unauthorized", 401)
		return
	}
	switch {
	case r.URL.Path == "/desktop" && r.Method == "GET":
		s.listen(w, r)
	case r.URL.Path == "/clip" && r.Method == "POST":
		s.send(w, r)
	default:
		http.NotFound(w, r)
	}
}
func (s *relay) listen(w http.ResponseWriter, r *http.Request) {
	up := websocket.Upgrader{HandshakeTimeout: 5 * time.Second}
	c, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close()
	p := &peer{c, make(chan struct{}, 1), make(chan receipt, 1), make(chan struct{})}
	s.mu.Lock()
	old := s.pc
	s.pc = p
	s.mu.Unlock()
	if old != nil {
		old.conn.Close()
	}
	defer func() {
		s.mu.Lock()
		if s.pc == p {
			s.pc = nil
		}
		close(p.done)
		s.mu.Unlock()
	}()
	c.SetReadLimit(256)
	c.SetReadDeadline(time.Now().Add(65 * time.Second))
	c.SetPongHandler(func(string) error { return c.SetReadDeadline(time.Now().Add(65 * time.Second)) })
	go func() {
		t := time.NewTicker(25 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-p.done:
				return
			case <-t.C:
				if c.WriteControl(websocket.PingMessage, nil, time.Now().Add(3*time.Second)) != nil {
					c.Close()
					return
				}
			}
		}
	}()
	for {
		var a receipt
		if c.ReadJSON(&a) != nil {
			return
		}
		select {
		case p.replies <- a:
		default:
			return
		}
	}
}
func (s *relay) send(w http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 65536+28))
	if err != nil {
		http.Error(w, "payload too large or unreadable", 413)
		return
	}
	if len(b) < 29 {
		http.Error(w, "invalid payload", 400)
		return
	}
	s.mu.Lock()
	p := s.pc
	s.mu.Unlock()
	if p == nil {
		http.Error(w, "offline", 503)
		return
	}
	select {
	case p.busy <- struct{}{}:
		defer func() { <-p.busy }()
	default:
		http.Error(w, "busy", 409)
		return
	}
	var id [16]byte
	rand.Read(id[:])
	msg := packet{hex.EncodeToString(id[:]), b}
	p.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if err = p.conn.WriteJSON(msg); err != nil {
		p.conn.Close()
		http.Error(w, "delivery unknown", 502)
		return
	}
	timer := time.NewTimer(7 * time.Second)
	defer timer.Stop()
	for {
		select {
		case a := <-p.replies:
			if a.ID != msg.ID {
				continue
			}
			if !a.OK {
				http.Error(w, "clipboard write failed", 422)
				return
			}
			w.WriteHeader(204)
			return
		case <-p.done:
			http.Error(w, "delivery unknown", 502)
			return
		case <-timer.C:
			http.Error(w, "ack timeout", 504)
			return
		case <-r.Context().Done():
			return
		}
	}
}
func main() {
	token := os.Getenv("BUFFER_TOKEN")
	if len(token) < 32 {
		log.Fatal("set BUFFER_TOKEN to a random token of at least 32 characters")
	}
	srv := http.Server{Addr: "127.0.0.1:8787", Handler: &relay{token: token}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 12 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
