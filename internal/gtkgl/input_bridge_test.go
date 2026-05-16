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

func TestBuildMouseEventFractionalScale(t *testing.T) {
	evt := BuildMouseEvent(10.5, 2.25, 0, 1.2)
	if evt.X != 12 || evt.Y != 2 {
		t.Fatalf("BuildMouseEvent coords = (%d,%d), want (12,2)", evt.X, evt.Y)
	}
}

func TestMiddleClickHandlerStoredWithHost(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	called := false
	ib.SetMiddleClickHandler(func(x, y float64) bool {
		called = true
		if x != 10 || y != 20 {
			t.Fatalf("middle click coords=(%v,%v), want (10,20)", x, y)
		}
		return true
	})

	host, scale, handler := ib.currentHostAndMiddleClickHandler()
	if host != nil {
		t.Fatalf("host = %v, want nil", host)
	}
	if scale != 1 {
		t.Fatalf("scale = %v, want 1", scale)
	}
	if handler == nil {
		t.Fatalf("middle click handler nil")
	}
	if !handler(10, 20) || !called {
		t.Fatalf("middle click handler not invoked/consuming")
	}
}

func TestMiddleClickConsumedReleaseOnlyOnce(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	ib.setMiddleClickConsumed(true)
	if !ib.consumeMiddleClickRelease() {
		t.Fatalf("first release was not consumed")
	}
	if ib.consumeMiddleClickRelease() {
		t.Fatalf("second release consumed unexpectedly")
	}
}

func TestClipboardShortcutAction(t *testing.T) {
	if action, ok := clipboardShortcutAction(gdkKeyLowercaseC, uint(gdk.ControlMaskValue)); !ok || action != "copy" {
		t.Fatalf("ctrl-c action=(%q,%v), want copy,true", action, ok)
	}
	if action, ok := clipboardShortcutAction(gdkKeyUppercaseX, uint(gdk.ControlMaskValue)); !ok || action != "cut" {
		t.Fatalf("ctrl-x action=(%q,%v), want cut,true", action, ok)
	}
	if _, ok := clipboardShortcutAction(gdkKeyLowercaseC, uint(gdk.ControlMaskValue|gdk.ShiftMaskValue)); ok {
		t.Fatalf("ctrl-shift-c unexpectedly matched")
	}
}

func TestMirrorClipboardShortcut(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	var gotAction, gotText string
	ib.SetClipboardShortcutHandler(func() string { return "selected" }, func(action, text string) {
		gotAction, gotText = action, text
	})
	ib.mirrorClipboardShortcut(gdkKeyLowercaseC, uint(gdk.ControlMaskValue))
	if gotAction != "copy" || gotText != "selected" {
		t.Fatalf("shortcut=(%q,%q), want copy,selected", gotAction, gotText)
	}
}
