package main

import (
	"errors"
	"golang.org/x/sys/windows"
	"runtime"
	"time"
	"unsafe"
)

var (
	user           = windows.NewLazySystemDLL("user32.dll")
	kernel         = windows.NewLazySystemDLL("kernel32.dll")
	createWindow   = user.NewProc("CreateWindowExW")
	destroyWindow  = user.NewProc("DestroyWindow")
	openClipboard  = user.NewProc("OpenClipboard")
	closeClipboard = user.NewProc("CloseClipboard")
	emptyClipboard = user.NewProc("EmptyClipboard")
	setClipboard   = user.NewProc("SetClipboardData")
	alloc          = kernel.NewProc("GlobalAlloc")
	lock           = kernel.NewProc("GlobalLock")
	unlock         = kernel.NewProc("GlobalUnlock")
	free           = kernel.NewProc("GlobalFree")
)

func writeClipboard(s string) error {
	words, err := windows.UTF16FromString(s)
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	class, _ := windows.UTF16PtrFromString("STATIC")
	hwnd, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	if hwnd == 0 {
		return err
	}
	defer destroyWindow.Call(hwnd)
	mem, _, err := alloc.Call(2, uintptr(len(words)*2))
	if mem == 0 {
		return err
	}
	owned := true
	defer func() {
		if owned {
			free.Call(mem)
		}
	}()
	ptr, _, err := lock.Call(mem)
	if ptr == 0 {
		return err
	}
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(words)), words)
	unlock.Call(mem)
	for i := 0; ; i++ {
		ok, _, e := openClipboard.Call(hwnd)
		if ok != 0 {
			break
		}
		if i == 14 {
			return e
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer closeClipboard.Call()
	if ok, _, e := emptyClipboard.Call(); ok == 0 {
		return e
	}
	if ok, _, e := setClipboard.Call(13, mem); ok == 0 {
		return e
	}
	owned = false
	return nil
}

func readClipboard() (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for i := 0; ; i++ {
		ok, _, err := openClipboard.Call(0)
		if ok != 0 {
			break
		}
		if i == 14 {
			return "", err
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer closeClipboard.Call()
	handle, _, err := user.NewProc("GetClipboardData").Call(13)
	if handle == 0 {
		return "", err
	}
	size, _, err := kernel.NewProc("GlobalSize").Call(handle)
	if size < 2 {
		return "", errors.New("empty clipboard")
	}
	ptr, _, err := lock.Call(handle)
	if ptr == 0 {
		return "", err
	}
	defer unlock.Call(handle)
	words := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), min(size/2, 65537))
	for i, ch := range words {
		if ch == 0 {
			return windows.UTF16ToString(words[:i]), nil
		}
	}
	return "", errors.New("clipboard text exceeds limit")
}
