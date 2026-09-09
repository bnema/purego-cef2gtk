package gtkgl

import (
	"math"
	"testing"
)

func TestInjectScrollStartsWheelBurst(t *testing.T) {
	ib, _, rec := newAnimatedTestBridge(forwardCounter(map[ScrollPhase]int{}), ScrollOptions{WheelSmoothing: true})
	if !ib.InjectScroll(0, -80) {
		t.Fatal("inject rejected with host attached")
	}
	s := ib.scroll.session
	if s.kind != scrollSessionWheel || !s.burstActive {
		t.Fatalf("inject did not start wheel burst: %+v", s)
	}
	if s.pendingY != -80 {
		t.Fatalf("pending = %v, want -80", s.pendingY)
	}
	if len(rec.subs) != 0 {
		t.Fatalf("fresh inject submitted %d events", len(rec.subs))
	}
}

func TestInjectScrollJoinsLiveBurst(t *testing.T) {
	ib, _, _ := newAnimatedTestBridge(forwardCounter(map[ScrollPhase]int{}), ScrollOptions{WheelSmoothing: true})
	ib.InjectScroll(0, -80)
	ib.InjectScroll(0, -80)
	ib.InjectScroll(0, -80)
	s := ib.scroll.session
	if s.kind != scrollSessionWheel || !s.burstActive {
		t.Fatalf("repeat injects split the burst: %+v", s)
	}
	// Repeats accumulate into one burst instead of restarting it; the
	// delivery origin stays frozen at the first impulse.
	if s.x != 0 || s.y != 0 {
		t.Fatalf("origin moved to (%v,%v), want frozen (0,0)", s.x, s.y)
	}
	if s.pendingY >= -80 {
		t.Fatalf("pending = %v, want repeats accumulated below -80", s.pendingY)
	}
}

func TestInjectScrollCoastsAfterRelease(t *testing.T) {
	ib, _, rec := newAnimatedTestBridge(forwardCounter(map[ScrollPhase]int{}), ScrollOptions{WheelSmoothing: true})
	ib.InjectScroll(0, -80)
	ib.InjectScroll(0, -80)
	ib.InjectScroll(0, -80)
	// Key release sends nothing: the tail coasts on its own.
	last := ib.scroll.session.lastImpulseT
	tm := last
	for i := 0; i < 10000; i++ {
		tm += 1.0 / 165
		if !ib.scroll.step(tm) {
			break
		}
	}
	var ty int64
	for _, sub := range rec.subs {
		ty += int64(sub.dy)
	}
	if ty > -200 {
		t.Fatalf("coast total = %d, want most of -240 delivered", ty)
	}
	for _, sub := range rec.subs {
		if sub.evt.X != 0 || sub.evt.Y != 0 {
			t.Fatalf("synthetic coords = (%d,%d), want frozen origin (0,0)", sub.evt.X, sub.evt.Y)
		}
	}
}

func TestInjectScrollRejectsWithoutHost(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	ib.SetScrollOptions(ScrollOptions{WheelSmoothing: true}, nil)
	if ib.InjectScroll(0, -80) {
		t.Fatal("inject accepted without host")
	}
	if ib.scroll.session.kind != scrollSessionNone {
		t.Fatalf("inject without host opened session: %+v", ib.scroll.session)
	}
}

func TestInjectScrollRejectsNonFinite(t *testing.T) {
	ib, _, rec := newAnimatedTestBridge(forwardCounter(map[ScrollPhase]int{}), ScrollOptions{WheelSmoothing: true})
	if ib.InjectScroll(math.NaN(), 0) || ib.InjectScroll(0, math.Inf(1)) {
		t.Fatal("inject accepted non-finite deltas")
	}
	if ib.scroll.session.kind != scrollSessionNone || len(rec.subs) != 0 {
		t.Fatalf("non-finite inject left session %+v with %d subs", ib.scroll.session, len(rec.subs))
	}
}

func TestInjectScrollDirectWithoutSmoothing(t *testing.T) {
	ib, host, rec := newAnimatedTestBridge(forwardCounter(map[ScrollPhase]int{}), ScrollOptions{})
	if !ib.InjectScroll(10, -80) {
		t.Fatal("direct inject rejected with host attached")
	}
	if ib.scroll.session.kind != scrollSessionNone {
		t.Fatalf("direct inject opened animated session: %+v", ib.scroll.session)
	}
	if len(rec.subs) != 0 {
		t.Fatalf("direct inject submitted %d animated events", len(rec.subs))
	}
	if len(host.events) != 1 || host.events[0].dx != 10 || host.events[0].dy != -80 {
		t.Fatalf("direct inject events = %+v, want one (10,-80)", host.events)
	}
}

func TestInjectScrollRejectsAfterDetach(t *testing.T) {
	ib, host, rec := newAnimatedTestBridge(forwardCounter(map[ScrollPhase]int{}), ScrollOptions{WheelSmoothing: true})
	ib.Detach()
	if ib.InjectScroll(0, -80) {
		t.Fatal("inject accepted after detach")
	}
	if ib.scroll.session.kind != scrollSessionNone {
		t.Fatalf("inject after detach opened session: %+v", ib.scroll.session)
	}
	if len(rec.subs) != 0 || len(host.events) != 0 {
		t.Fatalf("inject after detach delivered %d animated + %d direct events", len(rec.subs), len(host.events))
	}
}
