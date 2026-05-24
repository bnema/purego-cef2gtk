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
	if v.sizeTickID != 0 {
		t.Fatalf("tick state not cleared after stop: id=%d", v.sizeTickID)
	}
	if v.sizeTickFunc == nil {
		t.Fatal("size tick callback should be retained for reuse after stop")
	}
}

func TestViewSizeTickObservation_ReusesCallbackAcrossCompletedArms(t *testing.T) {
	sample := sizeObservationSample{width: 800, height: 600, scale: 1}
	var callbacks []*gtk.TickCallback
	v := &View{
		widget:                    &gtk.Widget{},
		sizeObservationSampleFunc: func() sizeObservationSample { return sample },
		sizeTickRegistrar: func(cb *gtk.TickCallback) uint {
			callbacks = append(callbacks, cb)
			return uint(len(callbacks))
		},
	}

	v.handleObservationSignal()
	for v.sizeTickID != 0 {
		v.runSizeTickObservation()
	}
	v.handleObservationSignal()

	if len(callbacks) != 2 {
		t.Fatalf("tick registrations = %d, want 2", len(callbacks))
	}
	if callbacks[0] != callbacks[1] {
		t.Fatal("size tick callback pointer was replaced across completed observation arms")
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

func TestSizeObservationStrategy_GLAreaUsesWidgetScaleAndGLAreaResize(t *testing.T) {
	strategy := sizeObservationStrategy(true)
	if len(strategy.widgetNotifyDetails) != 1 || strategy.widgetNotifyDetails[0] != "scale-factor" {
		t.Fatalf("widget notify details = %v, want only scale-factor", strategy.widgetNotifyDetails)
	}
	if len(strategy.surfaceSizeNotifyDetails) != 0 {
		t.Fatalf("surface size notify details = %v, want none for GLArea", strategy.surfaceSizeNotifyDetails)
	}
	if len(strategy.surfaceScaleNotifyDetails) != 2 || strategy.surfaceScaleNotifyDetails[0] != "scale" || strategy.surfaceScaleNotifyDetails[1] != "scale-factor" {
		t.Fatalf("surface scale notify details = %v, want [scale scale-factor]", strategy.surfaceScaleNotifyDetails)
	}
	if !strategy.useGLAreaResize {
		t.Fatal("GLArea strategy should use GtkGLArea::resize")
	}
	if strategy.useSurfaceLayout {
		t.Fatal("GLArea strategy should not depend on GdkSurface::layout")
	}
}

func TestSizeObservationStrategy_GDKDMABUFUsesSurfaceLayoutWithoutDeadWidgetSizeNotify(t *testing.T) {
	strategy := sizeObservationStrategy(false)
	if len(strategy.widgetNotifyDetails) != 1 || strategy.widgetNotifyDetails[0] != "scale-factor" {
		t.Fatalf("widget notify details = %v, want only scale-factor", strategy.widgetNotifyDetails)
	}
	if len(strategy.surfaceSizeNotifyDetails) != 2 || strategy.surfaceSizeNotifyDetails[0] != "width" || strategy.surfaceSizeNotifyDetails[1] != "height" {
		t.Fatalf("surface size notify details = %v, want [width height]", strategy.surfaceSizeNotifyDetails)
	}
	if len(strategy.surfaceScaleNotifyDetails) != 2 || strategy.surfaceScaleNotifyDetails[0] != "scale" || strategy.surfaceScaleNotifyDetails[1] != "scale-factor" {
		t.Fatalf("surface scale notify details = %v, want [scale scale-factor]", strategy.surfaceScaleNotifyDetails)
	}
	if strategy.useGLAreaResize {
		t.Fatal("GDKDMABUF strategy should not use GtkGLArea::resize")
	}
	if !strategy.useSurfaceLayout {
		t.Fatal("GDKDMABUF strategy should use GdkSurface::layout to observe child allocation relayouts")
	}
}

func TestShouldEmitSizeHooksOnSizeChangeOnly(t *testing.T) {
	tests := []struct {
		name         string
		sizeChanged  bool
		scaleChanged bool
		want         bool
	}{
		{name: "size change only", sizeChanged: true, scaleChanged: false, want: true},
		{name: "size and scale change", sizeChanged: true, scaleChanged: true, want: true},
		{name: "scale change only", sizeChanged: false, scaleChanged: true, want: false},
		{name: "no change", sizeChanged: false, scaleChanged: false, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldEmitSizeHooks(tt.sizeChanged, tt.scaleChanged); got != tt.want {
				t.Fatalf("shouldEmitSizeHooks(%t, %t) = %t, want %t", tt.sizeChanged, tt.scaleChanged, got, tt.want)
			}
		})
	}
}
