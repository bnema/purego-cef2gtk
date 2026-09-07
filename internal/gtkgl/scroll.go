package gtkgl

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/puregotk/v4/gdk"
	"github.com/bnema/puregotk/v4/gtk"
)

// ScrollPhase identifies the stage of a GTK scroll operation.
type ScrollPhase int

const (
	ScrollPhaseBegin ScrollPhase = iota
	ScrollPhaseUpdate
	ScrollPhaseEnd
	ScrollPhaseDecelerate
)

// ScrollDecision controls whether a scroll event should be forwarded to CEF.
type ScrollDecision int

const (
	ScrollForwardToCEF ScrollDecision = iota
	ScrollConsume
)

// ScrollOptions configures GTK scroll delta translation before forwarding to CEF.
// Wheel zero values keep legacy wheel behavior; precise zero values use a
// WebKitGTK-like touchpad/surface scale.
type ScrollOptions struct {
	WheelMultiplier      float64
	PreciseMultiplier    float64
	HorizontalMultiplier float64
	VerticalMultiplier   float64
	MaxDelta             int32
	// TouchpadInertia enables direct touchpad tracking with exponential
	// release decay on valid release. Zero value keeps direct behavior.
	TouchpadInertia bool
	// WheelSmoothing interpolates accepted wheel impulses across frames
	// without a long coast. Zero value keeps direct behavior.
	WheelSmoothing bool
}

// ScrollEvent describes a GTK scroll event after CEF delta translation.
type ScrollEvent struct {
	Phase                ScrollPhase
	X, Y                 float64
	DX, DY               float64
	DeltaX, DeltaY       int32
	Modifiers            uint
	Unit                 gdk.ScrollUnit
	UnitKnown            bool
	VelocityX, VelocityY float64
}

// NavigationSwipeAction identifies a browser-history swipe action derived
// from precise horizontal touchpad scrolling.
type NavigationSwipeAction int

const (
	NavigationSwipeBack NavigationSwipeAction = iota
	NavigationSwipeForward
)

// NavigationSwipeOptions configures WebKitGTK-like back/forward swipe recognition.
type NavigationSwipeOptions struct {
	Enabled          bool
	MinDelta         float64
	MaxVerticalRatio float64
}

type navigationSwipeState struct {
	options            NavigationSwipeOptions
	canNavigateBack    func() bool
	canNavigateForward func() bool
	onNavigate         func(NavigationSwipeAction)
	cumulativeDX       float64
	cumulativeDY       float64
	recognized         bool
	verticalCanceled   bool
}

// scrollController owns scroll translation options, the interception callback,
// and per-session navigation-swipe state. Host delivery, pointer coordinates,
// and GTK controller lifetime stay with InputBridge; this controller only
// routes scroll input so motion state can later live here without touching
// other input types.
type scrollController struct {
	mu         sync.Mutex
	options    ScrollOptions
	onScroll   func(ScrollEvent) ScrollDecision
	navigation navigationSwipeState
	// epoch retires animated motion: every session captures the epoch at
	// creation and the submission gate rejects submissions from older
	// epochs. invalidate() bumps it lock-free; cleanup is epoch-scoped.
	epoch atomic.Uint64
	// session owns the current gesture/burst motion and its residuals.
	session scrollSession
	// sender submits wheel events; production uses the CEF host call
	// directly, tests stub it to observe gated output.
	sender func(host cef.BrowserHost, evt *cef.MouseEvent, dx, dy int32)
	// now reports engine-clock seconds; the bridge installs a
	// frame-clock reader, tests inject a manual clock.
	now func() float64
	// overloadCompletions tallies explicit overload discards. It lives on
	// the controller (not the session) so the record survives session
	// clearing.
	overloadCompletions uint64
	// tickBackend wires frame scheduling to the owning widget. Nil
	// without a widget (unit tests drive step manually).
	tickBackend *scrollTickBackend
	tickState   scrollTickState
	// tracer sinks the bounded opt-in scroll trace. Nil disables it.
	tracer *scrollTracer
}

// scrollEpochBlock spaces controller epoch bases so cleanup tokens never
// collide across bridge instances: a stale token from a detached bridge
// cannot match a live session on a reattached one. One block covers 4B
// invalidations per controller, far beyond any session lifetime.
const scrollEpochBlock = uint64(1) << 32

var scrollEpochBase atomic.Uint64

func newScrollController() *scrollController {
	c := &scrollController{}
	c.epoch.Store(scrollEpochBase.Add(scrollEpochBlock))
	return c
}

func (c *scrollController) setOptions(opts ScrollOptions, fn func(ScrollEvent) ScrollDecision) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.options = opts
	c.onScroll = fn
	c.mu.Unlock()
}

func (c *scrollController) snapshot() (ScrollOptions, func(ScrollEvent) ScrollDecision) {
	if c == nil {
		return ScrollOptions{}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.options, c.onScroll
}

func (c *scrollController) setNavigationHandler(opts NavigationSwipeOptions, canBack, canForward func() bool, onNavigate func(NavigationSwipeAction)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.navigation = navigationSwipeState{
		options:            opts,
		canNavigateBack:    canBack,
		canNavigateForward: canForward,
		onNavigate:         onNavigate,
	}
	c.mu.Unlock()
}

func (c *scrollController) navState() navigationSwipeState {
	if c == nil {
		return navigationSwipeState{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.navigation
}

func (c *scrollController) setNavState(state navigationSwipeState) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.navigation = state
	c.mu.Unlock()
}

func (c *scrollController) resetNav() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.navigation.cumulativeDX = 0
	c.navigation.cumulativeDY = 0
	c.navigation.recognized = false
	c.navigation.verticalCanceled = false
	c.mu.Unlock()
}

func (ib *InputBridge) currentScrollState() (cef.BrowserHost, float64, float64, float64, ScrollOptions, func(ScrollEvent) ScrollDecision) {
	ib.mu.Lock()
	host, x, y, scale := ib.host, ib.lastX, ib.lastY, ib.scale
	ib.mu.Unlock()
	opts, handler := ib.scroll.snapshot()
	return host, x, y, scale, opts, handler
}

func (ib *InputBridge) currentNavigationSwipeState() navigationSwipeState {
	return ib.scroll.navState()
}

func (ib *InputBridge) setNavigationSwipeState(state navigationSwipeState) {
	ib.scroll.setNavState(state)
}

func (ib *InputBridge) resetNavigationSwipe() {
	ib.scroll.resetNav()
}

func (ib *InputBridge) onScrollUpdate(dx, dy float64, unit gdk.ScrollUnit, unitKnown bool, mods uint) {
	host, x, y, scale, opts, handler := ib.currentScrollState()
	if profiler := ib.profiler.Load(); profiler != nil {
		profiler.RecordScroll(dx, dy)
	}
	effectiveUnit := unit
	if !unitKnown {
		effectiveUnit = gdk.ScrollUnitWheelValue
	}
	deltaX, deltaY := TranslateScrollDeltasWithOptions(dx, dy, effectiveUnit, opts)
	event := ScrollEvent{
		Phase:     ScrollPhaseUpdate,
		X:         x,
		Y:         y,
		DX:        dx,
		DY:        dy,
		DeltaX:    deltaX,
		DeltaY:    deltaY,
		Modifiers: mods,
		Unit:      unit,
		UnitKnown: unitKnown,
	}
	if ib.handleNavigationSwipe(event) {
		return
	}
	if class := ib.scroll.animatedClass(opts, unit, unitKnown, mods); class != scrollClassNone {
		epochBefore := ib.scroll.epoch.Load()
		consumed := handler != nil && handler(event) == ScrollConsume
		if ib.scroll.tracing() {
			ib.scroll.tracef("input update class=%d unit=%v known=%v dx=%.2f dy=%.2f ix=%d iy=%d mods=%x xy=(%.1f,%.1f) consumed=%v epoch=%d mono=%.3f gdk_us=%d", class, unit, unitKnown, dx, dy, deltaX, deltaY, mods, x, y, consumed, epochBefore, ib.scrollNow(), ib.frameClockMicro())
		}
		if ib.scroll.epoch.Load() != epochBefore {
			// The application invalidated from inside its callback:
			// drop this update without delivery or session writes.
			// Later physical input starts a fresh gesture.
			return
		}
		ib.routeAnimatedUpdate(class, host, x, y, scale, mods, effectiveUnit, unitKnown, dx, dy, opts, consumed, deltaX, deltaY, epochBefore)
		return
	}
	if handler != nil && handler(event) == ScrollConsume {
		return
	}
	if host == nil {
		return
	}
	evt := BuildMouseEvent(x, y, mods, scale)
	if unitKnown && unit == gdk.ScrollUnitSurfaceValue {
		evt.Modifiers |= uint32(cef.EventFlagsEventflagPrecisionScrollingDelta)
	}
	host.SendMouseWheelEvent(&evt, deltaX, deltaY)
}

// routeAnimatedUpdate handles one update after the application callback ran
// without locks. A handler that invalidates (then returns Forward) still
// suppresses the stale update: every submission below revalidates the epoch
// (and, for synthetic output, host identity) through the gate, so no new
// submission from the invalidated session can occur. Synthetic output never
// passes through OnScroll or navigation recognition.
func (ib *InputBridge) routeAnimatedUpdate(class animatedScrollClass, host cef.BrowserHost, x, y, scale float64, mods uint, unit gdk.ScrollUnit, unitKnown bool, dx, dy float64, opts ScrollOptions, consumed bool, deltaX, deltaY int32, epochBefore uint64) {
	now := ib.scrollNow()
	c := ib.scroll
	switch class {
	case scrollClassTouchpad:
		if consumed {
			c.updateTouch(unit, unitKnown, true, mods, x, y, epochBefore)
			return
		}
		c.ensureTouchSession(x, y, scale, mods, host, epochBefore)
		if !c.updateTouch(unit, unitKnown, false, mods, x, y, epochBefore) {
			// Modifier change or class race lost the session: deliver
			// directly without arming release.
			ib.submitAnimatedDirect(host, x, y, mods, scale, unit, unitKnown, deltaX, deltaY, now, epochBefore)
			return
		}
		ib.submitAnimatedDirect(host, x, y, mods, scale, unit, unitKnown, deltaX, deltaY, now, epochBefore)
	case scrollClassWheel:
		c.abandonTouch()
		if consumed {
			return
		}
		fx, fy := translateScrollFloat(dx, dy, unit, opts)
		c.impulseWheel(now, x, y, scale, mods, host, fx, fy, epochBefore)
	}
}

func (ib *InputBridge) submitAnimatedDirect(host cef.BrowserHost, x, y float64, mods uint, scale float64, unit gdk.ScrollUnit, unitKnown bool, deltaX, deltaY int32, now float64, epoch uint64) {
	evt := BuildMouseEvent(x, y, mods, scale)
	if unitKnown && unit == gdk.ScrollUnitSurfaceValue {
		evt.Modifiers |= uint32(cef.EventFlagsEventflagPrecisionScrollingDelta)
	}
	// The update's own epoch, never reloaded: a racing invalidation
	// rejects this submission instead of adopting it.
	ib.scroll.submitPhysical(epoch, host, &evt, deltaX, deltaY, now)
}

func (ib *InputBridge) onScrollBoundary(phase ScrollPhase, unit gdk.ScrollUnit, unitKnown bool, mods uint) {
	host, x, y, scale, opts, handler := ib.currentScrollState()
	switch phase {
	case ScrollPhaseBegin:
		ib.resetNavigationSwipe()
		if opts.TouchpadInertia {
			// A fresh begin invalidates previous motion before the
			// application is notified below.
			beginEpoch := ib.scroll.beginTouch(x, y, scale, mods, host)
			ib.scroll.tracef("input begin epoch=%d mono=%.3f gdk_us=%d", beginEpoch, ib.scrollNow(), ib.frameClockMicro())
		}
	case ScrollPhaseEnd:
		ib.finishNavigationSwipe()
		ib.resetNavigationSwipe()
		ib.scroll.tracef("input end")
		ib.scroll.endTouch()
	}
	if handler == nil {
		return
	}
	handler(ScrollEvent{
		Phase:     phase,
		X:         x,
		Y:         y,
		Modifiers: mods,
		Unit:      unit,
		UnitKnown: unitKnown,
	})
}

func (ib *InputBridge) onScrollDecelerate(velocityX, velocityY float64, unit gdk.ScrollUnit, unitKnown bool, mods uint) {
	host, x, y, scale, opts, handler := ib.currentScrollState()
	if opts.TouchpadInertia {
		armed := ib.scroll.releaseFromDecelerate(ib.scrollNow(), x, y, scale, mods, host, velocityX, velocityY, opts)
		ib.scroll.tracef("input decelerate v=(%.1f,%.1f) armed=%v", velocityX, velocityY, armed)
	}
	if handler == nil {
		return
	}
	handler(ScrollEvent{
		Phase:     ScrollPhaseDecelerate,
		X:         x,
		Y:         y,
		Modifiers: mods,
		Unit:      unit,
		UnitKnown: unitKnown,
		VelocityX: velocityX,
		VelocityY: velocityY,
	})
}

// suppressRelease poisons touchpad release eligibility without retiring
// the session epoch; see scrollController.suppressRelease.
func (ib *InputBridge) suppressRelease() {
	ib.scroll.suppressRelease()
}

// scrollNow reports engine-clock seconds for session timestamps: the
// shared monotonic clock for physical events and frame ticks. Tests inject
// a manual clock through the controller hook.
func (ib *InputBridge) scrollNow() float64 {
	if ib == nil || ib.scroll == nil {
		return wallClockSeconds()
	}
	if now := ib.scroll.now; now != nil {
		return now()
	}
	return wallClockSeconds()
}

// frameClockMicro reports the raw GDK frame-clock time in microseconds for
// trace interval comparison (0 when unavailable). Engine timestamps use the
// monotonic clock instead; never mix the two domains in dt math.
func (ib *InputBridge) frameClockMicro() int64 {
	if ib == nil {
		return 0
	}
	ib.mu.Lock()
	widget := ib.widget
	ib.mu.Unlock()
	if widget == nil {
		return 0
	}
	clock := widget.GetFrameClock()
	if clock == nil {
		return 0
	}
	return clock.GetFrameTime()
}

func (ib *InputBridge) handleNavigationSwipe(event ScrollEvent) bool {
	state := ib.currentNavigationSwipeState()
	if !state.options.Enabled || state.onNavigate == nil || !isPreciseScrollEvent(event) {
		return false
	}

	if state.verticalCanceled {
		state.cumulativeDX = 0
		state.cumulativeDY = 0
		ib.setNavigationSwipeState(state)
		return false
	}

	// GTK scroll deltas are inverted compared to WebKit's navigation swipe
	// direction model. Match WebKitGTK's ViewGestureController, which negates
	// scroll deltas before deciding Back vs Forward.
	state.cumulativeDX += -event.DX
	state.cumulativeDY += event.DY
	if navigationSwipeIsTooVertical(state) {
		state.cumulativeDX = 0
		state.cumulativeDY = 0
		state.verticalCanceled = true
	}
	ib.setNavigationSwipeState(state)
	return false
}

func (ib *InputBridge) finishNavigationSwipe() {
	state := ib.currentNavigationSwipeState()
	if !state.options.Enabled || state.onNavigate == nil || state.recognized || state.verticalCanceled {
		return
	}
	absDX := math.Abs(state.cumulativeDX)
	if absDX <= normalizedNavigationSwipeMinDelta(state.options.MinDelta) || navigationSwipeIsTooVertical(state) {
		return
	}
	action, ok := navigationSwipeActionForDelta(state.cumulativeDX, state.canNavigateBack, state.canNavigateForward)
	if !ok {
		return
	}
	state.recognized = true
	ib.setNavigationSwipeState(state)
	// Release eligibility dies before the navigation action runs: synthetic
	// motion must never continue into the newly navigated page.
	ib.suppressRelease()
	state.onNavigate(action)
}

func navigationSwipeIsTooVertical(state navigationSwipeState) bool {
	absDX, absDY := math.Abs(state.cumulativeDX), math.Abs(state.cumulativeDY)
	return absDX == 0 || absDY >= absDX*normalizedNavigationSwipeRatio(state.options.MaxVerticalRatio)
}

func isPreciseScrollEvent(event ScrollEvent) bool {
	return event.UnitKnown && event.Unit == gdk.ScrollUnitSurfaceValue
}

func navigationSwipeActionForDelta(dx float64, canBack, canForward func() bool) (NavigationSwipeAction, bool) {
	if dx > 0 && canBack != nil && canBack() {
		return NavigationSwipeBack, true
	}
	if dx < 0 && canForward != nil && canForward() {
		return NavigationSwipeForward, true
	}
	return NavigationSwipeBack, false
}

const defaultNavigationSwipeCommitDistance = 400 * 0.5

func normalizedNavigationSwipeMinDelta(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		// WebKitGTK tracks touchpad swipe progress as distance / 400 and commits
		// past 0.5 progress. In this bridge MinDelta represents that raw GTK
		// surface-unit commit distance, not translated CEF wheel deltas.
		return defaultNavigationSwipeCommitDistance
	}
	return value
}

func normalizedNavigationSwipeRatio(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 0.5
	}
	return value
}

const cefScrollUnitsPerNotch = 240

func TranslateScrollDeltas(dx, dy float64) (int32, int32) {
	return int32(dx * cefScrollUnitsPerNotch), int32(-dy * cefScrollUnitsPerNotch)
}

func TranslateScrollDeltasWithOptions(dx, dy float64, unit gdk.ScrollUnit, opts ScrollOptions) (int32, int32) {
	fx, fy := translateScrollFloat(dx, dy, unit, opts)
	if unit == gdk.ScrollUnitSurfaceValue {
		return int32(math.Round(fx)), int32(math.Round(fy))
	}
	return int32(fx), int32(fy)
}

// translateScrollFloat applies multipliers and the one-time MaxDelta clamp
// in float64 output units. Integer conversion (truncation for wheels,
// rounding for precise surfaces) happens at delivery so animated sessions
// can retain fractions across updates and frames.
func translateScrollFloat(dx, dy float64, unit gdk.ScrollUnit, opts ScrollOptions) (float64, float64) {
	multiplier := normalizeMultiplier(opts.WheelMultiplier)
	unitScale := float64(cefScrollUnitsPerNotch)
	if unit == gdk.ScrollUnitSurfaceValue {
		multiplier = normalizePreciseMultiplier(opts.PreciseMultiplier)
		unitScale = 1
	}
	horizontal := normalizeMultiplier(opts.HorizontalMultiplier)
	vertical := normalizeMultiplier(opts.VerticalMultiplier)
	return clampScrollFloat(dx*unitScale*multiplier*horizontal, opts.MaxDelta),
		clampScrollFloat(-dy*unitScale*multiplier*vertical, opts.MaxDelta)
}

func normalizeMultiplier(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 1
	}
	return value
}

func normalizePreciseMultiplier(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 2.5
	}
	return value
}

func clampScrollFloat(value float64, maxAbs int32) float64 {
	// Non-finite input never becomes a scroll delta: int32(NaN) is
	// platform-defined and would otherwise inject a garbage jump.
	if !isFinite(value) {
		return 0
	}
	limit := maxInt32Float
	if maxAbs > 0 {
		limit = float64(maxAbs)
	}
	if value > limit {
		value = limit
	}
	if value < -limit {
		value = -limit
	}
	return value
}

func currentScrollUnit(controller gtk.EventControllerScroll) (gdk.ScrollUnit, bool) {
	// Do not call GtkEventController.GetCurrentEvent here. The current puregotk
	// binding treats the returned GdkEvent as a GObject and refs it with
	// g_object_ref_sink(), but GdkEvent is a boxed type. That produces a GLib
	// assertion on every scroll event. GtkEventControllerScroll.GetUnit() exposes
	// the scroll unit we need without wrapping the current event.
	return controller.GetUnit(), true
}

const maxInt32Float = float64(int32(1<<31 - 1))
