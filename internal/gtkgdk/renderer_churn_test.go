package gtkgdk

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/cefadapter"
	"github.com/bnema/purego-cef2gtk/internal/dmabuf"
)

func validSinglePlaneFrame(fd int) dmabuf.SinglePlaneFrame {
	return dmabuf.SinglePlaneFrame{
		CodedSize: dmabuf.Size{Width: 640, Height: 480},
		Format:    dmabuf.FormatARGB8888,
		Modifier:  0x0102030405060708,
		Plane: dmabuf.Plane{
			FD:     fd,
			Stride: 2560,
			Offset: 128,
			Size:   640*480*4 + 128,
		},
	}
}

func TestDuplicateSinglePlaneFrameOwnsOnlyDuplicatedFD(t *testing.T) {
	file, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestFile(t, file)

	duplicateFD := -1
	r := &Renderer{
		dupFD: func(fd int) (int, error) {
			duplicateFD, err = dupFDClOExec(fd)
			return duplicateFD, err
		},
		closeFD: unix.Close,
	}
	owned, err := r.duplicateSinglePlaneFrame(validSinglePlaneFrame(int(file.Fd())))
	if err != nil {
		t.Fatalf("duplicateSinglePlaneFrame: %v", err)
	}
	if owned.Plane.FD != duplicateFD || owned.Plane.FD == int(file.Fd()) {
		t.Fatalf("owned fd = %d, duplicate = %d, borrowed = %d", owned.Plane.FD, duplicateFD, file.Fd())
	}
	r.releaseOwnedFrame(owned)
	assertFDClosed(t, duplicateFD)
	assertFDOpen(t, int(file.Fd()))
}

func TestBuildInlineSinglePlaneClosesFDOnFailure(t *testing.T) {
	file, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestFile(t, file)

	duplicateFD := -1
	r := &Renderer{
		builder: &fakeBuilder{buildErr: errors.New("build failed")},
		dupFD: func(fd int) (int, error) {
			duplicateFD, err = dupFDClOExec(fd)
			return duplicateFD, err
		},
		closeFD: unix.Close,
	}
	owned, err := r.duplicateSinglePlaneFrame(validSinglePlaneFrame(int(file.Fd())))
	if err != nil {
		t.Fatalf("duplicateSinglePlaneFrame: %v", err)
	}
	if _, err := r.buildTextureFromOwnedFrame(owned); !errors.Is(err, ErrTextureBuildFailed) {
		t.Fatalf("buildTextureFromOwnedFrame error = %v, want %v", err, ErrTextureBuildFailed)
	}
	assertFDClosed(t, duplicateFD)
	assertFDOpen(t, int(file.Fd()))
}

func TestRetiredTextureRingRetiresInFIFOOrder(t *testing.T) {
	r := &Renderer{}
	textures := make([]*ownedTexture, retiredTextureLimit+1)
	for i := range textures {
		textures[i] = &ownedTexture{}
		r.retireOwnedTexture(textures[i])
	}
	if r.retiredCount != retiredTextureLimit {
		t.Fatalf("retired count = %d, want %d", r.retiredCount, retiredTextureLimit)
	}
	for i := 0; i < retiredTextureLimit; i++ {
		if got := r.retiredAt(i); got != textures[i+1] {
			t.Fatalf("retired texture %d = %p, want %p", i, got, textures[i+1])
		}
	}
}

func BenchmarkImportOnePlaneFrame(b *testing.B) {
	file, err := os.Open("/dev/null")
	if err != nil {
		b.Fatal(err)
	}
	defer file.Close()

	r := &Renderer{dupFD: dupFDClOExec, closeFD: unix.Close}
	info := cef.NewAcceleratedPaintInfo()
	info.PlaneCount = 1
	info.Format = int32(cef.ColorTypeBgra8888)
	info.Planes[0].Fd = int32(file.Fd())
	info.Planes[0].Stride = 2560
	info.Planes[0].Size = 640 * 480 * 4
	info.Extra.CodedSize.Width = 640
	info.Extra.CodedSize.Height = 480
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame, err := cefadapter.SinglePlaneFrameFromAcceleratedPaint(&info)
		if err != nil {
			b.Fatal(err)
		}
		owned, err := r.duplicateSinglePlaneFrame(frame)
		if err != nil {
			b.Fatal(err)
		}
		r.releaseOwnedFrame(owned)
	}
}
