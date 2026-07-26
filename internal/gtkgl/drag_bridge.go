package gtkgl

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkdnd"
	"github.com/bnema/puregotk/v4/gdk"
	"github.com/bnema/puregotk/v4/glib"
	"github.com/bnema/puregotk/v4/gobject"
	"github.com/bnema/puregotk/v4/gtk"
)

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
	newWriteHandler     func(cef.WriteHandler) cef.WriteHandler
	newStreamWriter     func(cef.WriteHandler) cef.StreamWriter
	setDragIcon         func(*gdk.Drag, gdk.Paintable, int, int)
	unrefProvider       func(*gdk.ContentProvider)
	unrefBytes          func(*glib.Bytes)
	unrefTexture        func(*gdk.Texture)
	unrefDrag           func(*gdk.Drag)
	sourceHost          cef.BrowserHost
	sourceText          string
	sourceLinkURL       string
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
	newMetadataData     func(gtkdnd.DragMetadata) cef.DragData
	newDragData         func() cef.DragData
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
	b.newWriteHandler = cef.NewWriteHandler
	b.newStreamWriter = cef.StreamWriterCreateForHandler
	b.setDragIcon = gtk.DragIconSetFromPaintable
	b.unrefProvider = func(value *gdk.ContentProvider) { value.Unref() }
	b.unrefBytes = func(value *glib.Bytes) { value.Unref() }
	b.unrefTexture = func(value *gdk.Texture) { value.Unref() }
	b.unrefDrag = func(value *gdk.Drag) { value.Unref() }
	b.protocol = NewDragProtocol(b.sourceEndedAt, b.sourceSystemEnded, b.sourceDisarm)
	b.targetProtocol = NewTargetDragProtocol()
	b.externalReader = gtkdnd.NewAsyncReader(externalDropReadLimits())
	b.newMetadataData = gtkdnd.NewMetadataDragData
	b.newDragData = cef.DragDataCreate
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
