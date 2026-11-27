package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPhoneCachePairingIsolation(t *testing.T) {
	pairPath := filepath.Join(t.TempDir(), "pair.bin")
	p := testPairing()
	pairJSON, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := savePairing(pairPath, pairJSON); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(pairPath)
	if err != nil {
		t.Fatal(err)
	}
	c := openPhoneCache(pairPath, p)
	if c.name() != "" {
		t.Fatal("a missing cache did not start empty")
	}
	const first, last = "Nothing Phone (3)", "Mia & 李's Phone"
	for _, name := range []string{first, last} {
		if err := c.record(name); err != nil {
			t.Fatal(err)
		}
		if c.name() != name || openPhoneCache(pairPath, p).name() != name {
			t.Fatal("latest phone name did not round-trip for the same pairing")
		}
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(pairPath), "phone.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(last)) {
		t.Fatal("phone name was stored in plaintext")
	}
	if _, err := protect(b, true); err == nil {
		t.Fatal("phone cache decrypted without the pairing entropy")
	}
	for _, tc := range []struct {
		name   string
		change func(*pairing)
	}{
		{"URL", func(p *pairing) { p.URL += "/" }},
		{"token", func(p *pairing) { p.Token = strings.Repeat("b", 32) }},
		{"key", func(p *pairing) { p.Key = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := p
			tc.change(&other)
			if openPhoneCache(pairPath, other).name() != "" {
				t.Fatal("phone name leaked across pairings")
			}
		})
	}
	after, err := os.ReadFile(pairPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("recording a phone name changed pair.bin")
	}
}

func TestPhoneCacheRejectsInvalidNames(t *testing.T) {
	pairPath := filepath.Join(t.TempDir(), "pair.bin")
	p := testPairing()
	c := openPhoneCache(pairPath, p)
	const known = "Nothing Phone"
	if err := c.record(known); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", " Pixel", "Pixel\tPhone", "Pixel\x00Phone", "Pixel\u2028Phone", "Pixel\u202ePhone", "\xff", strings.Repeat("界", 65)} {
		if err := c.record(name); err == nil || c.name() != known {
			t.Fatal("an invalid update replaced the last valid name")
		}
	}
	if openPhoneCache(pairPath, p).name() != known {
		t.Fatal("an invalid update changed the saved name")
	}
	invalid, err := protectWithEntropy([]byte("Pixel\u202ePhone"), c.entropy[:], false)
	if err != nil {
		t.Fatal(err)
	}
	for _, blob := range [][]byte{invalid, []byte("corrupt DPAPI data"), make([]byte, maxPhoneBlobSize+1)} {
		if err := os.WriteFile(c.path, blob, 0600); err != nil {
			t.Fatal(err)
		}
		if openPhoneCache(pairPath, p).name() != "" {
			t.Fatal("an invalid cache was displayed")
		}
	}
}

func TestPhoneCacheRetriesFailedSave(t *testing.T) {
	pairPath := filepath.Join(t.TempDir(), "pair.bin")
	p := testPairing()
	c := openPhoneCache(pairPath, p)
	const oldName, newName = "Old name", "New name"
	if err := c.record(oldName); err != nil {
		t.Fatal(err)
	}
	func() {
		path, err := windows.UTF16PtrFromString(c.path)
		if err != nil {
			t.Fatal(err)
		}
		// Deny delete sharing so Windows cannot replace the existing file.
		h, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer windows.CloseHandle(h)
		if err := c.record(newName); !errors.Is(err, errSavePhoneName) {
			t.Fatalf("expected a save failure, got %v", err)
		}
		if c.name() != newName || openPhoneCache(pairPath, p).name() != oldName {
			t.Fatal("failed save should update the live name and preserve the previous file")
		}
	}()
	if err := c.record(newName); err != nil || openPhoneCache(pairPath, p).name() != newName {
		t.Fatal("repeating the same update did not retry persistence")
	}
	files, err := os.ReadDir(filepath.Dir(pairPath))
	if err != nil || len(files) != 1 || files[0].Name() != "phone.bin" {
		t.Fatal("a save left a temporary cache file behind")
	}
	c.close()
	if err := c.record(oldName); !errors.Is(err, os.ErrClosed) || openPhoneCache(pairPath, p).name() != newName {
		t.Fatal("a late callback rewrote the cache after close")
	}
}

func TestPhoneCacheConcurrentRecords(t *testing.T) {
	pairPath := filepath.Join(t.TempDir(), "pair.bin")
	p := testPairing()
	c := openPhoneCache(pairPath, p)
	var writers sync.WaitGroup
	for i := range 8 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			if err := c.record(fmt.Sprintf("Phone %d", i)); err != nil {
				t.Error(err)
			}
			if c.name() == "" {
				t.Error("record did not publish a name")
			}
		}()
	}
	writers.Wait()
	if got := openPhoneCache(pairPath, p).name(); got == "" || got != c.name() {
		t.Fatal("last in-memory update and persisted name differed")
	}
}
