package gtkgl

import "math"

const defaultDragThreshold = 8

// PointerPhase is the current press/drag phase tracked for the first button.
type PointerPhase uint8

const (
	PointerIdle PointerPhase = iota
	PointerPressed
	PointerDragging
)

// PointerAbort describes the last known pointer state when an interaction is
// canceled. Coordinates remain in GTK logical units.
type PointerAbort struct {
	Button      uint
	X           float64
	Y           float64
	CoordsValid bool
	State       uint
}

// PointerTracker tracks a single pointer interaction independently of GTK and CEF.
type PointerTracker struct {
	phase PointerPhase

	button      uint
	pressX      float64
	pressY      float64
	lastX       float64
	lastY       float64
	coordsValid bool
	lastState   uint
	threshold   float64
	dndArmed    bool
	dndCanceled bool
	onDragStart func()
	onAborted   func(PointerAbort)
}

// NewPointerTracker creates a tracker. Non-finite and non-positive thresholds
// use the GTK default fallback of 8 logical pixels.
func NewPointerTracker(threshold float64, onDragStart func(), onAborted func(PointerAbort)) *PointerTracker {
	if math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold <= 0 {
		threshold = defaultDragThreshold
	}
	return &PointerTracker{
		threshold:   threshold,
		onDragStart: onDragStart,
		onAborted:   onAborted,
	}
}

// Phase returns the current interaction phase.
func (t *PointerTracker) Phase() PointerPhase {
	if t == nil {
		return PointerIdle
	}
	return t.phase
}

// Press records a real button press. While active, the first button remains tracked.
func (t *PointerTracker) Press(x, y float64, button, state uint) {
	if t == nil {
		return
	}
	t.lastX, t.lastY = x, y
	t.coordsValid = true
	t.lastState = state
	if t.phase != PointerIdle {
		return
	}
	t.phase = PointerPressed
	t.button = button
	t.pressX, t.pressY = x, y
	t.dndCanceled = false
}

// Motion records real pointer coordinates and returns the sequence-claim
// callback once either axis moves strictly farther than the configured
// threshold. The caller must invoke the callback after releasing any lock that
// protects the tracker because GTK callbacks may re-enter the bridge.
func (t *PointerTracker) Motion(x, y float64, state uint) func() {
	if t == nil {
		return nil
	}
	t.lastX, t.lastY = x, y
	t.coordsValid = true
	t.lastState = state
	if t.phase != PointerPressed {
		return nil
	}
	if math.Abs(x-t.pressX) <= t.threshold && math.Abs(y-t.pressY) <= t.threshold {
		return nil
	}
	t.phase = PointerDragging
	return t.onDragStart
}

// Release closes the interaction only when the first tracked button is released.
func (t *PointerTracker) Release(x, y float64, button, state uint) {
	if t == nil {
		return
	}
	t.lastX, t.lastY = x, y
	t.coordsValid = true
	t.lastState = state
	if t.phase != PointerIdle && button == t.button {
		t.resetInteraction()
	}
}

// Cancel aborts an active interaction. While DnD is armed, the abort callback
// is suspended and the interaction remains active until DnD is disarmed.
func (t *PointerTracker) Cancel() {
	abort, onAborted, canceled := t.cancel()
	if canceled && onAborted != nil {
		onAborted(abort)
	}
}

func (t *PointerTracker) cancel() (PointerAbort, func(PointerAbort), bool) {
	if t == nil || t.phase == PointerIdle {
		return PointerAbort{}, nil, false
	}
	if t.dndArmed {
		t.dndCanceled = true
		return PointerAbort{}, nil, false
	}
	abort := t.abortState()
	onAborted := t.onAborted
	t.resetInteraction()
	return abort, onAborted, true
}

// ArmDnd suspends cancel recovery while a native DnD grab owns the pointer.
func (t *PointerTracker) ArmDnd() {
	if t != nil {
		t.dndArmed = true
	}
}

// DisarmDnd ends DnD suspension. An interaction canceled by the DnD grab is
// cleared silently because the native drag protocol owns its completion.
func (t *PointerTracker) DisarmDnd() {
	if t == nil {
		return
	}
	t.dndArmed = false
	if t.dndCanceled {
		t.resetInteraction()
	}
}

func (t *PointerTracker) abortState() PointerAbort {
	return PointerAbort{
		Button:      t.button,
		X:           t.lastX,
		Y:           t.lastY,
		CoordsValid: t.coordsValid,
		State:       t.lastState,
	}
}

func (t *PointerTracker) detach() {
	if t == nil {
		return
	}
	t.onDragStart = nil
	t.onAborted = nil
	t.dndArmed = false
	t.resetInteraction()
	t.lastX, t.lastY = 0, 0
	t.coordsValid = false
	t.lastState = 0
}

func (t *PointerTracker) resetInteraction() {
	t.phase = PointerIdle
	t.button = 0
	t.pressX, t.pressY = 0, 0
	t.dndCanceled = false
}
