package gtkgl

import (
	"errors"
	"sync"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkdnd"
	"github.com/bnema/puregotk/v4/gdk"
	"github.com/bnema/puregotk/v4/glib"
	"github.com/bnema/puregotk/v4/gobject"
	"github.com/bnema/puregotk/v4/gtk"
)

type dragPayload struct {
	Text string
	URI  string
}
type nativeDragStart func(dragPayload, cef.DragOperationsMask, int32, int32) (*gdk.Drag, []*gdk.ContentProvider, []*glib.Bytes, error)
type dragScheduler func(func()) uint
type dragStatus func(*gdk.Drop, gdk.DragAction, gdk.DragAction)

type DragBridge struct {
	mu             sync.Mutex
	widget         *gtk.Widget
	input          *InputBridge
	host           cef.BrowserHost
	protocol       *DragProtocol
	targetProtocol *TargetDragProtocol
	target         *gtk.DropTargetAsync
	targetHandlers []uint
	schedule       dragScheduler
	status         dragStatus
	retainDrop     func(*gdk.Drop)
	releaseDrop    func(*gdk.Drop)
	startNative    nativeDragStart
	cleanupNative  func(*gdk.Drag, []*gdk.ContentProvider, []*glib.Bytes)
	sourceHost     cef.BrowserHost
	sourcePayload  dragPayload
	sourceDrag     *gdk.Drag
	sourceHandlers []uint
	providers      []*gdk.ContentProvider
	bytes          []*glib.Bytes
	selectedAction cef.DragOperationsMask
	selectedKnown  bool
	activeDrop     *gdk.Drop
	activeAllowed  cef.DragOperationsMask
	statusPending  bool
	statusTicket   uint64
	statusRevision uint64
	dropGeneration uint64
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
	b.protocol = NewDragProtocol(b.sourceEndedAt, b.sourceSystemEnded, b.sourceDisarm)
	b.targetProtocol = NewTargetDragProtocol()
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

func (b *DragBridge) Start(browser cef.Browser, data cef.DragData, offered cef.DragOperationsMask, x, y int32) int32 {
	if b == nil || browser == nil || data == nil {
		return 0
	}
	gen, ok := b.protocol.Begin()
	if !ok {
		return 0
	}
	payload := dragPayload{Text: data.GetFragmentText(), URI: data.GetLinkURL()}
	if payload.Text == "" {
		payload.Text = payload.URI
	}
	b.mu.Lock()
	b.sourceHost = browser.GetHost()
	b.sourcePayload = payload
	b.selectedAction, b.selectedKnown = 0, false
	b.mu.Unlock()
	id := b.schedule(func() {
		if !b.protocol.IsStarting(gen) {
			return
		}
		drag, providers, bytes, err := b.startNative(payload, offered, x, y)
		if err != nil || drag == nil {
			b.protocol.Cancel(gen)
			return
		}
		if !b.protocol.Activate(gen) {
			// The transition that made this generation stale already closed and
			// disarmed the protocol; only the newly-created GTK objects remain.
			b.cleanupNative(drag, providers, bytes)
			return
		}
		b.mu.Lock()
		b.sourceDrag, b.providers, b.bytes = drag, providers, bytes
		cancel := func(gdk.Drag, gdk.DragCancelReason) { b.protocol.Cancel(gen) }
		finished := func(d gdk.Drag) {
			b.protocol.Finish(gen, SourceFinish{Operation: GDKToCEFDragActions(d.GetSelectedAction())})
		}
		b.sourceHandlers = []uint{drag.ConnectCancel(&cancel), drag.ConnectDndFinished(&finished)}
		b.mu.Unlock()
	})
	if id == 0 {
		b.protocol.RejectStart(gen)
		return 0
	}
	return 1
}

func (b *DragBridge) beginNative(payload dragPayload, offered cef.DragOperationsMask, x, y int32) (*gdk.Drag, []*gdk.ContentProvider, []*glib.Bytes, error) {
	if b.widget == nil {
		return nil, nil, nil, errors.New("missing widget")
	}
	native := b.widget.GetNative()
	if native == nil {
		return nil, nil, nil, errors.New("missing native")
	}
	defer gobject.ObjectNewFromInternalPtr(native.GoPointer()).Unref()
	surface := native.GetSurface()
	if surface == nil {
		return nil, nil, nil, errors.New("missing surface")
	}
	defer surface.Unref()
	display := surface.GetDisplay()
	if display == nil {
		return nil, nil, nil, errors.New("missing display")
	}
	defer display.Unref()
	seat := display.GetDefaultSeat()
	if seat == nil {
		return nil, nil, nil, errors.New("missing seat")
	}
	defer seat.Unref()
	device := seat.GetPointer()
	if device == nil {
		return nil, nil, nil, errors.New("missing pointer device")
	}
	defer device.Unref()
	mime, value := "text/plain;charset=utf-8", payload.Text
	if payload.URI != "" {
		mime, value = "text/uri-list", payload.URI+"\r\n"
	}
	content := []byte(value)
	bytes := glib.NewBytes(content, uint(len(content)))
	if bytes == nil {
		return nil, nil, nil, errors.New("content bytes")
	}
	provider := gdk.NewContentProviderForBytes(mime, bytes)
	if provider == nil {
		bytes.Unref()
		return nil, nil, nil, errors.New("content provider")
	}
	if b.input != nil {
		b.input.ArmDnd()
	}
	scale := float64(b.widget.GetScaleFactor())
	if b.input != nil {
		scale = b.input.Scale()
	}
	drag := gdk.DragBegin(surface, device, provider, CEFToGDKDragActions(offered), DeviceToLogicalCoordinate(x, scale), DeviceToLogicalCoordinate(y, scale))
	if drag == nil {
		if b.input != nil {
			b.input.DisarmDnd()
		}
		provider.Unref()
		bytes.Unref()
		return nil, nil, nil, errors.New("drag begin")
	}
	return drag, []*gdk.ContentProvider{provider}, []*glib.Bytes{bytes}, nil
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
	text, uri := b.sourcePayload.Text, b.sourcePayload.URI
	b.mu.Unlock()
	if own && text != "" {
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
	if drop == nil || !b.targetProtocol.Drop(drop.GoPointer()) {
		return false
	}
	b.clearActiveDrop(drop.GoPointer())
	drag := drop.GetDrag()
	if drag == nil {
		return false
	} // external payload reading starts in B-2
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
	drop.Finish(action)
	return true
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
func (b *DragBridge) releaseNative(drag *gdk.Drag, providers []*gdk.ContentProvider, values []*glib.Bytes) {
	if drag != nil {
		drag.Unref()
	}
	for _, provider := range providers {
		provider.Unref()
	}
	for _, value := range values {
		value.Unref()
	}
}

func (b *DragBridge) cleanupSource() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sourceDrag != nil {
		for _, id := range b.sourceHandlers {
			if id != 0 {
				gobject.SignalHandlerDisconnect(&b.sourceDrag.Object, id)
			}
		}
		b.sourceDrag.Unref()
	}
	for _, p := range b.providers {
		p.Unref()
	}
	for _, v := range b.bytes {
		v.Unref()
	}
	b.sourceDrag = nil
	b.sourceHandlers = nil
	b.providers = nil
	b.bytes = nil
	b.sourceHost = nil
}
func (b *DragBridge) Detach() {
	if b == nil {
		return
	}
	b.protocol.Detach()
	b.targetProtocol.Detach()
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
