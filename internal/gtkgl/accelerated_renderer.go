package gtkgl

import (
	"errors"
	"fmt"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/cefadapter"
	"github.com/bnema/purego-cef2gtk/internal/dmabuf"
	"github.com/bnema/purego-cef2gtk/internal/egl"
	"github.com/bnema/purego-cef2gtk/internal/gl"
	"github.com/bnema/puregotk/v4/gtk"
)

var (
	ErrRendererNotInitialized = errors.New("accelerated renderer not initialized")
	ErrNilAcceleratedRenderer = errors.New("nil accelerated renderer")
)

type eglImporter interface {
	ImportDMABUF(dmabuf.BorrowedFrame) (egl.Image, error)
	Destroy(egl.Image) error
}

type glImporter interface {
	gl.Driver
	ImportEGLImageToTexture(uintptr) (gl.Texture, error)
}

type glCloser interface {
	Close() error
}

type eglCloser interface {
	Close() error
}

type textureCopier interface {
	CopyImportedToOwned(gl.Texture, dmabuf.Size, gl.Texture) (gl.Texture, error)
	DrawTextureToCurrentFramebuffer(gl.Texture, dmabuf.Size) error
	Close()
}

// QueuedFrame is the internally queued owned texture produced by ImportCopyAndQueue.
type QueuedFrame struct {
	Texture gl.Texture
	Size    dmabuf.Size
}

// AcceleratedRenderer owns the internal DMABUF import/copy lifecycle. All
// methods must be called from the GTK main thread because it owns GtkGLArea and
// current-context GL/EGL state.
type AcceleratedRenderer struct {
	area     *gtk.GLArea
	egl      eglImporter
	gl       glImporter
	copier   textureCopier
	queued   QueuedFrame
	initFunc func(*gtk.GLArea) (eglImporter, glImporter, textureCopier, error)
}

func NewAcceleratedRenderer(area *gtk.GLArea) *AcceleratedRenderer {
	return &AcceleratedRenderer{area: area}
}

// InitializeOnGTKThread initializes EGL/GL import and copy primitives with the
// GtkGLArea context current. It must be called on the GTK thread.
func (r *AcceleratedRenderer) InitializeOnGTKThread() error {
	if r == nil {
		return ErrNilAcceleratedRenderer
	}
	init := r.initFunc
	if init == nil {
		init = defaultAcceleratedRendererInit
	}
	eglImporter, glBackend, copier, err := init(r.area)
	if err != nil {
		return err
	}
	r.egl = eglImporter
	r.gl = glBackend
	r.copier = copier
	return nil
}

func defaultAcceleratedRendererInit(area *gtk.GLArea) (eglImporter, glImporter, textureCopier, error) {
	probe, err := ProbeCurrentGLAreaContext(area)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := probe.Validate(); err != nil {
		return nil, nil, nil, err
	}
	eglImporter, err := egl.NewImporterFromCurrentDisplay()
	if err != nil {
		return nil, nil, nil, err
	}
	glBackend, err := gl.NewBackendFromCurrentContext()
	if err != nil {
		if closer, ok := any(eglImporter).(eglCloser); ok {
			_ = closer.Close()
		}
		return nil, nil, nil, err
	}
	copier, err := gl.NewTexturedQuadCopierForAPI(glBackend, probe.GLAPI)
	if err != nil {
		if closer, ok := any(eglImporter).(eglCloser); ok {
			_ = closer.Close()
		}
		_ = glBackend.Close()
		return nil, nil, nil, err
	}
	return eglImporter, glBackend, copier, nil
}

// ImportCopyAndQueueOnGTKThread imports and copies callback-scoped CEF DMABUF
// metadata on the GTK main context before returning. OnAcceleratedPaint callers
// must not return to CEF until this method completes because CEF owns the DMABUF
// resources only for the duration of the callback.
func (r *AcceleratedRenderer) ImportCopyAndQueueOnGTKThread(info *cef.AcceleratedPaintInfo) (queued QueuedFrame, retErr error) {
	RunOnGTKThreadSync(func() {
		if r == nil {
			retErr = ErrNilAcceleratedRenderer
			return
		}
		if r.area != nil {
			r.area.MakeCurrent()
			if gerr := r.area.GetError(); gerr != nil {
				retErr = fmt.Errorf("gtk gl area error: %s", glibErrorMessage(gerr))
				return
			}
		}
		queued, retErr = r.ImportCopyAndQueue(info)
	})
	return queued, retErr
}

// ImportCopyAndQueue imports a CEF accelerated-paint DMABUF, binds it to a
// temporary GL texture, copies it to an owned texture, queues the owned texture,
// and destroys callback-scoped resources before returning. Call on the GTK main
// thread with the GtkGLArea context current.
func (r *AcceleratedRenderer) ImportCopyAndQueue(info *cef.AcceleratedPaintInfo) (queued QueuedFrame, retErr error) {
	if r == nil {
		return QueuedFrame{}, ErrNilAcceleratedRenderer
	}
	if r.egl == nil || r.gl == nil || r.copier == nil {
		return QueuedFrame{}, ErrRendererNotInitialized
	}
	frame, err := cefadapter.BorrowedFrameFromAcceleratedPaint(info)
	if err != nil {
		return QueuedFrame{}, err
	}

	image, err := r.egl.ImportDMABUF(frame)
	if err != nil {
		return QueuedFrame{}, err
	}
	destroyed := false
	defer func() {
		if !destroyed {
			if err := r.egl.Destroy(image); err != nil && retErr == nil {
				retErr = fmt.Errorf("cleanup imported frame: %w", err)
			}
		}
	}()

	imported, err := r.gl.ImportEGLImageToTexture(uintptr(image))
	if err != nil {
		return QueuedFrame{}, err
	}
	defer func() {
		id := uint32(imported)
		r.gl.DeleteTextures(1, &id)
	}()

	owned, err := r.copier.CopyImportedToOwned(imported, frame.CodedSize, 0)
	if err != nil {
		return QueuedFrame{}, err
	}
	if err := r.egl.Destroy(image); err != nil {
		destroyed = true
		ownedID := uint32(owned)
		r.gl.DeleteTextures(1, &ownedID)
		return QueuedFrame{}, fmt.Errorf("cleanup imported frame: %w", err)
	}
	destroyed = true
	if r.queued.Texture != 0 {
		old := uint32(r.queued.Texture)
		r.gl.DeleteTextures(1, &old)
	}
	r.queued = QueuedFrame{Texture: owned, Size: frame.CodedSize}
	return r.queued, nil
}

func (r *AcceleratedRenderer) QueuedFrame() (QueuedFrame, bool) {
	if r == nil || r.queued.Texture == 0 {
		return QueuedFrame{}, false
	}
	return r.queued, true
}

// QueueRender schedules the GtkGLArea render signal on the GTK main context.
func (r *AcceleratedRenderer) QueueRender() {
	if r == nil || r.area == nil {
		return
	}
	QueueRenderOnGTKThread(r.area)
}

// RenderQueuedOnGTKThread draws the current owned texture into the GtkGLArea framebuffer.
func (r *AcceleratedRenderer) RenderQueuedOnGTKThread() error {
	if r == nil {
		return ErrNilAcceleratedRenderer
	}
	if r.copier == nil {
		return ErrRendererNotInitialized
	}
	queued, ok := r.QueuedFrame()
	if !ok {
		return nil
	}
	return r.copier.DrawTextureToCurrentFramebuffer(queued.Texture, queued.Size)
}

func (r *AcceleratedRenderer) Close() {
	if r == nil {
		return
	}
	if r.queued.Texture != 0 && r.gl != nil {
		id := uint32(r.queued.Texture)
		r.gl.DeleteTextures(1, &id)
		r.queued = QueuedFrame{}
	}
	if r.copier != nil {
		r.copier.Close()
		r.copier = nil
	}
	if r.gl != nil {
		if closer, ok := r.gl.(glCloser); ok {
			_ = closer.Close()
		}
		r.gl = nil
	}
	if r.egl != nil {
		if closer, ok := r.egl.(eglCloser); ok {
			_ = closer.Close()
		}
		r.egl = nil
	}
}
