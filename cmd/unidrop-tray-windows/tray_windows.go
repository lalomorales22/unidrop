package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

const (
	wmDestroy     = 0x0002
	wmClose       = 0x0010
	wmNull        = 0x0000
	wmContextMenu = 0x007b
	wmTimer       = 0x0113
	wmLButtonUp   = 0x0202
	wmLButtonDbl  = 0x0203
	wmRButtonUp   = 0x0205
	wmUser        = 0x0400
	wmApp         = 0x8000

	ninSelect       = wmUser
	ninKeySelect    = wmUser + 1
	ninBalloonClick = wmUser + 5

	nimAdd        = 0x00000000
	nimModify     = 0x00000001
	nimDelete     = 0x00000002
	nimSetVersion = 0x00000004

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010
	nifShowTip = 0x00000080

	notifyIconVersion4 = 4
	niifInfo           = 0x00000001

	mfString    = 0x00000000
	mfGray      = 0x00000001
	mfDisabled  = 0x00000002
	mfSeparator = 0x00000800
	mfByCommand = 0x00000000

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	createNoWindow     = 0x08000000
	errorAlreadyExists = 183

	trayCallbackMessage = wmApp + 1
	refreshTimerID      = 1
)

const (
	menuStatus = 100 + iota
	menuOpen
	menuModeAsk
	menuModeTrusted
	menuModeOff
	menuDownloads
	menuQuit
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")

	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procRegisterWindowMsgW  = user32.NewProc("RegisterWindowMessageW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procCheckMenuRadioItem  = user32.NewProc("CheckMenuRadioItem")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procSetTimer            = user32.NewProc("SetTimer")
	procKillTimer           = user32.NewProc("KillTimer")
	procLoadCursorW         = user32.NewProc("LoadCursorW")
	procCreateIconIndirect  = user32.NewProc("CreateIconIndirect")
	procDestroyIcon         = user32.NewProc("DestroyIcon")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	procShellExecuteW       = shell32.NewProc("ShellExecuteW")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procCreateMutexW        = kernel32.NewProc("CreateMutexW")
	procCloseHandle         = kernel32.NewProc("CloseHandle")
	procRtlMoveMemory       = kernel32.NewProc("RtlMoveMemory")
	procCreateDIBSection    = gdi32.NewProc("CreateDIBSection")
	procCreateBitmap        = gdi32.NewProc("CreateBitmap")
	procDeleteObject        = gdi32.NewProc("DeleteObject")

	activeTray *windowsTray
)

type point struct {
	X int32
	Y int32
}

type windowMessage struct {
	Window   uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Point    point
	LPrivate uint32
}

type windowClassEx struct {
	Size            uint32
	Style           uint32
	WindowProc      uintptr
	ClassExtra      int32
	WindowExtra     int32
	Instance        uintptr
	Icon            uintptr
	Cursor          uintptr
	BackgroundBrush uintptr
	MenuName        *uint16
	ClassName       *uint16
	SmallIcon       uintptr
}

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

type notifyIconData struct {
	Size        uint32
	Window      uintptr
	ID          uint32
	Flags       uint32
	Callback    uint32
	Icon        uintptr
	Tip         [128]uint16
	State       uint32
	StateMask   uint32
	Info        [256]uint16
	Version     uint32
	InfoTitle   [64]uint16
	InfoFlags   uint32
	ItemGUID    guid
	BalloonIcon uintptr
}

type iconInfo struct {
	IsIcon   int32
	HotspotX uint32
	HotspotY uint32
	Mask     uintptr
	Color    uintptr
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	ImageSize     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ColorsUsed    uint32
	ColorsNeeded  uint32
}

// These layouts are the 64-bit Win32 ABI contracts used by the supported
// Windows AMD64 and ARM64 builds. Both directions make any size drift fail at
// compile time instead of corrupting memory at runtime.
var (
	_ [976 - unsafe.Sizeof(notifyIconData{})]byte
	_ [unsafe.Sizeof(notifyIconData{}) - 976]byte
	_ [80 - unsafe.Sizeof(windowClassEx{})]byte
	_ [unsafe.Sizeof(windowClassEx{}) - 80]byte
	_ [48 - unsafe.Sizeof(windowMessage{})]byte
	_ [unsafe.Sizeof(windowMessage{}) - 48]byte
	_ [32 - unsafe.Sizeof(iconInfo{})]byte
	_ [unsafe.Sizeof(iconInfo{}) - 32]byte
)

type windowsTray struct {
	client         *coreClient
	window         uintptr
	icon           uintptr
	iconData       notifyIconData
	summary        coreSummary
	taskbarCreated uint32
	lastCoreStart  time.Time
	iconInstalled  bool
}

func runTray(baseURL string, openOnStart bool) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	client, err := newCoreClient(baseURL)
	if err != nil {
		return err
	}
	mutexName := utf16Pointer("Local\\UniDropTray")
	mutex, _, mutexErr := procCreateMutexW.Call(0, 1, uintptr(unsafe.Pointer(mutexName)))
	if mutex == 0 {
		return fmt.Errorf("create tray mutex: %w", mutexErr)
	}
	defer procCloseHandle.Call(mutex)
	if errno, ok := mutexErr.(syscall.Errno); ok && errno == errorAlreadyExists {
		if openOnStart {
			_ = openURL(baseURL)
		}
		return nil
	}

	summary, summaryErr := client.summary()
	if summaryErr != nil {
		_ = startCoreProcess()
		summary = waitForCore(client, 5*time.Second)
	}
	tray := &windowsTray{client: client, summary: summary, lastCoreStart: time.Now()}
	activeTray = tray
	defer func() { activeTray = nil }()
	if err := tray.createWindow(); err != nil {
		return err
	}
	defer tray.removeIcon()
	if err := tray.installIcon(); err != nil {
		return err
	}
	if timer, _, timerErr := procSetTimer.Call(tray.window, refreshTimerID, 2000, 0); timer == 0 {
		return fmt.Errorf("start tray refresh timer: %w", timerErr)
	}
	defer procKillTimer.Call(tray.window, refreshTimerID)
	if openOnStart {
		_ = tray.openPanel()
	}
	log.Printf("UniDrop %s Windows notification-area icon registered", appVersion)

	var message windowMessage
	for {
		result, _, getErr := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) == -1 {
			return fmt.Errorf("read Windows message: %w", getErr)
		}
		if result == 0 {
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
}

func (a *windowsTray) createWindow() error {
	instance, _, instanceErr := procGetModuleHandleW.Call(0)
	if instance == 0 {
		return fmt.Errorf("get application module: %w", instanceErr)
	}
	className := utf16Pointer("UniDropTrayWindow")
	cursor, _, _ := procLoadCursorW.Call(0, 32512)
	class := windowClassEx{
		Size:       uint32(unsafe.Sizeof(windowClassEx{})),
		WindowProc: syscall.NewCallback(windowProc),
		Instance:   instance,
		Cursor:     cursor,
		ClassName:  className,
	}
	if atom, _, registerErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		return fmt.Errorf("register tray window: %w", registerErr)
	}
	windowName := utf16Pointer("UniDrop")
	window, _, createErr := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windowName)), 0,
		0, 0, 0, 0, 0, 0, instance, 0,
	)
	if window == 0 {
		return fmt.Errorf("create tray window: %w", createErr)
	}
	a.window = window
	taskbarMessage := utf16Pointer("TaskbarCreated")
	registered, _, registerMessageErr := procRegisterWindowMsgW.Call(uintptr(unsafe.Pointer(taskbarMessage)))
	if registered == 0 {
		return fmt.Errorf("register taskbar recovery message: %w", registerMessageErr)
	}
	a.taskbarCreated = uint32(registered)
	return nil
}

func windowProc(window uintptr, message uint32, wParam, lParam uintptr) uintptr {
	a := activeTray
	if a == nil || (a.window != 0 && a.window != window) {
		result, _, _ := procDefWindowProcW.Call(window, uintptr(message), wParam, lParam)
		return result
	}
	if a.window == 0 {
		a.window = window
	}
	if message == a.taskbarCreated && a.taskbarCreated != 0 {
		a.iconInstalled = false
		_ = a.installIcon()
		return 0
	}
	switch message {
	case trayCallbackMessage:
		event := uint32(lParam & 0xffff)
		switch event {
		case wmLButtonUp, wmLButtonDbl, ninSelect, ninKeySelect, ninBalloonClick:
			if err := a.openPanel(); err != nil {
				a.showBalloon("UniDrop is starting", err.Error())
			}
		case wmRButtonUp, wmContextMenu:
			a.showMenu()
		}
		return 0
	case wmTimer:
		if wParam == refreshTimerID {
			a.refresh()
			return 0
		}
	case wmClose:
		procDestroyWindow.Call(window)
		return 0
	case wmDestroy:
		a.removeIcon()
		procPostQuitMessage.Call(0)
		return 0
	}
	result, _, _ := procDefWindowProcW.Call(window, uintptr(message), wParam, lParam)
	return result
}

func (a *windowsTray) installIcon() error {
	if a.icon == 0 {
		_, state := statusPresentation(a.summary)
		icon, err := createStateIcon(32, state)
		if err != nil {
			return err
		}
		a.icon = icon
	}
	tooltip, _ := statusPresentation(a.summary)
	a.iconData = notifyIconData{
		Size:     uint32(unsafe.Sizeof(notifyIconData{})),
		Window:   a.window,
		ID:       1,
		Flags:    nifMessage | nifIcon | nifTip | nifShowTip,
		Callback: trayCallbackMessage,
		Icon:     a.icon,
	}
	copyUTF16(a.iconData.Tip[:], tooltip)
	if result, _, notifyErr := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&a.iconData))); result == 0 {
		return fmt.Errorf("add notification-area icon: %w", notifyErr)
	}
	a.iconInstalled = true
	a.iconData.Version = notifyIconVersion4
	if result, _, versionErr := procShellNotifyIconW.Call(nimSetVersion, uintptr(unsafe.Pointer(&a.iconData))); result == 0 {
		return fmt.Errorf("set notification-area icon version: %w", versionErr)
	}
	return nil
}

func (a *windowsTray) updateIcon() {
	if !a.iconInstalled {
		_ = a.installIcon()
		return
	}
	tooltip, state := statusPresentation(a.summary)
	nextIcon, err := createStateIcon(32, state)
	if err != nil {
		log.Printf("create tray state icon: %v", err)
		return
	}
	data := notifyIconData{
		Size:   uint32(unsafe.Sizeof(notifyIconData{})),
		Window: a.window,
		ID:     1,
		Flags:  nifIcon | nifTip | nifShowTip,
		Icon:   nextIcon,
	}
	copyUTF16(data.Tip[:], tooltip)
	if result, _, notifyErr := procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&data))); result == 0 {
		procDestroyIcon.Call(nextIcon)
		log.Printf("update notification-area icon: %v", notifyErr)
		return
	}
	previous := a.icon
	a.icon = nextIcon
	a.iconData.Icon = nextIcon
	if previous != 0 {
		procDestroyIcon.Call(previous)
	}
}

func (a *windowsTray) removeIcon() {
	if a.iconInstalled {
		data := notifyIconData{Size: uint32(unsafe.Sizeof(notifyIconData{})), Window: a.window, ID: 1}
		procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
		a.iconInstalled = false
	}
	if a.icon != 0 {
		procDestroyIcon.Call(a.icon)
		a.icon = 0
	}
}

func (a *windowsTray) refresh() {
	previous := a.summary
	next, err := a.client.summary()
	if err != nil {
		next = coreSummary{}
		if time.Since(a.lastCoreStart) >= 10*time.Second {
			if startErr := startCoreProcess(); startErr != nil {
				log.Printf("restart secure core: %v", startErr)
			}
			a.lastCoreStart = time.Now()
		}
	}
	if next == previous {
		return
	}
	a.summary = next
	a.updateIcon()
	if next.Available && next.Pending > previous.Pending {
		a.showBalloon("Incoming UniDrop request", fmt.Sprintf("%d file request%s waiting for approval", next.Pending, pluralSuffix(next.Pending)))
	}
}

func (a *windowsTray) showBalloon(title, message string) {
	data := notifyIconData{
		Size:      uint32(unsafe.Sizeof(notifyIconData{})),
		Window:    a.window,
		ID:        1,
		Flags:     nifInfo,
		InfoFlags: niifInfo,
	}
	copyUTF16(data.InfoTitle[:], title)
	copyUTF16(data.Info[:], message)
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&data)))
}

func (a *windowsTray) showMenu() {
	menu, _, menuErr := procCreatePopupMenu.Call()
	if menu == 0 {
		log.Printf("create tray menu: %v", menuErr)
		return
	}
	defer procDestroyMenu.Call(menu)
	statusFlags := uintptr(mfString | mfGray | mfDisabled)
	appendMenu(menu, statusFlags, menuStatus, summaryLabel(a.summary))
	appendMenu(menu, mfString, menuOpen, openLabel(a.summary))
	appendSeparator(menu)
	actionFlags := uintptr(mfString)
	if !a.summary.Available {
		actionFlags |= mfGray | mfDisabled
	}
	appendMenu(menu, actionFlags, menuModeAsk, "Ask before receiving")
	appendMenu(menu, actionFlags, menuModeTrusted, "Auto-accept paired devices")
	appendMenu(menu, actionFlags, menuModeOff, "Receiving paused")
	checked := menuModeAsk
	switch a.summary.ReceiveMode {
	case "trusted":
		checked = menuModeTrusted
	case "off":
		checked = menuModeOff
	}
	procCheckMenuRadioItem.Call(menu, menuModeAsk, menuModeOff, uintptr(checked), mfByCommand)
	appendSeparator(menu)
	appendMenu(menu, actionFlags, menuDownloads, "Open received files")
	appendSeparator(menu)
	appendMenu(menu, mfString, menuQuit, "Quit UniDrop")

	var cursor point
	if result, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor))); result == 0 {
		return
	}
	procSetForegroundWindow.Call(a.window)
	command, _, _ := procTrackPopupMenu.Call(menu, tpmRightButton|tpmReturnCmd, uintptr(cursor.X), uintptr(cursor.Y), 0, a.window, 0)
	procPostMessageW.Call(a.window, wmNull, 0, 0)
	if command != 0 {
		a.dispatch(int(command))
	}
}

func (a *windowsTray) dispatch(command int) {
	var err error
	switch command {
	case menuOpen:
		err = a.openPanel()
	case menuModeAsk:
		err = a.client.setReceiveMode("ask")
	case menuModeTrusted:
		err = a.client.setReceiveMode("trusted")
	case menuModeOff:
		err = a.client.setReceiveMode("off")
	case menuDownloads:
		err = a.client.openDownloads()
	case menuQuit:
		_ = a.client.shutdown()
		procDestroyWindow.Call(a.window)
		return
	}
	if err != nil {
		log.Printf("tray action failed: %v", err)
		a.showBalloon("UniDrop action failed", err.Error())
		return
	}
	if command == menuModeAsk || command == menuModeTrusted || command == menuModeOff {
		a.refresh()
	}
}

func (a *windowsTray) openPanel() error {
	if summary, err := a.client.summary(); err == nil {
		if summary != a.summary {
			a.summary = summary
			a.updateIcon()
		}
	} else {
		if err := startCoreProcess(); err != nil {
			return err
		}
		a.lastCoreStart = time.Now()
		summary := waitForCore(a.client, 5*time.Second)
		if !summary.Available {
			return errors.New("the secure service has not become ready yet")
		}
		a.summary = summary
		a.updateIcon()
	}
	return openURL(a.client.baseURL)
}

func startCoreProcess() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	core := filepath.Join(filepath.Dir(executable), "unidrop.exe")
	if info, err := os.Stat(core); err != nil || !info.Mode().IsRegular() {
		return errors.New("installed UniDrop core was not found")
	}
	command := exec.Command(core, "--no-open")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if configDir, configErr := os.UserConfigDir(); configErr == nil {
		logDir := filepath.Join(configDir, "UniDrop")
		if os.MkdirAll(logDir, 0700) == nil {
			if coreLog, logErr := os.OpenFile(filepath.Join(logDir, "core.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600); logErr == nil {
				command.Stdout = coreLog
				command.Stderr = coreLog
				defer coreLog.Close()
			}
		}
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func openURL(target string) error {
	operation := utf16Pointer("open")
	path := utf16Pointer(target)
	result, _, shellErr := procShellExecuteW.Call(0, uintptr(unsafe.Pointer(operation)), uintptr(unsafe.Pointer(path)), 0, 0, 1)
	if result <= 32 {
		return fmt.Errorf("open UniDrop: %w", shellErr)
	}
	return nil
}

func appendMenu(menu, flags uintptr, id int, label string) {
	text := utf16Pointer(label)
	procAppendMenuW.Call(menu, flags, uintptr(id), uintptr(unsafe.Pointer(text)))
}

func appendSeparator(menu uintptr) {
	procAppendMenuW.Call(menu, mfSeparator, 0, 0)
}

func openLabel(summary coreSummary) string {
	if summary.Pending > 0 {
		return fmt.Sprintf("Open UniDrop - %d waiting", summary.Pending)
	}
	return "Open UniDrop"
}

func utf16Pointer(value string) *uint16 {
	pointer, err := syscall.UTF16PtrFromString(value)
	if err != nil {
		panic(err)
	}
	return pointer
}

func copyUTF16(destination []uint16, value string) {
	encoded, err := syscall.UTF16FromString(value)
	if err != nil {
		return
	}
	copy(destination, encoded)
	if len(destination) > 0 {
		destination[len(destination)-1] = 0
	}
}

func createStateIcon(size int, state iconState) (uintptr, error) {
	header := bitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:       int32(size),
		Height:      -int32(size),
		Planes:      1,
		BitCount:    32,
		Compression: 0,
		ImageSize:   uint32(size * size * 4),
	}
	var pixels uintptr
	color, _, colorErr := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&header)), 0, uintptr(unsafe.Pointer(&pixels)), 0, 0)
	if color == 0 || pixels == 0 {
		return 0, fmt.Errorf("create icon bitmap: %w", colorErr)
	}
	defer procDeleteObject.Call(color)
	bitmap := make([]byte, size*size*4)
	drawStateIcon(bitmap, size, state)
	procRtlMoveMemory.Call(pixels, uintptr(unsafe.Pointer(&bitmap[0])), uintptr(len(bitmap)))
	runtime.KeepAlive(bitmap)
	mask, _, maskErr := procCreateBitmap.Call(uintptr(size), uintptr(size), 1, 1, 0)
	if mask == 0 {
		return 0, fmt.Errorf("create icon mask: %w", maskErr)
	}
	defer procDeleteObject.Call(mask)
	info := iconInfo{IsIcon: 1, Mask: mask, Color: color}
	icon, _, iconErr := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&info)))
	if icon == 0 {
		return 0, fmt.Errorf("create state icon: %w", iconErr)
	}
	return icon, nil
}
