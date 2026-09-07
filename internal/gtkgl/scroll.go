package gtkgl

import (
	"math"
	"sync"

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
}

func newScrollController() *scrollController {
	return &scrollController{}
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

func (ib *InputBridge) onScrollBoundary(phase ScrollPhase, unit gdk.ScrollUnit, unitKnown bool, mods uint) {
	_, x, y, _, _, handler := ib.currentScrollState()
	switch phase {
	case ScrollPhaseBegin:
		ib.resetNavigationSwipe()
	case ScrollPhaseEnd:
		ib.finishNavigationSwipe()
		ib.resetNavigationSwipe()
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
	_, x, y, _, _, handler := ib.currentScrollState()
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
	multiplier := normalizeMultiplier(opts.WheelMultiplier)
	unitScale := float64(cefScrollUnitsPerNotch)
	round := false
	if unit == gdk.ScrollUnitSurfaceValue {
		multiplier = normalizePreciseMultiplier(opts.PreciseMultiplier)
		unitScale = 1
		round = true
	}
	horizontal := normalizeMultiplier(opts.HorizontalMultiplier)
	vertical := normalizeMultiplier(opts.VerticalMultiplier)
	deltaX := clampScrollDelta(dx*unitScale*multiplier*horizontal, opts.MaxDelta, round)
	deltaY := clampScrollDelta(-dy*unitScale*multiplier*vertical, opts.MaxDelta, round)
	return deltaX, deltaY
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

func clampScrollDelta(value float64, maxAbs int32, round bool) int32 {
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
	if round {
		value = math.Round(value)
	}
	return int32(value)
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
