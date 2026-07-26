package gtkdnd

import (
	"encoding/binary"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/bnema/purego-cef/cef"
)

var (
	ErrMalformedPayload = errors.New("malformed inbound drag payload")
	ErrRemoteFileURI    = errors.New("remote-host file URI is not allowed")
)

// DroppedFile is a local file represented in a CEF drag payload.
type DroppedFile struct {
	Path        string
	DisplayName string
}

// InboundPayload is the decoded, platform-independent content of an external drop.
type InboundPayload struct {
	Text      string
	HTML      string
	Files     []DroppedFile
	LinkURL   string
	LinkTitle string
}

// DragDataWriter is the subset of CefDragData used for inbound conversion.
type DragDataWriter interface {
	SetFragmentText(string)
	SetFragmentHtml(string)
	SetLinkURL(string)
	SetLinkTitle(string)
	AddFile(path, displayName string)
}

// ParseInboundPayload validates and decodes one MIME payload.
func ParseInboundPayload(mimeType string, data []byte) (InboundPayload, error) {
	base, params, err := mime.ParseMediaType(strings.ToLower(strings.TrimSpace(mimeType)))
	if err != nil || len(data) == 0 {
		return InboundPayload{}, ErrMalformedPayload
	}
	if charset := strings.ToLower(params["charset"]); charset != "" && charset != "utf-8" && charset != "utf8" {
		return InboundPayload{}, fmt.Errorf("%w: unsupported charset", ErrMalformedPayload)
	}
	if base == "text/x-moz-url" {
		return parseTitledLink(data)
	}
	if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return InboundPayload{}, ErrMalformedPayload
	}
	switch base {
	case "text/plain":
		return InboundPayload{Text: string(data)}, nil
	case "text/html":
		return InboundPayload{HTML: string(data)}, nil
	case "text/uri-list":
		return parseURIList(string(data))
	default:
		return InboundPayload{}, ErrMalformedPayload
	}
}

func parseTitledLink(data []byte) (InboundPayload, error) {
	value, err := decodeMozURL(data)
	if err != nil || strings.IndexByte(value, 0) >= 0 {
		return InboundPayload{}, ErrMalformedPayload
	}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	parts := strings.SplitN(value, "\n", 2)
	if len(parts) != 2 {
		return InboundPayload{}, fmt.Errorf("%w: titled link has no title", ErrMalformedPayload)
	}
	link, title := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || title == "" {
		return InboundPayload{}, fmt.Errorf("%w: invalid titled link", ErrMalformedPayload)
	}
	return InboundPayload{LinkURL: u.String(), LinkTitle: title}, nil
}

func decodeMozURL(data []byte) (string, error) {
	if len(data) >= 2 && ((data[0] == 0xff && data[1] == 0xfe) || (data[0] == 0xfe && data[1] == 0xff)) {
		if len(data)%2 != 0 {
			return "", ErrMalformedPayload
		}
		var order binary.ByteOrder = binary.LittleEndian
		if data[0] == 0xfe {
			order = binary.BigEndian
		}
		units := make([]uint16, (len(data)-2)/2)
		for i := range units {
			units[i] = order.Uint16(data[2+i*2:])
		}
		for i := 0; i < len(units); i++ {
			u := units[i]
			if u >= 0xd800 && u <= 0xdbff {
				if i+1 >= len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
					return "", ErrMalformedPayload
				}
				i++
			} else if u >= 0xdc00 && u <= 0xdfff {
				return "", ErrMalformedPayload
			}
		}
		return string(utf16.Decode(units)), nil
	}
	if !utf8.Valid(data) {
		return "", ErrMalformedPayload
	}
	return string(data), nil
}

func parseURIList(value string) (InboundPayload, error) {
	var payload InboundPayload
	for _, raw := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || u.Scheme == "" {
			return InboundPayload{}, fmt.Errorf("%w: invalid URI %q", ErrMalformedPayload, line)
		}
		switch strings.ToLower(u.Scheme) {
		case "file":
			if !strings.HasPrefix(strings.ToLower(line), "file://") || u.User != nil || u.Port() != "" || (u.Hostname() != "" && !strings.EqualFold(u.Hostname(), "localhost")) {
				if u.Hostname() != "" && !strings.EqualFold(u.Hostname(), "localhost") {
					return InboundPayload{}, fmt.Errorf("%w: %s", ErrRemoteFileURI, u.Hostname())
				}
				return InboundPayload{}, fmt.Errorf("%w: invalid local file URI", ErrMalformedPayload)
			}
			if !filepath.IsAbs(u.Path) || u.RawQuery != "" || u.Fragment != "" {
				return InboundPayload{}, fmt.Errorf("%w: invalid local file path", ErrMalformedPayload)
			}
			payload.Files = append(payload.Files, DroppedFile{Path: filepath.Clean(u.Path), DisplayName: filepath.Base(u.Path)})
		case "http", "https":
			if u.Host == "" || u.User != nil || payload.LinkURL != "" {
				return InboundPayload{}, fmt.Errorf("%w: invalid or multiple links", ErrMalformedPayload)
			}
			payload.LinkURL = u.String()
		default:
			return InboundPayload{}, fmt.Errorf("%w: unsupported URI scheme", ErrMalformedPayload)
		}
	}
	if len(payload.Files) == 0 && payload.LinkURL == "" {
		return InboundPayload{}, ErrMalformedPayload
	}
	return payload, nil
}

// ApplyInboundPayload fills the mutable CefDragData fields represented by payload.
func ApplyInboundPayload(data DragDataWriter, payload InboundPayload) {
	if data == nil {
		return
	}
	if payload.Text != "" {
		data.SetFragmentText(payload.Text)
	}
	if payload.HTML != "" {
		data.SetFragmentHtml(payload.HTML)
	}
	for _, file := range payload.Files {
		data.AddFile(file.Path, file.DisplayName)
	}
	if payload.LinkURL != "" {
		data.SetLinkURL(payload.LinkURL)
	}
	if payload.LinkTitle != "" {
		data.SetLinkTitle(payload.LinkTitle)
	}
}

// ApplyFileDropVeto returns None when an optional file policy rejects the
// local paths. A nil policy, and payloads without files, are allowed.
func ApplyFileDropVeto(payload InboundPayload, proposed cef.DragOperationsMask, allow func(paths []string) bool) cef.DragOperationsMask {
	if allow == nil || len(payload.Files) == 0 {
		return proposed
	}
	paths := make([]string, len(payload.Files))
	for i, file := range payload.Files {
		paths[i] = file.Path
	}
	if !allow(paths) {
		return cef.DragOperationsMaskDragOperationNone
	}
	return proposed
}

// NewInboundDragData constructs a complete CefDragData from a decoded payload.
func NewInboundDragData(payload InboundPayload) cef.DragData {
	data := cef.DragDataCreate()
	if data == nil {
		return nil
	}
	ApplyInboundPayload(data, payload)
	return data
}
