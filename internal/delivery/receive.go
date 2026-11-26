package delivery

import (
	"crypto/cipher"
	"github.com/gorilla/websocket"
	"time"
	"unicode/utf8"
)

// Receive acknowledges each message only after write returns successfully.
func Receive(c *websocket.Conn, g cipher.AEAD, write func(string) error) error {
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
			ID   string
			Data []byte
		}
		if err := c.ReadJSON(&m); err != nil {
			return err
		}
		ok := false
		if len(m.Data) >= g.NonceSize()+g.Overhead() {
			plain, e := g.Open(nil, m.Data[:g.NonceSize()], m.Data[g.NonceSize():], nil)
			if e == nil && utf8.Valid(plain) {
				ok = write(string(plain)) == nil
			}
		}
		c.SetWriteDeadline(time.Now().Add(3 * time.Second))
		if err := c.WriteJSON(struct {
			ID string `json:"id"`
			OK bool   `json:"ok"`
		}{m.ID, ok}); err != nil {
			return err
		}
	}
}
