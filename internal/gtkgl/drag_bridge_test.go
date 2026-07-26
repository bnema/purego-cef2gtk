package gtkgl

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"unsafe"

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
	text, html, link, title string
	image                   cef.Image
	files                   bool
	fileList                cef.StringList
}

func (d *dragTestData) GetFragmentText() string { return d.text }
func (d *dragTestData) GetFragmentHtml() string { return d.html }
func (d *dragTestData) GetLinkURL() string      { return d.link }
func (d *dragTestData) GetLinkTitle() string    { return d.title }
func (d *dragTestData) IsFile() bool            { return d.files }
func (d *dragTestData) GetFilePaths(list cef.StringList) int32 {
	d.fileList = list
	return 1
}
func (d *dragTestData) GetImage() cef.Image { return d.image }
func (d *dragTestData) GetImageHotspot() uintptr {
	y := int32(-9)
	return uintptr(uint64(uint32(17)) | uint64(uint32(y))<<32)
}

type dragTestBinary struct {
	cef.BinaryValue
	data     []byte
	releases int
}

func (b *dragTestBinary) GetSize() int { return len(b.data) }
func (b *dragTestBinary) GetData(dst unsafe.Pointer, size, offset int) int {
	return copy(unsafe.Slice((*byte)(dst), size), b.data[offset:])
}
func (b *dragTestBinary) Release() { b.releases++ }

type dragTestImage struct {
	cef.Image
	png      *dragTestBinary
	releases int
}

func (i *dragTestImage) GetAsPng(float32, int32, *int32, *int32) cef.BinaryValue { return i.png }
func (i *dragTestImage) Release()                                                { i.releases++ }

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

func TestDecodeImageHotspotUsesPackedCEFPointABI(t *testing.T) {
	negativeY := int32(-41)
	raw := uintptr(uint64(uint32(23)) | uint64(uint32(negativeY))<<32)
	x, y, ok := decodeImageHotspot(raw)
	if unsafe.Sizeof(raw) == 8 {
		if !ok || x != 23 || y != -41 {
			t.Fatalf("decoded hotspot=(%d,%d,%t)", x, y, ok)
		}
	} else if ok || x != 0 || y != 0 {
		t.Fatalf("non-64-bit hotspot fallback=(%d,%d,%t)", x, y, ok)
	}
}

func TestDragBridgeStartSnapshotsAllCEFContentBeforeScheduling(t *testing.T) {
	h := &dragTestHost{}
	binary := &dragTestBinary{data: []byte{0x89, 'P', 'N', 'G'}}
	image := &dragTestImage{png: binary}
	data := &dragTestData{text: "plain", html: "<b>rich</b>", link: "https://example.test", title: "Example", image: image, files: true}
	var idle func()
	var got dragPayload
	b := NewDragBridge(nil, nil, h)
	b.newStringList = func(...string) cef.StringList { return 77 }
	b.stringListToSlice = func(list cef.StringList) []string {
		if list != 77 {
			t.Fatalf("list=%d", list)
		}
		return []string{"/tmp/a.txt"}
	}
	freed := cef.StringList(0)
	b.freeStringList = func(list cef.StringList) { freed = list }
	b.schedule = func(fn func()) uint { idle = fn; return 1 }
	b.startNative = func(payload dragPayload, _ cef.DragOperationsMask, _, _ int32) (*nativeDragResources, error) {
		got = payload
		return nil, errors.New("stop after snapshot")
	}

	if b.Start(&dragTestBrowser{host: h}, data, cef.DragOperationsMaskDragOperationCopy, 1, 2) != 1 {
		t.Fatal("start rejected")
	}
	if data.fileList != 77 || freed != 77 || image.releases != 1 || binary.releases != 1 {
		t.Fatalf("CEF ownership list=%d freed=%d image releases=%d binary releases=%d", data.fileList, freed, image.releases, binary.releases)
	}
	data.text, data.html, data.link, data.title = "mutated", "mutated", "mutated", "mutated"
	binary.data[0] = 0
	idle()

	want := dragPayload{OutboundPayload: gtkdnd.OutboundPayload{
		Text: "plain", HTML: "<b>rich</b>", Files: []string{"/tmp/a.txt"}, LinkURL: "https://example.test", LinkTitle: "Example", ImagePNG: []byte{0x89, 'P', 'N', 'G'},
	}, Hotspot: imageHotspot{X: 17, Y: -9, Valid: unsafe.Sizeof(uintptr(0)) == 8}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scheduled payload=%+v, want %+v", got, want)
	}
}

func TestDragBridgeBuildsDeterministicProviderUnionAndCleansRootOwnership(t *testing.T) {
	b := NewDragBridge(nil, nil, nil)
	var mimes []string
	var nextByte uintptr = 100
	var nextProvider uintptr = 200
	var unionChildren []uintptr
	providerReleases := map[uintptr]int{}
	byteReleases := map[uintptr]int{}
	textureReleases := 0
	b.newContentBytes = func(_ []byte, _ uint) *glib.Bytes {
		nextByte++
		return glib.BytesNewFromInternalPtr(nextByte)
	}
	b.newContentProvider = func(mime string, _ *glib.Bytes) *gdk.ContentProvider {
		mimes = append(mimes, mime)
		nextProvider++
		return gdk.ContentProviderNewFromInternalPtr(nextProvider)
	}
	b.newContentUnion = func(children []uintptr) *gdk.ContentProvider {
		unionChildren = append([]uintptr(nil), children...)
		return gdk.ContentProviderNewFromInternalPtr(999)
	}
	b.newTextureFromBytes = func(*glib.Bytes) (*gdk.Texture, error) {
		return gdk.TextureNewFromInternalPtr(500), nil
	}
	b.unrefProvider = func(value *gdk.ContentProvider) { providerReleases[value.GoPointer()]++ }
	b.unrefBytes = func(value *glib.Bytes) { byteReleases[value.GoPointer()]++ }
	b.unrefTexture = func(*gdk.Texture) { textureReleases++ }

	resources, err := b.createNativeContent(dragPayload{OutboundPayload: gtkdnd.OutboundPayload{
		Text: "plain", HTML: "<b>rich</b>", LinkURL: "https://example.test/page", LinkTitle: "Page", ImagePNG: []byte("png"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	wantMIMEs := []string{"text/plain;charset=utf-8", "text/html", "text/uri-list", "text/x-moz-url", "image/png"}
	if !reflect.DeepEqual(mimes, wantMIMEs) {
		t.Fatalf("provider order=%v, want %v", mimes, wantMIMEs)
	}
	if !reflect.DeepEqual(unionChildren, []uintptr{201, 202, 203, 204, 205}) {
		t.Fatalf("union children=%v", unionChildren)
	}
	b.releaseNative(resources)
	if providerReleases[999] != 1 || len(providerReleases) != 1 {
		t.Fatalf("provider releases=%v; children are transfer-full", providerReleases)
	}
	if len(byteReleases) != 5 {
		t.Fatalf("GBytes releases=%v", byteReleases)
	}
	for ptr, count := range byteReleases {
		if count != 1 {
			t.Fatalf("GBytes %d releases=%d", ptr, count)
		}
	}
	if textureReleases != 1 {
		t.Fatalf("texture releases=%d", textureReleases)
	}
	b.releaseNative(resources)
	if providerReleases[999] != 1 || textureReleases != 1 {
		t.Fatalf("idempotent cleanup provider=%v texture=%d", providerReleases, textureReleases)
	}
}

func TestDragBridgeProviderUnionFailureHonorsTransferredChildren(t *testing.T) {
	b := NewDragBridge(nil, nil, nil)
	var next uintptr = 10
	childReleases, byteReleases := 0, 0
	b.newContentBytes = func([]byte, uint) *glib.Bytes {
		next++
		return glib.BytesNewFromInternalPtr(next)
	}
	b.newContentProvider = func(string, *glib.Bytes) *gdk.ContentProvider {
		next++
		return gdk.ContentProviderNewFromInternalPtr(next)
	}
	b.newContentUnion = func([]uintptr) *gdk.ContentProvider { return nil }
	b.newTextureFromBytes = func(*glib.Bytes) (*gdk.Texture, error) { return nil, errors.New("unused") }
	b.unrefProvider = func(*gdk.ContentProvider) { childReleases++ }
	b.unrefBytes = func(*glib.Bytes) { byteReleases++ }
	b.unrefTexture = func(*gdk.Texture) {}

	resources, err := b.createNativeContent(dragPayload{OutboundPayload: gtkdnd.OutboundPayload{Text: "plain", HTML: "<b>rich</b>"}})
	if err == nil || resources != nil {
		t.Fatalf("union failure resources=%+v err=%v", resources, err)
	}
	if childReleases != 0 || byteReleases != 2 {
		t.Fatalf("transferred child releases=%d GBytes releases=%d", childReleases, byteReleases)
	}
}

func TestDragBridgeNativeResourceCleanupHighCountIsExactlyOnce(t *testing.T) {
	b := NewDragBridge(nil, nil, nil)
	drags, providers, bytes, textures := 0, 0, 0, 0
	b.unrefDrag = func(*gdk.Drag) { drags++ }
	b.unrefProvider = func(*gdk.ContentProvider) { providers++ }
	b.unrefBytes = func(*glib.Bytes) { bytes++ }
	b.unrefTexture = func(*gdk.Texture) { textures++ }
	const operations = 10000
	for i := 0; i < operations; i++ {
		resources := &nativeDragResources{
			Drag: &gdk.Drag{}, Provider: &gdk.ContentProvider{}, Bytes: []*glib.Bytes{{}, {}}, Texture: &gdk.Texture{},
		}
		b.releaseNative(resources)
		b.releaseNative(resources)
	}
	if drags != operations || providers != operations || bytes != 2*operations || textures != operations {
		t.Fatalf("cleanup counts drag=%d provider=%d bytes=%d texture=%d", drags, providers, bytes, textures)
	}
}

func TestDragBridgeImageIconUsesHotspotAndDecodeFailureDoesNotAbort(t *testing.T) {
	b := NewDragBridge(nil, nil, nil)
	b.newContentBytes = func([]byte, uint) *glib.Bytes { return glib.BytesNewFromInternalPtr(101) }
	b.newContentProvider = func(string, *glib.Bytes) *gdk.ContentProvider {
		return gdk.ContentProviderNewFromInternalPtr(201)
	}
	b.unrefProvider = func(*gdk.ContentProvider) {}
	b.unrefBytes = func(*glib.Bytes) {}
	b.unrefDrag = func(*gdk.Drag) {}
	decodeTextureReleases := 0
	b.unrefTexture = func(*gdk.Texture) { decodeTextureReleases++ }

	decodeErr := errors.New("invalid png")
	b.newTextureFromBytes = func(*glib.Bytes) (*gdk.Texture, error) {
		return gdk.TextureNewFromInternalPtr(450), decodeErr
	}
	resources, err := b.createNativeContent(dragPayload{OutboundPayload: gtkdnd.OutboundPayload{ImagePNG: []byte("bad")}})
	if err != nil || resources == nil || resources.Provider == nil || resources.Texture != nil {
		t.Fatalf("decode fallback resources=%+v err=%v", resources, err)
	}
	if decodeTextureReleases != 1 {
		t.Fatalf("failed decode texture releases=%d", decodeTextureReleases)
	}
	iconCalls := 0
	b.setDragIcon = func(*gdk.Drag, gdk.Paintable, int, int) { iconCalls++ }
	resources.Drag = &gdk.Drag{}
	b.applyNativeIcon(resources, imageHotspot{X: 4, Y: 7, Valid: true})
	if iconCalls != 0 {
		t.Fatalf("decode failure set %d icons", iconCalls)
	}

	resources.Texture = gdk.TextureNewFromInternalPtr(500)
	var gotX, gotY int
	b.setDragIcon = func(_ *gdk.Drag, paintable gdk.Paintable, x, y int) {
		iconCalls++
		gotX, gotY = x, y
		if paintable.GoPointer() != 500 {
			t.Fatalf("paintable=%d", paintable.GoPointer())
		}
	}
	b.applyNativeIcon(resources, imageHotspot{X: 4, Y: -7, Valid: true})
	b.applyNativeIcon(resources, imageHotspot{X: 99, Y: 99, Valid: false})
	if iconCalls != 1 || gotX != 4 || gotY != -7 {
		t.Fatalf("icon calls=%d hotspot=(%d,%d)", iconCalls, gotX, gotY)
	}
	b.releaseNative(resources)
	if decodeTextureReleases != 2 {
		t.Fatalf("retained texture cleanup count=%d", decodeTextureReleases)
	}
}

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
	b.startNative = func(dragPayload, cef.DragOperationsMask, int32, int32) (*nativeDragResources, error) {
		return nil, errors.New("native start")
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
	b.startNative = func(payload dragPayload, _ cef.DragOperationsMask, _, _ int32) (*nativeDragResources, error) {
		got = payload
		b.protocol.Detach()
		return &nativeDragResources{Drag: &gdk.Drag{}}, nil
	}
	b.cleanupNative = func(*nativeDragResources) { cleanups++ }
	if b.Start(&dragTestBrowser{host: h}, &dragTestData{link: "https://example.invalid/item"}, cef.DragOperationsMaskDragOperationLink, 2, 4) != 1 {
		t.Fatal("start rejected")
	}
	idle()
	if got.LinkURL != "https://example.invalid/item" || got.Text != got.LinkURL {
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
	b.startNative = func(dragPayload, cef.DragOperationsMask, int32, int32) (*nativeDragResources, error) {
		starts++
		return nil, errors.New("must not run")
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
