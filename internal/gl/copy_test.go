package gl

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/bnema/purego-cef2gtk/internal/dmabuf"
)

type fakeDriver struct {
	calls          []string
	nextTexture    uint32
	nextBuffer     uint32
	nextVAO        uint32
	nextFBO        uint32
	fbStatus       uint32
	glErr          uint32
	shaderSources  []string
	lastBufferData []float32
}

func newFakeDriver() *fakeDriver {
	return &fakeDriver{nextTexture: 10, nextBuffer: 20, nextVAO: 30, nextFBO: 40, fbStatus: FramebufferComplete}
}
func (f *fakeDriver) call(s string) { f.calls = append(f.calls, s) }
func (f *fakeDriver) GenTextures(n int32, textures *uint32) {
	f.call("GenTextures")
	*textures = f.nextTexture
	f.nextTexture++
}
func (f *fakeDriver) DeleteTextures(n int32, textures *uint32)  { f.call("DeleteTextures") }
func (f *fakeDriver) BindTexture(target uint32, texture uint32) { f.call("BindTexture") }
func (f *fakeDriver) TexImage2D(target uint32, level, internalformat, width, height, border int32, format, xtype uint32, pixels unsafe.Pointer) {
	f.call("TexImage2D")
}
func (f *fakeDriver) TexParameteri(target uint32, pname uint32, param int32) { f.call("TexParameteri") }
func (f *fakeDriver) ActiveTexture(texture uint32)                           { f.call("ActiveTexture") }
func (f *fakeDriver) GenBuffers(n int32, buffers *uint32) {
	f.call("GenBuffers")
	*buffers = f.nextBuffer
	f.nextBuffer++
}
func (f *fakeDriver) DeleteBuffers(n int32, buffers *uint32)  { f.call("DeleteBuffers") }
func (f *fakeDriver) BindBuffer(target uint32, buffer uint32) { f.call("BindBuffer") }
func (f *fakeDriver) BufferData(target uint32, size int64, data unsafe.Pointer, usage uint32) {
	f.call("BufferData")
	if data != nil && size%4 == 0 {
		values := unsafe.Slice((*float32)(data), int(size/4))
		f.lastBufferData = append(f.lastBufferData[:0], values...)
	}
}
func (f *fakeDriver) CreateShader(shaderType uint32) uint32 {
	f.call("CreateShader")
	return shaderType
}
func (f *fakeDriver) ShaderSource(shader uint32, count int32, source **byte, length *int32) {
	f.call("ShaderSource")
	if source != nil && *source != nil && length != nil && *length > 0 {
		f.shaderSources = append(f.shaderSources, string(unsafe.Slice(*source, int(*length))))
	}
}
func (f *fakeDriver) CompileShader(shader uint32) { f.call("CompileShader") }
func (f *fakeDriver) GetShaderiv(shader uint32, pname uint32, params *int32) {
	f.call("GetShaderiv")
	*params = 1
}
func (f *fakeDriver) GetShaderInfoLog(shader uint32, bufSize int32, length *int32, infoLog *byte) {
	f.call("GetShaderInfoLog")
}
func (f *fakeDriver) DeleteShader(shader uint32)                 { f.call("DeleteShader") }
func (f *fakeDriver) CreateProgram() uint32                      { f.call("CreateProgram"); return 100 }
func (f *fakeDriver) AttachShader(program uint32, shader uint32) { f.call("AttachShader") }
func (f *fakeDriver) LinkProgram(program uint32)                 { f.call("LinkProgram") }
func (f *fakeDriver) GetProgramiv(program uint32, pname uint32, params *int32) {
	f.call("GetProgramiv")
	*params = 1
}
func (f *fakeDriver) GetProgramInfoLog(program uint32, bufSize int32, length *int32, infoLog *byte) {
	f.call("GetProgramInfoLog")
}
func (f *fakeDriver) UseProgram(program uint32)    { f.call("UseProgram") }
func (f *fakeDriver) DeleteProgram(program uint32) { f.call("DeleteProgram") }
func (f *fakeDriver) GetUniformLocation(program uint32, name *byte) int32 {
	f.call("GetUniformLocation")
	return 0
}
func (f *fakeDriver) GetAttribLocation(program uint32, name *byte) int32 {
	f.call("GetAttribLocation")
	switch cString(name) {
	case "a_pos":
		return 0
	case "a_uv":
		return 1
	default:
		return -1
	}
}
func (f *fakeDriver) Uniform1i(location int32, v0 int32) { f.call("Uniform1i") }
func (f *fakeDriver) GenVertexArrays(n int32, arrays *uint32) {
	f.call("GenVertexArrays")
	*arrays = f.nextVAO
	f.nextVAO++
}
func (f *fakeDriver) BindVertexArray(array uint32)               { f.call("BindVertexArray") }
func (f *fakeDriver) DeleteVertexArrays(n int32, arrays *uint32) { f.call("DeleteVertexArrays") }
func (f *fakeDriver) VertexAttribPointer(index uint32, size int32, xtype uint32, normalized bool, stride int32, pointer uintptr) {
	f.call("VertexAttribPointer")
}
func (f *fakeDriver) EnableVertexAttribArray(index uint32)  { f.call("EnableVertexAttribArray") }
func (f *fakeDriver) DisableVertexAttribArray(index uint32) { f.call("DisableVertexAttribArray") }
func (f *fakeDriver) GenFramebuffers(n int32, framebuffers *uint32) {
	f.call("GenFramebuffers")
	*framebuffers = f.nextFBO
	f.nextFBO++
}
func (f *fakeDriver) DeleteFramebuffers(n int32, framebuffers *uint32)  { f.call("DeleteFramebuffers") }
func (f *fakeDriver) BindFramebuffer(target uint32, framebuffer uint32) { f.call("BindFramebuffer") }
func (f *fakeDriver) FramebufferTexture2D(target uint32, attachment uint32, textarget uint32, texture uint32, level int32) {
	f.call("FramebufferTexture2D")
}
func (f *fakeDriver) CheckFramebufferStatus(target uint32) uint32 {
	f.call("CheckFramebufferStatus")
	return f.fbStatus
}
func (f *fakeDriver) Viewport(x, y, width, height int32)         { f.call("Viewport") }
func (f *fakeDriver) DrawArrays(mode uint32, first, count int32) { f.call("DrawArrays") }
func (f *fakeDriver) GetError() uint32 {
	f.call("GetError")
	err := f.glErr
	f.glErr = NoError
	return err
}

func cString(ptr *byte) string {
	if ptr == nil {
		return ""
	}
	const maxCStringLen = 256
	buf := make([]byte, 0, 16)
	for i := 0; i < maxCStringLen; i++ {
		b := *(*byte)(unsafe.Add(unsafe.Pointer(ptr), i))
		if b == 0 {
			break
		}
		buf = append(buf, b)
	}
	return string(buf)
}

func callIndex(calls []string, want string) int {
	for i, call := range calls {
		if call == want {
			return i
		}
	}
	return -1
}

func assertContainsCall(t *testing.T, calls []string, want string) {
	t.Helper()
	if callIndex(calls, want) < 0 {
		t.Fatalf("missing call %q in %v", want, calls)
	}
}

func assertCallBefore(t *testing.T, calls []string, before, after string) {
	t.Helper()
	beforeIndex := callIndex(calls, before)
	afterIndex := callIndex(calls, after)
	if beforeIndex < 0 || afterIndex < 0 || beforeIndex >= afterIndex {
		t.Fatalf("call order violation: want %q before %q in %v", before, after, calls)
	}
}

func TestNewTexturedQuadCopierForAPIUsesGLESShaders(t *testing.T) {
	fd := newFakeDriver()
	if _, err := NewTexturedQuadCopierForAPI(fd, GLAPIOpenGLES); err != nil {
		t.Fatalf("NewTexturedQuadCopierForAPI: %v", err)
	}
	joined := strings.Join(fd.shaderSources, "\n")
	if !strings.Contains(joined, "#version 100") || !strings.Contains(joined, "precision mediump float;") {
		t.Fatalf("GLES shader sources not used: %q", joined)
	}
}

func TestNewTexturedQuadCopierDefaultsToDesktopShaders(t *testing.T) {
	fd := newFakeDriver()
	if _, err := NewTexturedQuadCopier(fd); err != nil {
		t.Fatalf("NewTexturedQuadCopier: %v", err)
	}
	joined := strings.Join(fd.shaderSources, "\n")
	if !strings.Contains(joined, "#version 120") || strings.Contains(joined, "precision mediump float;") {
		t.Fatalf("desktop shader sources not used: %q", joined)
	}
}

func TestCopyImportedToOwnedChecksFramebufferBeforeDraw(t *testing.T) {
	fd := newFakeDriver()
	copier, err := NewTexturedQuadCopier(fd)
	if err != nil {
		t.Fatalf("NewTexturedQuadCopier: %v", err)
	}
	fd.calls = nil
	if _, err := copier.CopyImportedToOwned(Texture(7), dmabuf.Size{Width: 16, Height: 8}, 0); err != nil {
		t.Fatalf("CopyImportedToOwned: %v", err)
	}
	assertContainsCall(t, fd.calls, "GenTextures")
	assertContainsCall(t, fd.calls, "TexImage2D")
	assertContainsCall(t, fd.calls, "FramebufferTexture2D")
	assertContainsCall(t, fd.calls, "Viewport")
	assertContainsCall(t, fd.calls, "UseProgram")
	assertContainsCall(t, fd.calls, "DrawArrays")
	assertCallBefore(t, fd.calls, "CheckFramebufferStatus", "DrawArrays")
	assertCallBefore(t, fd.calls, "DrawArrays", "GetError")
	assertFloat32sEqual(t, fd.lastBufferData, quadVerticesRotate180)
}

func TestDrawTextureToCurrentFramebufferUsesIdentityQuad(t *testing.T) {
	fd := newFakeDriver()
	copier, err := NewTexturedQuadCopier(fd)
	if err != nil {
		t.Fatalf("NewTexturedQuadCopier: %v", err)
	}
	fd.calls = nil
	if err := copier.DrawTextureToCurrentFramebuffer(Texture(7), dmabuf.Size{Width: 16, Height: 8}); err != nil {
		t.Fatalf("DrawTextureToCurrentFramebuffer: %v", err)
	}
	assertContainsCall(t, fd.calls, "DrawArrays")
	assertFloat32sEqual(t, fd.lastBufferData, quadVerticesIdentity)
}

func assertFloat32sEqual(t *testing.T, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(got)=%d, len(want)=%d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("value[%d]=%v, want %v; got=%v want=%v", i, got[i], want[i], got, want)
		}
	}
}

func TestCopyImportedToOwnedPropagatesIncompleteFramebuffer(t *testing.T) {
	fd := newFakeDriver()
	fd.fbStatus = FramebufferIncompleteAttachment
	copier, err := NewTexturedQuadCopier(fd)
	if err != nil {
		t.Fatalf("NewTexturedQuadCopier: %v", err)
	}
	fd.calls = nil
	_, err = copier.CopyImportedToOwned(Texture(7), dmabuf.Size{Width: 16, Height: 8}, 0)
	if err == nil || !strings.Contains(err.Error(), "framebuffer incomplete") {
		t.Fatalf("expected incomplete framebuffer error, got %v", err)
	}
	if strings.Contains(strings.Join(fd.calls, ","), "DrawArrays") {
		t.Fatalf("drew despite incomplete framebuffer: %v", fd.calls)
	}
}

func TestCopyImportedToOwnedPropagatesGLError(t *testing.T) {
	fd := newFakeDriver()
	copier, err := NewTexturedQuadCopier(fd)
	if err != nil {
		t.Fatalf("NewTexturedQuadCopier: %v", err)
	}
	fd.glErr = InvalidOperation
	_, err = copier.CopyImportedToOwned(Texture(7), dmabuf.Size{Width: 16, Height: 8}, 0)
	if err == nil || !strings.Contains(err.Error(), "GL_INVALID_OPERATION") {
		t.Fatalf("expected GL error, got %v", err)
	}
}
