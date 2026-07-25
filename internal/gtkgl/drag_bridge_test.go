package gtkgl

import (
	"errors"
	"testing"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/puregotk/v4/gdk"
	"github.com/bnema/puregotk/v4/glib"
)

type dragTestBrowser struct {
	cef.Browser
	host cef.BrowserHost
}

func (b *dragTestBrowser) GetHost() cef.BrowserHost { return b.host }

type dragTestData struct {
	cef.DragData
	text string
	link string
}

func (d *dragTestData) GetFragmentText() string { return d.text }
func (d *dragTestData) GetLinkURL() string      { return d.link }

type dragTestHost struct {
	cef.BrowserHost
	ended   []SourceEnd
	systems int
}

func (h *dragTestHost) DragSourceEndedAt(x, y int32, op cef.DragOperationsMask) {
	h.ended = append(h.ended, SourceEnd{x, y, op})
}
func (h *dragTestHost) DragSourceSystemDragEnded() { h.systems++ }

func TestDragMouseEventUsesInputScaleAndButtonModifiers(t *testing.T) {
	input := NewInputBridge(nil, 2)
	e := input.DragMouseEvent(3.5, 4.5, uint(gdk.Button1MaskValue|gdk.ControlMaskValue))
	if e.X != 7 || e.Y != 9 {
		t.Fatalf("coordinates=(%d,%d)", e.X, e.Y)
	}
	want := uint32(cef.EventFlagsEventflagLeftMouseButton | cef.EventFlagsEventflagControlDown)
	if e.Modifiers&want != want {
		t.Fatalf("modifiers=%d want bits=%d", e.Modifiers, want)
	}
}

func TestDragBridgeSelectedNoneDoesNotInventFallbackAction(t *testing.T) {
	b := NewDragBridge(nil, nil, nil)
	b.mu.Lock()
	b.selectedKnown = true
	b.selectedAction = cef.DragOperationsMaskDragOperationNone
	b.mu.Unlock()
	n := NegotiateDragActions(cef.DragOperationsMaskDragOperationCopy|cef.DragOperationsMaskDragOperationMove, cef.DragOperationsMaskDragOperationEvery)
	if got := b.preferredAction(n); got != cef.DragOperationsMaskDragOperationNone {
		t.Fatalf("preferred action=%d", got)
	}
}

func TestDragBridgeSchedulerRefusalReturnsZeroWithoutTakingCEFCompletion(t *testing.T) {
	h := &dragTestHost{}
	b := NewDragBridge(nil, nil, h)
	b.schedule = func(func()) uint { return 0 }
	if got := b.Start(&dragTestBrowser{host: h}, &dragTestData{text: "card"}, cef.DragOperationsMaskDragOperationMove, 1, 2); got != 0 {
		t.Fatalf("Start=%d", got)
	}
	if len(h.ended) != 0 || h.systems != 0 {
		t.Fatalf("completion unexpectedly emitted: %v/%d", h.ended, h.systems)
	}
}

func TestDragBridgeAcceptedNativeFailureClosesExactlyOnceWithNone(t *testing.T) {
	h := &dragTestHost{}
	var idle func()
	b := NewDragBridge(nil, nil, h)
	b.schedule = func(fn func()) uint { idle = fn; return 1 }
	b.startNative = func(dragPayload, cef.DragOperationsMask, int32, int32) (*gdk.Drag, []*gdk.ContentProvider, []*glib.Bytes, error) {
		return nil, nil, nil, errors.New("native start")
	}
	if b.Start(&dragTestBrowser{host: h}, &dragTestData{text: "card"}, cef.DragOperationsMaskDragOperationMove, 1, 2) != 1 {
		t.Fatal("accepted start rejected")
	}
	idle()
	idle()
	if len(h.ended) != 1 || h.ended[0].Operation != 0 || h.systems != 1 {
		t.Fatalf("ended=%v systems=%d", h.ended, h.systems)
	}
}

func TestDragBridgeSnapshotsLinkAsURIAndCleansStaleNativeResult(t *testing.T) {
	h := &dragTestHost{}
	var idle func()
	var got dragPayload
	cleanups := 0
	b := NewDragBridge(nil, nil, h)
	b.schedule = func(fn func()) uint { idle = fn; return 1 }
	b.startNative = func(payload dragPayload, _ cef.DragOperationsMask, _, _ int32) (*gdk.Drag, []*gdk.ContentProvider, []*glib.Bytes, error) {
		got = payload
		b.protocol.Detach()
		return &gdk.Drag{}, nil, nil, nil
	}
	b.cleanupNative = func(*gdk.Drag, []*gdk.ContentProvider, []*glib.Bytes) { cleanups++ }
	if b.Start(&dragTestBrowser{host: h}, &dragTestData{link: "https://example.invalid/item"}, cef.DragOperationsMaskDragOperationLink, 2, 4) != 1 {
		t.Fatal("start rejected")
	}
	idle()
	if got.URI != "https://example.invalid/item" || got.Text != got.URI {
		t.Fatalf("payload=%+v", got)
	}
	if cleanups != 1 {
		t.Fatalf("native cleanup count=%d", cleanups)
	}
}

func TestDragBridgeDetachMakesAcceptedIdleStale(t *testing.T) {
	h := &dragTestHost{}
	var idle func()
	starts := 0
	b := NewDragBridge(nil, nil, h)
	b.schedule = func(fn func()) uint { idle = fn; return 1 }
	b.startNative = func(dragPayload, cef.DragOperationsMask, int32, int32) (*gdk.Drag, []*gdk.ContentProvider, []*glib.Bytes, error) {
		starts++
		return nil, nil, nil, errors.New("must not run")
	}
	if b.Start(&dragTestBrowser{host: h}, &dragTestData{text: "card"}, cef.DragOperationsMaskDragOperationMove, 1, 2) != 1 {
		t.Fatal("start rejected")
	}
	b.Detach()
	idle()
	if starts != 0 {
		t.Fatalf("stale idle performed %d native starts", starts)
	}
	if len(h.ended) != 1 || h.ended[0].Operation != 0 || h.systems != 1 {
		t.Fatalf("detach completion=%v/%d", h.ended, h.systems)
	}
}
