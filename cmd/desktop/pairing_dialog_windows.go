package main

import (
	"encoding/json"
	"errors"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmCommand   = 0x0111
	wmInitDlg   = 0x0110
	wmDrawItem  = 0x002b
	wmNCDestroy = 0x0082
	idOK        = 1
	idCancel    = 2
	idRelay     = 100
	idToken     = 101
	idQR        = 102

	wsTabStop = 0x00010000
	bsDefault = 0x0001
	esScroll  = 0x0080 // ES_AUTOHSCROLL
	esSecret  = 0x0020 // ES_PASSWORD
	ssDraw    = 0x000d // SS_OWNERDRAW

	dlgButton = 0x0080
	dlgEdit   = 0x0081
	dlgStatic = 0x0082
)

var (
	dialogBox           = user.NewProc("DialogBoxIndirectParamW")
	createDialog        = user.NewProc("CreateDialogIndirectParamW")
	endDialog           = user.NewProc("EndDialog")
	isDialogMessage     = user.NewProc("IsDialogMessageW")
	showWindow          = user.NewProc("ShowWindow")
	getDlgItem          = user.NewProc("GetDlgItem")
	getWindowTextLength = user.NewProc("GetWindowTextLengthW")
	getWindowText       = user.NewProc("GetWindowTextW")
	setThreadDPI        = user.NewProc("SetThreadDpiAwarenessContext")
	fillRect            = user.NewProc("FillRect")
	getStockObject      = windows.NewLazySystemDLL("gdi32.dll").NewProc("GetStockObject")
)

type dialogControl struct {
	class, id, x, y, width, height uint16
	style, exStyle                 uint32
	label                          string
}

// Build a standard Win32 dialog template in dialog units. IDs, focus order,
// default buttons and the dialog font are interpreted by the dialog manager.
func dialogTemplate(width, height uint16, title string, controls []dialogControl) []uint16 {
	const style = 0x80c80000 | 0x0840 // WS_POPUP | WS_CAPTION | WS_SYSMENU | DS_CENTER | DS_SETFONT
	t := []uint16{style & 0xffff, style >> 16, 0, 0, uint16(len(controls)), 0, 0, width, height, 0, 0}
	text := func(s string) { t = append(append(t, utf16.Encode([]rune(s))...), 0) }
	text(title)
	t = append(t, 9) // Point size (DS_SETFONT).
	text("Segoe UI")
	for _, c := range controls {
		if len(t)%2 != 0 { // Each DLGITEMTEMPLATE starts at a DWORD boundary.
			t = append(t, 0)
		}
		style := c.style | 0x50000000 // WS_CHILD | WS_VISIBLE
		t = append(t, uint16(style), uint16(style>>16), uint16(c.exStyle), uint16(c.exStyle>>16),
			c.x, c.y, c.width, c.height, c.id, 0xffff, c.class)
		text(c.label)
		t = append(t, 0) // No creation data.
	}
	return t
}

var setupTemplate = dialogTemplate(244, 122, "Buffer — Set up pairing", []dialogControl{
	{class: dlgStatic, id: 0xffff, x: 10, y: 9, width: 224, height: 11, label: "&Relay HTTPS URL"},
	{class: dlgEdit, id: idRelay, x: 10, y: 23, width: 224, height: 14, style: wsTabStop | esScroll, exStyle: 0x200}, // WS_EX_CLIENTEDGE
	{class: dlgStatic, id: 0xffff, x: 10, y: 44, width: 224, height: 11, label: "BUFFER_&TOKEN from your relay"},
	{class: dlgEdit, id: idToken, x: 10, y: 58, width: 224, height: 14, style: wsTabStop | esScroll | esSecret, exStyle: 0x200},
	{class: dlgStatic, id: 0xffff, x: 10, y: 77, width: 224, height: 11, label: "The token is hidden and saved encrypted on this PC."},
	{class: dlgButton, id: idOK, x: 110, y: 98, width: 70, height: 15, style: wsTabStop | bsDefault, label: "Show QR"},
	{class: dlgButton, id: idCancel, x: 184, y: 98, width: 50, height: 15, style: wsTabStop, label: "Cancel"},
})

var qrTemplate = dialogTemplate(252, 283, "Buffer — Pair phone", []dialogControl{
	{class: dlgStatic, id: 0xffff, x: 10, y: 10, width: 232, height: 25, label: "Open Buffer on Android and scan this code. Keep it private: it grants access to your pairing."},
	{class: dlgStatic, id: idQR, x: 10, y: 37, width: 232, height: 220, style: ssDraw},
	{class: dlgButton, id: idCancel, x: 192, y: 261, width: 50, height: 15, style: wsTabStop | bsDefault, label: "Close"},
})

type pairingUI struct {
	module, setupProc, qrProc uintptr
	submit                    func(relay, token string) error
	qrWindow                  uintptr
	qrModal                   bool
	bitmap                    [][]bool
}

func newPairingUI() (*pairingUI, error) {
	var module windows.Handle
	if err := windows.GetModuleHandleEx(windows.GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT, nil, &module); err != nil {
		return nil, err
	}
	u := &pairingUI{module: uintptr(module)}
	u.setupProc = windows.NewCallback(u.setupDialogProc)
	u.qrProc = windows.NewCallback(u.qrDialogProc)
	return u, nil
}

// Let Windows scale the controls at the monitor's DPI rather than letting it
// bitmap-scale the whole window (including the QR) on recent Windows versions.
func pairingDialogDPI() func() {
	if setThreadDPI.Find() == nil {
		if old, _, _ := setThreadDPI.Call(^uintptr(3)); old != 0 { // PER_MONITOR_AWARE_V2 (-4)
			return func() { setThreadDPI.Call(old) }
		}
	}
	return func() {}
}

func (u *pairingUI) setup(path string) (p pairing, bitmap [][]bool, err error) {
	u.submit = func(relay, token string) error {
		candidate, err := newPairing(relay, token)
		if err != nil {
			return err
		}
		code, err := pairingQR(candidate)
		if err != nil {
			return err
		}
		b, err := json.Marshal(candidate) // Keep the stored schema independent of the QR payload.
		if err != nil || savePairing(path, b) != nil {
			return errors.New("Buffer could not save the pairing to %APPDATA%\\Buffer\\pair.bin.")
		}
		p, bitmap = candidate, code
		return nil
	}
	defer func() { u.submit = nil }()
	defer pairingDialogDPI()()
	r, _, _ := dialogBox.Call(u.module, uintptr(unsafe.Pointer(&setupTemplate[0])), 0, u.setupProc, 0)
	if r == ^uintptr(0) {
		return pairing{}, nil, errors.New("Buffer could not open the pairing setup window.")
	}
	return p, bitmap, nil
}

func dialogInput(hwnd, id uintptr) string {
	edit, _, _ := getDlgItem.Call(hwnd, id)
	n, _, _ := getWindowTextLength.Call(edit)
	b := make([]uint16, n+1)
	getWindowText.Call(edit, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	defer clear(b)
	return windows.UTF16ToString(b)
}

func (u *pairingUI) setupDialogProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch {
	case msg == wmInitDlg:
		return 1 // Assign focus to the first tab stop.
	case msg == wmCommand && wParam == idOK:
		if err := u.submit(dialogInput(hwnd, idRelay), dialogInput(hwnd, idToken)); err != nil {
			showMessage(hwnd, err.Error(), mbError)
		} else {
			endDialog.Call(hwnd, idOK)
		}
		return 1
	case msg == wmClose || msg == wmCommand && wParam == idCancel:
		endDialog.Call(hwnd, idCancel)
		return 1
	}
	return 0
}

var errQRWindow = errors.New("Buffer could not open the pairing QR window.")

func (u *pairingUI) showStartupQR(bitmap [][]bool) error {
	u.bitmap = bitmap
	defer func() { u.bitmap = nil }()
	defer pairingDialogDPI()()
	r, _, _ := dialogBox.Call(u.module, uintptr(unsafe.Pointer(&qrTemplate[0])), 0, u.qrProc, 1)
	if r == ^uintptr(0) {
		return errQRWindow
	}
	return nil
}

func (u *pairingUI) showTrayQR(owner uintptr, p pairing) error {
	if u.qrWindow != 0 {
		showWindow.Call(u.qrWindow, 9) // SW_RESTORE
		setForegroundWindow.Call(u.qrWindow)
		return nil
	}
	bitmap, err := pairingQR(p)
	if err != nil {
		return err
	}
	u.bitmap = bitmap
	defer pairingDialogDPI()()
	hwnd, _, _ := createDialog.Call(u.module, uintptr(unsafe.Pointer(&qrTemplate[0])), owner, u.qrProc, 0)
	if hwnd == 0 {
		u.bitmap = nil
		return errQRWindow
	}
	showWindow.Call(hwnd, 5) // SW_SHOW
	setForegroundWindow.Call(hwnd)
	return nil
}

func (u *pairingUI) qrDialogProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch {
	case msg == wmInitDlg:
		u.qrWindow, u.qrModal = hwnd, lParam != 0
		return 1
	case msg == wmDrawItem && wParam == idQR:
		drawQR((*drawItem)(unsafe.Pointer(lParam)), u.bitmap)
		return 1
	case msg == wmClose || msg == wmCommand && wParam == idCancel:
		if u.qrModal {
			endDialog.Call(hwnd, idOK)
		} else {
			destroyWindow.Call(hwnd)
		}
		return 1
	case msg == wmNCDestroy && u.qrWindow == hwnd:
		u.qrWindow, u.bitmap = 0, nil
	}
	return 0
}

type rect struct{ left, top, right, bottom int32 }

type drawItem struct { // DRAWITEMSTRUCT
	controlType, controlID, itemID, action, state uint32
	window, dc                                    uintptr
	bounds                                        rect
	data                                          uintptr
}

func drawQR(item *drawItem, bitmap [][]bool) {
	white, _, _ := getStockObject.Call(0) // WHITE_BRUSH
	black, _, _ := getStockObject.Call(4) // BLACK_BRUSH
	bounds := item.bounds
	fillRect.Call(item.dc, uintptr(unsafe.Pointer(&bounds)), white)
	if len(bitmap) == 0 {
		return
	}
	w, h, n := bounds.right-bounds.left, bounds.bottom-bounds.top, int32(len(bitmap))
	size := min(w, h) / n
	if size == 0 {
		return
	}
	x0, y0 := bounds.left+(w-n*size)/2, bounds.top+(h-n*size)/2
	for y, row := range bitmap {
		for x := 0; x < len(row); x++ {
			if !row[x] {
				continue
			}
			start := x
			for x+1 < len(row) && row[x+1] {
				x++
			}
			box := rect{x0 + int32(start)*size, y0 + int32(y)*size, x0 + int32(x+1)*size, y0 + int32(y+1)*size}
			fillRect.Call(item.dc, uintptr(unsafe.Pointer(&box)), black)
		}
	}
}
