package cef2gtk

import (
	"testing"

	"github.com/bnema/puregotk/v4/gtk"
)

func TestViewSizeTickObservation_RearmWhileActiveResetsWithoutSecondRegistration(t *testing.T) {
	sample := sizeObservationSample{width: 800, height: 600, scale: 1}
	registrations := 0
	v := &View{
		widget:                    &gtk.Widget{},
		sizeObservationSampleFunc: func() sizeObservationSample { return sample },
		sizeTickRegistrar: func(*gtk.TickCallback) uint {
			registrations++
			return 42
		},
	}

	v.handleObservationSignal()
	if registrations != 1 {
		t.Fatalf("tick registrations = %d, want 1", registrations)
	}
	if v.sizeTickID != 42 {
		t.Fatalf("sizeTickID = %d, want 42", v.sizeTickID)
	}
	if !v.runSizeTickObservation() {
		t.Fatal("first tick should continue")
	}
	if !v.runSizeTickObservation() {
		t.Fatal("second tick should continue before re-arm")
	}

	v.handleObservationSignal()
	if registrations != 1 {
		t.Fatalf("tick re-registered while already active: got %d registrations, want 1", registrations)
	}
	if !v.runSizeTickObservation() {
		t.Fatal("first tick after re-arm should continue")
	}
	if !v.runSizeTickObservation() {
		t.Fatal("second tick after re-arm should continue")
	}
	if v.runSizeTickObservation() {
		t.Fatal("third stable tick after re-arm should stop")
	}
	if v.sizeTickID != 0 || v.sizeTickFunc != nil {
		t.Fatalf("tick state not cleared after stop: id=%d callback=%v", v.sizeTickID, v.sizeTickFunc != nil)
	}
}

func TestViewSizeTickObservation_RearmsOnScaleOnlySignal(t *testing.T) {
	sample := sizeObservationSample{width: 800, height: 600, scale: 1}
	registrations := 0
	v := &View{
		widget:                    &gtk.Widget{},
		sizeObservationSampleFunc: func() sizeObservationSample { return sample },
		sizeTickRegistrar: func(*gtk.TickCallback) uint {
			registrations++
			return 7
		},
	}

	v.handleObservationSignal()
	if !v.runSizeTickObservation() {
		t.Fatal("initial tick should continue")
	}

	sample.scale = 1.25
	v.handleObservationSignal()
	if registrations != 1 {
		t.Fatalf("tick re-registered for scale-only signal: got %d registrations, want 1", registrations)
	}
	if !v.runSizeTickObservation() {
		t.Fatal("first tick after scale-only re-arm should continue")
	}
	if !v.runSizeTickObservation() {
		t.Fatal("second tick after scale-only re-arm should continue")
	}
	if v.runSizeTickObservation() {
		t.Fatal("third stable tick after scale-only re-arm should stop")
	}
}

func TestViewSizeTickObservation_StopsAfterBudgetDuringSentinelChurn(t *testing.T) {
	samples := []sizeObservationSample{
		{width: 1, height: 1, scale: 1},
		{width: 0, height: 1, scale: 1},
		{width: 1, height: 0, scale: 1},
		{width: 1, height: 1, scale: 1},
	}
	idx := 0
	v := &View{
		widget: &gtk.Widget{},
		sizeObservationSampleFunc: func() sizeObservationSample {
			sample := samples[idx%len(samples)]
			idx++
			return sample
		},
		sizeTickRegistrar: func(*gtk.TickCallback) uint { return 9 },
	}

	v.handleObservationSignal()
	for i := 0; i < sizeTickMaxFrames-1; i++ {
		if !v.runSizeTickObservation() {
			t.Fatalf("tick stopped too early at frame %d", i+1)
		}
	}
	if v.runSizeTickObservation() {
		t.Fatal("tick should stop after the max frame budget under sentinel churn")
	}
	if v.sizeTickID != 0 {
		t.Fatalf("sizeTickID = %d after budget stop, want 0", v.sizeTickID)
	}
}
