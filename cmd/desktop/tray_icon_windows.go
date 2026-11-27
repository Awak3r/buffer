package main

import (
	_ "embed"
	"unsafe"
)

// Rasterized from tray_icon.svg so the small icon doesn't depend on Windows resampling.
//
//go:embed tray_icon_16.png
var trayIcon16 []byte

//go:embed tray_icon_32.png
var trayIcon32 []byte

var (
	getSystemMetrics       = user.NewProc("GetSystemMetrics")
	createIconFromResource = user.NewProc("CreateIconFromResourceEx")
	destroyIcon            = user.NewProc("DestroyIcon")
)

func loadTrayIcon() (uintptr, error) {
	const smCXSmallIcon, smCYSmallIcon = 49, 50 // SM_CXSMICON, SM_CYSMICON
	width, _, _ := getSystemMetrics.Call(smCXSmallIcon)
	height, _, _ := getSystemMetrics.Call(smCYSmallIcon)
	return newTrayIcon(width, height)
}

func newTrayIcon(width, height uintptr) (uintptr, error) {
	art := trayIcon16
	if width > 16 || height > 16 {
		art = trayIcon32
	}
	// CreateIconFromResourceEx accepts PNG image bytes in a DWORD-aligned buffer.
	words := make([]uint32, (len(art)+3)/4)
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&words[0])), len(art)), art)
	const iconResourceVersion = 0x00030000
	icon, _, err := createIconFromResource.Call(uintptr(unsafe.Pointer(&words[0])), uintptr(len(art)), 1, iconResourceVersion, width, height, 0)
	if icon == 0 {
		return 0, err
	}
	return icon, nil
}
