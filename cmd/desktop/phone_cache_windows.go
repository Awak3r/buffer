package main

import (
	"buffer/internal/delivery"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

const maxPhoneBlobSize = 16 << 10

var errSavePhoneName = errors.New("could not save phone name")

type phoneCache struct {
	path    string
	entropy [sha256.Size]byte
	latest  atomic.Pointer[string] // Tray reads never wait for DPAPI or disk I/O.

	mu     sync.Mutex
	saved  string
	closed bool
}

func openPhoneCache(pairPath string, p pairing) *phoneCache {
	// Length-prefix the exact stored values so a different pairing cannot decrypt this blob.
	identity := []byte("Buffer phone cache v1\x00")
	for _, value := range [...]string{p.URL, p.Token, p.Key} {
		identity = binary.AppendUvarint(identity, uint64(len(value)))
		identity = append(identity, value...)
	}
	c := &phoneCache{
		path:    filepath.Join(filepath.Dir(pairPath), "phone.bin"),
		entropy: sha256.Sum256(identity),
	}
	if name := c.load(); name != "" {
		c.saved = name
		c.latest.Store(&name)
	}
	return c
}

func (c *phoneCache) load() string {
	f, err := os.Open(c.path)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxPhoneBlobSize+1))
	if err != nil || len(b) > maxPhoneBlobSize {
		return ""
	}
	b, err = protectWithEntropy(b, c.entropy[:], true)
	if err != nil {
		return ""
	}
	name := string(b)
	if !delivery.ValidPhoneName(name) {
		return ""
	}
	return name
}

func (c *phoneCache) name() string {
	if p := c.latest.Load(); p != nil {
		return *p
	}
	return ""
}

func (c *phoneCache) record(name string) error {
	if !delivery.ValidPhoneName(name) {
		return errors.New("invalid phone name")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return os.ErrClosed
	}
	// A valid update can be shown now even if it cannot yet be saved for the next launch.
	c.latest.Store(&name)
	if name == c.saved {
		return nil
	}
	b, err := protectWithEntropy([]byte(name), c.entropy[:], false)
	if err != nil || len(b) > maxPhoneBlobSize || replacePhoneBlob(c.path, b) != nil {
		return errSavePhoneName
	}
	c.saved = name
	return nil
}

func (c *phoneCache) close() {
	// Before releasing the instance claim, finish any save and reject late callbacks.
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
}

func replacePhoneBlob(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".phone-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	// Write beside the old cache and only replace it once the new blob is complete.
	return os.Rename(f.Name(), path)
}
