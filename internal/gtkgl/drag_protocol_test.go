package gtkgl

import (
	"testing"
	"time"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/puregotk/v4/gdk"
)

func TestNegotiateDragActions(t *testing.T) {
	tests := []struct {
		name             string
		offered, allowed cef.DragOperationsMask
		wantAllowed      cef.DragOperationsMask
		wantPreferred    cef.DragOperationsMask
	}{
		{"none", 0, cef.DragOperationsMaskDragOperationEvery, 0, 0},
		{"only move", cef.DragOperationsMaskDragOperationMove, cef.DragOperationsMaskDragOperationEvery, cef.DragOperationsMaskDragOperationMove, cef.DragOperationsMaskDragOperationMove},
		{"only link", cef.DragOperationsMaskDragOperationLink, cef.DragOperationsMaskDragOperationEvery, cef.DragOperationsMaskDragOperationLink, cef.DragOperationsMaskDragOperationLink},
		{"copy before move", cef.DragOperationsMaskDragOperationCopy | cef.DragOperationsMaskDragOperationMove, cef.DragOperationsMaskDragOperationEvery, cef.DragOperationsMaskDragOperationCopy | cef.DragOperationsMaskDragOperationMove, cef.DragOperationsMaskDragOperationCopy},
		{"move before link", cef.DragOperationsMaskDragOperationMove | cef.DragOperationsMaskDragOperationLink, cef.DragOperationsMaskDragOperationEvery, cef.DragOperationsMaskDragOperationMove | cef.DragOperationsMaskDragOperationLink, cef.DragOperationsMaskDragOperationMove},
		{"empty intersection", cef.DragOperationsMaskDragOperationMove, cef.DragOperationsMaskDragOperationCopy, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NegotiateDragActions(tt.offered, tt.allowed)
			if n.Allowed != tt.wantAllowed || n.Preferred != tt.wantPreferred {
				t.Fatalf("got allowed=%d preferred=%d", n.Allowed, n.Preferred)
			}
		})
	}
}

func TestDragActionMappingBitForBit(t *testing.T) {
	cefMask := cef.DragOperationsMaskDragOperationCopy | cef.DragOperationsMaskDragOperationMove | cef.DragOperationsMaskDragOperationLink
	gdkMask := gdk.ActionCopyValue | gdk.ActionMoveValue | gdk.ActionLinkValue
	if got := CEFToGDKDragActions(cefMask); got != gdkMask {
		t.Fatalf("CEF->GDK = %d", got)
	}
	if got := GDKToCEFDragActions(gdkMask); got != cefMask {
		t.Fatalf("GDK->CEF = %d", got)
	}
}

type protocolRecorder struct {
	ended   []SourceEnd
	systems int
	disarms int
}

func (r *protocolRecorder) endedAt(e SourceEnd) { r.ended = append(r.ended, e) }

func TestDragProtocolGenerationAndIdempotentClosure(t *testing.T) {
	r := &protocolRecorder{}
	p := NewDragProtocol(r.endedAt, func() { r.systems++ }, func() { r.disarms++ })
	gen, ok := p.Begin()
	if !ok {
		t.Fatal("first Begin rejected")
	}
	if _, ok := p.Begin(); ok {
		t.Fatal("concurrent Begin accepted")
	}
	if !p.Activate(gen) {
		t.Fatal("activation rejected")
	}
	if p.Activate(gen - 1) {
		t.Fatal("stale activation accepted")
	}
	p.Finish(gen, SourceFinish{Operation: cef.DragOperationsMaskDragOperationMove})
	p.Finish(gen, SourceFinish{Operation: cef.DragOperationsMaskDragOperationCopy})
	if len(r.ended) != 1 || r.systems != 1 || r.disarms != 1 {
		t.Fatalf("ended=%v systems=%d disarms=%d", r.ended, r.systems, r.disarms)
	}
	if r.ended[0].X != -1 || r.ended[0].Y != -1 || r.ended[0].Operation != cef.DragOperationsMaskDragOperationMove {
		t.Fatalf("end=%+v", r.ended[0])
	}
}

func TestDragProtocolRejectStartOnlyResetsMatchingStartingGeneration(t *testing.T) {
	var nilProtocol *DragProtocol
	nilProtocol.RejectStart(1)

	p := NewDragProtocol(nil, nil, nil)
	gen, ok := p.Begin()
	if !ok {
		t.Fatal("begin rejected")
	}
	p.RejectStart(gen + 1)
	if !p.IsStarting(gen) {
		t.Fatal("mismatched generation reset starting state")
	}
	p.RejectStart(gen)
	if current, active := p.CurrentGeneration(); current != gen || active {
		t.Fatalf("matching rejection generation=%d active=%v", current, active)
	}
	next, ok := p.Begin()
	if !ok || next != gen+1 {
		t.Fatalf("begin after rejection generation=%d ok=%v", next, ok)
	}
	if !p.Activate(next) {
		t.Fatal("activation rejected")
	}
	p.RejectStart(next)
	if current, active := p.CurrentGeneration(); current != next || !active {
		t.Fatalf("active generation changed by rejection: generation=%d active=%v", current, active)
	}
}

func TestDragProtocolCurrentGenerationReportsNilIdleStartingAndActiveStates(t *testing.T) {
	var nilProtocol *DragProtocol
	if gen, active := nilProtocol.CurrentGeneration(); gen != 0 || active {
		t.Fatalf("nil protocol generation=%d active=%v", gen, active)
	}

	p := NewDragProtocol(nil, nil, nil)
	if gen, active := p.CurrentGeneration(); gen != 0 || active {
		t.Fatalf("idle protocol generation=%d active=%v", gen, active)
	}
	gen, _ := p.Begin()
	if current, active := p.CurrentGeneration(); current != gen || !active {
		t.Fatalf("starting protocol generation=%d active=%v", current, active)
	}
	p.Activate(gen)
	if current, active := p.CurrentGeneration(); current != gen || !active {
		t.Fatalf("active protocol generation=%d active=%v", current, active)
	}
	p.Finish(gen, SourceFinish{})
	if current, active := p.CurrentGeneration(); current != gen || active {
		t.Fatalf("finished protocol generation=%d active=%v", current, active)
	}
}

func TestDragProtocolIsStartingRequiresMatchingStartingGeneration(t *testing.T) {
	var nilProtocol *DragProtocol
	if nilProtocol.IsStarting(1) {
		t.Fatal("nil protocol reported starting")
	}

	p := NewDragProtocol(nil, nil, nil)
	if p.IsStarting(0) {
		t.Fatal("idle protocol reported starting")
	}
	gen, _ := p.Begin()
	if !p.IsStarting(gen) {
		t.Fatal("matching generation not reported starting")
	}
	if p.IsStarting(gen + 1) {
		t.Fatal("mismatched generation reported starting")
	}
	p.Activate(gen)
	if p.IsStarting(gen) {
		t.Fatal("active generation reported starting")
	}
}

func TestDragProtocolOwnDropKeepsCoordinatesAndOnlySystemEndsLater(t *testing.T) {
	r := &protocolRecorder{}
	p := NewDragProtocol(r.endedAt, func() { r.systems++ }, nil)
	gen, _ := p.Begin()
	p.Activate(gen)
	if !p.OwnDrop(gen, 42, 24, cef.DragOperationsMaskDragOperationMove) {
		t.Fatal("own drop rejected")
	}
	p.Finish(gen, SourceFinish{Operation: cef.DragOperationsMaskDragOperationCopy})
	if len(r.ended) != 1 || r.ended[0].X != 42 || r.ended[0].Y != 24 || r.ended[0].Operation != cef.DragOperationsMaskDragOperationMove || r.systems != 1 {
		t.Fatalf("ended=%v systems=%d", r.ended, r.systems)
	}
}

func TestTargetDragProtocolRejectsOutOfOrderAndStaleCallbacks(t *testing.T) {
	p := NewTargetDragProtocol()
	if p.Motion(1, 2, 3, 4) != TargetMotionRejected || p.Leave(1) || p.Drop(1) {
		t.Fatal("callback accepted before enter")
	}
	gen := p.Enter(10)
	if gen == 0 || p.Motion(10, 2, 3, 4) != TargetMotionDispatch {
		t.Fatal("entered target rejected motion")
	}
	if p.Enter(10) != 0 || p.Enter(11) != 0 {
		t.Fatal("duplicate enter accepted")
	}
	if p.Motion(11, 2, 3, 4) != TargetMotionRejected || p.Leave(11) || p.Drop(11) {
		t.Fatal("stale target accepted callback")
	}
	if !p.Drop(10) {
		t.Fatal("entered target rejected drop")
	}
	if p.Drop(10) || p.Leave(10) || p.Motion(10, 2, 3, 4) != TargetMotionRejected {
		t.Fatal("callback accepted after drop")
	}
}

func TestTargetDragProtocolDeduplicatesActionInducedMotion(t *testing.T) {
	p := NewTargetDragProtocol()
	p.Enter(20)

	if got := p.Motion(20, 100, 200, 7); got != TargetMotionDispatch {
		t.Fatalf("first motion = %v, want dispatch", got)
	}
	if got := p.Motion(20, 100, 200, 7); got != TargetMotionDuplicate {
		t.Fatalf("unchanged motion = %v, want duplicate", got)
	}
	if got := p.Motion(20, 101, 200, 7); got != TargetMotionDispatch {
		t.Fatalf("changed x motion = %v, want dispatch", got)
	}
	if got := p.Motion(20, 101, 200, 8); got != TargetMotionDispatch {
		t.Fatalf("changed modifiers motion = %v, want dispatch", got)
	}
}

func TestTargetDragProtocolMotionDeduplicationResetsAcrossEntries(t *testing.T) {
	p := NewTargetDragProtocol()
	p.Enter(20)
	if p.Motion(20, 100, 200, 7) != TargetMotionDispatch || !p.Leave(20) {
		t.Fatal("first target lifecycle failed")
	}
	p.Enter(21)
	if got := p.Motion(21, 100, 200, 7); got != TargetMotionDispatch {
		t.Fatalf("first motion after re-enter = %v, want dispatch", got)
	}
	p.Detach()
	if got := p.Motion(21, 101, 200, 7); got != TargetMotionRejected {
		t.Fatalf("motion after detach = %v, want rejected", got)
	}
}

func TestTargetDragProtocolExternalCompletionRequiresOneContentRealReentry(t *testing.T) {
	p := NewTargetDragProtocol()
	gen := p.Enter(30)
	if gen == 0 {
		t.Fatal("enter rejected")
	}
	plan, ok := p.BeginDrop(30)
	if !ok || !plan.RequireContentReal {
		t.Fatalf("external plan=%+v ok=%v", plan, ok)
	}
	if complete, ok := p.CompleteDrop(plan.Generation); !ok || !complete.RequireContentReal {
		t.Fatalf("first completion=%+v ok=%v", complete, ok)
	}
	if _, ok := p.CompleteDrop(plan.Generation); ok {
		t.Fatal("duplicate completion accepted")
	}
}

func TestTargetDragProtocolDispatchBlocksLaterGenerationUntilOldCallbackReturns(t *testing.T) {
	p := NewTargetDragProtocol()
	p.Enter(35)
	plan, _ := p.BeginDrop(35)
	enteredDispatch := make(chan struct{})
	releaseDispatch := make(chan struct{})
	dispatchDone := make(chan bool, 1)
	go func() {
		dispatchDone <- p.DispatchDrop(plan.Generation, func(TargetDropPlan) {
			close(enteredDispatch)
			select {
			case <-releaseDispatch:
			case <-time.After(time.Second):
				t.Error("timed out waiting to release old content dispatch")
			}
		})
	}()
	select {
	case <-enteredDispatch:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for old content dispatch")
	}
	newEnter := make(chan uint64, 1)
	go func() { newEnter <- p.Enter(36) }()
	select {
	case <-newEnter:
		t.Fatal("later generation entered during old content dispatch")
	case <-time.After(50 * time.Millisecond):
		// The later generation remains blocked while the old callback runs.
	}
	close(releaseDispatch)
	select {
	case ok := <-dispatchDone:
		if !ok {
			t.Fatal("old content dispatch failed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for old content dispatch to return")
	}
	select {
	case generation := <-newEnter:
		if generation == 0 {
			t.Fatal("later enter failed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for later generation to enter")
	}
}

func TestTargetDragProtocolOwnContentDoesNotReenterAndStaleReadIsRejected(t *testing.T) {
	p := NewTargetDragProtocol()
	p.Enter(40)
	if !p.MarkContentReal(40) {
		t.Fatal("own content not marked")
	}
	own, ok := p.BeginDrop(40)
	if !ok || own.RequireContentReal {
		t.Fatalf("own plan=%+v ok=%v", own, ok)
	}
	p.Enter(41)
	if _, ok := p.CompleteDrop(own.Generation); ok {
		t.Fatal("completion from prior generation accepted")
	}
}

func TestTargetDragProtocolLeaveClosesOnlyMatchingEnter(t *testing.T) {
	p := NewTargetDragProtocol()
	p.Enter(20)
	if !p.Leave(20) {
		t.Fatal("matching leave rejected")
	}
	if p.Leave(20) || p.Motion(20, 1, 2, 3) != TargetMotionRejected {
		t.Fatal("duplicate callback accepted after leave")
	}
}

func TestDeviceToLogicalCoordinate(t *testing.T) {
	if got := DeviceToLogicalCoordinate(9, 2); got != 4.5 {
		t.Fatalf("DeviceToLogicalCoordinate=%v", got)
	}
	if got := DeviceToLogicalCoordinate(9, 0); got != 9 {
		t.Fatalf("invalid-scale DeviceToLogicalCoordinate=%v", got)
	}
}

func TestDragProtocolDetachFinishesCapturedGenerationWithoutIncrement(t *testing.T) {
	r := &protocolRecorder{}
	p := NewDragProtocol(r.endedAt, func() { r.systems++ }, func() { r.disarms++ })
	gen, ok := p.Begin()
	if !ok {
		t.Fatal("Begin rejected")
	}
	if !p.Activate(gen) {
		t.Fatal("activation rejected")
	}

	p.Detach()

	if current, active := p.CurrentGeneration(); current != gen || active {
		t.Fatalf("generation after Detach=%d active=%v, want captured generation %d idle", current, active, gen)
	}
	if len(r.ended) != 1 || r.systems != 1 || r.disarms != 1 {
		t.Fatalf("ended=%v systems=%d disarms=%d", r.ended, r.systems, r.disarms)
	}
}

func TestDragProtocolStaleDetachedCleanupPreservesNewBegin(t *testing.T) {
	p := NewDragProtocol(nil, nil, nil)
	oldGen, _ := p.Begin()
	p.Activate(oldGen)
	p.Finish(oldGen, SourceFinish{})

	newGen, ok := p.Begin()
	if !ok {
		t.Fatal("new Begin rejected")
	}
	p.finishDetached(oldGen)

	if !p.IsStarting(newGen) {
		t.Fatal("stale detached cleanup cleared new generation")
	}
}

func TestDragProtocolCancelAndDetachUseNone(t *testing.T) {
	r := &protocolRecorder{}
	p := NewDragProtocol(r.endedAt, func() { r.systems++ }, nil)
	gen, _ := p.Begin()
	p.Cancel(gen)
	gen2, _ := p.Begin()
	p.Detach()
	if len(r.ended) != 2 || r.ended[0].Operation != 0 || r.ended[1].Operation != 0 || r.systems != 2 {
		t.Fatalf("ended=%v systems=%d gen2=%d", r.ended, r.systems, gen2)
	}
	if p.Activate(gen2) {
		t.Fatal("detached generation activated")
	}
}
