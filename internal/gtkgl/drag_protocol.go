package gtkgl

import (
	"sync"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/puregotk/v4/gdk"
)

// DragNegotiation keeps offered/allowed masks separate. Preferred is chosen
// deterministically from their intersection in Copy, Move, Link order.
type DragNegotiation struct {
	Offered, TargetAllowed, Allowed, Preferred cef.DragOperationsMask
}

func NegotiateDragActions(offered, targetAllowed cef.DragOperationsMask) DragNegotiation {
	allowed := offered & targetAllowed
	preferred := cef.DragOperationsMaskDragOperationNone
	for _, action := range []cef.DragOperationsMask{cef.DragOperationsMaskDragOperationCopy, cef.DragOperationsMaskDragOperationMove, cef.DragOperationsMaskDragOperationLink} {
		if allowed&action != 0 {
			preferred = action
			break
		}
	}
	return DragNegotiation{Offered: offered, TargetAllowed: targetAllowed, Allowed: allowed, Preferred: preferred}
}

func CEFToGDKDragActions(mask cef.DragOperationsMask) gdk.DragAction {
	var out gdk.DragAction
	if mask&cef.DragOperationsMaskDragOperationCopy != 0 {
		out |= gdk.ActionCopyValue
	}
	if mask&cef.DragOperationsMaskDragOperationMove != 0 {
		out |= gdk.ActionMoveValue
	}
	if mask&cef.DragOperationsMaskDragOperationLink != 0 {
		out |= gdk.ActionLinkValue
	}
	return out
}

func GDKToCEFDragActions(actions gdk.DragAction) cef.DragOperationsMask {
	var out cef.DragOperationsMask
	if actions&gdk.ActionCopyValue != 0 {
		out |= cef.DragOperationsMaskDragOperationCopy
	}
	if actions&gdk.ActionMoveValue != 0 {
		out |= cef.DragOperationsMaskDragOperationMove
	}
	if actions&gdk.ActionLinkValue != 0 {
		out |= cef.DragOperationsMaskDragOperationLink
	}
	return out
}

// DeviceToLogicalCoordinate converts CEF device-pixel coordinates to the
// logical coordinates required by GDK.
func DeviceToLogicalCoordinate(value int32, scale float64) float64 {
	return float64(value) / normalizeScale(scale)
}

type targetDragState struct {
	mu         sync.Mutex
	generation uint64
	token      uintptr
	entered    bool
}

// TargetDragProtocol serializes GTK target callbacks and rejects callbacks
// that do not belong to the currently entered GdkDrop.
type TargetDragProtocol struct{ state targetDragState }

func NewTargetDragProtocol() *TargetDragProtocol { return &TargetDragProtocol{} }

func (p *TargetDragProtocol) Enter(token uintptr) uint64 {
	if p == nil || token == 0 {
		return 0
	}
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	if p.state.entered {
		return 0
	}
	p.state.generation++
	p.state.token, p.state.entered = token, true
	return p.state.generation
}

func (p *TargetDragProtocol) Motion(token uintptr) bool { return p.matches(token, false) }
func (p *TargetDragProtocol) Leave(token uintptr) bool  { return p.matches(token, true) }
func (p *TargetDragProtocol) Drop(token uintptr) bool   { return p.matches(token, true) }

func (p *TargetDragProtocol) matches(token uintptr, close bool) bool {
	if p == nil || token == 0 {
		return false
	}
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	if !p.state.entered || token != p.state.token {
		return false
	}
	if close {
		p.state.entered, p.state.token = false, 0
	}
	return true
}

func (p *TargetDragProtocol) Detach() {
	if p == nil {
		return
	}
	p.state.mu.Lock()
	p.state.generation++
	p.state.entered, p.state.token = false, 0
	p.state.mu.Unlock()
}

type dragProtocolState uint8

const (
	dragIdle dragProtocolState = iota
	dragStarting
	dragActive
)

type SourceEnd struct {
	X, Y      int32
	Operation cef.DragOperationsMask
}
type SourceFinish struct {
	X, Y                 int32
	CoordinatesAvailable bool
	Operation            cef.DragOperationsMask
}

// DragProtocol is the pure, generation-guarded source-side protocol machine.
// GTK callbacks may race CEF callbacks, so all transitions are serialized.
type DragProtocol struct {
	mu          sync.Mutex
	generation  uint64
	state       dragProtocolState
	dropped     bool
	endedAtSent bool
	onEndedAt   func(SourceEnd)
	onSystemEnd func()
	onDisarm    func()
}

func NewDragProtocol(onEndedAt func(SourceEnd), onSystemEnd, onDisarm func()) *DragProtocol {
	return &DragProtocol{onEndedAt: onEndedAt, onSystemEnd: onSystemEnd, onDisarm: onDisarm}
}

func (p *DragProtocol) Begin() (uint64, bool) {
	if p == nil {
		return 0, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != dragIdle {
		return p.generation, false
	}
	p.generation++
	p.state = dragStarting
	p.dropped, p.endedAtSent = false, false
	return p.generation, true
}

// RejectStart returns a scheduler-refused start to idle without emitting CEF
// completion callbacks because StartDragging will return 0 to CEF.
func (p *DragProtocol) RejectStart(gen uint64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	if gen == p.generation && p.state == dragStarting {
		p.state, p.dropped, p.endedAtSent = dragIdle, false, false
	}
	p.mu.Unlock()
}

func (p *DragProtocol) CurrentGeneration() (uint64, bool) {
	if p == nil {
		return 0, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.generation, p.state != dragIdle
}

func (p *DragProtocol) IsStarting(gen uint64) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return gen == p.generation && p.state == dragStarting
}

func (p *DragProtocol) Activate(gen uint64) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if gen != p.generation || p.state != dragStarting {
		return false
	}
	p.state = dragActive
	return true
}

func (p *DragProtocol) OwnDrop(gen uint64, x, y int32, operation cef.DragOperationsMask) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	if gen != p.generation || p.state != dragActive || p.dropped {
		p.mu.Unlock()
		return false
	}
	p.dropped, p.endedAtSent = true, true
	cb := p.onEndedAt
	p.mu.Unlock()
	if cb != nil {
		cb(SourceEnd{X: x, Y: y, Operation: operation})
	}
	return true
}

func (p *DragProtocol) Cancel(gen uint64) { p.Finish(gen, SourceFinish{}) }

func (p *DragProtocol) Finish(gen uint64, finish SourceFinish) {
	if p == nil {
		return
	}
	p.mu.Lock()
	if gen != p.generation || p.state == dragIdle {
		p.mu.Unlock()
		return
	}
	x, y := int32(-1), int32(-1)
	if finish.CoordinatesAvailable {
		x, y = finish.X, finish.Y
	}
	ended := p.onEndedAt
	needEnded := !p.endedAtSent
	system, disarm := p.onSystemEnd, p.onDisarm
	p.state = dragIdle
	// dropped is deliberately retained until all callback decisions are copied.
	p.dropped, p.endedAtSent = false, false
	p.mu.Unlock()
	if needEnded && ended != nil {
		ended(SourceEnd{X: x, Y: y, Operation: finish.Operation})
	}
	if system != nil {
		system()
	}
	if disarm != nil {
		disarm()
	}
}

func (p *DragProtocol) Detach() {
	if p == nil {
		return
	}
	p.mu.Lock()
	gen, active := p.generation, p.state != dragIdle
	p.generation++
	p.mu.Unlock()
	if active {
		// Finish the invalidated operation under its old generation through a
		// dedicated closure path because normal stale callbacks are rejected.
		p.finishDetached(gen)
	}
}

func (p *DragProtocol) finishDetached(oldGen uint64) {
	p.mu.Lock()
	if p.state == dragIdle {
		p.mu.Unlock()
		return
	}
	ended, needEnded, system, disarm := p.onEndedAt, !p.endedAtSent, p.onSystemEnd, p.onDisarm
	p.state, p.dropped, p.endedAtSent = dragIdle, false, false
	p.mu.Unlock()
	if needEnded && ended != nil {
		ended(SourceEnd{X: -1, Y: -1, Operation: cef.DragOperationsMaskDragOperationNone})
	}
	if system != nil {
		system()
	}
	if disarm != nil {
		disarm()
	}
	_ = oldGen
}
