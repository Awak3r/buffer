package delivery

import (
	"crypto/cipher"
	"crypto/rand"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

const phoneNameAAD = "buffer/phone-name/v1"

func ValidPhoneName(name string) bool {
	return name != "" && len(name) <= 256 && utf8.ValidString(name) &&
		utf8.RuneCountInString(name) <= 64 && strings.TrimSpace(name) == name &&
		strings.IndexFunc(name, func(r rune) bool {
			return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
		}) == -1
}

func receivePush(g cipher.AEAD, data []byte, write, recordPhone func(string) error) bool {
	n := g.NonceSize()
	if len(data) < n+g.Overhead() {
		return false
	}
	nonce, ciphertext := data[:n], data[n:]
	if plain, err := g.Open(nil, nonce, ciphertext, nil); err == nil {
		return utf8.Valid(plain) && write(string(plain)) == nil
	}
	if recordPhone == nil || len(ciphertext) > g.Overhead()+256 {
		return false
	}
	// The AAD distinguishes metadata from legacy clipboard text without changing the relay envelope.
	plain, err := g.Open(nil, nonce, ciphertext, []byte(phoneNameAAD))
	return err == nil && ValidPhoneName(string(plain)) && recordPhone(string(plain)) == nil
}

// Receive acknowledges requests only after the corresponding operation succeeds.
func Receive(c *websocket.Conn, g cipher.AEAD, write func(string) error, read func() (string, error), recordPhone func(string) error) error {
	c.SetReadLimit(96 << 10)
	c.SetReadDeadline(time.Now().Add(70 * time.Second))
	pong := c.PingHandler()
	c.SetPingHandler(func(data string) error {
		if err := c.SetReadDeadline(time.Now().Add(70 * time.Second)); err != nil {
			return err
		}
		return pong(data)
	})
	for {
		var m struct {
			Pull bool
			ID   string
			Data []byte
		}
		if err := c.ReadJSON(&m); err != nil {
			return err
		}
		ok := false
		var response []byte
		if m.Pull {
			if read != nil && len(m.Data) == g.NonceSize()+g.Overhead() {
				_, err := g.Open(nil, m.Data[:g.NonceSize()], m.Data[g.NonceSize():], []byte("buffer/pull/v1"))
				if err == nil {
					value, err := read()
					if err == nil && len(value) > 0 && len(value) <= 65536 {
						nonce := make([]byte, g.NonceSize())
						rand.Read(nonce)
						response = g.Seal(nonce, nonce, []byte(value), m.Data)
						ok = true
					}
				}
			}
		} else {
			ok = receivePush(g, m.Data, write, recordPhone)
		}
		c.SetWriteDeadline(time.Now().Add(3 * time.Second))
		if err := c.WriteJSON(struct {
			ID   string `json:"id"`
			OK   bool   `json:"ok"`
			Data []byte `json:"data,omitempty"`
		}{m.ID, ok, response}); err != nil {
			return err
		}
	}
}
