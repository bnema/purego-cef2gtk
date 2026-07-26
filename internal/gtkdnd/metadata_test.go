package gtkdnd

import "testing"

func TestURIAndFileOnlyMetadataRequiresNonEmptyEnterPlaceholder(t *testing.T) {
	for _, formats := range [][]string{{"text/uri-list"}, {"application/x-gtk-file-list"}} {
		metadata := MetadataFromFormats(formats)
		if !MetadataNeedsPlaceholder(metadata) {
			t.Fatalf("formats %v would produce an empty CEF enter object", formats)
		}
	}
}

func TestMetadataFromFormats(t *testing.T) {
	tests := []struct {
		name                 string
		formats              []string
		file, fragment, link bool
	}{
		{"uri list", []string{"text/uri-list"}, true, false, true},
		{"moz url", []string{"text/x-moz-url"}, false, false, true},
		{"html", []string{"text/html"}, false, true, false},
		{"text", []string{"text/plain;charset=utf-8"}, false, true, false},
		{"file", []string{"application/x-gtk-file-list"}, true, false, false},
		{"combined", []string{"text/html", "text/plain", "text/uri-list"}, true, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MetadataFromFormats(tt.formats)
			if got.IsFile != tt.file || got.IsFragment != tt.fragment || got.IsLink != tt.link {
				t.Fatalf("got %+v", got)
			}
			for _, format := range tt.formats {
				if !got.Has(format) {
					t.Fatalf("missing %q", format)
				}
			}
		})
	}
}
