package gtkgl

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkdnd"
	"github.com/bnema/puregotk/v4/gdk"
	"github.com/bnema/puregotk/v4/gio"
	"github.com/bnema/puregotk/v4/glib"
	"github.com/bnema/puregotk/v4/gobject"
	"github.com/bnema/puregotk/v4/gtk"
)

type imageHotspot struct {
	X, Y  int32
	Valid bool
}

type dragPayload struct {
	gtkdnd.OutboundPayload
	Hotspot imageHotspot
}

type nativeDragResources struct {
	Drag     *gdk.Drag
	Provider *gdk.ContentProvider
	Bytes    []*glib.Bytes
	Texture  *gdk.Texture
}

type nativeDragStart func(dragPayload, cef.DragOperationsMask, int32, int32) (*nativeDragResources, error)
type dragScheduler func(func()) uint
type dragStatus func(*gdk.Drop, gdk.DragAction, gdk.DragAction)

type releasableAsyncSource interface {
	gtkdnd.AsyncSource
	Release()
}

const dndTraceEnvVar = "PUREGO_CEF2GTK_TRACE_DND"

func traceDND(format string, args ...any) {
	if os.Getenv(dndTraceEnvVar) == "1" {
		_, _ = fmt.Fprintf(os.Stderr, "purego-cef2gtk dnd: "+format+"\n", args...)
	}
}

// nativeDropSource owns one cancellable and any stream returned by ReadFinish.
// Callback values remain pinned while GLib has an outstanding operation; GIO
// guarantees every async callback runs once, including after cancellation.
type nativeDropSource struct {
	mu               sync.Mutex
	drop             *gdk.Drop
	cancellable      *gio.Cancellable
	stream           *gio.InputStream
	openCallback     *gio.AsyncReadyCallback
	readCallback     *gio.AsyncReadyCallback
	unrefCallback    func(any) error
	pending          int
	releaseRequested bool
	released         bool
}

type nativeDropStream struct{ source *nativeDropSource }

func resolvedDropMIME(requested, actual string, stream *gio.InputStream, err error) string {
	if actual == "" && stream != nil && err == nil {
		return requested
	}
	return actual
}

func newNativeDropSource(drop *gdk.Drop) releasableAsyncSource {
	if drop == nil {
		return nil
	}
	cancellable := gio.NewCancellable()
	if cancellable == nil {
		return nil
	}
	// The source keeps a separate reference because the bridge may finish and
	// release its operation reference before a cancellation callback arrives.
	drop.Ref()
	return &nativeDropSource{drop: drop, cancellable: cancellable, unrefCallback: glib.UnrefCallback}
}

func (s *nativeDropSource) OpenAsync(mime string, done func(gtkdnd.AsyncStream, string, error)) {
	s.mu.Lock()
	if s.released || s.pending != 0 {
		s.mu.Unlock()
		done(nil, "", errors.New("drop read source unavailable"))
		return
	}
	s.pending++
	callback := gio.AsyncReadyCallback(func(_, resultPtr, _ uintptr) {
		var actual string
		stream, err := s.drop.ReadFinish(&gio.AsyncResultBase{Ptr: resultPtr}, &actual)
		actual = resolvedDropMIME(mime, actual, stream, err)
		traceDND("read-open requested_mime=%q actual_mime=%q stream=%t error=%v", mime, actual, stream != nil, err)
		s.mu.Lock()
		if stream != nil {
			if err == nil {
				s.stream = stream
			} else {
				stream.Unref()
			}
		}
		s.mu.Unlock()
		s.callbackDone(true)
		done(&nativeDropStream{source: s}, actual, err)
	})
	s.openCallback = &callback
	cancellable := s.cancellable
	s.mu.Unlock()
	s.drop.ReadAsync([]string{mime}, 0, cancellable, &callback, 0)
}

func (s *nativeDropSource) Cancel() {
	s.mu.Lock()
	cancellable := s.cancellable
	s.mu.Unlock()
	if cancellable != nil {
		cancellable.Cancel()
	}
}

func (s *nativeDropSource) Release() {
	s.mu.Lock()
	if s.released || s.releaseRequested {
		s.mu.Unlock()
		return
	}
	s.releaseRequested = true
	if s.pending != 0 {
		s.mu.Unlock()
		return
	}
	s.releaseLocked()
	s.mu.Unlock()
}

func (s *nativeDropSource) callbackDone(open bool) {
	s.mu.Lock()
	var callback *gio.AsyncReadyCallback
	if open {
		callback, s.openCallback = s.openCallback, nil
	} else {
		callback, s.readCallback = s.readCallback, nil
	}
	unref := s.unrefCallback
	s.pending--
	if s.pending == 0 && s.releaseRequested {
		s.releaseLocked()
	}
	s.mu.Unlock()
	if callback != nil && unref != nil {
		_ = unref(callback)
	}
}

func (s *nativeDropSource) releaseLocked() {
	if s.released {
		return
	}
	s.released = true
	if s.stream != nil {
		s.stream.Unref()
		s.stream = nil
	}
	if s.cancellable != nil {
		s.cancellable.Unref()
		s.cancellable = nil
	}
	if s.drop != nil {
		s.drop.Unref()
		s.drop = nil
	}
	s.openCallback, s.readCallback = nil, nil
}

func (s *nativeDropStream) ReadAsync(maxBytes int, done func([]byte, error)) {
	if s == nil || s.source == nil {
		done(nil, errors.New("missing drop stream"))
		return
	}
	source := s.source
	source.mu.Lock()
	if source.released || source.stream == nil || source.pending != 0 {
		source.mu.Unlock()
		done(nil, errors.New("drop stream unavailable"))
		return
	}
	source.pending++
	callback := gio.AsyncReadyCallback(func(_, resultPtr, _ uintptr) {
		bytes, err := source.stream.ReadBytesFinish(&gio.AsyncResultBase{Ptr: resultPtr})
		var chunk []byte
		if bytes != nil {
			size := bytes.GetSize()
			ptr := bytes.GetData(&size)
			if size != 0 && ptr != 0 {
				raw := *(*unsafe.Pointer)(unsafe.Pointer(&ptr))
				chunk = append([]byte(nil), unsafe.Slice((*byte)(raw), int(size))...)
			}
			bytes.Unref()
		}
		traceDND("read-chunk bytes=%d eof=%t error=%v", len(chunk), len(chunk) == 0 && err == nil, err)
		source.callbackDone(false)
		done(chunk, err)
	})
	source.readCallback = &callback
	stream, cancellable := source.stream, source.cancellable
	source.mu.Unlock()
	stream.ReadBytesAsync(uint(maxBytes), 0, cancellable, &callback, 0)
}

type DragBridge struct {
	mu                  sync.Mutex
	widget              *gtk.Widget
	input               *InputBridge
	host                cef.BrowserHost
	protocol            *DragProtocol
	targetProtocol      *TargetDragProtocol
	target              *gtk.DropTargetAsync
	targetHandlers      []uint
	schedule            dragScheduler
	status              dragStatus
	retainDrop          func(*gdk.Drop)
	releaseDrop         func(*gdk.Drop)
	startNative         nativeDragStart
	cleanupNative       func(*nativeDragResources)
	newStringList       func(...string) cef.StringList
	freeStringList      func(cef.StringList)
	stringListToSlice   func(cef.StringList) []string
	newContentBytes     func([]byte, uint) *glib.Bytes
	newContentProvider  func(string, *glib.Bytes) *gdk.ContentProvider
	newContentUnion     func([]uintptr) *gdk.ContentProvider
	newTextureFromBytes func(*glib.Bytes) (*gdk.Texture, error)
	setDragIcon         func(*gdk.Drag, gdk.Paintable, int, int)
	unrefProvider       func(*gdk.ContentProvider)
	unrefBytes          func(*glib.Bytes)
	unrefTexture        func(*gdk.Texture)
	unrefDrag           func(*gdk.Drag)
	sourceHost          cef.BrowserHost
	sourcePayload       dragPayload
	sourceDrag          *gdk.Drag
	sourceHandlers      []uint
	nativeResources     *nativeDragResources
	selectedAction      cef.DragOperationsMask
	selectedKnown       bool
	activeDrop          *gdk.Drop
	activeAllowed       cef.DragOperationsMask
	statusPending       bool
	statusTicket        uint64
	statusRevision      uint64
	dropGeneration      uint64
	externalReader      *gtkdnd.AsyncReader
	newInboundData      func(gtkdnd.InboundPayload) cef.DragData
	fileDropAllowed     func([]string) bool
	newDropSource       func(*gdk.Drop) releasableAsyncSource
}

func externalDropReadLimits() gtkdnd.ReadLimits {
	return gtkdnd.ReadLimits{
		SupportedMIMEs:  []string{"text/uri-list", "text/x-moz-url", "text/html", "text/plain;charset=utf-8", "text/plain"},
		PayloadBytes:    32 << 20,
		CumulativeBytes: 256 << 20,
		ChunkBytes:      64 << 10,
	}
}

func NewDragBridge(widget *gtk.Widget, input *InputBridge, host cef.BrowserHost) *DragBridge {
	b := &DragBridge{widget: widget, input: input, host: host}
	b.schedule = func(fn func()) uint {
		cb := glib.SourceOnceFunc(func(uintptr) { fn() })
		return glib.IdleAddOnce(&cb, 0)
	}
	b.status = func(drop *gdk.Drop, actions, preferred gdk.DragAction) {
		drop.Status(actions, preferred)
	}
	b.retainDrop = func(drop *gdk.Drop) { drop.Ref() }
	b.releaseDrop = func(drop *gdk.Drop) { drop.Unref() }
	b.startNative = b.beginNative
	b.cleanupNative = b.releaseNative
	b.newStringList = cef.NewStringList
	b.freeStringList = cef.FreeStringList
	b.stringListToSlice = cef.StringListToSlice
	b.newContentBytes = glib.NewBytes
	b.newContentProvider = gdk.NewContentProviderForBytes
	b.newContentUnion = func(providers []uintptr) *gdk.ContentProvider {
		if len(providers) == 0 {
			return nil
		}
		return gdk.NewContentProviderUnion(uintptr(unsafe.Pointer(&providers[0])), uint(len(providers)))
	}
	b.newTextureFromBytes = gdk.NewTextureFromBytes
	b.setDragIcon = gtk.DragIconSetFromPaintable
	b.unrefProvider = func(value *gdk.ContentProvider) { value.Unref() }
	b.unrefBytes = func(value *glib.Bytes) { value.Unref() }
	b.unrefTexture = func(value *gdk.Texture) { value.Unref() }
	b.unrefDrag = func(value *gdk.Drag) { value.Unref() }
	b.protocol = NewDragProtocol(b.sourceEndedAt, b.sourceSystemEnded, b.sourceDisarm)
	b.targetProtocol = NewTargetDragProtocol()
	b.externalReader = gtkdnd.NewAsyncReader(externalDropReadLimits())
	b.newInboundData = gtkdnd.NewInboundDragData
	b.newDropSource = newNativeDropSource
	return b
}

func (b *DragBridge) Attach() bool {
	if b == nil || b.widget == nil {
		return false
	}
	t := gtk.NewDropTargetAsync(nil, gdk.ActionCopyValue|gdk.ActionMoveValue|gdk.ActionLinkValue)
	if t == nil {
		return false
	}
	accept := func(_ gtk.DropTargetAsync, ptr uintptr) bool { return ptr != 0 }
	enter := func(_ gtk.DropTargetAsync, ptr uintptr, x, y float64) gdk.DragAction {
		return b.targetEnter(gdk.DropNewFromInternalPtr(ptr), x, y)
	}
	motion := func(_ gtk.DropTargetAsync, ptr uintptr, x, y float64) gdk.DragAction {
		return b.targetMotion(gdk.DropNewFromInternalPtr(ptr), x, y)
	}
	leave := func(_ gtk.DropTargetAsync, ptr uintptr) {
		if !b.targetProtocol.Leave(ptr) {
			return
		}
		b.clearActiveDrop(ptr)
		if h := b.currentHost(); h != nil {
			h.DragTargetDragLeave()
		}
	}
	drop := func(_ gtk.DropTargetAsync, ptr uintptr, x, y float64) bool {
		return b.targetDrop(gdk.DropNewFromInternalPtr(ptr), x, y)
	}
	b.targetHandlers = []uint{t.ConnectAccept(&accept), t.ConnectDragEnter(&enter), t.ConnectDragMotion(&motion), t.ConnectDragLeave(&leave), t.ConnectDrop(&drop)}
	b.target = t
	b.widget.AddController(&t.EventController)
	return true
}

func (b *DragBridge) SetHost(host cef.BrowserHost) {
	if b != nil {
		b.mu.Lock()
		b.host = host
		b.mu.Unlock()
	}
}
func (b *DragBridge) currentHost() cef.BrowserHost { b.mu.Lock(); defer b.mu.Unlock(); return b.host }

// SetFileDropHandler installs an optional policy for validated local paths.
func (b *DragBridge) SetFileDropHandler(allow func([]string) bool) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.fileDropAllowed = allow
	b.mu.Unlock()
}

// UpdateCursor receives CEF's selected operation on the CEF UI thread and
// posts the corresponding status update to GTK.
func (b *DragBridge) UpdateCursor(operation cef.DragOperationsMask) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.selectedAction, b.selectedKnown = operation, true
	b.statusRevision++
	if b.statusPending {
		b.mu.Unlock()
		return
	}
	b.statusPending = true
	b.statusTicket++
	ticket, generation, revision := b.statusTicket, b.dropGeneration, b.statusRevision
	b.mu.Unlock()

	b.scheduleStatus(ticket, generation, revision, true)
}

func (b *DragBridge) scheduleStatus(ticket, generation, revision uint64, retryConcurrent bool) {
	id := b.schedule(func() {
		b.mu.Lock()
		if !b.statusPending || b.statusTicket != ticket || b.dropGeneration != generation {
			b.mu.Unlock()
			return
		}
		b.statusPending = false
		drop, allowed := b.activeDrop, b.activeAllowed
		selected, known := b.selectedAction, b.selectedKnown
		if drop != nil && known {
			b.retainDrop(drop)
		}
		b.mu.Unlock()
		if drop == nil || !known {
			return
		}
		defer b.releaseDrop(drop)
		preferred := NegotiateDragActions(allowed, selected).Preferred
		b.status(drop, CEFToGDKDragActions(allowed), CEFToGDKDragActions(preferred))
	})
	if id != 0 {
		return
	}

	b.mu.Lock()
	if !b.statusPending || b.statusTicket != ticket {
		b.mu.Unlock()
		return
	}
	b.statusPending = false
	if !retryConcurrent || b.dropGeneration != generation || b.statusRevision == revision {
		b.mu.Unlock()
		return
	}
	b.statusPending = true
	b.statusTicket++
	nextTicket, nextGeneration, nextRevision := b.statusTicket, b.dropGeneration, b.statusRevision
	b.mu.Unlock()

	// Retry only a write that arrived while the refused attempt was in flight.
	// A second refusal remains idle until another UpdateCursor call, avoiding a
	// scheduler-failure busy loop.
	b.scheduleStatus(nextTicket, nextGeneration, nextRevision, false)
}

func (b *DragBridge) setActiveDrop(drop *gdk.Drop) {
	b.clearActiveDrop(0)
	if drop == nil {
		return
	}
	b.retainDrop(drop)
	b.mu.Lock()
	b.activeDrop = drop
	b.mu.Unlock()
}

func (b *DragBridge) clearActiveDrop(token uintptr) {
	b.mu.Lock()
	drop := b.activeDrop
	if drop != nil && token != 0 && drop.GoPointer() != token {
		b.mu.Unlock()
		return
	}
	b.activeDrop, b.activeAllowed = nil, 0
	b.dropGeneration++
	b.statusPending = false
	b.mu.Unlock()
	if drop != nil {
		b.releaseDrop(drop)
	}
}

func (b *DragBridge) preferredAction(n DragNegotiation) cef.DragOperationsMask {
	b.mu.Lock()
	b.activeAllowed = n.Allowed
	selected, known := b.selectedAction, b.selectedKnown
	b.mu.Unlock()
	if known {
		return NegotiateDragActions(n.Allowed, selected).Preferred
	}
	return n.Preferred
}

func (b *DragBridge) dragMouseEvent(drop *gdk.Drop, x, y float64) cef.MouseEvent {
	var mods uint
	if drop != nil {
		if device := drop.GetDevice(); device != nil {
			mods = uint(device.GetModifierState())
			device.Unref()
		}
	}
	if b.input != nil {
		return b.input.DragMouseEvent(x, y, mods)
	}
	return BuildMouseEvent(x, y, mods, 1)
}

func decodeImageHotspot(raw uintptr) (int32, int32, bool) {
	if unsafe.Sizeof(raw) != 8 {
		return 0, 0, false
	}
	return int32(uint32(raw)), int32(uint32(uint64(raw) >> 32)), true
}

func releaseCEFHandle(value any) {
	if releaser, ok := value.(interface{ Release() }); ok {
		releaser.Release()
	}
}

func (b *DragBridge) snapshotDragData(data cef.DragData) dragPayload {
	payload := dragPayload{OutboundPayload: gtkdnd.OutboundPayload{
		Text:      data.GetFragmentText(),
		HTML:      data.GetFragmentHtml(),
		LinkURL:   data.GetLinkURL(),
		LinkTitle: data.GetLinkTitle(),
	}}
	if payload.Text == "" {
		payload.Text = payload.LinkURL
	}
	if data.IsFile() {
		if list := b.newStringList(); list != 0 {
			data.GetFilePaths(list)
			payload.Files = append([]string(nil), b.stringListToSlice(list)...)
			b.freeStringList(list)
		}
	}
	image := data.GetImage()
	if image == nil {
		return payload
	}
	defer releaseCEFHandle(image)
	payload.Hotspot.X, payload.Hotspot.Y, payload.Hotspot.Valid = decodeImageHotspot(data.GetImageHotspot())
	var width, height int32
	png := image.GetAsPng(1, 1, &width, &height)
	if png == nil {
		return payload
	}
	defer releaseCEFHandle(png)
	if size := png.GetSize(); size > 0 {
		content := make([]byte, size)
		if copied := png.GetData(unsafe.Pointer(&content[0]), size, 0); copied > 0 {
			payload.ImagePNG = append([]byte(nil), content[:copied]...)
		}
	}
	return payload
}

func (b *DragBridge) Start(browser cef.Browser, data cef.DragData, offered cef.DragOperationsMask, x, y int32) int32 {
	if b == nil || browser == nil || data == nil {
		return 0
	}
	gen, ok := b.protocol.Begin()
	if !ok {
		return 0
	}
	// CEF drag data is only valid on the calling thread. Snapshot every value,
	// including image bytes, before posting any work to GTK.
	payload := b.snapshotDragData(data)
	b.mu.Lock()
	b.sourceHost = browser.GetHost()
	b.sourcePayload = payload
	b.selectedAction, b.selectedKnown = 0, false
	b.mu.Unlock()
	id := b.schedule(func() {
		if !b.protocol.IsStarting(gen) {
			return
		}
		resources, err := b.startNative(payload, offered, x, y)
		if err != nil || resources == nil || resources.Drag == nil {
			if resources != nil {
				b.cleanupNative(resources)
			}
			b.protocol.Cancel(gen)
			return
		}
		// Publish the GTK resources atomically with activation relative to
		// Detach's completion cleanup. Terminal signals are connected before the
		// lock is released, so an immediately delivered signal observes them.
		b.mu.Lock()
		if !b.protocol.Activate(gen) {
			b.mu.Unlock()
			b.cleanupNative(resources)
			return
		}
		b.sourceDrag, b.nativeResources = resources.Drag, resources
		cancel := func(gdk.Drag, gdk.DragCancelReason) { b.protocol.Cancel(gen) }
		finished := func(d gdk.Drag) {
			b.protocol.Finish(gen, SourceFinish{Operation: GDKToCEFDragActions(d.GetSelectedAction())})
		}
		b.sourceHandlers = []uint{resources.Drag.ConnectCancel(&cancel), resources.Drag.ConnectDndFinished(&finished)}
		b.mu.Unlock()
	})
	if id == 0 {
		b.protocol.RejectStart(gen)
		return 0
	}
	return 1
}

func (b *DragBridge) createNativeContent(payload dragPayload) (*nativeDragResources, error) {
	formats := gtkdnd.OutboundFormats(payload.OutboundPayload)
	if len(formats) == 0 {
		return nil, errors.New("missing drag content")
	}
	resources := &nativeDragResources{}
	children := make([]*gdk.ContentProvider, 0, len(formats))
	cleanupChildren := func() {
		for _, provider := range children {
			b.unrefProvider(provider)
		}
		for _, value := range resources.Bytes {
			b.unrefBytes(value)
		}
		if resources.Texture != nil {
			b.unrefTexture(resources.Texture)
		}
	}
	for _, format := range formats {
		value := b.newContentBytes(format.Value, uint(len(format.Value)))
		if value == nil {
			cleanupChildren()
			return nil, errors.New("content bytes")
		}
		resources.Bytes = append(resources.Bytes, value)
		provider := b.newContentProvider(format.MIME, value)
		if provider == nil {
			cleanupChildren()
			return nil, errors.New("content provider")
		}
		children = append(children, provider)
		if format.MIME == "image/png" {
			// An invalid image only suppresses the icon. The PNG provider remains
			// available and the drag can still start.
			texture, err := b.newTextureFromBytes(value)
			if err == nil {
				resources.Texture = texture
			} else if texture != nil {
				b.unrefTexture(texture)
			}
		}
	}
	if len(children) == 1 {
		resources.Provider = children[0]
		return resources, nil
	}
	pointers := make([]uintptr, len(children))
	for i, provider := range children {
		pointers[i] = provider.GoPointer()
	}
	// GIR marks providers transfer-full. From this call onward the union owns
	// every child, including when construction reports failure.
	resources.Provider = b.newContentUnion(pointers)
	children = nil
	if resources.Provider == nil {
		for _, value := range resources.Bytes {
			b.unrefBytes(value)
		}
		if resources.Texture != nil {
			b.unrefTexture(resources.Texture)
		}
		return nil, errors.New("content provider union")
	}
	return resources, nil
}

func (b *DragBridge) beginNative(payload dragPayload, offered cef.DragOperationsMask, x, y int32) (*nativeDragResources, error) {
	if b.widget == nil {
		return nil, errors.New("missing widget")
	}
	native := b.widget.GetNative()
	if native == nil {
		return nil, errors.New("missing native")
	}
	defer gobject.ObjectNewFromInternalPtr(native.GoPointer()).Unref()
	surface := native.GetSurface()
	if surface == nil {
		return nil, errors.New("missing surface")
	}
	defer surface.Unref()
	display := surface.GetDisplay()
	if display == nil {
		return nil, errors.New("missing display")
	}
	defer display.Unref()
	seat := display.GetDefaultSeat()
	if seat == nil {
		return nil, errors.New("missing seat")
	}
	defer seat.Unref()
	device := seat.GetPointer()
	if device == nil {
		return nil, errors.New("missing pointer device")
	}
	defer device.Unref()
	resources, err := b.createNativeContent(payload)
	if err != nil {
		return nil, err
	}
	if b.input != nil {
		b.input.ArmDnd()
	}
	scale := float64(b.widget.GetScaleFactor())
	if b.input != nil {
		scale = b.input.Scale()
	}
	resources.Drag = gdk.DragBegin(surface, device, resources.Provider, CEFToGDKDragActions(offered), DeviceToLogicalCoordinate(x, scale), DeviceToLogicalCoordinate(y, scale))
	if resources.Drag == nil {
		if b.input != nil {
			b.input.DisarmDnd()
		}
		b.releaseNative(resources)
		return nil, errors.New("drag begin")
	}
	b.applyNativeIcon(resources, payload.Hotspot)
	return resources, nil
}

func (b *DragBridge) applyNativeIcon(resources *nativeDragResources, hotspot imageHotspot) {
	if resources == nil || resources.Drag == nil || resources.Texture == nil || !hotspot.Valid {
		return
	}
	b.setDragIcon(resources.Drag, resources.Texture, int(hotspot.X), int(hotspot.Y))
}

func (b *DragBridge) targetEnter(drop *gdk.Drop, x, y float64) gdk.DragAction {
	if drop == nil || b.targetProtocol.Enter(drop.GoPointer()) == 0 {
		return gdk.ActionNoneValue
	}
	b.setActiveDrop(drop)
	formats := drop.GetFormats()
	var names []string
	if formats != nil {
		var n uint
		names = formats.GetMimeTypes(&n)
	}
	metadata := gtkdnd.MetadataFromFormats(names)
	data := gtkdnd.NewMetadataDragData(metadata)
	// For our own B-1 drag the in-memory text snapshot is authoritative and
	// available without beginning the B-2 asynchronous external reader.
	candidate := drop.GetDrag()
	ownDrop := candidate != nil
	if candidate != nil {
		candidate.Unref()
	}
	b.mu.Lock()
	own := ownDrop && b.sourceDrag != nil
	text, uri := b.sourcePayload.Text, b.sourcePayload.LinkURL
	b.mu.Unlock()
	if own && text != "" {
		b.targetProtocol.MarkContentReal(drop.GoPointer())
		data = cef.DragDataCreate()
		if uri != "" {
			data.SetLinkURL(uri)
		} else {
			data.SetFragmentText(text)
		}
	}
	offered := GDKToCEFDragActions(drop.GetActions())
	n := NegotiateDragActions(offered, cef.DragOperationsMaskDragOperationCopy|cef.DragOperationsMaskDragOperationMove|cef.DragOperationsMaskDragOperationLink)
	preferred := b.preferredAction(n)
	// GtkDropTargetAsync applies the returned action with its internal
	// gtk_drop_status() bookkeeping after this signal handler returns. Calling
	// GdkDrop.Status here would bypass that bookkeeping and can re-enter motion.
	if h := b.currentHost(); h != nil && data != nil {
		e := b.dragMouseEvent(drop, x, y)
		h.DragTargetDragEnter(data, &e, n.Allowed)
	}
	return CEFToGDKDragActions(preferred)
}
func (b *DragBridge) targetMotion(drop *gdk.Drop, x, y float64) gdk.DragAction {
	if drop == nil {
		return gdk.ActionNoneValue
	}
	e := b.dragMouseEvent(drop, x, y)
	decision := b.targetProtocol.Motion(drop.GoPointer(), e.X, e.Y, e.Modifiers)
	if decision == TargetMotionRejected {
		return gdk.ActionNoneValue
	}
	offered := GDKToCEFDragActions(drop.GetActions())
	n := NegotiateDragActions(offered, cef.DragOperationsMaskDragOperationCopy|cef.DragOperationsMaskDragOperationMove|cef.DragOperationsMaskDragOperationLink)
	preferred := b.preferredAction(n)
	if decision == TargetMotionDispatch {
		if h := b.currentHost(); h != nil {
			h.SendMouseMoveEvent(&e, 0)
			h.DragTargetDragOver(&e, n.Allowed)
		}
	}
	// Returning the preferred action lets GtkDropTargetAsync call its private
	// gtk_drop_status() exactly once. Status-induced duplicate motions are not
	// redispatched to CEF, which breaks the Wayland Copy/None feedback cycle.
	return CEFToGDKDragActions(preferred)
}
func (b *DragBridge) targetDrop(drop *gdk.Drop, x, y float64) bool {
	if drop == nil {
		return false
	}
	plan, ok := b.targetProtocol.BeginDrop(drop.GoPointer())
	if !ok {
		return false
	}
	b.clearActiveDrop(drop.GoPointer())
	drag := drop.GetDrag()
	traceDND("target-drop generation=%d own=%t actions=%d require_content_real=%t", plan.Generation, drag != nil, drop.GetActions(), plan.RequireContentReal)
	if drag == nil {
		formats := drop.GetFormats()
		var names []string
		if formats != nil {
			var count uint
			names = formats.GetMimeTypes(&count)
		}
		n := NegotiateDragActions(GDKToCEFDragActions(drop.GetActions()), cef.DragOperationsMaskDragOperationEvery)
		e := b.dragMouseEvent(drop, x, y)
		traceDND("external-drop advertised=%q offered=%d allowed=%d deterministic_preferred=%d selected_preferred=%d", names, n.Offered, n.Allowed, n.Preferred, b.preferredAction(n))
		b.retainDrop(drop)
		source := b.newDropSource(drop)
		if source == nil {
			b.targetProtocol.CompleteDrop(plan.Generation)
			drop.Finish(gdk.ActionNoneValue)
			b.releaseDrop(drop)
			return true
		}
		b.beginExternalDrop(plan, names, source, e, b.preferredAction(n), func(action gdk.DragAction) {
			drop.Finish(action)
			b.releaseDrop(drop)
		})
		return true
	}
	defer drag.Unref()
	action := drag.GetSelectedAction()
	if action == gdk.ActionNoneValue {
		n := NegotiateDragActions(GDKToCEFDragActions(drop.GetActions()), cef.DragOperationsMaskDragOperationEvery)
		action = CEFToGDKDragActions(n.Preferred)
	}
	e := b.dragMouseEvent(drop, x, y)
	if h := b.currentHost(); h != nil {
		h.DragTargetDrop(&e)
	}
	if gen, ok := b.protocol.CurrentGeneration(); ok {
		b.protocol.OwnDrop(gen, e.X, e.Y, GDKToCEFDragActions(action))
	}
	b.targetProtocol.CompleteDrop(plan.Generation)
	drop.Finish(action)
	return true
}

func (b *DragBridge) beginExternalDrop(plan TargetDropPlan, advertised []string, source releasableAsyncSource, event cef.MouseEvent, proposed cef.DragOperationsMask, finish func(gdk.DragAction)) {
	traceDND("external-read-start generation=%d proposed=%d advertised_count=%d", plan.Generation, proposed, len(advertised))
	if source == nil {
		finish(gdk.ActionNoneValue)
		return
	}
	b.externalReader.Read(advertised, source, func(result gtkdnd.ReadResult) {
		defer source.Release()
		finishAction := gdk.ActionNoneValue
		traceDND("external-read-done generation=%d mime=%q bytes=%d error=%v", result.Generation, result.MIME, len(result.Data), result.Err)
		if result.Err == nil {
			payload, err := gtkdnd.ParseInboundPayload(result.MIME, result.Data)
			traceDND("external-parse files=%d text=%t html=%t link=%t error=%v", len(payload.Files), payload.Text != "", payload.HTML != "", payload.LinkURL != "", err)
			if err == nil {
				b.mu.Lock()
				allow, makeData := b.fileDropAllowed, b.newInboundData
				b.mu.Unlock()
				accepted := gtkdnd.ApplyFileDropVeto(payload, proposed, allow)
				traceDND("external-policy proposed=%d accepted=%d", proposed, accepted)
				if accepted != cef.DragOperationsMaskDragOperationNone {
					dispatched := b.targetProtocol.DispatchDrop(plan.Generation, func(completed TargetDropPlan) {
						data := makeData(payload)
						if data == nil {
							return
						}
						if h := b.currentHost(); h != nil {
							if completed.RequireContentReal {
								h.DragTargetDragEnter(data, &event, accepted)
								h.DragTargetDragOver(&event, accepted)
							}
							h.DragTargetDrop(&event)
							finishAction = CEFToGDKDragActions(accepted)
						}
					})
					traceDND("external-dispatch generation=%d dispatched=%t finish_action=%d", plan.Generation, dispatched, finishAction)
				}
			}
		}
		// Errors, refusals, and stale callbacks can only close their own
		// generation and retained drop.
		b.targetProtocol.CompleteDrop(plan.Generation)
		traceDND("external-finish generation=%d action=%d", plan.Generation, finishAction)
		finish(finishAction)
	})
}

func (b *DragBridge) sourceEndedAt(e SourceEnd) {
	b.mu.Lock()
	h := b.sourceHost
	b.mu.Unlock()
	if h != nil {
		h.DragSourceEndedAt(e.X, e.Y, e.Operation)
	}
}
func (b *DragBridge) sourceSystemEnded() {
	b.mu.Lock()
	h := b.sourceHost
	b.mu.Unlock()
	if h != nil {
		h.DragSourceSystemDragEnded()
	}
	b.cleanupSource()
}
func (b *DragBridge) sourceDisarm() {
	if b.input != nil {
		b.input.DisarmDnd()
	}
}
func (b *DragBridge) releaseNative(resources *nativeDragResources) {
	if resources == nil {
		return
	}
	if resources.Drag != nil {
		b.unrefDrag(resources.Drag)
		resources.Drag = nil
	}
	// A union provider owns its children (GIR transfer-full), so only the root
	// provider is retained and released here.
	if resources.Provider != nil {
		b.unrefProvider(resources.Provider)
		resources.Provider = nil
	}
	if resources.Texture != nil {
		b.unrefTexture(resources.Texture)
		resources.Texture = nil
	}
	for _, value := range resources.Bytes {
		b.unrefBytes(value)
	}
	resources.Bytes = nil
}

func (b *DragBridge) cleanupSource() {
	b.mu.Lock()
	if b.sourceDrag != nil {
		for _, id := range b.sourceHandlers {
			if id != 0 {
				gobject.SignalHandlerDisconnect(&b.sourceDrag.Object, id)
			}
		}
	}
	resources := b.nativeResources
	b.sourceDrag = nil
	b.sourceHandlers = nil
	b.nativeResources = nil
	b.sourceHost = nil
	b.mu.Unlock()
	b.releaseNative(resources)
}
func (b *DragBridge) Detach() {
	if b == nil {
		return
	}
	b.protocol.Detach()
	b.targetProtocol.Detach()
	b.externalReader.Detach()
	b.clearActiveDrop(0)
	b.cleanupSource()
	if b.widget != nil && b.target != nil {
		for _, id := range b.targetHandlers {
			if id != 0 {
				gobject.SignalHandlerDisconnect(&b.target.Object, id)
			}
		}
		b.widget.RemoveController(&b.target.EventController)
	}
	b.target = nil
	b.targetHandlers = nil
}
