package gtkgl

import (
	"errors"
	"reflect"
	"testing"
	"unsafe"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/dmabuf"
	"github.com/bnema/purego-cef2gtk/internal/egl"
	"github.com/bnema/purego-cef2gtk/internal/gl"
)

// These hand-written fakes intentionally verify callback-scoped resource
// cleanup ordering across EGL import, GL import, copier, and destroy calls.
// Generated mocks would obscure the ordered lifecycle this test protects.
type fakeEGLImporter struct {
	calls      *[]string
	image      egl.Image
	importErr  error
	destroyErr error
}

func (f fakeEGLImporter) ImportDMABUF(frame dmabuf.BorrowedFrame) (egl.Image, error) {
	*f.calls = append(*f.calls, "egl.ImportDMABUF")
	if f.importErr != nil {
		return 0, f.importErr
	}
	return f.image, nil
}
func (f fakeEGLImporter) Destroy(image egl.Image) error {
	*f.calls = append(*f.calls, "egl.Destroy")
	return f.destroyErr
}

type fakeGLImporter struct {
	calls     *[]string
	texture   gl.Texture
	importErr error
}

func (f fakeGLImporter) ImportEGLImageToTexture(uintptr) (gl.Texture, error) {
	*f.calls = append(*f.calls, "gl.ImportEGLImageToTexture")
	if f.importErr != nil {
		return 0, f.importErr
	}
	return f.texture, nil
}
func (f fakeGLImporter) GenTextures(int32, *uint32) {}
func (f fakeGLImporter) DeleteTextures(n int32, textures *uint32) {
	*f.calls = append(*f.calls, "gl.DeleteTextures")
}
func (f fakeGLImporter) BindTexture(uint32, uint32) {}
func (f fakeGLImporter) TexImage2D(uint32, int32, int32, int32, int32, int32, uint32, uint32, unsafe.Pointer) {
}
func (f fakeGLImporter) TexParameteri(uint32, uint32, int32)                             {}
func (f fakeGLImporter) ActiveTexture(uint32)                                            {}
func (f fakeGLImporter) GenBuffers(int32, *uint32)                                       {}
func (f fakeGLImporter) DeleteBuffers(int32, *uint32)                                    {}
func (f fakeGLImporter) BindBuffer(uint32, uint32)                                       {}
func (f fakeGLImporter) BufferData(uint32, int64, unsafe.Pointer, uint32)                {}
func (f fakeGLImporter) CreateShader(uint32) uint32                                      { return 0 }
func (f fakeGLImporter) ShaderSource(uint32, int32, **byte, *int32)                      {}
func (f fakeGLImporter) CompileShader(uint32)                                            {}
func (f fakeGLImporter) GetShaderiv(uint32, uint32, *int32)                              {}
func (f fakeGLImporter) GetShaderInfoLog(uint32, int32, *int32, *byte)                   {}
func (f fakeGLImporter) DeleteShader(uint32)                                             {}
func (f fakeGLImporter) CreateProgram() uint32                                           { return 0 }
func (f fakeGLImporter) AttachShader(uint32, uint32)                                     {}
func (f fakeGLImporter) LinkProgram(uint32)                                              {}
func (f fakeGLImporter) GetProgramiv(uint32, uint32, *int32)                             {}
func (f fakeGLImporter) GetProgramInfoLog(uint32, int32, *int32, *byte)                  {}
func (f fakeGLImporter) UseProgram(uint32)                                               {}
func (f fakeGLImporter) DeleteProgram(uint32)                                            {}
func (f fakeGLImporter) GetUniformLocation(uint32, *byte) int32                          { return 0 }
func (f fakeGLImporter) GetAttribLocation(uint32, *byte) int32                           { return 0 }
func (f fakeGLImporter) Uniform1i(int32, int32)                                          {}
func (f fakeGLImporter) GenVertexArrays(int32, *uint32)                                  {}
func (f fakeGLImporter) BindVertexArray(uint32)                                          {}
func (f fakeGLImporter) DeleteVertexArrays(int32, *uint32)                               {}
func (f fakeGLImporter) VertexAttribPointer(uint32, int32, uint32, bool, int32, uintptr) {}
func (f fakeGLImporter) EnableVertexAttribArray(uint32)                                  {}
func (f fakeGLImporter) DisableVertexAttribArray(uint32)                                 {}
func (f fakeGLImporter) GenFramebuffers(int32, *uint32)                                  {}
func (f fakeGLImporter) DeleteFramebuffers(int32, *uint32)                               {}
func (f fakeGLImporter) BindFramebuffer(uint32, uint32)                                  {}
func (f fakeGLImporter) FramebufferTexture2D(uint32, uint32, uint32, uint32, int32)      {}
func (f fakeGLImporter) CheckFramebufferStatus(uint32) uint32                            { return gl.FramebufferComplete }
func (f fakeGLImporter) Viewport(int32, int32, int32, int32)                             {}
func (f fakeGLImporter) DrawArrays(uint32, int32, int32)                                 {}
func (f fakeGLImporter) GetError() uint32                                                { return gl.NoError }

type fakeCopier struct {
	calls   *[]string
	texture gl.Texture
	err     error
}

func (f fakeCopier) CopyImportedToOwned(src gl.Texture, size dmabuf.Size, dst gl.Texture) (gl.Texture, error) {
	*f.calls = append(*f.calls, "copier.CopyImportedToOwned")
	if f.err != nil {
		return 0, f.err
	}
	return f.texture, nil
}
func (f fakeCopier) DrawTextureToCurrentFramebuffer(src gl.Texture, size dmabuf.Size) error {
	*f.calls = append(*f.calls, "copier.DrawTextureToCurrentFramebuffer")
	return f.err
}
func (f fakeCopier) Close() { *f.calls = append(*f.calls, "copier.Close") }

func validPaintInfoForRenderer() cef.AcceleratedPaintInfo {
	info := cef.NewAcceleratedPaintInfo()
	info.PlaneCount = 1
	info.Format = int32(cef.ColorTypeBgra8888)
	info.Planes[0].Fd = 9
	info.Planes[0].Stride = 128
	info.Extra.CodedSize.Width = 16
	info.Extra.CodedSize.Height = 8
	return info
}

func TestAcceleratedRendererImportCopyAndQueueLifecycle(t *testing.T) {
	calls := []string{}
	r := &AcceleratedRenderer{
		egl:    fakeEGLImporter{calls: &calls, image: 11},
		gl:     fakeGLImporter{calls: &calls, texture: 22},
		copier: fakeCopier{calls: &calls, texture: 33},
	}
	info := validPaintInfoForRenderer()
	queued, err := r.ImportCopyAndQueue(&info)
	if err != nil {
		t.Fatalf("ImportCopyAndQueue: %v", err)
	}
	// Success explicitly destroys egl.Destroy before gl.DeleteTextures; error paths
	// below rely on deferred cleanup order. Both orders are intentional lifecycle checks.
	wantCalls := []string{"egl.ImportDMABUF", "gl.ImportEGLImageToTexture", "copier.CopyImportedToOwned", "egl.Destroy", "gl.DeleteTextures"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	if queued.Texture != 33 || queued.Size != (dmabuf.Size{Width: 16, Height: 8}) {
		t.Fatalf("queued = %#v", queued)
	}
}

func TestAcceleratedRendererDestroysImageOnCopyError(t *testing.T) {
	calls := []string{}
	copyErr := errors.New("copy failed")
	r := &AcceleratedRenderer{
		egl:    fakeEGLImporter{calls: &calls, image: 11},
		gl:     fakeGLImporter{calls: &calls, texture: 22},
		copier: fakeCopier{calls: &calls, err: copyErr},
	}
	info := validPaintInfoForRenderer()
	if _, err := r.ImportCopyAndQueue(&info); !errors.Is(err, copyErr) {
		t.Fatalf("err = %v, want copyErr", err)
	}
	wantCalls := []string{"egl.ImportDMABUF", "gl.ImportEGLImageToTexture", "copier.CopyImportedToOwned", "gl.DeleteTextures", "egl.Destroy"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
}

func TestAcceleratedRendererPropagatesDestroyErrorBeforeQueue(t *testing.T) {
	calls := []string{}
	destroyErr := errors.New("destroy failed")
	r := &AcceleratedRenderer{
		egl:    fakeEGLImporter{calls: &calls, image: 11, destroyErr: destroyErr},
		gl:     fakeGLImporter{calls: &calls, texture: 22},
		copier: fakeCopier{calls: &calls, texture: 33},
	}
	info := validPaintInfoForRenderer()
	if _, err := r.ImportCopyAndQueue(&info); !errors.Is(err, destroyErr) {
		t.Fatalf("err = %v, want destroyErr", err)
	}
	if _, ok := r.QueuedFrame(); ok {
		t.Fatal("queued frame despite destroy failure")
	}
}
