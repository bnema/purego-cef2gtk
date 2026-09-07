package gtkgl

import (
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf16"

	"github.com/bnema/purego-cef/cef"
	internalprofile "github.com/bnema/purego-cef2gtk/internal/profile"
	"github.com/bnema/puregotk/v4/gdk"
	"github.com/bnema/puregotk/v4/gio"
	"github.com/bnema/puregotk/v4/glib"
	"github.com/bnema/puregotk/v4/gobject"
	"github.com/bnema/puregotk/v4/gtk"
)

const (
	gdkDeadKeyStart = 0xfe50
	gdkDeadKeyEnd   = 0xfe8c

	maxBMPCodepoint     = 0xFFFF
	minPrintable        = 0x20
	maxSingleByteKeyval = 0x100
)

// InputBridge translates GTK/GDK input events from a GTK widget into CEF OSR input.
type controllerBinding struct {
	controller *gtk.EventController
	handlers   []uint
}

type InputBridge struct {
	mu    sync.Mutex
	host  cef.BrowserHost
	scale float64

	lastX, lastY        float64
	clipboard           *gdk.Clipboard
	imContext           *gtk.IMContextSimple
	detached            bool
	focused             bool
	focusKnown          bool
	focusDelivered      bool
	visible             bool
	visibilityKnown     bool
	visibilityDelivered bool

	widget                 *gtk.Widget
	controllers            []controllerBinding
	callbacks              []any
	imContextCommitHandler uint

	onMiddleClick       func(x, y float64) bool
	middleClickConsumed bool
	scroll              *scrollController
	selectionText       func() string
	onClipboardShortcut func(action, text string)
	profiler            atomic.Pointer[internalprofile.Recorder]
	pointerTracker      *PointerTracker
}

// Scroll translation, routing, and navigation-swipe recognition live in
// scroll.go under the bridge's scroll controller.

// NewInputBridge creates an input bridge. Scale values <= 0 are treated as 1.
func NewInputBridge(host cef.BrowserHost, scale float64) *InputBridge {
	controller := newScrollController()
	if scrollTraceEnabled() {
		controller.tracer = newScrollTracer()
	}
	return &InputBridge{
		host:           host,
		scale:          normalizeScale(scale),
		pointerTracker: NewPointerTracker(defaultDragThreshold, nil, nil),
		scroll:         controller,
	}
}

// ArmDnd suspends pointer-cancel recovery while native drag-and-drop owns the pointer.
func (ib *InputBridge) ArmDnd() {
	if ib == nil {
		return
	}
	ib.mu.Lock()
	defer ib.mu.Unlock()
	if ib.pointerTracker != nil {
		ib.pointerTracker.ArmDnd()
	}
}

// DisarmDnd restores pointer-cancel handling after native drag-and-drop completes.
func (ib *InputBridge) DisarmDnd() {
	if ib == nil {
		return
	}
	ib.mu.Lock()
	defer ib.mu.Unlock()
	if ib.pointerTracker != nil {
		ib.pointerTracker.DisarmDnd()
	}
}

// SetScale updates the device scale used for pointer coordinate translation.
func (ib *InputBridge) SetScale(scale float64) {
	if ib == nil {
		return
	}
	ib.mu.Lock()
	ib.scale = normalizeScale(scale)
	ib.mu.Unlock()
}

// SetMiddleClickHandler configures a callback for middle-button press events.
// If the callback returns true, the press/release pair is consumed locally and
// is not forwarded to CEF.
func (ib *InputBridge) SetMiddleClickHandler(fn func(x, y float64) bool) {
	if ib == nil {
		return
	}
	ib.mu.Lock()
	ib.onMiddleClick = fn
	ib.mu.Unlock()
}

// SetScrollOptions configures scroll translation and an optional interception
// callback. If the callback returns ScrollConsume, the update event is not
// forwarded to CEF.
func (ib *InputBridge) SetScrollOptions(opts ScrollOptions, fn func(ScrollEvent) ScrollDecision) {
	if ib == nil {
		return
	}
	ib.scroll.setOptions(opts, fn)
}

// SetNavigationSwipeHandler configures browser-history navigation recognition
// from precise horizontal touchpad scroll streams.
func (ib *InputBridge) SetNavigationSwipeHandler(opts NavigationSwipeOptions, canBack, canForward func() bool, onNavigate func(NavigationSwipeAction)) {
	if ib == nil {
		return
	}
	ib.scroll.setNavigationHandler(opts, canBack, canForward, onNavigate)
}

// SetClipboardShortcutHandler configures callbacks used to mirror explicit
// Ctrl+C/Ctrl+X shortcuts to application-level clipboard orchestration.
func (ib *InputBridge) SetProfiler(profiler *internalprofile.Recorder) {
	if ib == nil {
		return
	}
	ib.profiler.Store(profiler)
}

func (ib *InputBridge) SetClipboardShortcutHandler(selectionText func() string, onShortcut func(action, text string)) {
	if ib == nil {
		return
	}
	ib.mu.Lock()
	ib.selectionText = selectionText
	ib.onClipboardShortcut = onShortcut
	ib.mu.Unlock()
}

// InvalidateScroll immediately retires animated scroll motion and release
// eligibility from any thread, returning the retired epoch. Pass that epoch
// to CancelScrollEpoch for GTK cleanup of the old tick and session state;
// absent bridges report zero.
func (ib *InputBridge) InvalidateScroll() uint64 {
	if ib == nil || ib.scroll == nil {
		return 0
	}
	return ib.scroll.invalidate()
}

// CancelScroll synchronously invalidates and cleans up scroll motion.
// Call only on the GTK thread; off-thread paths use InvalidateScroll plus
// queued CancelScrollEpoch.
func (ib *InputBridge) CancelScroll() {
	if ib == nil || ib.scroll == nil {
		return
	}
	ib.scroll.cancelNow()
}

// CancelScrollEpoch performs GTK-only cleanup for a retired epoch. It
// never clears a newer session and reports whether cleanup ran.
func (ib *InputBridge) CancelScrollEpoch(epoch uint64) bool {
	if ib == nil || ib.scroll == nil {
		return false
	}
	return ib.scroll.cleanupEpoch(epoch)
}

// SetVisible records view visibility and notifies CEF once per transition.
func (ib *InputBridge) SetVisible(visible bool) {
	if ib == nil {
		return
	}
	ib.mu.Lock()
	if !ib.visibilityKnown || ib.visible != visible {
		ib.visible = visible
		ib.visibilityKnown = true
		ib.visibilityDelivered = false
	}
	host := ib.host
	if host == nil || ib.visibilityDelivered {
		ib.mu.Unlock()
		return
	}
	ib.visibilityDelivered = true
	ib.mu.Unlock()
	if !visible {
		ib.scroll.cancelNow()
	}
	wasHidden(host, visible)
}

// SetHost updates the CEF browser host used for subsequent input dispatch and
// synchronizes state that GTK observed before the asynchronous host attachment.
func (ib *InputBridge) SetHost(host cef.BrowserHost) {
	if ib == nil {
		return
	}
	ib.mu.Lock()
	hostChanged := !sameBrowserHost(ib.host, host)
	if hostChanged {
		ib.host = host
		ib.visibilityDelivered = false
		ib.focusDelivered = false
	}
	if host == nil {
		ib.mu.Unlock()
		if hostChanged {
			ib.scroll.cancelNow()
		}
		return
	}

	deliverVisibility := ib.visibilityKnown && !ib.visibilityDelivered
	visible := ib.visible
	deliverFocus := ib.focusKnown && !ib.focusDelivered
	focused := ib.focused
	revealOnFocus := deliverFocus && focused && deliverVisibility && visible
	if revealOnFocus {
		// Focus synchronization can include WasHidden(0), so it also delivers the
		// known visible state without issuing the same GTK observation twice.
		deliverVisibility = false
		ib.visibilityDelivered = true
	}
	if deliverVisibility {
		ib.visibilityDelivered = true
	}
	if deliverFocus {
		ib.focusDelivered = true
	}
	ib.mu.Unlock()
	if hostChanged {
		ib.scroll.cancelNow()
	}

	if deliverVisibility {
		wasHidden(host, visible)
	}
	if deliverFocus {
		if focused {
			syncWindowlessBrowserFocus(host, revealOnFocus)
		} else {
			host.SetFocus(0)
		}
	}
}

func sameBrowserHost(a, b cef.BrowserHost) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	aType, bType := reflect.TypeOf(a), reflect.TypeOf(b)
	return aType == bType && aType.Comparable() && a == b
}

func wasHidden(host cef.BrowserHost, visible bool) {
	if visible {
		host.WasHidden(0)
		return
	}
	host.WasHidden(1)
}

// Attach creates GTK event controllers and attaches them to the GLArea.
func (ib *InputBridge) Attach(area *gtk.GLArea) {
	if area == nil {
		return
	}
	ib.AttachToWidget(&area.Widget)
}

// AttachToWidget creates GTK event controllers and attaches them to widget.
func (ib *InputBridge) AttachToWidget(widget *gtk.Widget) {
	if ib == nil || widget == nil {
		return
	}
	ib.mu.Lock()
	ib.widget = widget
	ib.detached = false
	if display := widget.GetDisplay(); display != nil {
		ib.clipboard = display.GetClipboard()
	}
	ib.mu.Unlock()

	click := gtk.NewGestureClick()
	click.SetButton(0)
	ib.mu.Lock()
	ib.pointerTracker = NewPointerTracker(defaultDragThreshold, func() {
		click.SetState(gtk.EventSequenceClaimedValue)
	}, nil)
	ib.mu.Unlock()

	motion := gtk.NewEventControllerMotion()
	motionCb := func(g gtk.EventControllerMotion, x, y float64) {
		ib.onMouseMove(x, y, uint(g.GetCurrentEventState()), false)
	}
	motionHandlerID := motion.ConnectMotion(&motionCb)
	leaveCb := func(g gtk.EventControllerMotion) {
		ib.onMouseMove(0, 0, uint(g.GetCurrentEventState()), true)
	}
	leaveHandlerID := motion.ConnectLeave(&leaveCb)
	ib.addController(widget, &motion.EventController, []uint{motionHandlerID, leaveHandlerID}, &motionCb, &leaveCb)

	pressedCb := func(g gtk.GestureClick, nPress int, x, y float64) {
		widget.GrabFocus()
		ib.onMousePress(x, y, g.GetCurrentButton(), uint(g.GetCurrentEventState()), nPress)
	}
	pressedHandlerID := click.ConnectPressed(&pressedCb)
	releasedCb := func(g gtk.GestureClick, nPress int, x, y float64) {
		ib.onMouseRelease(x, y, g.GetCurrentButton(), uint(g.GetCurrentEventState()), nPress)
	}
	releasedHandlerID := click.ConnectReleased(&releasedCb)
	cancelCb := func(_ gtk.Gesture, _ uintptr) {
		ib.onMouseCancel()
	}
	cancelHandlerID := click.ConnectCancel(&cancelCb)
	ib.addController(widget, &click.EventController, []uint{pressedHandlerID, releasedHandlerID, cancelHandlerID}, &pressedCb, &releasedCb, &cancelCb)

	scroll := gtk.NewEventControllerScroll(gtk.EventControllerScrollBothAxesValue | gtk.EventControllerScrollKineticValue)
	scrollBeginCb := func(g gtk.EventControllerScroll) {
		unit, unitKnown := currentScrollUnit(g)
		ib.onScrollBoundary(ScrollPhaseBegin, unit, unitKnown, uint(g.GetCurrentEventState()))
	}
	scrollBeginHandlerID := scroll.ConnectScrollBegin(&scrollBeginCb)
	scrollCb := func(g gtk.EventControllerScroll, dx, dy float64) bool {
		unit, unitKnown := currentScrollUnit(g)
		ib.onScrollUpdate(dx, dy, unit, unitKnown, uint(g.GetCurrentEventState()))
		return true
	}
	scrollHandlerID := scroll.ConnectScroll(&scrollCb)
	scrollEndCb := func(g gtk.EventControllerScroll) {
		unit, unitKnown := currentScrollUnit(g)
		ib.onScrollBoundary(ScrollPhaseEnd, unit, unitKnown, uint(g.GetCurrentEventState()))
	}
	scrollEndHandlerID := scroll.ConnectScrollEnd(&scrollEndCb)
	scrollDecelerateCb := func(g gtk.EventControllerScroll, velocityX, velocityY float64) {
		unit, unitKnown := currentScrollUnit(g)
		ib.onScrollDecelerate(velocityX, velocityY, unit, unitKnown, uint(g.GetCurrentEventState()))
	}
	scrollDecelerateHandlerID := scroll.ConnectDecelerate(&scrollDecelerateCb)
	ib.addController(widget, &scroll.EventController, []uint{scrollBeginHandlerID, scrollHandlerID, scrollEndHandlerID, scrollDecelerateHandlerID}, &scrollBeginCb, &scrollCb, &scrollEndCb, &scrollDecelerateCb)

	focus := gtk.NewEventControllerFocus()
	focusEnterCb := func(_ gtk.EventControllerFocus) {
		if ib.imContext != nil {
			ib.imContext.FocusIn()
		}
		ib.onFocusIn()
	}
	focusEnterHandlerID := focus.ConnectEnter(&focusEnterCb)
	focusLeaveCb := func(_ gtk.EventControllerFocus) {
		if ib.imContext != nil {
			ib.imContext.Reset()
			ib.imContext.FocusOut()
		}
		ib.onFocusOut()
	}
	focusLeaveHandlerID := focus.ConnectLeave(&focusLeaveCb)
	ib.addController(widget, &focus.EventController, []uint{focusEnterHandlerID, focusLeaveHandlerID}, &focusEnterCb, &focusLeaveCb)

	key := gtk.NewEventControllerKey()
	ib.imContext = gtk.NewIMContextSimple()
	if ib.imContext != nil {
		commitCb := func(_ gtk.IMContext, text string) { ib.onIMCommit(text) }
		commitHandlerID := ib.imContext.ConnectCommit(&commitCb)
		key.SetImContext(&ib.imContext.IMContext)
		ib.imContext.SetClientWidget(widget)
		ib.callbacks = append(ib.callbacks, &commitCb)
		ib.imContextCommitHandler = commitHandlerID
	}
	keyPressCb := func(_ gtk.EventControllerKey, keyval, keycode uint, state gdk.ModifierType) bool {
		mods := uint(state)
		ib.mirrorClipboardShortcut(keyval, mods)
		if mods&(uint(gdk.ControlMaskValue)|uint(gdk.MetaMaskValue)) != 0 && (keyval == gdkKeyLowercaseV || keyval == gdkKeyUppercaseV) {
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
	keyPressHandlerID := key.ConnectKeyPressed(&keyPressCb)
	keyReleaseCb := func(_ gtk.EventControllerKey, keyval, keycode uint, state gdk.ModifierType) {
		ib.onKeyRelease(keyval, keycode, uint(state))
	}
	keyReleaseHandlerID := key.ConnectKeyReleased(&keyReleaseCb)
	ib.addController(widget, &key.EventController, []uint{keyPressHandlerID, keyReleaseHandlerID}, &keyPressCb, &keyReleaseCb)

	widget.SetFocusable(true)
	widget.SetCanFocus(true)
	ib.wireScrollTick(widget)
	ib.syncWidgetVisibility(widget.GetMapped(), widget.GetVisible())
}

// wireScrollTick installs frame scheduling for animated scroll motion.
// Timestamps still use the shared monotonic engine clock; the widget only
// drives tick delivery.
func (ib *InputBridge) wireScrollTick(widget *gtk.Widget) {
	if ib == nil || ib.scroll == nil || widget == nil {
		return
	}
	ib.scroll.tickBackend = &scrollTickBackend{
		registrar: func(cb *gtk.TickCallback) uint {
			return widget.AddTickCallback(cb, 0, nil)
		},
		remover: widget.RemoveTickCallback,
		unrefer: func(cb *gtk.TickCallback) {
			_ = glib.UnrefCallback(cb)
		},
	}
}

func (ib *InputBridge) syncWidgetVisibility(mapped, visible bool) {
	ib.SetVisible(mapped && visible)
}

func (ib *InputBridge) addController(widget *gtk.Widget, controller *gtk.EventController, handlers []uint, callbacks ...any) {
	widget.AddController(controller)
	ib.mu.Lock()
	ib.controllers = append(ib.controllers, controllerBinding{controller: controller, handlers: handlers})
	ib.callbacks = append(ib.callbacks, callbacks...)
	ib.mu.Unlock()
}

// Detach removes controllers previously attached by this bridge.
func (ib *InputBridge) Detach() {
	if ib == nil {
		return
	}
	ib.mu.Lock()
	widget := ib.widget
	controllers := append([]controllerBinding(nil), ib.controllers...)
	imContext := ib.imContext
	imContextCommitHandler := ib.imContextCommitHandler
	ib.detached = true
	ib.controllers = nil
	ib.callbacks = nil
	ib.clipboard = nil
	ib.imContext = nil
	ib.imContextCommitHandler = 0
	ib.widget = nil
	if ib.pointerTracker != nil {
		ib.pointerTracker.detach()
	}
	ib.mu.Unlock()
	ib.scroll.cancelNow()
	ib.scroll.teardownTick()
	if imContext != nil && imContextCommitHandler != 0 {
		gobject.SignalHandlerDisconnect(&imContext.Object, imContextCommitHandler)
	}
	if widget == nil {
		return
	}
	for _, binding := range controllers {
		controller := binding.controller
		if controller == nil {
			continue
		}
		for _, handlerID := range binding.handlers {
			if handlerID != 0 {
				gobject.SignalHandlerDisconnect(&controller.Object, handlerID)
			}
		}
		widget.RemoveController(controller)
	}
}

func (ib *InputBridge) currentHost() cef.BrowserHost {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.host
}

func (ib *InputBridge) currentHostAndMiddleClickHandler() (cef.BrowserHost, float64, func(x, y float64) bool) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.host, ib.scale, ib.onMiddleClick
}

func (ib *InputBridge) currentClipboardShortcutHandlers() (func() string, func(action, text string)) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.selectionText, ib.onClipboardShortcut
}

func (ib *InputBridge) setMiddleClickConsumed(consumed bool) {
	ib.mu.Lock()
	ib.middleClickConsumed = consumed
	ib.mu.Unlock()
}

func (ib *InputBridge) consumeMiddleClickRelease() bool {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	consumed := ib.middleClickConsumed
	ib.middleClickConsumed = false
	return consumed
}

func (ib *InputBridge) onMouseMove(x, y float64, mods uint, leave bool) {
	ib.mu.Lock()
	host, scale := ib.host, ib.scale
	tracker := ib.pointerTracker
	var claimGesture func()
	if leave {
		if tracker != nil && tracker.Phase() != PointerIdle {
			ib.mu.Unlock()
			if profiler := ib.profiler.Load(); profiler != nil {
				profiler.RecordSuppressedLeaveDuringDrag()
			}
			return
		}
		if tracker == nil || !tracker.coordsValid {
			ib.mu.Unlock()
			return
		}
		x, y = tracker.lastX, tracker.lastY
	} else {
		ib.lastX, ib.lastY = x, y
		if tracker != nil {
			claimGesture = tracker.Motion(x, y, mods)
		}
	}
	ib.mu.Unlock()
	if claimGesture != nil {
		claimGesture()
	}
	ib.scroll.notePointer(x, y)
	if host == nil {
		return
	}
	evt := BuildMouseEvent(x, y, mods, scale)
	var mouseLeave int32
	if leave {
		mouseLeave = 1
	}
	host.SendMouseMoveEvent(&evt, mouseLeave)
}

func (ib *InputBridge) onMousePress(x, y float64, button, mods uint, clickCount int) {
	// A press grabs the pointer: retire animated motion before handling it.
	ib.scroll.cancelNow()
	ib.mu.Lock()
	ib.lastX, ib.lastY = x, y
	if ib.pointerTracker != nil {
		ib.pointerTracker.Press(x, y, button, mods)
	}
	host, scale, consumeMiddle := ib.host, ib.scale, ib.onMiddleClick
	ib.mu.Unlock()
	if host == nil {
		return
	}
	if button == 2 && consumeMiddle != nil && consumeMiddle(x, y) {
		ib.setMiddleClickConsumed(true)
		return
	}
	if button == 2 {
		ib.setMiddleClickConsumed(false)
	}
	evt := BuildMouseEvent(x, y, mods, scale)
	host.SendMouseClickEvent(&evt, TranslateMouseButton(button), 0, int32(clickCount))
}

func (ib *InputBridge) onMouseRelease(x, y float64, button, mods uint, clickCount int) {
	ib.mu.Lock()
	ib.lastX, ib.lastY = x, y
	if ib.pointerTracker != nil {
		ib.pointerTracker.Release(x, y, button, mods)
	}
	host, scale := ib.host, ib.scale
	ib.mu.Unlock()
	if host == nil {
		return
	}
	if button == 2 && ib.consumeMiddleClickRelease() {
		return
	}
	state := mods &^ gdkButtonMask(button)
	evt := BuildMouseEvent(x, y, state, scale)
	host.SendMouseClickEvent(&evt, TranslateMouseButton(button), 1, int32(clickCount))
}

func (ib *InputBridge) onMouseCancel() {
	if ib == nil {
		return
	}
	ib.scroll.cancelNow()
	ib.mu.Lock()
	tracker := ib.pointerTracker
	if tracker == nil {
		ib.mu.Unlock()
		return
	}
	abort, _, canceled := tracker.cancel()
	consumedMiddleClick := canceled && abort.Button == 2 && ib.middleClickConsumed
	if consumedMiddleClick {
		ib.middleClickConsumed = false
	}
	host, scale := ib.host, ib.scale
	ib.mu.Unlock()
	if !canceled || host == nil {
		return
	}
	if !consumedMiddleClick {
		if profiler := ib.profiler.Load(); profiler != nil {
			profiler.RecordPressWithoutMatchedRelease()
		}
		state := abort.State &^ gdkButtonMask(abort.Button)
		evt := BuildMouseEvent(abort.X, abort.Y, state, scale)
		host.SendMouseClickEvent(&evt, TranslateMouseButton(abort.Button), 1, 1)
	}
	host.SendCaptureLostEvent()
}

func gdkButtonMask(button uint) uint {
	switch button {
	case 1:
		return uint(gdk.Button1MaskValue)
	case 2:
		return uint(gdk.Button2MaskValue)
	case 3:
		return uint(gdk.Button3MaskValue)
	default:
		return 0
	}
}

// Scroll routing lives in scroll.go.

// Scroll update routing lives in scroll.go.

// Scroll boundary routing lives in scroll.go.

// Scroll decelerate routing lives in scroll.go.

// Navigation-swipe recognition lives in scroll.go.

// Swipe completion lives in scroll.go.

// Navigation-swipe helpers live in scroll.go.

// Swipe thresholds live in scroll.go.

func (ib *InputBridge) onFocusIn() {
	ib.mu.Lock()
	if !ib.focusKnown || !ib.focused {
		ib.focused = true
		ib.focusKnown = true
		ib.focusDelivered = false
	}
	host := ib.host
	if host == nil || ib.focusDelivered {
		ib.mu.Unlock()
		return
	}
	ib.focusDelivered = true
	reveal := ib.visibilityKnown && ib.visible
	if reveal {
		ib.visibilityDelivered = true
	}
	ib.mu.Unlock()
	syncWindowlessBrowserFocus(host, reveal)
}

func (ib *InputBridge) onFocusOut() {
	ib.scroll.cancelNow()
	ib.mu.Lock()
	if !ib.focusKnown || ib.focused {
		ib.focused = false
		ib.focusKnown = true
		ib.focusDelivered = false
	}
	host := ib.host
	if host == nil || ib.focusDelivered {
		ib.mu.Unlock()
		return
	}
	ib.focusDelivered = true
	ib.mu.Unlock()
	host.SetFocus(0)
}

func (ib *InputBridge) mirrorClipboardShortcut(keyval, mods uint) {
	action, ok := clipboardShortcutAction(keyval, mods)
	if !ok {
		return
	}
	selectionText, onShortcut := ib.currentClipboardShortcutHandlers()
	if selectionText == nil || onShortcut == nil {
		return
	}
	text := selectionText()
	if text == "" {
		return
	}
	onShortcut(action, text)
}

func clipboardShortcutAction(keyval, mods uint) (string, bool) {
	if mods&uint(gdk.ControlMaskValue) == 0 {
		return "", false
	}
	if mods&uint(gdk.ShiftMaskValue) != 0 || mods&uint(gdk.AltMaskValue) != 0 {
		return "", false
	}
	switch keyval {
	case gdkKeyLowercaseC, gdkKeyUppercaseC:
		return "copy", true
	case gdkKeyLowercaseX, gdkKeyUppercaseX:
		return "cut", true
	default:
		return "", false
	}
}

func syncWindowlessBrowserFocus(host cef.BrowserHost, reveal bool) {
	if host == nil {
		return
	}
	if reveal {
		host.WasHidden(0)
	}
	host.SetFocus(1)
	host.Invalidate(cef.PaintElementTypePetView)
}

func (ib *InputBridge) onKeyPress(keyval, keycode, mods uint) {
	ib.scroll.noteModifiers(mods)
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
	ib.scroll.noteModifiers(mods)
	host := ib.currentHost()
	if host == nil {
		return
	}
	evt := BuildKeyEvent(keyval, keycode, mods, cef.KeyEventTypeKeyeventKeyup)
	host.SendKeyEvent(&evt)
}

// pasteFromClipboard deliberately reads from GDK instead of calling
// CefFrame.Paste. In Linux windowless (OSR) mode CEF has no native GTK window
// from which to obtain the Wayland/X11 clipboard, so CefFrame.Paste can be a
// no-op even though it is the preferred API for windowed browsers.
func (ib *InputBridge) pasteFromClipboard() {
	ib.mu.Lock()
	clipboard := ib.clipboard
	detached := ib.detached
	ib.mu.Unlock()
	if detached || clipboard == nil {
		return
	}
	asyncCb := gio.AsyncReadyCallback(func(_, resultPtr, _ uintptr) {
		text, err := clipboard.ReadTextFinish(&gio.AsyncResultBase{Ptr: resultPtr})
		if err != nil || text == "" {
			return
		}
		ib.injectClipboardText(text)
	})
	clipboard.ReadTextAsync(nil, &asyncCb, 0)
}

func (ib *InputBridge) injectClipboardText(text string) {
	ib.mu.Lock()
	detached := ib.detached
	host := ib.host
	ib.mu.Unlock()
	if detached || host == nil || text == "" {
		return
	}
	browser := host.GetBrowser()
	if browser == nil {
		return
	}
	frame := browser.GetFocusedFrame()
	if frame == nil {
		frame = browser.GetMainFrame()
	}
	if frame != nil {
		frame.ExecuteJavaScript(pasteJavaScript(text), "", 0)
	}
}

// pasteJavaScript asks Chromium's editing engine to insert the GDK clipboard
// text first. That path preserves selection, contenteditable behavior, undo,
// and the input events expected by controlled web forms. The prototype setter
// is a fallback for input/textarea elements where execCommand is unavailable;
// assigning active.value directly would update only the DOM on frameworks such
// as React, leaving their form state stale until the user typed another key.
func pasteJavaScript(text string) string {
	quoted := strings.ReplaceAll(strconv.Quote(text), "</", "<\\/")
	return `(function(text){
const active=document.activeElement;
if(!active)return;
if(document.execCommand&&document.execCommand('insertText',false,text))return;
if(active.tagName!=='INPUT'&&active.tagName!=='TEXTAREA')return;
const proto=active.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype;
const descriptor=Object.getOwnPropertyDescriptor(proto,'value');
const setter=descriptor&&descriptor.set;
if(!setter)return;
const start=active.selectionStart==null?active.value.length:active.selectionStart;
const end=active.selectionEnd==null?start:active.selectionEnd;
const next=active.value.slice(0,start)+text+active.value.slice(end);
setter.call(active,next);
const caret=start+text.length;
try{active.setSelectionRange(caret,caret);}catch(_){}
active.dispatchEvent(new InputEvent('input',{bubbles:true,composed:true,inputType:'insertText',data:text}));
})(` + quoted + `);`
}

// DragMouseEvent applies the same scale and modifier translation as normal
// pointer delivery. The GDK device state includes the button mask while the
// native drag owns the pointer grab.
func (ib *InputBridge) DragMouseEvent(x, y float64, gdkMods uint) cef.MouseEvent {
	if ib == nil {
		return BuildMouseEvent(x, y, gdkMods, 1)
	}
	ib.mu.Lock()
	scale := ib.scale
	ib.mu.Unlock()
	return BuildMouseEvent(x, y, gdkMods, scale)
}

func (ib *InputBridge) Scale() float64 {
	if ib == nil {
		return 1
	}
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.scale
}

func BuildMouseEvent(x, y float64, gdkMods uint, scale float64) cef.MouseEvent {
	scale = normalizeScale(scale)
	return cef.MouseEvent{
		X:         logicalToDeviceCoord(x, scale),
		Y:         logicalToDeviceCoord(y, scale),
		Modifiers: TranslateModifiers(gdkMods),
	}
}

func logicalToDeviceCoord(value float64, scale float64) int32 {
	return int32(math.Floor(value * normalizeScale(scale)))
}

func normalizeScale(scale float64) float64 {
	if math.IsNaN(scale) || math.IsInf(scale, 0) || scale <= 0 {
		return 1
	}
	return scale
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

// Scroll delta translation lives in scroll.go.

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
	gdkKeyLowercaseC      = 0x063
	gdkKeyUppercaseC      = 0x043
	gdkKeyLowercaseV      = 0x076
	gdkKeyUppercaseV      = 0x056
	gdkKeyLowercaseX      = 0x078
	gdkKeyUppercaseX      = 0x058
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
