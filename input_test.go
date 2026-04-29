package cef2gtk

import "testing"

func TestInputOptionsNormalizedScale(t *testing.T) {
	if got := (InputOptions{}).normalizedScale(); got != 1 {
		t.Fatalf("zero scale normalized to %d, want 1", got)
	}
	if got := (InputOptions{Scale: -2}).normalizedScale(); got != 1 {
		t.Fatalf("negative scale normalized to %d, want 1", got)
	}
	if got := (InputOptions{Scale: 2}).normalizedScale(); got != 2 {
		t.Fatalf("scale normalized to %d, want 2", got)
	}
}
