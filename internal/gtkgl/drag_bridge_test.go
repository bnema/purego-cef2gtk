package gtkgl

import (
	"errors"
	"sync"
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

func TestDragBridgeUpdateCursorCoalescesToLatestOperation(t *testing.T) {
	b := NewDragBridge(nil, nil, nil)
	var idles []func()
	b.schedule = func(fn func()) uint {
		idles = append(idles, fn)
		return uint(len(idles))
	}
	var preferred []gdk.DragAction
	b.status = func(_ *gdk.Drop, _ gdk.DragAction, action gdk.DragAction) {
		preferred = append(preferred, action)
	}
	b.mu.Lock()
	b.activeDrop = &gdk.Drop{}
	b.activeAllowed = cef.DragOperationsMaskDragOperationCopy
	b.mu.Unlock()

	b.UpdateCursor(cef.DragOperationsMaskDragOperationNone)
	b.UpdateCursor(cef.DragOperationsMaskDragOperationCopy)

	if len(idles) != 1 {
		t.Fatalf("scheduled idles=%d, want 1", len(idles))
	}
	idles[0]()
	if len(preferred) != 1 || preferred[0] != gdk.ActionCopyValue {
		t.Fatalf("preferred updates=%v, want only Copy", preferred)
	}
}

func TestDragBridgeUpdateCursorRetainsDropDuringStatus(t *testing.T) {
	b := NewDragBridge(nil, nil, nil)
	var idle func()
	b.schedule = func(fn func()) uint { idle = fn; return 1 }

	var lifetimeMu sync.Mutex
	refs := 0
	b.retainDrop = func(*gdk.Drop) {
		lifetimeMu.Lock()
		refs++
		lifetimeMu.Unlock()
	}
	b.releaseDrop = func(*gdk.Drop) {
		lifetimeMu.Lock()
		refs--
		lifetimeMu.Unlock()
	}
	statusEntered := make(chan struct{})
	statusRelease := make(chan struct{})
	statusUnlocked := make(chan bool, 1)
	b.status = func(_ *gdk.Drop, _, _ gdk.DragAction) {
		unlocked := b.mu.TryLock()
		if unlocked {
			b.mu.Unlock()
		}
		statusUnlocked <- unlocked
		close(statusEntered)
		<-statusRelease
	}

	b.setActiveDrop(&gdk.Drop{})
	b.mu.Lock()
	b.activeAllowed = cef.DragOperationsMaskDragOperationCopy
	b.mu.Unlock()
	b.UpdateCursor(cef.DragOperationsMaskDragOperationCopy)

	idleDone := make(chan struct{})
	go func() {
		idle()
		close(idleDone)
	}()
	<-statusEntered

	clearDone := make(chan struct{})
	go func() {
		b.clearActiveDrop(0)
		close(clearDone)
	}()
	<-clearDone
	lifetimeMu.Lock()
	refsDuringStatus := refs
	lifetimeMu.Unlock()
	if refsDuringStatus != 1 {
		t.Fatalf("drop refs during status=%d, want temporary ref", refsDuringStatus)
	}
	if !<-statusUnlocked {
		t.Fatal("status called while bridge mutex held")
	}

	close(statusRelease)
	<-idleDone
	lifetimeMu.Lock()
	refsAfterStatus := refs
	lifetimeMu.Unlock()
	if refsAfterStatus != 0 {
		t.Fatalf("drop refs after status=%d, want balanced lifetime", refsAfterStatus)
	}
}

func TestDragBridgeUpdateCursorKeepsSettledNone(t *testing.T) {
	b := NewDragBridge(nil, nil, nil)
	var idles []func()
	b.schedule = func(fn func()) uint {
		idles = append(idles, fn)
		return uint(len(idles))
	}
	var preferred []gdk.DragAction
	b.status = func(_ *gdk.Drop, _ gdk.DragAction, action gdk.DragAction) {
		preferred = append(preferred, action)
	}
	b.mu.Lock()
	b.activeDrop = &gdk.Drop{}
	b.activeAllowed = cef.DragOperationsMaskDragOperationCopy
	b.mu.Unlock()

	b.UpdateCursor(cef.DragOperationsMaskDragOperationCopy)
	b.UpdateCursor(cef.DragOperationsMaskDragOperationNone)
	b.UpdateCursor(cef.DragOperationsMaskDragOperationNone)

	if len(idles) != 1 {
		t.Fatalf("scheduled idles=%d, want 1", len(idles))
	}
	idles[0]()
	if len(preferred) != 1 || preferred[0] != gdk.ActionNoneValue {
		t.Fatalf("preferred updates=%v, want only None", preferred)
	}
}

func TestDragBridgeUpdateCursorRecoversAfterSchedulingRefusal(t *testing.T) {
	b := NewDragBridge(nil, nil, nil)
	var idle func()
	attempts := 0
	b.schedule = func(fn func()) uint {
		attempts++
		if attempts == 1 {
			return 0
		}
		idle = fn
		return 1
	}
	var preferred []gdk.DragAction
	b.status = func(_ *gdk.Drop, _ gdk.DragAction, action gdk.DragAction) {
		preferred = append(preferred, action)
	}
	b.mu.Lock()
	b.activeDrop = &gdk.Drop{}
	b.activeAllowed = cef.DragOperationsMaskDragOperationCopy
	b.mu.Unlock()

	b.UpdateCursor(cef.DragOperationsMaskDragOperationNone)
	b.UpdateCursor(cef.DragOperationsMaskDragOperationCopy)

	if attempts != 2 || idle == nil {
		t.Fatalf("schedule attempts=%d idle=%v", attempts, idle != nil)
	}
	idle()
	if len(preferred) != 1 || preferred[0] != gdk.ActionCopyValue {
		t.Fatalf("preferred updates=%v, want only Copy", preferred)
	}
}

func TestDragBridgeUpdateCursorPreservesConcurrentWriteAfterSchedulingRefusal(t *testing.T) {
	b := NewDragBridge(nil, nil, nil)
	firstScheduleEntered := make(chan struct{})
	allowFirstRefusal := make(chan struct{})
	var scheduleMu sync.Mutex
	attempts := 0
	var idle func()
	b.schedule = func(fn func()) uint {
		scheduleMu.Lock()
		attempts++
		attempt := attempts
		scheduleMu.Unlock()
		if attempt == 1 {
			close(firstScheduleEntered)
			<-allowFirstRefusal
			return 0
		}
		idle = fn
		return 1
	}
	var preferred []gdk.DragAction
	b.status = func(_ *gdk.Drop, _ gdk.DragAction, action gdk.DragAction) {
		preferred = append(preferred, action)
	}
	b.mu.Lock()
	b.activeDrop = &gdk.Drop{}
	b.activeAllowed = cef.DragOperationsMaskDragOperationCopy
	b.mu.Unlock()

	firstDone := make(chan struct{})
	go func() {
		b.UpdateCursor(cef.DragOperationsMaskDragOperationNone)
		close(firstDone)
	}()
	<-firstScheduleEntered
	b.UpdateCursor(cef.DragOperationsMaskDragOperationCopy)
	close(allowFirstRefusal)
	<-firstDone

	scheduleMu.Lock()
	gotAttempts := attempts
	scheduleMu.Unlock()
	if gotAttempts != 2 || idle == nil {
		t.Fatalf("schedule attempts=%d idle=%v, want latest write rescheduled once", gotAttempts, idle != nil)
	}
	idle()
	if len(preferred) != 1 || preferred[0] != gdk.ActionCopyValue {
		t.Fatalf("preferred updates=%v, want only latest Copy", preferred)
	}
}

func TestDragBridgeUpdateCursorDoesNotBusyRetrySchedulerRefusal(t *testing.T) {
	b := NewDragBridge(nil, nil, nil)
	firstScheduleEntered := make(chan struct{})
	allowRefusal := make(chan struct{})
	var scheduleMu sync.Mutex
	attempts := 0
	b.schedule = func(func()) uint {
		scheduleMu.Lock()
		attempts++
		attempt := attempts
		scheduleMu.Unlock()
		if attempt == 1 {
			close(firstScheduleEntered)
			<-allowRefusal
		}
		return 0
	}

	firstDone := make(chan struct{})
	go func() {
		b.UpdateCursor(cef.DragOperationsMaskDragOperationNone)
		close(firstDone)
	}()
	<-firstScheduleEntered
	b.UpdateCursor(cef.DragOperationsMaskDragOperationCopy)
	close(allowRefusal)
	<-firstDone

	scheduleMu.Lock()
	gotAttempts := attempts
	scheduleMu.Unlock()
	if gotAttempts != 2 {
		t.Fatalf("schedule attempts=%d, want one bounded retry", gotAttempts)
	}
}

func TestDragBridgeUpdateCursorRejectsStaleDropGeneration(t *testing.T) {
	b := NewDragBridge(nil, nil, nil)
	b.retainDrop = func(*gdk.Drop) {}
	b.releaseDrop = func(*gdk.Drop) {}
	var idles []func()
	b.schedule = func(fn func()) uint {
		idles = append(idles, fn)
		return uint(len(idles))
	}
	type update struct {
		drop   *gdk.Drop
		action gdk.DragAction
	}
	var updates []update
	b.status = func(drop *gdk.Drop, _ gdk.DragAction, action gdk.DragAction) {
		updates = append(updates, update{drop, action})
	}
	first, second := &gdk.Drop{}, &gdk.Drop{}
	b.setActiveDrop(first)
	b.mu.Lock()
	b.activeAllowed = cef.DragOperationsMaskDragOperationCopy
	b.mu.Unlock()
	b.UpdateCursor(cef.DragOperationsMaskDragOperationCopy)

	b.setActiveDrop(second)
	b.mu.Lock()
	b.activeAllowed = cef.DragOperationsMaskDragOperationCopy
	b.mu.Unlock()
	b.UpdateCursor(cef.DragOperationsMaskDragOperationNone)

	if len(idles) != 2 {
		t.Fatalf("scheduled idles=%d, want one per generation", len(idles))
	}
	idles[0]()
	idles[1]()
	if len(updates) != 1 || updates[0].drop != second || updates[0].action != gdk.ActionNoneValue {
		t.Fatalf("status updates=%v, want only second drop with None", updates)
	}
}

func TestDragBridgeUpdateCursorConcurrentLatestWins(t *testing.T) {
	b := NewDragBridge(nil, nil, nil)
	var scheduleMu sync.Mutex
	var idles []func()
	b.schedule = func(fn func()) uint {
		scheduleMu.Lock()
		defer scheduleMu.Unlock()
		idles = append(idles, fn)
		return uint(len(idles))
	}
	var preferred []gdk.DragAction
	b.status = func(_ *gdk.Drop, _ gdk.DragAction, action gdk.DragAction) {
		preferred = append(preferred, action)
	}
	b.mu.Lock()
	b.activeDrop = &gdk.Drop{}
	b.activeAllowed = cef.DragOperationsMaskDragOperationCopy
	b.mu.Unlock()

	const updates = 10000
	var wg sync.WaitGroup
	wg.Add(updates)
	for i := 0; i < updates; i++ {
		op := cef.DragOperationsMaskDragOperationNone
		if i%2 == 0 {
			op = cef.DragOperationsMaskDragOperationCopy
		}
		go func() {
			defer wg.Done()
			b.UpdateCursor(op)
		}()
	}
	wg.Wait()
	b.UpdateCursor(cef.DragOperationsMaskDragOperationCopy)

	scheduleMu.Lock()
	queued := append([]func(){}, idles...)
	scheduleMu.Unlock()
	if len(queued) != 1 {
		t.Fatalf("scheduled idles=%d, want 1", len(queued))
	}
	queued[0]()
	if len(preferred) != 1 || preferred[0] != gdk.ActionCopyValue {
		t.Fatalf("preferred updates=%v, want only latest Copy", preferred)
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
