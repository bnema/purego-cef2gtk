package gtkgdk

import (
	"testing"
	"unsafe"

	"github.com/bnema/purego-cef2gtk/internal/dmabuf"
)

func TestCRectangleMatchesGdkRectangleLayout(t *testing.T) {
	if got := unsafe.Sizeof(cRectangle{}); got != 16 {
		t.Fatalf("sizeof(cRectangle) = %d, want 16 (four C ints)", got)
	}
}

func TestLetterboxAllocationPillarbox(t *testing.T) {
	// Stale 1600x900 buffer holds a narrower page letterboxed with 200px side bars.
	coded := dmabuf.Size{Width: 1600, Height: 900}
	crop := dmabuf.Rect{X: 200, Y: 0, Width: 1200, Height: 900}

	got := letterboxAllocation(1200, 900, coded, crop)

	want := cRectangle{X: -200, Y: 0, Width: 1600, Height: 900}
	if got != want {
		t.Fatalf("allocation = %+v, want %+v", got, want)
	}
}

func TestLetterboxAllocationScalesToWidget(t *testing.T) {
	coded := dmabuf.Size{Width: 1000, Height: 1000}
	crop := dmabuf.Rect{X: 0, Y: 250, Width: 1000, Height: 500}

	got := letterboxAllocation(2000, 1000, coded, crop)

	want := cRectangle{X: 0, Y: -500, Width: 2000, Height: 2000}
	if got != want {
		t.Fatalf("allocation = %+v, want %+v", got, want)
	}
}
