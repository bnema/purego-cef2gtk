package gtkdnd

import (
	"encoding/binary"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// OutboundFormat is one MIME value offered to a native drop target.
type OutboundFormat struct {
	MIME  string
	Value []byte
}

// OutboundFormats serializes platform-independent drag content in a stable order.
func OutboundFormats(payload OutboundPayload) []OutboundFormat {
	var formats []OutboundFormat
	if payload.Text != "" {
		formats = append(formats, OutboundFormat{MIME: "text/plain;charset=utf-8", Value: []byte(payload.Text)})
	}
	if payload.HTML != "" {
		formats = append(formats, OutboundFormat{MIME: "text/html", Value: []byte(payload.HTML)})
	}
	if uriList := outboundFileURIList(payload.Files); uriList != nil {
		formats = append(formats, OutboundFormat{MIME: "text/uri-list", Value: uriList})
	} else if link := outboundHTTPURL(payload.LinkURL); link != "" {
		formats = append(formats, OutboundFormat{MIME: "text/uri-list", Value: []byte(link + "\r\n")})
		if payload.LinkTitle != "" && strings.IndexByte(payload.LinkTitle, 0) < 0 {
			formats = append(formats, OutboundFormat{MIME: "text/x-moz-url", Value: encodeMozURL(link + "\n" + payload.LinkTitle)})
		}
	} else if link := outboundLocalFileURL(payload.LinkURL); link != "" {
		formats = append(formats, OutboundFormat{MIME: "text/uri-list", Value: []byte(link + "\r\n")})
	}
	if strings.HasPrefix(payload.ImageMIME, "image/") && len(payload.ImageBytes) != 0 {
		formats = append(formats, OutboundFormat{MIME: payload.ImageMIME, Value: append([]byte(nil), payload.ImageBytes...)})
	}
	return formats
}

func encodeMozURL(value string) []byte {
	units := utf16.Encode([]rune(value))
	encoded := make([]byte, 2+2*len(units))
	encoded[0], encoded[1] = 0xff, 0xfe
	for i, unit := range units {
		binary.LittleEndian.PutUint16(encoded[2+i*2:], unit)
	}
	return encoded
}

func outboundHTTPURL(value string) string {
	if strings.IndexByte(value, 0) >= 0 {
		return ""
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil {
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.String()
}

func outboundLocalFileURL(value string) string {
	if strings.IndexByte(value, 0) >= 0 {
		return ""
	}
	u, err := url.Parse(value)
	if err != nil || !strings.EqualFold(u.Scheme, "file") || u.User != nil {
		return ""
	}
	if len(value) < len(u.Scheme)+3 || !strings.HasPrefix(value[len(u.Scheme)+1:], "//") {
		return ""
	}
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		return ""
	}
	if !filepath.IsAbs(u.Path) || strings.HasPrefix(u.Path, "//") || strings.IndexByte(u.Path, 0) >= 0 {
		return ""
	}
	if u.RawQuery != "" || u.ForceQuery || strings.Contains(value, "#") {
		return ""
	}
	u.Scheme = "file"
	return u.String()
}

func outboundFileURIList(paths []string) []byte {
	var value strings.Builder
	for _, path := range paths {
		if !filepath.IsAbs(path) || strings.IndexByte(path, 0) >= 0 {
			continue
		}
		uri := url.URL{Scheme: "file", Path: filepath.Clean(path)}
		value.WriteString(uri.String())
		value.WriteString("\r\n")
	}
	if value.Len() == 0 {
		return nil
	}
	return []byte(value.String())
}
