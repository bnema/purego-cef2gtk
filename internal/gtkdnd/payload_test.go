package gtkdnd

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/bnema/purego-cef/cef"
	"unicode/utf16"
)

type recordingDragData struct {
	text, html, linkURL, linkTitle string
	files                          []DroppedFile
}

func (d *recordingDragData) SetFragmentText(text string) { d.text = text }
func (d *recordingDragData) SetFragmentHtml(html string) { d.html = html }
func (d *recordingDragData) SetLinkURL(url string)       { d.linkURL = url }
func (d *recordingDragData) SetLinkTitle(title string)   { d.linkTitle = title }
func (d *recordingDragData) AddFile(path, displayName string) {
	d.files = append(d.files, DroppedFile{Path: path, DisplayName: displayName})
}

func TestHTMLPayloadMapsToCEFDragData(t *testing.T) {
	payload, err := ParseInboundPayload("text/html", []byte("<strong>drop</strong>"))
	if err != nil {
		t.Fatal(err)
	}
	data := &recordingDragData{}
	ApplyInboundPayload(data, payload)
	if data.html != "<strong>drop</strong>" {
		t.Fatalf("fragment HTML = %q", data.html)
	}
}

func TestURIListMapsLocalFilesAndHTTPLinkToCEFDragData(t *testing.T) {
	payload, err := ParseInboundPayload("text/uri-list", []byte("# dragged items\r\nfile:///tmp/a%20file.txt\r\nfile://localhost/tmp/b.png\r\nhttps://example.test/path\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	data := &recordingDragData{}
	ApplyInboundPayload(data, payload)
	if len(data.files) != 2 || data.files[0] != (DroppedFile{Path: "/tmp/a file.txt", DisplayName: "a file.txt"}) || data.files[1].Path != "/tmp/b.png" {
		t.Fatalf("files = %#v", data.files)
	}
	if data.linkURL != "https://example.test/path" {
		t.Fatalf("link URL = %q", data.linkURL)
	}
}

func TestTitledLinkPayloadMapsURLAndTitleToCEFDragData(t *testing.T) {
	for _, data := range [][]byte{
		[]byte("https://example.test/article\nReadable title"),
		utf16LE("https://example.test/article\r\nReadable title"),
	} {
		payload, err := ParseInboundPayload("text/x-moz-url", data)
		if err != nil {
			t.Fatal(err)
		}
		dragData := &recordingDragData{}
		ApplyInboundPayload(dragData, payload)
		if dragData.linkURL != "https://example.test/article" || dragData.linkTitle != "Readable title" {
			t.Fatalf("link = %q, title = %q", dragData.linkURL, dragData.linkTitle)
		}
	}
}

func utf16LE(value string) []byte {
	units := utf16.Encode([]rune(value))
	data := make([]byte, 2+len(units)*2)
	data[0], data[1] = 0xff, 0xfe
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[2+i*2:], unit)
	}
	return data
}

func TestMalformedPayloadsAndRemoteFileURIsAreRejected(t *testing.T) {
	tests := []struct {
		name, mime string
		data       []byte
		want       error
	}{
		{name: "remote file host", mime: "text/uri-list", data: []byte("file://remote.test/tmp/x"), want: ErrRemoteFileURI},
		{name: "invalid percent escape", mime: "text/uri-list", data: []byte("file:///tmp/%ZZ"), want: ErrMalformedPayload},
		{name: "relative file URI", mime: "text/uri-list", data: []byte("file:/tmp/x"), want: ErrMalformedPayload},
		{name: "unsupported URI scheme", mime: "text/uri-list", data: []byte("ftp://example.test/x"), want: ErrMalformedPayload},
		{name: "invalid UTF-8", mime: "text/plain", data: []byte{0xff}, want: ErrMalformedPayload},
		{name: "unsupported charset", mime: "text/plain;charset=iso-8859-1", data: []byte("text"), want: ErrMalformedPayload},
		{name: "titled link missing title", mime: "text/x-moz-url", data: []byte("https://example.test"), want: ErrMalformedPayload},
		{name: "odd UTF-16", mime: "text/x-moz-url", data: []byte{0xff, 0xfe, 0x01}, want: ErrMalformedPayload},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseInboundPayload(tt.mime, tt.data)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestFileDropVetoDefaultsAllowAndDenialReturnsNone(t *testing.T) {
	payload := InboundPayload{Files: []DroppedFile{{Path: "/tmp/a.txt", DisplayName: "a.txt"}}}
	proposed := cef.DragOperationsMaskDragOperationCopy
	if got := ApplyFileDropVeto(payload, proposed, nil); got != proposed {
		t.Fatalf("default operation = %v, want %v", got, proposed)
	}
	called := false
	if got := ApplyFileDropVeto(payload, proposed, func(paths []string) bool {
		called = true
		return len(paths) == 0
	}); got != cef.DragOperationsMaskDragOperationNone {
		t.Fatalf("veto operation = %v, want None", got)
	}
	if !called {
		t.Fatal("file-drop hook was not called")
	}
	if got := ApplyFileDropVeto(payload, proposed, func(paths []string) bool { return true }); got != proposed {
		t.Fatalf("allowed operation = %v, want %v", got, proposed)
	}
}

func TestPlainTextPayloadMapsToCEFDragData(t *testing.T) {
	payload, err := ParseInboundPayload("text/plain;charset=utf-8", []byte("hello, drag"))
	if err != nil {
		t.Fatal(err)
	}
	data := &recordingDragData{}
	ApplyInboundPayload(data, payload)
	if data.text != "hello, drag" {
		t.Fatalf("fragment text = %q", data.text)
	}
}
