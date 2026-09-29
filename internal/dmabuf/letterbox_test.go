package dmabuf

import "testing"

func TestLetterboxCrop(t *testing.T) {
	coded := Size{Width: 1600, Height: 900}
	tests := []struct {
		name    string
		content Rect
		want    Rect
		ok      bool
	}{
		{"full frame", Rect{Width: 1600, Height: 900}, Rect{}, false},
		{"empty", Rect{}, Rect{}, false},
		{"out of bounds", Rect{X: 500, Width: 1200, Height: 900}, Rect{}, false},
		{"negative origin", Rect{X: -1, Width: 1200, Height: 900}, Rect{}, false},
		{"pillarbox", Rect{X: 200, Width: 1200, Height: 900}, Rect{X: 200, Width: 1200, Height: 900}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := LetterboxCrop(coded, tt.content)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("LetterboxCrop = %+v,%v want %+v,%v", got, ok, tt.want, tt.ok)
			}
		})
	}
}
