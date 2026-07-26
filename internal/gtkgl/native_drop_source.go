package gtkgl

import (
	"errors"
	"sync"
	"unsafe"

	"github.com/bnema/purego-cef2gtk/internal/gtkdnd"
	"github.com/bnema/puregotk/v4/gdk"
	"github.com/bnema/puregotk/v4/gio"
	"github.com/bnema/puregotk/v4/glib"
)

// nativeDropSource owns one cancellable and any stream returned by ReadFinish.
// Callback values remain pinned while GLib has an outstanding operation; GIO
// guarantees every async callback runs once, including after cancellation.
type nativeDropSource struct {
	mu               sync.Mutex
	drop             *gdk.Drop
	cancellable      *gio.Cancellable
	stream           *gio.InputStream
	openCallback     *gio.AsyncReadyCallback
	readCallback     *gio.AsyncReadyCallback
	unrefCallback    func(any) error
	pending          int
	releaseRequested bool
	released         bool
}

type nativeDropStream struct{ source *nativeDropSource }

func resolvedDropMIME(requested, actual string, stream *gio.InputStream, err error) string {
	if actual == "" && stream != nil && err == nil {
		return requested
	}
	return actual
}

func newNativeDropSource(drop *gdk.Drop) releasableAsyncSource {
	if drop == nil {
		return nil
	}
	cancellable := gio.NewCancellable()
	if cancellable == nil {
		return nil
	}
	// The source keeps a separate reference because the bridge may finish and
	// release its operation reference before a cancellation callback arrives.
	drop.Ref()
	return &nativeDropSource{drop: drop, cancellable: cancellable, unrefCallback: glib.UnrefCallback}
}

func (s *nativeDropSource) OpenAsync(mime string, done func(gtkdnd.AsyncStream, string, error)) {
	s.mu.Lock()
	if s.released || s.pending != 0 {
		s.mu.Unlock()
		done(nil, "", errors.New("drop read source unavailable"))
		return
	}
	s.pending++
	callback := gio.AsyncReadyCallback(func(_, resultPtr, _ uintptr) {
		var actual string
		stream, err := s.drop.ReadFinish(&gio.AsyncResultBase{Ptr: resultPtr}, &actual)
		actual = resolvedDropMIME(mime, actual, stream, err)
		traceDND("read-open requested_mime=%q actual_mime=%q stream=%t error=%v", mime, actual, stream != nil, err)
		s.mu.Lock()
		if stream != nil {
			if err == nil {
				s.stream = stream
			} else {
				stream.Unref()
			}
		}
		s.mu.Unlock()
		s.callbackDone(true)
		done(&nativeDropStream{source: s}, actual, err)
	})
	s.openCallback = &callback
	cancellable := s.cancellable
	s.mu.Unlock()
	s.drop.ReadAsync([]string{mime}, 0, cancellable, &callback, 0)
}

func (s *nativeDropSource) Cancel() {
	s.mu.Lock()
	cancellable := s.cancellable
	s.mu.Unlock()
	if cancellable != nil {
		cancellable.Cancel()
	}
}

func (s *nativeDropSource) Release() {
	s.mu.Lock()
	if s.released || s.releaseRequested {
		s.mu.Unlock()
		return
	}
	s.releaseRequested = true
	if s.pending != 0 {
		s.mu.Unlock()
		return
	}
	s.releaseLocked()
	s.mu.Unlock()
}

func (s *nativeDropSource) callbackDone(open bool) {
	s.mu.Lock()
	var callback *gio.AsyncReadyCallback
	if open {
		callback, s.openCallback = s.openCallback, nil
	} else {
		callback, s.readCallback = s.readCallback, nil
	}
	unref := s.unrefCallback
	s.pending--
	if s.pending == 0 && s.releaseRequested {
		s.releaseLocked()
	}
	s.mu.Unlock()
	if callback != nil && unref != nil {
		_ = unref(callback)
	}
}

func (s *nativeDropSource) releaseLocked() {
	if s.released {
		return
	}
	s.released = true
	if s.stream != nil {
		s.stream.Unref()
		s.stream = nil
	}
	if s.cancellable != nil {
		s.cancellable.Unref()
		s.cancellable = nil
	}
	if s.drop != nil {
		s.drop.Unref()
		s.drop = nil
	}
	s.openCallback, s.readCallback = nil, nil
}

func (s *nativeDropStream) ReadAsync(maxBytes int, done func([]byte, error)) {
	if s == nil || s.source == nil {
		done(nil, errors.New("missing drop stream"))
		return
	}
	source := s.source
	source.mu.Lock()
	if source.released || source.stream == nil || source.pending != 0 {
		source.mu.Unlock()
		done(nil, errors.New("drop stream unavailable"))
		return
	}
	source.pending++
	callback := gio.AsyncReadyCallback(func(_, resultPtr, _ uintptr) {
		bytes, err := source.stream.ReadBytesFinish(&gio.AsyncResultBase{Ptr: resultPtr})
		var chunk []byte
		if bytes != nil {
			size := bytes.GetSize()
			ptr := bytes.GetData(&size)
			if size != 0 && ptr != 0 {
				raw := *(*unsafe.Pointer)(unsafe.Pointer(&ptr))
				chunk = append([]byte(nil), unsafe.Slice((*byte)(raw), int(size))...)
			}
			bytes.Unref()
		}
		traceDND("read-chunk bytes=%d eof=%t error=%v", len(chunk), len(chunk) == 0 && err == nil, err)
		source.callbackDone(false)
		done(chunk, err)
	})
	source.readCallback = &callback
	stream, cancellable := source.stream, source.cancellable
	source.mu.Unlock()
	stream.ReadBytesAsync(uint(maxBytes), 0, cancellable, &callback, 0)
}
