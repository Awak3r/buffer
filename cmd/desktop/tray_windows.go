package main

import (
	"context"
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	mbForeground = 0x10000
	mbError      = 0x10
	mbInfo       = 0x40

	wmClose       = 0x0010
	wmContextMenu = 0x007b
	wmLeftUp      = 0x0202
	wmRightUp     = 0x0205
	wmKeySelect   = 0x0401 // NIN_KEYSELECT
	wmTray        = 0x8001 // WM_APP + 1
	wmStatus      = 0x8002 // WM_APP + 2

	nimAdd        = 0
	nimModify     = 1
	nimDelete     = 2
	nimSetFocus   = 3
	nimSetVersion = 4
	nifMessage    = 1
	nifIcon       = 2
	nifTip        = 4
	nifShowTip    = 0x80

	trayClass = "BufferDesktopTray"
)

var (
	registerClass         = user.NewProc("RegisterClassW")
	registerWindowMessage = user.NewProc("RegisterWindowMessageW")
	defWindowProc         = user.NewProc("DefWindowProcW")
	getMessage            = user.NewProc("GetMessageW")
	translateMessage      = user.NewProc("TranslateMessage")
	dispatchMessage       = user.NewProc("DispatchMessageW")
	postMessage           = user.NewProc("PostMessageW")
	postQuitMessage       = user.NewProc("PostQuitMessage")
	setForegroundWindow   = user.NewProc("SetForegroundWindow")
	getCursorPos          = user.NewProc("GetCursorPos")
	createPopupMenu       = user.NewProc("CreatePopupMenu")
	appendMenu            = user.NewProc("AppendMenuW")
	trackPopupMenu        = user.NewProc("TrackPopupMenu")
	destroyMenu           = user.NewProc("DestroyMenu")
	shellNotifyIcon       = windows.NewLazySystemDLL("shell32.dll").NewProc("Shell_NotifyIconW")
)

type windowClass struct { // WNDCLASSW
	Style               uint32
	Proc                uintptr
	ClsExtra, WndExtra  int32
	Instance, Icon      uintptr
	Cursor, Background  uintptr
	MenuName, ClassName *uint16
}

type point struct{ X, Y int32 }

type windowMessage struct { // MSG
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          point
	Private        uint32
}

type notifyIcon struct { // NOTIFYICONDATAW
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                windows.GUID
	BalloonIcon         uintptr
}

func showMessage(owner uintptr, message string, style uint32) int32 {
	text, _ := windows.UTF16PtrFromString(message)
	caption, _ := windows.UTF16PtrFromString("Buffer")
	result, _ := windows.MessageBox(windows.HWND(owner), text, caption, style|mbForeground)
	return result
}

type tray struct {
	icon             notifyIcon
	taskbarCreated   uintptr
	status           connectionStatus
	ui               *pairingUI
	pairing          pairing
	modern, menuOpen bool
}

func runTray(ui *pairingUI, p pairing, openQR bool, client func(context.Context, func(connectionStatus))) error {
	name, _ := windows.UTF16PtrFromString(trayClass)
	taskbar, _ := windows.UTF16PtrFromString("TaskbarCreated")
	t := tray{ui: ui, pairing: p}
	t.taskbarCreated, _, _ = registerWindowMessage.Call(uintptr(unsafe.Pointer(taskbar)))
	wc := windowClass{Proc: windows.NewCallback(t.windowProc), Instance: ui.module, ClassName: name}
	if ok, _, err := registerClass.Call(uintptr(unsafe.Pointer(&wc))); ok == 0 {
		return err
	}
	// A hidden top-level window (not HWND_MESSAGE) receives Explorer's TaskbarCreated broadcast.
	hwnd, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(name)), 0, 0, 0, 0, 0, 0, 0, 0, ui.module, 0)
	if hwnd == 0 {
		return err
	}
	defer destroyWindow.Call(hwnd)
	icon, err := loadTrayIcon()
	if err != nil {
		return err
	}
	defer destroyIcon.Call(icon) // Keep the handle alive through re-registration and NIM_DELETE.
	t.icon = notifyIcon{Size: uint32(unsafe.Sizeof(t.icon)), Window: hwnd, ID: 1, Icon: icon, Callback: wmTray, Version: 4}
	if !t.addIcon() {
		return errors.New("could not add notification icon")
	}
	defer t.notify(nimDelete)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client(ctx, func(status connectionStatus) { postMessage.Call(hwnd, wmStatus, uintptr(status), 0) })
	if openQR {
		t.showPairing(hwnd)
	}
	for {
		var msg windowMessage
		r, _, err := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		switch int32(r) {
		case -1:
			return err
		case 0:
			return nil
		}
		if ui.qrWindow != 0 {
			if handled, _, _ := isDialogMessage.Call(ui.qrWindow, uintptr(unsafe.Pointer(&msg))); handled != 0 {
				continue
			}
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (t *tray) notify(action uintptr) bool {
	ok, _, _ := shellNotifyIcon.Call(action, uintptr(unsafe.Pointer(&t.icon)))
	return ok != 0
}

func (t *tray) updateTip() {
	tip, _ := windows.UTF16FromString("Buffer: " + t.status.String())
	copy(t.icon.Tip[:], tip)
}

func (t *tray) addIcon() bool {
	t.updateTip()
	t.icon.Flags = nifMessage | nifIcon | nifTip | nifShowTip
	if !t.notify(nimAdd) {
		return false
	}
	t.modern = t.notify(nimSetVersion)
	return true
}

func (t *tray) windowProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch {
	case msg == wmTray:
		var p point
		switch uint16(lParam) {
		case wmLeftUp, wmKeySelect:
			if t.modern {
				p = point{int32(int16(wParam)), int32(int16(wParam >> 16))}
				break
			}
			fallthrough
		case wmRightUp, wmContextMenu:
			if uint16(lParam) == wmRightUp && t.modern {
				return 0
			}
			if ok, _, _ := getCursorPos.Call(uintptr(unsafe.Pointer(&p))); ok == 0 {
				return 0
			}
		default:
			return 0
		}
		t.showMenu(hwnd, p)
	case msg == wmStatus:
		if status := connectionStatus(wParam); status != t.status {
			t.status = status
			t.updateTip()
			t.icon.Flags = nifTip | nifShowTip
			if !t.notify(nimModify) {
				t.addIcon()
			}
		}
	case msg == wmClose:
		postQuitMessage.Call(0)
	case msg == t.taskbarCreated && msg != 0 && t.icon.Window != 0:
		t.addIcon()
	default:
		r, _, _ := defWindowProc.Call(hwnd, msg, wParam, lParam)
		return r
	}
	return 0
}

func (t *tray) showMenu(hwnd uintptr, p point) {
	if t.menuOpen {
		return
	}
	t.menuOpen = true
	defer func() { t.menuOpen = false }()
	menu, _, _ := createPopupMenu.Call()
	if menu == 0 {
		return
	}
	defer destroyMenu.Call(menu)
	const (
		menuPair    = 1
		menuExit    = 2
		mfGrayed    = 1
		mfSeparator = 0x800
		tpmFlags    = 0x2 | 0x80 | 0x100 // TPM_RIGHTBUTTON | TPM_NONOTIFY | TPM_RETURNCMD
	)
	appendMenuItem := func(flags, id uintptr, label string) {
		text, _ := windows.UTF16PtrFromString(label)
		appendMenu.Call(menu, flags, id, uintptr(unsafe.Pointer(text)))
	}
	appendMenuItem(mfGrayed, 0, "Buffer: "+t.status.String())
	appendMenu.Call(menu, mfSeparator, 0, 0)
	appendMenuItem(0, menuPair, pairPhoneLabel)
	appendMenuItem(0, menuExit, "Exit")
	setForegroundWindow.Call(hwnd)
	selected, _, _ := trackPopupMenu.Call(menu, tpmFlags, uintptr(p.X), uintptr(p.Y), 0, hwnd, 0)
	postMessage.Call(hwnd, 0, 0, 0) // WM_NULL lets subsequent tray menus dismiss correctly.
	switch selected {
	case menuPair:
		t.showPairing(hwnd)
	case menuExit:
		postQuitMessage.Call(0)
	default:
		t.notify(nimSetFocus)
	}
}

func (t *tray) showPairing(hwnd uintptr) {
	if err := t.ui.showTrayQR(hwnd, t.pairing); err != nil {
		showMessage(hwnd, err.Error(), mbError)
	}
}
