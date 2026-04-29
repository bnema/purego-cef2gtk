package gtkgl

import (
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/puregotk/v4/gdk"
	"github.com/bnema/puregotk/v4/gio"
	"github.com/bnema/puregotk/v4/gtk"
)

const (
	gdkDeadKeyStart = 0xfe50
	gdkDeadKeyEnd   = 0xfe8c

	maxBMPCodepoint     = 0xFFFF
	minPrintable        = 0x20
	maxSingleByteKeyval = 0x100
)

// InputBridge translates GTK/GDK input events from a GtkGLArea into CEF OSR input.
type InputBridge struct {
	mu    sync.Mutex
	host  cef.BrowserHost
	scale int32

	lastX, lastY float64
	clipboard    *gdk.Clipboard
	imContext    *gtk.IMContextSimple
	detached     bool

	area        *gtk.GLArea
	controllers []*gtk.EventController
	callbacks   []any
}

// NewInputBridge creates an input bridge. Scale values <= 0 are treated as 1.
func NewInputBridge(host cef.BrowserHost, scale int32) *InputBridge {
	if scale <= 0 {
		scale = 1
	}
	return &InputBridge{host: host, scale: scale}
}

// SetHost updates the CEF browser host used for subsequent input dispatch.
func (ib *InputBridge) SetHost(host cef.BrowserHost) {
	if ib == nil {
		return
	}
	ib.mu.Lock()
	ib.host = host
	ib.mu.Unlock()
}

// Attach creates GTK event controllers and attaches them to the GLArea.
func (ib *InputBridge) Attach(area *gtk.GLArea) {
	if ib == nil || area == nil {
		return
	}
	ib.mu.Lock()
	ib.area = area
	ib.detached = false
	if display := gdk.DisplayGetDefault(); display != nil {
		ib.clipboard = display.GetClipboard()
	}
	ib.mu.Unlock()

	motion := gtk.NewEventControllerMotion()
	motionCb := func(g gtk.EventControllerMotion, x, y float64) {
		ib.onMouseMove(x, y, uint(g.GetCurrentEventState()), false)
	}
	motion.ConnectMotion(&motionCb)
	leaveCb := func(g gtk.EventControllerMotion) {
		ib.onMouseMove(0, 0, uint(g.GetCurrentEventState()), true)
	}
	motion.ConnectLeave(&leaveCb)
	ib.addController(area, &motion.EventController, &motionCb, &leaveCb)

	click := gtk.NewGestureClick()
	click.SetButton(0)
	pressedCb := func(g gtk.GestureClick, nPress int, x, y float64) {
		area.GrabFocus()
		ib.onMousePress(x, y, g.GetCurrentButton(), uint(g.GetCurrentEventState()), nPress)
	}
	click.ConnectPressed(&pressedCb)
	releasedCb := func(g gtk.GestureClick, nPress int, x, y float64) {
		ib.onMouseRelease(x, y, g.GetCurrentButton(), uint(g.GetCurrentEventState()), nPress)
	}
	click.ConnectReleased(&releasedCb)
	ib.addController(area, &click.EventController, &pressedCb, &releasedCb)

	scroll := gtk.NewEventControllerScroll(gtk.EventControllerScrollBothAxesValue | gtk.EventControllerScrollKineticValue)
	scrollCb := func(_ gtk.EventControllerScroll, dx, dy float64) bool {
		ib.onScroll(dx, dy)
		return true
	}
	scroll.ConnectScroll(&scrollCb)
	ib.addController(area, &scroll.EventController, &scrollCb)

	focus := gtk.NewEventControllerFocus()
	focusEnterCb := func(_ gtk.EventControllerFocus) {
		if ib.imContext != nil {
			ib.imContext.FocusIn()
		}
		ib.onFocusIn()
	}
	focus.ConnectEnter(&focusEnterCb)
	focusLeaveCb := func(_ gtk.EventControllerFocus) {
		if ib.imContext != nil {
			ib.imContext.Reset()
			ib.imContext.FocusOut()
		}
		ib.onFocusOut()
	}
	focus.ConnectLeave(&focusLeaveCb)
	ib.addController(area, &focus.EventController, &focusEnterCb, &focusLeaveCb)

	key := gtk.NewEventControllerKey()
	ib.imContext = gtk.NewIMContextSimple()
	if ib.imContext != nil {
		commitCb := func(_ gtk.IMContext, text string) { ib.onIMCommit(text) }
		ib.imContext.ConnectCommit(&commitCb)
		key.SetImContext(&ib.imContext.IMContext)
		ib.imContext.SetClientWidget(&area.Widget)
		ib.callbacks = append(ib.callbacks, &commitCb)
	}
	keyPressCb := func(_ gtk.EventControllerKey, keyval, keycode uint, state gdk.ModifierType) bool {
		mods := uint(state)
		if mods&uint(gdk.ControlMaskValue) != 0 && (keyval == gdkKeyLowercaseV || keyval == gdkKeyUppercaseV) {
			ib.pasteFromClipboard()
			return true
		}
		ib.onKeyPress(keyval, keycode, mods)
		if mods&uint(gdk.ControlMaskValue) != 0 || mods&uint(gdk.AltMaskValue) != 0 {
			return false
		}
		if keyval >= gdkKeyF1Start && keyval <= gdkKeyF12End || keyval == gdkKeyEscape {
			return false
		}
		return true
	}
	key.ConnectKeyPressed(&keyPressCb)
	keyReleaseCb := func(_ gtk.EventControllerKey, keyval, keycode uint, state gdk.ModifierType) {
		ib.onKeyRelease(keyval, keycode, uint(state))
	}
	key.ConnectKeyReleased(&keyReleaseCb)
	ib.addController(area, &key.EventController, &keyPressCb, &keyReleaseCb)

	area.SetFocusable(true)
	area.SetCanFocus(true)
}

func (ib *InputBridge) addController(area *gtk.GLArea, controller *gtk.EventController, callbacks ...any) {
	area.AddController(controller)
	ib.mu.Lock()
	ib.controllers = append(ib.controllers, controller)
	ib.callbacks = append(ib.callbacks, callbacks...)
	ib.mu.Unlock()
}

// Detach removes controllers previously attached by this bridge.
func (ib *InputBridge) Detach() {
	if ib == nil {
		return
	}
	ib.mu.Lock()
	area := ib.area
	controllers := append([]*gtk.EventController(nil), ib.controllers...)
	ib.detached = true
	ib.controllers = nil
	ib.callbacks = nil
	ib.imContext = nil
	ib.area = nil
	ib.clipboard = nil
	ib.mu.Unlock()
	if area == nil {
		return
	}
	for _, controller := range controllers {
		if controller != nil {
			area.RemoveController(controller)
		}
	}
}

func (ib *InputBridge) currentHost() cef.BrowserHost {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.host
}

func (ib *InputBridge) onMouseMove(x, y float64, mods uint, leave bool) {
	ib.mu.Lock()
	host := ib.host
	if !leave {
		ib.lastX, ib.lastY = x, y
	}
	ib.mu.Unlock()
	if host == nil {
		return
	}
	evt := BuildMouseEvent(x, y, mods, ib.scale)
	var mouseLeave int32
	if leave {
		mouseLeave = 1
	}
	host.SendMouseMoveEvent(&evt, mouseLeave)
}

func (ib *InputBridge) onMousePress(x, y float64, button, mods uint, clickCount int) {
	host := ib.currentHost()
	if host == nil {
		return
	}
	if button == 1 {
		syncWindowlessBrowserFocus(host)
	}
	evt := BuildMouseEvent(x, y, mods, ib.scale)
	host.SendMouseClickEvent(&evt, TranslateMouseButton(button), 0, int32(clickCount))
}

func (ib *InputBridge) onMouseRelease(x, y float64, button, mods uint, clickCount int) {
	host := ib.currentHost()
	if host == nil {
		return
	}
	evt := BuildMouseEvent(x, y, mods, ib.scale)
	host.SendMouseClickEvent(&evt, TranslateMouseButton(button), 1, int32(clickCount))
}

func (ib *InputBridge) onScroll(dx, dy float64) {
	ib.mu.Lock()
	host, x, y := ib.host, ib.lastX, ib.lastY
	ib.mu.Unlock()
	if host == nil {
		return
	}
	evt := BuildMouseEvent(x, y, 0, ib.scale)
	deltaX, deltaY := TranslateScrollDeltas(dx, dy)
	host.SendMouseWheelEvent(&evt, deltaX, deltaY)
}

func (ib *InputBridge) onFocusIn() { syncWindowlessBrowserFocus(ib.currentHost()) }
func (ib *InputBridge) onFocusOut() {
	if h := ib.currentHost(); h != nil {
		h.SetFocus(0)
	}
}

func syncWindowlessBrowserFocus(host cef.BrowserHost) {
	if host == nil {
		return
	}
	host.WasHidden(0)
	host.SetFocus(1)
	host.Invalidate(cef.PaintElementTypePetView)
}

func (ib *InputBridge) onKeyPress(keyval, keycode, mods uint) {
	host := ib.currentHost()
	if host == nil {
		return
	}
	if keyval >= gdkDeadKeyStart && keyval <= gdkDeadKeyEnd {
		return
	}
	evt := BuildKeyEvent(keyval, keycode, mods, cef.KeyEventTypeKeyeventRawkeydown)
	host.SendKeyEvent(&evt)
	if ch := KeyvalToChar(keyval); ch != 0 {
		charEvt := BuildKeyEvent(keyval, keycode, mods, cef.KeyEventTypeKeyeventChar)
		charEvt.WindowsKeyCode = int32(ch)
		charEvt.Character = ch
		charEvt.UnmodifiedCharacter = ch
		host.SendKeyEvent(&charEvt)
	}
}

func (ib *InputBridge) onIMCommit(text string) {
	host := ib.currentHost()
	if host == nil {
		return
	}
	for _, r := range text {
		if r > maxBMPCodepoint {
			hi, lo := utf16.EncodeRune(r)
			if hi == unicode.ReplacementChar || lo == unicode.ReplacementChar {
				continue
			}
			ib.sendChar(host, uint16(hi))
			ib.sendChar(host, uint16(lo))
			continue
		}
		ib.sendChar(host, uint16(r))
	}
}

func (ib *InputBridge) sendChar(host cef.BrowserHost, ch uint16) {
	evt := cef.NewKeyEvent()
	evt.Type = cef.KeyEventTypeKeyeventChar
	evt.WindowsKeyCode = int32(ch)
	evt.Character = ch
	evt.UnmodifiedCharacter = ch
	host.SendKeyEvent(&evt)
}

func (ib *InputBridge) onKeyRelease(keyval, keycode, mods uint) {
	host := ib.currentHost()
	if host == nil {
		return
	}
	evt := BuildKeyEvent(keyval, keycode, mods, cef.KeyEventTypeKeyeventKeyup)
	host.SendKeyEvent(&evt)
}

func (ib *InputBridge) pasteFromClipboard() {
	ib.mu.Lock()
	cb := ib.clipboard
	detached := ib.detached
	ib.mu.Unlock()
	if detached || cb == nil {
		return
	}
	asyncCb := gio.AsyncReadyCallback(func(_, resultPtr, _ uintptr) {
		text, err := cb.ReadTextFinish(&gio.AsyncResultBase{Ptr: resultPtr})
		if err != nil || text == "" {
			return
		}
		ib.mu.Lock()
		detached := ib.detached
		host := ib.host
		ib.mu.Unlock()
		if detached || host == nil {
			return
		}
		browser := host.GetBrowser()
		if browser == nil {
			return
		}
		frame := browser.GetMainFrame()
		if frame == nil {
			return
		}
		frame.ExecuteJavaScript("document.execCommand('insertText',false,"+jsString(text)+")", "", 0)
	})
	cb.ReadTextAsync(nil, &asyncCb, 0)
}

func jsString(s string) string {
	q := strconv.Quote(s)
	return strings.ReplaceAll(q, "</", "<\\/")
}

func BuildMouseEvent(x, y float64, gdkMods uint, scale int32) cef.MouseEvent {
	if scale <= 0 {
		scale = 1
	}
	return cef.MouseEvent{X: int32(x * float64(scale)), Y: int32(y * float64(scale)), Modifiers: TranslateModifiers(gdkMods)}
}

func BuildKeyEvent(keyval, keycode, gdkMods uint, eventType cef.KeyEventType) cef.KeyEvent {
	evt := cef.NewKeyEvent()
	evt.Type = eventType
	evt.WindowsKeyCode = GDKKeyvalToWindowsVK(keyval)
	evt.NativeKeyCode = int32(keycode)
	evt.Modifiers = TranslateModifiers(gdkMods)
	return evt
}

func TranslateModifiers(gdkState uint) uint32 {
	var flags uint32
	if gdkState&uint(gdk.ShiftMaskValue) != 0 {
		flags |= uint32(cef.EventFlagsEventflagShiftDown)
	}
	if gdkState&uint(gdk.ControlMaskValue) != 0 {
		flags |= uint32(cef.EventFlagsEventflagControlDown)
	}
	if gdkState&uint(gdk.AltMaskValue) != 0 {
		flags |= uint32(cef.EventFlagsEventflagAltDown)
	}
	if gdkState&uint(gdk.Button1MaskValue) != 0 {
		flags |= uint32(cef.EventFlagsEventflagLeftMouseButton)
	}
	if gdkState&uint(gdk.Button2MaskValue) != 0 {
		flags |= uint32(cef.EventFlagsEventflagMiddleMouseButton)
	}
	if gdkState&uint(gdk.Button3MaskValue) != 0 {
		flags |= uint32(cef.EventFlagsEventflagRightMouseButton)
	}
	return flags
}

func TranslateMouseButton(gdkButton uint) cef.MouseButtonType {
	switch gdkButton {
	case 1:
		return cef.MouseButtonTypeMbtLeft
	case 2:
		return cef.MouseButtonTypeMbtMiddle
	case 3:
		return cef.MouseButtonTypeMbtRight
	default:
		return cef.MouseButtonTypeMbtLeft
	}
}

const cefScrollUnitsPerNotch = 120

func TranslateScrollDeltas(dx, dy float64) (int32, int32) {
	return int32(dx * cefScrollUnitsPerNotch), int32(-dy * cefScrollUnitsPerNotch)
}

const (
	gdkKeyReturn          = 0xff0d
	gdkKeyTab             = 0xff09
	gdkKeyBackSpace       = 0xff08
	gdkKeyEscape          = 0xff1b
	gdkKeyDelete          = 0xffff
	gdkKeySpace           = 0x020
	gdkKeyHome            = 0xff50
	gdkKeyEnd             = 0xff57
	gdkKeyPageUp          = 0xff55
	gdkKeyPageDown        = 0xff56
	gdkKeyLowercaseV      = 0x076
	gdkKeyUppercaseV      = 0x056
	gdkKeyLowercaseAStart = 0x061
	gdkKeyLowercaseAEnd   = 0x07a
	gdkKeyUppercaseAStart = 0x041
	gdkKeyUppercaseAEnd   = 0x05a
	gdkKeyDigit0Start     = 0x030
	gdkKeyDigit9End       = 0x039
	gdkKeyF1Start         = 0xffbe
	gdkKeyF12End          = 0xffc9
	gdkKeyArrowStart      = 0xff51
	gdkKeyArrowEnd        = 0xff54
	vkA                   = 0x41
	vkF1                  = 0x70
	vkArrowLeft           = 0x25
)

func KeyvalToChar(keyval uint) uint16 {
	switch keyval {
	case gdkKeyReturn:
		return '\r'
	case gdkKeyTab:
		return '\t'
	case gdkKeyBackSpace:
		return '\b'
	}
	cp := gdk.KeyvalToUnicode(keyval)
	if cp == 0 || cp > maxBMPCodepoint || cp < minPrintable {
		return 0
	}
	return uint16(cp)
}

func GDKKeyvalToWindowsVK(keyval uint) int32 {
	if vk, ok := gdkKeyvalToVKRange(keyval); ok {
		return vk
	}
	if vk, ok := gdkKeyvalToVKMap[keyval]; ok {
		return vk
	}
	if keyval < maxSingleByteKeyval {
		return int32(keyval)
	}
	return 0
}

func gdkKeyvalToVKRange(keyval uint) (int32, bool) {
	switch {
	case keyval >= gdkKeyLowercaseAStart && keyval <= gdkKeyLowercaseAEnd:
		return int32(keyval-gdkKeyLowercaseAStart) + vkA, true
	case keyval >= gdkKeyUppercaseAStart && keyval <= gdkKeyUppercaseAEnd:
		return int32(keyval-gdkKeyUppercaseAStart) + vkA, true
	case keyval >= gdkKeyDigit0Start && keyval <= gdkKeyDigit9End:
		return int32(keyval), true
	case keyval >= gdkKeyF1Start && keyval <= gdkKeyF12End:
		return int32(keyval-gdkKeyF1Start) + vkF1, true
	case keyval >= gdkKeyArrowStart && keyval <= gdkKeyArrowEnd:
		return int32(keyval-gdkKeyArrowStart) + vkArrowLeft, true
	default:
		return 0, false
	}
}

var gdkKeyvalToVKMap = map[uint]int32{
	gdkKeyReturn: 0x0D, gdkKeyEscape: 0x1B, gdkKeyTab: 0x09, gdkKeyBackSpace: 0x08, gdkKeyDelete: 0x2E, gdkKeySpace: 0x20, gdkKeyHome: 0x24, gdkKeyEnd: 0x23, gdkKeyPageUp: 0x21, gdkKeyPageDown: 0x22, 0xff63: 0x2D,
	0xffe1: 0xA0, 0xffe2: 0xA1, 0xffe3: 0xA2, 0xffe4: 0xA3, 0xffe9: 0xA4, 0xffea: 0xA5,
	'.': 0xBE, '>': 0xBE, ',': 0xBC, '<': 0xBC, '-': 0xBD, '_': 0xBD, '=': 0xBB, '+': 0xBB, ';': 0xBA, ':': 0xBA, '/': 0xBF, '?': 0xBF, '`': 0xC0, '~': 0xC0, '[': 0xDB, '{': 0xDB, '\\': 0xDC, '|': 0xDC, ']': 0xDD, '}': 0xDD, '\'': 0xDE, '"': 0xDE,
	'!': 0x31, '@': 0x32, '#': 0x33, '$': 0x34, '%': 0x35, '^': 0x36, '&': 0x37, '*': 0x38, '(': 0x39, ')': 0x30,
}
