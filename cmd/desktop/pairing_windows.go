package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	qr "github.com/skip2/go-qrcode"
	"golang.org/x/sys/windows"
)

type pairing struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	Key   string `json:"key"`
}

func pairingPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(root, "Buffer", "pair.bin"))
}

func claimInstance(name string) (func(), error) {
	// Keep the named object alive for this config, even during setup. No thread owns it.
	id := sha256.Sum256([]byte(strings.ToLower(name)))
	mutexName, _ := windows.UTF16PtrFromString(fmt.Sprintf(`Global\Buffer-%x`, id))
	h, err := windows.CreateMutex(nil, false, mutexName)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		windows.CloseHandle(h)
		return nil, errors.New("Buffer is already running for this pairing. " + pairingHelp)
	}
	if err != nil {
		return nil, errors.New("Buffer could not check whether another copy is running. Close any other Buffer instance and try again.")
	}
	return func() { windows.CloseHandle(h) }, nil
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

func loadPairing(name string) (pairing, error) {
	var p pairing
	b, err := os.ReadFile(name)
	if err != nil {
		return p, err
	}
	b, err = protect(b, true)
	if err != nil || json.Unmarshal(b, &p) != nil {
		return pairing{}, errors.New("could not decrypt or decode pairing")
	}
	return p, nil
}

func savePairing(name string, b []byte) error {
	b, err := protect(b, false)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err = errors.Join(err, f.Close()); err != nil {
		os.Remove(name)
	}
	return err
}

func relayURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		strings.Trim(u.EscapedPath(), "/") != "" || u.ForceQuery || u.RawQuery != "" || strings.Contains(value, "#") {
		return nil, errors.New("Enter an HTTPS relay URL with no path, query, fragment, or credentials (for example, https://clip.example.com).")
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
			return nil, errors.New("The relay URL has an invalid port.")
		}
	}
	u.Path, u.RawPath = "", ""
	return u, nil
}

func validToken(token string) bool {
	return len(token) >= 32 && strings.IndexFunc(token, func(r rune) bool {
		return r < ' ' || r == 0x7f
	}) == -1
}

func (p pairing) clientConfig() (string, cipher.AEAD, error) {
	u, err := relayURL(p.URL)
	if err != nil {
		return "", nil, err
	}
	key, err := base64.StdEncoding.DecodeString(p.Key)
	if !validToken(p.Token) || err != nil || len(key) != 32 {
		return "", nil, errors.New("invalid pairing credentials")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", nil, err
	}
	g, err := cipher.NewGCM(block)
	u.Scheme, u.Path = "wss", "/desktop"
	return u.String(), g, err
}

func newPairing(relay, token string) (pairing, error) {
	u, err := relayURL(strings.TrimSpace(relay))
	if err != nil {
		return pairing{}, err
	}
	token = strings.TrimSpace(token)
	if !validToken(token) {
		return pairing{}, errors.New("The relay token must be at least 32 bytes and contain no control characters.")
	}
	var key [32]byte
	rand.Read(key[:])
	return pairing{URL: u.String(), Token: token, Key: base64.StdEncoding.EncodeToString(key[:])}, nil
}

func validDeviceName(name string) bool {
	if len(name) == 0 || len(name) > 63 {
		return false
	}
	for i := range len(name) {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func pairingQR(p pairing) ([][]bool, error) {
	name, _ := os.Hostname()
	return encodePairingQR(p, name)
}

func encodePairingQR(p pairing, name string) ([][]bool, error) {
	if !validDeviceName(name) {
		name = ""
	}
	payload := struct {
		pairing
		DeviceName string `json:"device_name,omitempty"`
	}{p, name}
	for {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, errors.New("Buffer could not encode the pairing.")
		}
		code, err := qr.New(string(b), qr.Medium)
		if err == nil {
			return code.Bitmap(), nil // Includes the four-module quiet zone.
		}
		if payload.DeviceName == "" {
			return nil, errors.New("The pairing data could not fit in a QR code.")
		}
		// An optional device name must not make an existing pairing unscannable.
		payload.DeviceName = ""
	}
}
