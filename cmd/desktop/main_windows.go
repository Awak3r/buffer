package main

import (
	"buffer/internal/delivery"
	"context"
	"crypto/cipher"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

type connectionStatus uintptr

const (
	connecting connectionStatus = iota
	connected
	retrying
	authRejected
)

func (s connectionStatus) String() string {
	switch s {
	case connected:
		return "Connected"
	case retrying:
		return "Disconnected; retrying"
	case authRejected:
		return "Authorization rejected; retrying"
	default:
		return "Connecting"
	}
}

func runClient(ctx context.Context, endpoint, token string, g cipher.AEAD, dialer *websocket.Dialer, status func(connectionStatus)) {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	for ctx.Err() == nil {
		next := retrying
		c, response, err := dialer.DialContext(ctx, endpoint, h)
		if err == nil {
			stop := context.AfterFunc(ctx, func() { c.Close() })
			status(connected)
			_ = delivery.Receive(c, g, writeClipboard, readClipboard)
			stop()
			c.Close()
		} else if response != nil && (response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden) {
			next = authRejected
		}
		if ctx.Err() != nil {
			return
		}
		status(next)
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

const pairingHelp = "To show your pairing QR again, choose Exit in the Buffer tray menu, then run buffer.exe -pair. " +
	"Press Enter after scanning to close the pairing console and return to the tray."

const invalidPairing = "Buffer could not read or validate %APPDATA%\\Buffer\\pair.bin. " +
	"Restore a valid file, or move it aside and start Buffer again to set up a new pairing."

func main() {
	flags := flag.NewFlagSet("buffer.exe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	showQR := flags.Bool("pair", false, "show the local pairing QR")
	if err := flags.Parse(os.Args[1:]); err != nil || flags.NArg() != 0 {
		if errors.Is(err, flag.ErrHelp) {
			showMessage(0, "Launch buffer.exe to start Buffer. "+pairingHelp, mbInfo)
		} else {
			showMessage(0, "Unknown arguments. Launch buffer.exe or buffer.exe -pair.", mbError)
		}
		return
	}

	name, err := pairingPath()
	if err != nil {
		showMessage(0, "Buffer could not locate your Windows application data folder.", mbError)
		return
	}
	release, err := claimInstance(name)
	if err != nil {
		showMessage(0, err.Error(), mbError)
		return
	}
	defer release()

	p, err := loadPairing(name)
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		showMessage(0, invalidPairing, mbError)
		return
	}
	if missing {
		if !*showQR && showMessage(0, "Buffer is not paired on this PC. Open the temporary pairing console to set it up now?", mbSetup) != idYes {
			return
		}
		p, err = showPairing(name, p, true)
		if err != nil {
			showMessage(0, err.Error(), mbError)
			return
		}
	}

	endpoint, g, err := p.clientConfig()
	if err != nil {
		showMessage(0, invalidPairing, mbError)
		return
	}
	if !missing && *showQR {
		if _, err := showPairing(name, p, false); err != nil {
			showMessage(0, err.Error(), mbError)
			return
		}
	}
	if err := runTray(func(ctx context.Context, status func(connectionStatus)) {
		runClient(ctx, endpoint, p.Token, g, websocket.DefaultDialer, status)
	}); err != nil {
		showMessage(0, "Buffer could not display its notification area icon. Make sure the Windows taskbar is running and try again.", mbError)
	}
}
