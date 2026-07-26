package gtkgl

import (
	"errors"
	"sync"
	"testing"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkdnd"
	"github.com/bnema/puregotk/v4/gdk"
	"github.com/bnema/puregotk/v4/gio"
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
	mu      sync.Mutex
	ended   []SourceEnd
	systems int
	target  []string
}

func (h *dragTestHost) DragSourceEndedAt(x, y int32, op cef.DragOperationsMask) {
	h.ended = append(h.ended, SourceEnd{x, y, op})
}
func (h *dragTestHost) DragSourceSystemDragEnded() { h.systems++ }
func (h *dragTestHost) DragTargetDragEnter(cef.DragData, *cef.MouseEvent, cef.DragOperationsMask) {
	h.mu.Lock()
	h.target = append(h.target, "enter")
	h.mu.Unlock()
}
func (h *dragTestHost) DragTargetDragOver(*cef.MouseEvent, cef.DragOperationsMask) {
	h.mu.Lock()
	h.target = append(h.target, "over")
	h.mu.Unlock()
}
func (h *dragTestHost) DragTargetDrop(*cef.MouseEvent) {
	h.mu.Lock()
	h.target = append(h.target, "drop")
	h.mu.Unlock()
}

type bridgeFakeStream struct {
	mu        sync.Mutex
	callbacks []func([]byte, error)
}

func (s *bridgeFakeStream) ReadAsync(_ int, callback func([]byte, error)) {
	s.mu.Lock()
	s.callbacks = append(s.callbacks, callback)
	s.mu.Unlock()
}
func (s *bridgeFakeStream) complete(chunk []byte, err error) {
	s.mu.Lock()
	callback := s.callbacks[0]
	s.callbacks = s.callbacks[1:]
	s.mu.Unlock()
	callback(chunk, err)
}

type bridgeFakeSource struct {
	stream   *bridgeFakeStream
	open     func(gtkdnd.AsyncStream, string, error)
	cancels  int
	releases int
}

func (s *bridgeFakeSource) OpenAsync(_ string, callback func(gtkdnd.AsyncStream, string, error)) {
	s.open = callback
}
func (s *bridgeFakeSource) Cancel()  { s.cancels++ }
func (s *bridgeFakeSource) Release() { s.releases++ }

type bridgeFakeDragData struct{ cef.DragData }

func TestDragBridgeExternalDropReadsToEOFFinishesAndReentersOnce(t *testing.T) {
	h := &dragTestHost{}
	b := NewDragBridge(nil, nil, h)
	b.newInboundData = func(gtkdnd.InboundPayload) cef.DragData { return &bridgeFakeDragData{} }
	token := uintptr(50)
	b.targetProtocol.Enter(token)
	plan, ok := b.targetProtocol.BeginDrop(token)
	if !ok {
		t.Fatal("begin drop rejected")
	}
	source := &bridgeFakeSource{stream: &bridgeFakeStream{}}
	finishes := []gdk.DragAction{}
	e := cef.MouseEvent{X: 7, Y: 9}
	b.beginExternalDrop(plan, []string{"text/plain"}, source, e,
		cef.DragOperationsMaskDragOperationCopy, func(action gdk.DragAction) { finishes = append(finishes, action) })
	source.open(source.stream, "text/plain", nil)
	source.stream.complete([]byte("hello "), nil)
	source.stream.complete([]byte("world"), nil)
	if len(finishes) != 0 {
		t.Fatal("drop finished before EOF")
	}
	source.stream.complete(nil, nil)

	if got := h.target; len(got) != 3 || got[0] != "enter" || got[1] != "over" || got[2] != "drop" {
		t.Fatalf("target order=%v", got)
	}
	if len(finishes) != 1 || finishes[0] != gdk.ActionCopyValue {
		t.Fatalf("finishes=%v", finishes)
	}
	if source.cancels != 0 || source.releases != 1 {
		t.Fatalf("source cancels=%d releases=%d", source.cancels, source.releases)
	}
}

func TestDragBridgeExternalDropDetachCancelsAndLateCallbacksCannotFinishTwice(t *testing.T) {
	h := &dragTestHost{}
	b := NewDragBridge(nil, nil, h)
	b.newInboundData = func(gtkdnd.InboundPayload) cef.DragData { return &bridgeFakeDragData{} }
	token := uintptr(60)
	b.targetProtocol.Enter(token)
	plan, _ := b.targetProtocol.BeginDrop(token)
	source := &bridgeFakeSource{stream: &bridgeFakeStream{}}
	var finishes []gdk.DragAction
	b.beginExternalDrop(plan, []string{"text/plain"}, source, cef.MouseEvent{},
		cef.DragOperationsMaskDragOperationCopy, func(action gdk.DragAction) { finishes = append(finishes, action) })
	open := source.open
	b.Detach()
	open(source.stream, "text/plain", nil)
	open(source.stream, "text/plain", nil)

	if len(finishes) != 1 || finishes[0] != gdk.ActionNoneValue {
		t.Fatalf("finishes=%v", finishes)
	}
	if source.cancels != 1 || source.releases != 1 || len(h.target) != 0 {
		t.Fatalf("cancels=%d releases=%d target=%v", source.cancels, source.releases, h.target)
	}
}

func TestResolvedDropMIMEFallsBackOnlyForSuccessfulSingleMIMERead(t *testing.T) {
	requested := "text/uri-list"
	stream := &gio.InputStream{}
	readErr := errors.New("read finish")

	tests := []struct {
		name   string
		actual string
		stream *gio.InputStream
		err    error
		want   string
	}{
		{name: "empty successful output", stream: stream, want: requested},
		{name: "non-empty output", actual: "image/jpeg", stream: stream, want: "image/jpeg"},
		{name: "missing stream", want: ""},
		{name: "failed read", stream: stream, err: readErr, want: ""},
		{name: "failed read with actual output", actual: "image/jpeg", stream: stream, err: readErr, want: "image/jpeg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolvedDropMIME(requested, tt.actual, tt.stream, tt.err); got != tt.want {
				t.Fatalf("resolved MIME=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestNativeDropSourceReleasesEveryAsyncCallbackSlot(t *testing.T) {
	source := &nativeDropSource{}
	released := 0
	source.unrefCallback = func(any) error {
		released++
		return nil
	}

	const operations = 1000
	for i := 0; i < operations; i++ {
		openCallback := gio.AsyncReadyCallback(func(_, _, _ uintptr) {})
		source.mu.Lock()
		source.pending = 1
		source.openCallback = &openCallback
		source.mu.Unlock()
		source.callbackDone(true)

		readCallback := gio.AsyncReadyCallback(func(_, _, _ uintptr) {})
		source.mu.Lock()
		source.pending = 1
		source.readCallback = &readCallback
		source.mu.Unlock()
		source.callbackDone(false)
	}

	want := 2 * operations
	if released != want {
		t.Fatalf("released callback slots=%d, want %d", released, want)
	}
}

func TestExternalDropReadLimitsBoundPayloadAndCumulativeBytes(t *testing.T) {
	limits := externalDropReadLimits()
	if limits.PayloadBytes <= 0 || limits.CumulativeBytes <= 0 {
		t.Fatalf("production limits must be bounded: %+v", limits)
	}
	if limits.CumulativeBytes < limits.PayloadBytes {
		t.Fatalf("cumulative limit %d is smaller than payload limit %d", limits.CumulativeBytes, limits.PayloadBytes)
	}
}

func TestDragBridgeExternalFileDropInvokesPolicyAndVetoesCEFDispatch(t *testing.T) {
	h := &dragTestHost{}
	b := NewDragBridge(nil, nil, h)
	b.newInboundData = func(gtkdnd.InboundPayload) cef.DragData { return &bridgeFakeDragData{} }
	var paths []string
	b.SetFileDropHandler(func(got []string) bool {
		paths = append([]string(nil), got...)
		return false
	})
	token := uintptr(55)
	b.targetProtocol.Enter(token)
	plan, _ := b.targetProtocol.BeginDrop(token)
	source := &bridgeFakeSource{stream: &bridgeFakeStream{}}
	var finishes []gdk.DragAction
	b.beginExternalDrop(plan, []string{"text/uri-list"}, source, cef.MouseEvent{},
		cef.DragOperationsMaskDragOperationCopy, func(action gdk.DragAction) { finishes = append(finishes, action) })
	source.open(source.stream, "text/uri-list", nil)
	source.stream.complete([]byte("file:///tmp/drop.txt\r\n"), nil)
	source.stream.complete(nil, nil)

	if len(paths) != 1 || paths[0] != "/tmp/drop.txt" {
		t.Fatalf("policy paths=%v", paths)
	}
	if len(h.target) != 0 {
		t.Fatalf("vetoed drop reached CEF: %v", h.target)
	}
	if len(finishes) != 1 || finishes[0] != gdk.ActionNoneValue {
		t.Fatalf("finishes=%v", finishes)
	}
}

func TestDragBridgeExternalDropStressKeepsGenerationsIsolated(t *testing.T) {
	h := &dragTestHost{}
	b := NewDragBridge(nil, nil, h)
	b.newInboundData = func(gtkdnd.InboundPayload) cef.DragData { return &bridgeFakeDragData{} }
	const operations = 1000
	finishes := make([]gdk.DragAction, 0, operations)
	for i := 0; i < operations; i++ {
		token := uintptr(100 + i)
		b.targetProtocol.Enter(token)
		plan, _ := b.targetProtocol.BeginDrop(token)
		source := &bridgeFakeSource{stream: &bridgeFakeStream{}}
		b.beginExternalDrop(plan, []string{"text/plain"}, source, cef.MouseEvent{},
			cef.DragOperationsMaskDragOperationCopy, func(action gdk.DragAction) { finishes = append(finishes, action) })
		source.open(source.stream, "text/plain", nil)
		source.stream.complete([]byte("x"), nil)
		source.stream.complete(nil, nil)
		if source.releases != 1 {
			t.Fatalf("operation %d releases=%d", i, source.releases)
		}
	}
	if len(finishes) != operations {
		t.Fatalf("finishes=%d", len(finishes))
	}
	for i, action := range finishes {
		if action != gdk.ActionCopyValue {
			t.Fatalf("finish %d action=%v", i, action)
		}
	}
}

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
