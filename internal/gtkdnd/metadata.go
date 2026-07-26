package gtkdnd

import (
	"strings"

	"github.com/bnema/purego-cef/cef"
)

// DragMetadata is the content classification available at ::enter, before any
// asynchronous payload read is permitted.
type DragMetadata struct {
	IsFile, IsFragment, IsLink bool
	Formats                    map[string]struct{}
}

func (m DragMetadata) Has(format string) bool { _, ok := m.Formats[strings.ToLower(format)]; return ok }

func MetadataFromFormats(formats []string) DragMetadata {
	m := DragMetadata{Formats: make(map[string]struct{}, len(formats))}
	for _, raw := range formats {
		format := strings.ToLower(strings.TrimSpace(raw))
		m.Formats[format] = struct{}{}
		switch {
		case format == "text/uri-list":
			m.IsFile, m.IsLink = true, true
		case format == "text/x-moz-url":
			m.IsLink = true
		case format == "text/html", strings.HasPrefix(format, "text/plain"):
			m.IsFragment = true
		case format == "application/x-gtk-file-list", format == "x-special/gnome-copied-files":
			m.IsFile = true
		}
	}
	return m
}

func MetadataNeedsPlaceholder(metadata DragMetadata) bool {
	return len(metadata.Formats) != 0
}

// NewMetadataDragData creates the richest safe CEF enter object possible
// without reading the drop. Full external payload construction belongs to B-2.
func NewMetadataDragData(metadata DragMetadata) cef.DragData {
	data := cef.DragDataCreate()
	if data == nil {
		return nil
	}
	// CEF requires a semantically non-empty enter object. A metadata-only
	// marker is safe for every announced format, including URI/file-only drops;
	// it does not fabricate a path or URL before B-2 reads the payload.
	if MetadataNeedsPlaceholder(metadata) {
		data.SetFragmentHtml("<!-- external drag: content pending -->")
	}
	// File paths and URLs are filled by the B-2 asynchronous reader; callers
	// retain DragMetadata for policy decisions.
	return data
}
