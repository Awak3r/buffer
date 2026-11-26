package main

import (
	"buffer/internal/delivery"
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/gorilla/websocket"
	qr "github.com/skip2/go-qrcode"
	"golang.org/x/sys/windows"
)

type pairing struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	Key   string `json:"key"`
}

func protect(b []byte, decrypt bool) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("empty configuration")
	}
	in := windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	var out windows.DataBlob
	var err error
	if decrypt {
		err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, 1, &out)
	} else {
		err = windows.CryptProtectData(&in, nil, nil, 0, nil, 1, &out)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}
func config(reader *bufio.Reader) (pairing, error) {
	var p pairing
	root, err := os.UserConfigDir()
	if err != nil {
		return p, err
	}
	dir := filepath.Join(root, "Buffer")
	name := filepath.Join(dir, "pair.bin")
	b, err := os.ReadFile(name)
	if err == nil {
		b, err = protect(b, true)
		if err == nil {
			err = json.Unmarshal(b, &p)
		}
		return p, err
	}
	if !os.IsNotExist(err) {
		return p, err
	}
	fmt.Print("Relay HTTPS URL: ")
	line, _ := reader.ReadString('\n')
	u, err := url.Parse(strings.TrimSpace(line))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" {
		return p, errors.New("expected https://host")
	}
	fmt.Print("BUFFER_TOKEN from server: ")
	line, _ = reader.ReadString('\n')
	token := strings.TrimSpace(line)
	if len(token) < 32 {
		return p, errors.New("token too short")
	}
	var key [32]byte
	rand.Read(key[:])
	p = pairing{"https://" + u.Host, token, base64.StdEncoding.EncodeToString(key[:])}
	b, err = json.Marshal(p)
	if err != nil {
		return p, err
	}
	b, err = protect(b, false)
	if err != nil {
		return p, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return p, err
	}
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return p, err
	}
	_, err = f.Write(b)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		os.Remove(name)
		return p, errors.Join(err, closeErr)
	}
	return p, nil
}
func receive(p pairing, g cipher.AEAD) error {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+p.Token)
	c, _, err := websocket.DefaultDialer.Dial("wss"+strings.TrimPrefix(p.URL, "https")+"/desktop", h)
	if err != nil {
		return err
	}
	defer c.Close()
	log.Print("Connected. Ready to receive text.")
	return delivery.Receive(c, g, writeClipboard, readClipboard)
}
func main() {
	show := flag.Bool("pair", false, "display local pairing QR")
	flag.Parse()
	reader := bufio.NewReader(os.Stdin)
	p, err := config(reader)
	if err != nil {
		log.Fatal(err)
	}
	if *show {
		b, _ := json.Marshal(p)
		code, err := qr.New(string(b), qr.Medium)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("Scan with Buffer on Android. Maximize this terminal if the QR wraps.")
		fmt.Print(code.ToSmallString(false))
		fmt.Print("Press Enter when paired: ")
		reader.ReadString('\n')
	}
	key, err := base64.StdEncoding.DecodeString(p.Key)
	if err != nil {
		log.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		log.Fatal(err)
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		log.Fatal(err)
	}
	for {
		err := receive(p, g)
		log.Printf("Disconnected (%v). Retrying in 3s.", err)
		time.Sleep(3 * time.Second)
	}
}
