package gtkgdk

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/bnema/purego-cef2gtk/internal/dmabuf"
	"github.com/bnema/puregotk/v4/gdk"
	"github.com/bnema/puregotk/v4/glib"
)

type fakeFormats struct {
	allowed  bool
	fourcc   uint32
	modifier uint64
	calls    int
}

func (f *fakeFormats) Contains(fourcc uint32, modifier uint64) bool {
	f.calls++
	f.fourcc = fourcc
	f.modifier = modifier
	return f.allowed
}

type fakeBuilder struct {
	display       *gdk.Display
	width, height uint
	fourcc        uint32
	modifier      uint64
	premultiplied bool
	nPlanes       uint
	fd            int
	stride        uint
	offset        uint
	destroy       *glib.DestroyNotify
	data          uintptr
	buildErr      error
	texture       *gdk.Texture
	builds        int
}

func (b *fakeBuilder) SetDisplay(display *gdk.Display) { b.display = display }
func (b *fakeBuilder) SetWidth(width uint)             { b.width = width }
func (b *fakeBuilder) SetHeight(height uint)           { b.height = height }
func (b *fakeBuilder) SetFourcc(fourcc uint32)         { b.fourcc = fourcc }
func (b *fakeBuilder) SetModifier(modifier uint64)     { b.modifier = modifier }
func (b *fakeBuilder) SetPremultiplied(p bool)         { b.premultiplied = p }
func (b *fakeBuilder) SetNPlanes(n uint)               { b.nPlanes = n }
func (b *fakeBuilder) SetFd(_ uint, fd int)            { b.fd = fd }
func (b *fakeBuilder) SetStride(_ uint, stride uint)   { b.stride = stride }
func (b *fakeBuilder) SetOffset(_ uint, offset uint)   { b.offset = offset }
func (b *fakeBuilder) Build(destroy *glib.DestroyNotify, data uintptr) (*gdk.Texture, error) {
	b.builds++
	b.destroy = destroy
	b.data = data
	if b.buildErr != nil {
		return nil, b.buildErr
	}
	return b.texture, nil
}

func validFrame(fd int) dmabuf.BorrowedFrame {
	return dmabuf.BorrowedFrame{
		CodedSize: dmabuf.Size{Width: 640, Height: 480},
		Format:    dmabuf.FormatARGB8888,
		Modifier:  0x0102030405060708,
		Planes: []dmabuf.Plane{{
			FD:     fd,
			Stride: 2560,
			Offset: 128,
			Size:   640 * 480 * 4,
		}},
	}
}

func fakeTexture() *gdk.Texture {
	texture := &gdk.Texture{}
	texture.SetGoPointer(1)
	return texture
}

func buildTextureFromFrameForTest(r *Renderer, frame dmabuf.BorrowedFrame) (*ownedTexture, error) {
	owned, err := r.duplicateFrame(frame)
	if err != nil {
		return nil, err
	}
	built, err := r.buildTextureFromOwnedFrame(owned)
	if err != nil {
		r.releaseOwnedFrame(owned)
		return nil, err
	}
	return built, nil
}

func TestBuildTextureDuplicatesFDAndRendererOwnsDuplicate(t *testing.T) {
	file, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	builder := &fakeBuilder{texture: fakeTexture()}
	r := &Renderer{builder: builder, dupFD: dupFDClOExec, closeFD: unix.Close}
	built, err := buildTextureFromFrameForTest(r, validFrame(int(file.Fd())))
	if err != nil {
		t.Fatalf("buildTextureFromFrame error = %v", err)
	}
	if built == nil || built.texture == nil || built.texture.GoPointer() == 0 {
		t.Fatal("texture was not returned")
	}
	if builder.fd == int(file.Fd()) {
		t.Fatal("borrowed CEF fd escaped to GDK without dup")
	}
	if builder.width != 640 || builder.height != 480 || builder.fourcc != uint32(dmabuf.FormatXRGB8888) ||
		builder.modifier != 0x0102030405060708 || builder.nPlanes != 1 || builder.premultiplied ||
		builder.stride != 2560 || builder.offset != 128 {
		t.Fatalf("unexpected builder state: %+v", builder)
	}
	assertFDOpen(t, int(file.Fd()))
	assertFDOpen(t, builder.fd)
	if builder.destroy != nil || builder.data != 0 {
		t.Fatalf("destroy notify should not be passed to builder: destroy=%v data=%#x", builder.destroy, builder.data)
	}
	// Avoid Unref on the fake texture pointer; this unit only verifies FD
	// ownership. Runtime texture unrefs are covered by live GTK validation.
	built.texture = nil
	r.releaseOwnedTexture(built)
	assertFDClosed(t, builder.fd)
	assertFDOpen(t, int(file.Fd()))
}

func TestBuildTextureClosesDuplicateWhenBuildFails(t *testing.T) {
	file, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	builder := &fakeBuilder{buildErr: errors.New("no dmabuf")}
	r := &Renderer{builder: builder, dupFD: dupFDClOExec, closeFD: unix.Close}
	if _, err := buildTextureFromFrameForTest(r, validFrame(int(file.Fd()))); !errors.Is(err, ErrTextureBuildFailed) {
		t.Fatalf("buildTextureFromFrame error = %v, want %v", err, ErrTextureBuildFailed)
	}
	if builder.fd == int(file.Fd()) {
		t.Fatal("borrowed CEF fd escaped to builder without dup")
	}
	assertFDClosed(t, builder.fd)
	assertFDOpen(t, int(file.Fd()))
}

func TestBuildTextureRejectsUnsupportedInitialFormatBeforeDup(t *testing.T) {
	called := false
	r := &Renderer{
		builder: &fakeBuilder{texture: fakeTexture()},
		dupFD: func(int) (int, error) {
			called = true
			return -1, nil
		},
		closeFD: unix.Close,
	}
	frame := validFrame(3)
	frame.Format = dmabuf.FourCC(0x31323334)
	if _, err := buildTextureFromFrameForTest(r, frame); !errors.Is(err, dmabuf.ErrUnsupportedFormat) {
		t.Fatalf("buildTextureFromFrame error = %v, want %v", err, dmabuf.ErrUnsupportedFormat)
	}
	if called {
		t.Fatal("dup called for unsupported format")
	}
}

func TestBuildTextureRejectsUnsupportedDisplayModifierAndClosesDuplicate(t *testing.T) {
	file, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	formats := &fakeFormats{allowed: false}
	builder := &fakeBuilder{texture: fakeTexture()}
	duplicateFD := -1
	defer func() {
		if duplicateFD >= 0 {
			_ = unix.Close(duplicateFD)
		}
	}()
	r := &Renderer{
		formats: formats,
		builder: builder,
		dupFD: func(fd int) (int, error) {
			dup, err := dupFDClOExec(fd)
			duplicateFD = dup
			return dup, err
		},
		closeFD: unix.Close,
	}
	frame := validFrame(int(file.Fd()))
	if _, err := buildTextureFromFrameForTest(r, frame); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("buildTextureFromFrame error = %v, want %v", err, ErrUnsupportedFormat)
	}
	if formats.calls != 1 || formats.fourcc != uint32(gdkTextureFormat(frame.Format)) || formats.modifier != frame.Modifier {
		t.Fatalf("display format check = calls:%d fourcc:%#x modifier:%#x", formats.calls, formats.fourcc, formats.modifier)
	}
	if builder.builds != 0 {
		t.Fatalf("builder builds = %d, want 0", builder.builds)
	}
	assertFDClosed(t, duplicateFD)
	assertFDOpen(t, int(file.Fd()))
}

func TestBuildTextureReturnsDupFailureBeforeBuild(t *testing.T) {
	want := errors.New("dup failed")
	builder := &fakeBuilder{texture: fakeTexture()}
	closed := false
	r := &Renderer{
		formats: &fakeFormats{allowed: true},
		builder: builder,
		dupFD: func(int) (int, error) {
			return -1, want
		},
		closeFD: func(int) error {
			closed = true
			return nil
		},
	}
	if _, err := buildTextureFromFrameForTest(r, validFrame(3)); !errors.Is(err, want) {
		t.Fatalf("buildTextureFromFrame error = %v, want %v", err, want)
	}
	if builder.builds != 0 {
		t.Fatalf("builder builds = %d, want 0", builder.builds)
	}
	if closed {
		t.Fatal("closeFD called even though no duplicate fd was created")
	}
}

func TestBuildTextureRejectsInvalidFrameBeforeDup(t *testing.T) {
	called := false
	r := &Renderer{
		builder: &fakeBuilder{texture: fakeTexture()},
		dupFD: func(int) (int, error) {
			called = true
			return -1, nil
		},
		closeFD: unix.Close,
	}
	frame := validFrame(3)
	frame.Planes = nil
	if _, err := buildTextureFromFrameForTest(r, frame); !errors.Is(err, dmabuf.ErrUnsupportedPlanes) {
		t.Fatalf("buildTextureFromFrame error = %v, want %v", err, dmabuf.ErrUnsupportedPlanes)
	}
	if called {
		t.Fatal("dup called for invalid frame")
	}
}

func assertFDOpen(t *testing.T, fd int) {
	t.Helper()
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != nil {
		t.Fatalf("fd %d should be open: %v", fd, err)
	}
}

func assertFDClosed(t *testing.T, fd int) {
	t.Helper()
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("fd %d should be closed, F_GETFD error = %v", fd, err)
	}
}
