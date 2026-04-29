package dmabuf

import "testing"

// i915FormatModYTiled is the Intel i915 Y-tiled modifier (vendor 0x01, modifier 0x02).
const i915FormatModYTiled uint64 = 0x0100000000000002

func TestModifierHiLo(t *testing.T) {
	const modifier uint64 = 0x0123456789abcdef
	hi, lo := ModifierHiLo(modifier)
	if hi != 0x01234567 || lo != 0x89abcdef {
		t.Fatalf("ModifierHiLo(%#x) = (%#x, %#x)", modifier, hi, lo)
	}
}

func TestModifierHasExplicitAttrs(t *testing.T) {
	if ModifierHasExplicitAttrs(ModifierInvalid) {
		t.Fatalf("ModifierHasExplicitAttrs(%#x) = true, want false", ModifierInvalid)
	}
	for _, modifier := range []uint64{ModifierLinear, i915FormatModYTiled} {
		if !ModifierHasExplicitAttrs(modifier) {
			t.Fatalf("ModifierHasExplicitAttrs(%#x) = false, want true", modifier)
		}
	}
}
