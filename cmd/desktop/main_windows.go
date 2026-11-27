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
	"runtime"
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

const pairPhoneLabel = "Pair phone…"

const pairingHelp = "To show the pairing QR, right-click the Buffer icon in the notification area (including hidden icons) and choose “" + pairPhoneLabel + "”."

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
	// Pairing dialogs, the tray window, and the message loop share one Windows thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ui, err := newPairingUI()
	if err != nil {
		showMessage(0, "Buffer could not initialize its Windows interface.", mbError)
		return
	}
	if missing {
		var bitmap [][]bool
		p, bitmap, err = ui.setup(name)
		if err != nil {
			showMessage(0, err.Error(), mbError)
			return
		}
		if bitmap == nil { // First-run setup was canceled before saving.
			return
		}
		if err := ui.showStartupQR(bitmap); err != nil {
			showMessage(0, err.Error()+" "+pairingHelp, mbError)
		}
	}

	endpoint, g, err := p.clientConfig()
	if err != nil {
		showMessage(0, invalidPairing, mbError)
		return
	}
	if err := runTray(ui, p, !missing && *showQR, func(ctx context.Context, status func(connectionStatus)) {
		runClient(ctx, endpoint, p.Token, g, websocket.DefaultDialer, status)
	}); err != nil {
		showMessage(0, "Buffer could not display its notification area icon. Make sure the Windows taskbar is running and try again.", mbError)
	}
}
