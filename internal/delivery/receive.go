package delivery

import (
	"crypto/cipher"
	"crypto/rand"
	"github.com/gorilla/websocket"
	"time"
	"unicode/utf8"
)

// Receive acknowledges each message only after write returns successfully.
func Receive(c *websocket.Conn, g cipher.AEAD, write func(string) error, read func() (string, error)) error {
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
		} else if len(m.Data) >= g.NonceSize()+g.Overhead() {
			plain, e := g.Open(nil, m.Data[:g.NonceSize()], m.Data[g.NonceSize():], nil)
			if e == nil && utf8.Valid(plain) {
				ok = write(string(plain)) == nil
			}
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
