package gtkdnd

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
	"unicode/utf16"
)

func TestOutboundFormatsIncludesUTF8PlainText(t *testing.T) {
	got := OutboundFormats(OutboundPayload{Text: "drag text"})
	want := []OutboundFormat{{MIME: "text/plain;charset=utf-8", Value: []byte("drag text")}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("formats = %#v, want %#v", got, want)
	}
}

func TestOutboundFormatsSerializesAbsoluteFilesAndPrefersThemToLinkURI(t *testing.T) {
	got := OutboundFormats(OutboundPayload{
		Files:   []string{"/tmp/first file.txt", "/var/tmp/second.png"},
		LinkURL: "https://example.test/ignored",
	})
	want := []OutboundFormat{{
		MIME:  "text/uri-list",
		Value: []byte("file:///tmp/first%20file.txt\r\nfile:///var/tmp/second.png\r\n"),
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("formats = %#v, want %#v", got, want)
	}
}

func TestOutboundFormatsOmitsInvalidFilePaths(t *testing.T) {
	got := OutboundFormats(OutboundPayload{Files: []string{
		"relative/file.txt",
		"/tmp/bad\x00name.txt",
		"/tmp/valid.txt",
	}})
	want := []OutboundFormat{{MIME: "text/uri-list", Value: []byte("file:///tmp/valid.txt\r\n")}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("formats = %#v, want %#v", got, want)
	}

	if got := OutboundFormats(OutboundPayload{Files: []string{"relative.txt", "/tmp/bad\x00name"}}); got != nil {
		t.Fatalf("all-invalid files produced formats: %#v", got)
	}
}

func TestOutboundFormatsSerializesTitledHTTPLinkWithoutFiles(t *testing.T) {
	const (
		link  = "https://example.test/article"
		title = "Résumé 🚀"
	)
	got := OutboundFormats(OutboundPayload{LinkURL: link, LinkTitle: title})

	logicalMozURL := link + "\n" + title
	units := utf16.Encode([]rune(logicalMozURL))
	mozURLBytes := make([]byte, 2+2*len(units))
	copy(mozURLBytes, []byte{0xff, 0xfe})
	for i, unit := range units {
		binary.LittleEndian.PutUint16(mozURLBytes[2+i*2:], unit)
	}
	want := []OutboundFormat{
		{MIME: "text/uri-list", Value: []byte(link + "\r\n")},
		{MIME: "text/x-moz-url", Value: mozURLBytes},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("formats = %#v, want %#v", got, want)
	}

	roundTrip, err := ParseInboundPayload(got[1].MIME, got[1].Value)
	if err != nil {
		t.Fatalf("ParseInboundPayload() error = %v", err)
	}
	if roundTrip.LinkURL != link || roundTrip.LinkTitle != title {
		t.Fatalf("round-trip payload = %#v, want URL %q and title %q", roundTrip, link, title)
	}
}

func TestOutboundFormatsIncludesPNGImageBytes(t *testing.T) {
	image := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a}
	got := OutboundFormats(OutboundPayload{ImagePNG: image})
	if len(got) != 1 || got[0].MIME != "image/png" || !bytes.Equal(got[0].Value, image) {
		t.Fatalf("formats = %#v", got)
	}
}

func TestOutboundFormatsIncludesHTMLAfterText(t *testing.T) {
	got := OutboundFormats(OutboundPayload{Text: "drag text", HTML: "<strong>drag</strong>"})
	want := []OutboundFormat{
		{MIME: "text/plain;charset=utf-8", Value: []byte("drag text")},
		{MIME: "text/html", Value: []byte("<strong>drag</strong>")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("formats = %#v, want %#v", got, want)
	}
}
