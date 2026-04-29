package gtkgl

import (
	"testing"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/puregotk/v4/gdk"
)

func TestTranslateScrollDeltas(t *testing.T) {
	x, y := TranslateScrollDeltas(1.5, -2)
	if x != 180 || y != 240 {
		t.Fatalf("TranslateScrollDeltas = (%d,%d), want (180,240)", x, y)
	}
}

func TestTranslateMouseButton(t *testing.T) {
	if got := TranslateMouseButton(2); got != cef.MouseButtonTypeMbtMiddle {
		t.Fatalf("middle button = %v", got)
	}
	if got := TranslateMouseButton(99); got != cef.MouseButtonTypeMbtLeft {
		t.Fatalf("unknown button = %v", got)
	}
}

func TestTranslateModifiers(t *testing.T) {
	mods := uint(gdk.ShiftMaskValue | gdk.ControlMaskValue | gdk.Button1MaskValue)
	got := TranslateModifiers(mods)
	want := uint32(cef.EventFlagsEventflagShiftDown | cef.EventFlagsEventflagControlDown | cef.EventFlagsEventflagLeftMouseButton)
	if got != want {
		t.Fatalf("TranslateModifiers = %#x, want %#x", got, want)
	}
}

func TestGDKKeyvalToWindowsVK(t *testing.T) {
	tests := map[uint]int32{
		'a':    0x41,
		'A':    0x41,
		'7':    0x37,
		'!':    0x31,
		'.':    0xBE,
		0xffbe: 0x70, // GDK_KEY_F1
		0xff51: 0x25, // GDK_KEY_Left
	}
	for keyval, want := range tests {
		if got := GDKKeyvalToWindowsVK(keyval); got != want {
			t.Fatalf("GDKKeyvalToWindowsVK(%#x) = %#x, want %#x", keyval, got, want)
		}
	}
}

func TestBuildMouseEventScaleDefault(t *testing.T) {
	evt := BuildMouseEvent(10.5, 2, 0, 0)
	if evt.X != 10 || evt.Y != 2 {
		t.Fatalf("BuildMouseEvent coords = (%d,%d), want (10,2)", evt.X, evt.Y)
	}
}
