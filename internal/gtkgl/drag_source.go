package gtkgl

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkdnd"
	"github.com/bnema/puregotk/v4/gdk"
	"github.com/bnema/puregotk/v4/glib"
	"github.com/bnema/puregotk/v4/gobject"
)

type imageHotspot struct {
	X, Y  int32
	Valid bool
}

type dragPayload struct {
	gtkdnd.OutboundPayload
	IconPNG []byte
	Hotspot imageHotspot
}

type nativeDragResources struct {
	Drag     *gdk.Drag
	Provider *gdk.ContentProvider
	Bytes    []*glib.Bytes
	Texture  *gdk.Texture
}

type nativeDragStart func(dragPayload, cef.DragOperationsMask, int32, int32) (*nativeDragResources, error)

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

const maxOutboundFileBytes = 32 << 20

var (
	errDragFileWrite    = errors.New("invalid drag file write")
	errDragFileTooLarge = errors.New("drag file exceeds capture limit")
)

type boundedDragFileWriter struct {
	data  []byte
	pos   int
	limit int
	err   error
}

func newBoundedDragFileWriter(limit int) *boundedDragFileWriter {
	return &boundedDragFileWriter{limit: limit}
}

func (w *boundedDragFileWriter) Write(ptr unsafe.Pointer, size, n int) int {
	if w == nil || w.err != nil {
		return 0
	}
	if ptr == nil || size <= 0 || n < 0 || (n != 0 && size > int(^uint(0)>>1)/n) {
		w.err = errDragFileWrite
		return 0
	}
	total := size * n
	if total > w.limit-w.pos {
		w.err = errDragFileTooLarge
		return 0
	}
	end := w.pos + total
	if end > len(w.data) {
		w.data = append(w.data, make([]byte, end-len(w.data))...)
	}
	copy(w.data[w.pos:end], unsafe.Slice((*byte)(ptr), total))
	w.pos = end
	return n
}

func (w *boundedDragFileWriter) SeekOffset(offset int64, whence int32) int32 {
	if w == nil || w.err != nil {
		return -1
	}
	var base int64
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = int64(w.pos)
	case io.SeekEnd:
		base = int64(len(w.data))
	default:
		w.err = errDragFileWrite
		return -1
	}
	next := base + offset
	if next < 0 || next > int64(w.limit) {
		w.err = errDragFileWrite
		return -1
	}
	w.pos = int(next)
	return 0
}

func (w *boundedDragFileWriter) Tell() int64   { return int64(w.pos) }
func (*boundedDragFileWriter) Flush() int32    { return 0 }
func (*boundedDragFileWriter) MayBlock() int32 { return 0 }

func draggedImageMIME(name string, content []byte) string {
	if name == "" || name == "." || name == ".." || strings.IndexByte(name, 0) >= 0 || strings.ContainsAny(name, "/\\") || filepath.Base(name) != name {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png":
		if len(content) >= 8 && bytes.Equal(content[:8], []byte("\x89PNG\r\n\x1a\n")) {
			return "image/png"
		}
	case ".jpg", ".jpeg":
		if len(content) >= 3 && bytes.Equal(content[:3], []byte{0xff, 0xd8, 0xff}) {
			return "image/jpeg"
		}
	case ".svg":
		decoder := xml.NewDecoder(bytes.NewReader(content))
		for {
			token, err := decoder.Token()
			if err != nil {
				return ""
			}
			switch value := token.(type) {
			case xml.Directive:
				return ""
			case xml.StartElement:
				if strings.EqualFold(value.Name.Local, "svg") {
					return "image/svg+xml"
				}
				return ""
			}
		}
	}
	return ""
}

func (b *DragBridge) snapshotFileContent(data cef.DragData) (string, []byte) {
	if b.newWriteHandler == nil || b.newStreamWriter == nil {
		return "", nil
	}
	handler := newBoundedDragFileWriter(maxOutboundFileBytes)
	callback := b.newWriteHandler(handler)
	if callback == nil {
		return "", nil
	}
	defer releaseCEFHandle(callback)
	writer := b.newStreamWriter(callback)
	if writer == nil {
		return "", nil
	}
	defer releaseCEFHandle(writer)
	written := data.GetFileContents(writer)
	if handler.err != nil || written <= 0 || written != len(handler.data) {
		return "", nil
	}
	mime := draggedImageMIME(data.GetFileName(), handler.data)
	if mime == "" {
		return "", nil
	}
	return mime, append([]byte(nil), handler.data...)
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
		if b.newStringList != nil {
			if list := b.newStringList(); list != 0 {
				data.GetFilePaths(list)
				payload.Files = append([]string(nil), b.stringListToSlice(list)...)
				b.freeStringList(list)
			}
		}
		payload.ImageMIME, payload.ImageBytes = b.snapshotFileContent(data)
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
	if size := png.GetSize(); size > 0 && size <= maxOutboundFileBytes {
		content := make([]byte, size)
		if copied := png.GetData(unsafe.Pointer(&content[0]), size, 0); copied == size {
			payload.IconPNG = content
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
	b.sourceText, b.sourceLinkURL = payload.Text, payload.LinkURL
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
		b.cleanupSource()
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
	}
	if len(payload.IconPNG) != 0 {
		iconBytes := b.newContentBytes(payload.IconPNG, uint(len(payload.IconPNG)))
		if iconBytes != nil {
			resources.Bytes = append(resources.Bytes, iconBytes)
			texture, err := b.newTextureFromBytes(iconBytes)
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
	b.sourceText, b.sourceLinkURL = "", ""
	b.mu.Unlock()
	b.releaseNative(resources)
}
