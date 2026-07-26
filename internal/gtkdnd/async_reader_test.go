package gtkdnd

import (
	"errors"
	"sync"
	"testing"
	"time"
)

const asyncTestTimeout = time.Second

type fakeAsyncSource struct {
	mu           sync.Mutex
	stream       *fakeAsyncStream
	openErr      error
	openedMIME   string
	openCallback func(AsyncStream, string, error)
	cancelCount  int
}

func (s *fakeAsyncSource) OpenAsync(mime string, callback func(AsyncStream, string, error)) {
	s.mu.Lock()
	s.openedMIME = mime
	s.openCallback = callback
	stream, err := s.stream, s.openErr
	s.mu.Unlock()
	callback(stream, mime, err)
}

func (s *fakeAsyncSource) Cancel() {
	s.mu.Lock()
	s.cancelCount++
	s.mu.Unlock()
}

type controlledSource struct {
	mu           sync.Mutex
	openCallback func(AsyncStream, string, error)
	cancelCount  int
}

func (s *controlledSource) OpenAsync(_ string, callback func(AsyncStream, string, error)) {
	s.mu.Lock()
	s.openCallback = callback
	s.mu.Unlock()
}

func (s *controlledSource) Cancel() {
	s.mu.Lock()
	s.cancelCount++
	s.mu.Unlock()
}

func (s *controlledSource) completeOpen(stream AsyncStream, err error) {
	s.completeOpenMIME(stream, "text/plain", err)
}

func (s *controlledSource) completeOpenMIME(stream AsyncStream, mime string, err error) {
	s.mu.Lock()
	callback := s.openCallback
	s.mu.Unlock()
	callback(stream, mime, err)
}

type controlledStream struct {
	mu        sync.Mutex
	callbacks []func([]byte, error)
}

func (s *controlledStream) ReadAsync(_ int, callback func([]byte, error)) {
	s.mu.Lock()
	s.callbacks = append(s.callbacks, callback)
	s.mu.Unlock()
}

func (s *controlledStream) complete(index int, chunk []byte, err error) {
	s.mu.Lock()
	callback := s.callbacks[index]
	s.mu.Unlock()
	callback(chunk, err)
}

type fakeRead struct {
	chunk []byte
	err   error
}

type fakeAsyncStream struct {
	mu    sync.Mutex
	reads []fakeRead
	calls int
}

func (s *fakeAsyncStream) ReadAsync(_ int, callback func([]byte, error)) {
	s.mu.Lock()
	i := s.calls
	s.calls++
	var read fakeRead
	if i < len(s.reads) {
		read = s.reads[i]
	}
	s.mu.Unlock()
	callback(read.chunk, read.err)
}

func awaitResult(t *testing.T, results <-chan ReadResult) ReadResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(asyncTestTimeout):
		t.Fatal("timed out waiting for asynchronous read result")
		return ReadResult{}
	}
}

func TestAsyncReaderReadsChunksUntilEOF(t *testing.T) {
	reader := NewAsyncReader(ReadLimits{PayloadBytes: 32, CumulativeBytes: 64, ChunkBytes: 4})
	source := &fakeAsyncSource{stream: &fakeAsyncStream{reads: []fakeRead{
		{chunk: []byte("abcd")},
		{chunk: []byte("ef")},
		{},
	}}}
	results := make(chan ReadResult, 1)

	reader.Read([]string{"text/plain"}, source, func(result ReadResult) { results <- result })
	result := awaitResult(t, results)

	if result.Err != nil || result.MIME != "text/plain" || string(result.Data) != "abcdef" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if source.stream.calls != 3 {
		t.Fatalf("read calls = %d, want 3", source.stream.calls)
	}
}

func TestAsyncReaderRejectsUnexpectedReadFinishMIME(t *testing.T) {
	reader := NewAsyncReader(ReadLimits{SupportedMIMEs: []string{"text/plain"}})
	source := &controlledSource{}
	results := make(chan ReadResult, 1)
	reader.Read([]string{"text/plain"}, source, func(result ReadResult) { results <- result })
	source.completeOpenMIME(&controlledStream{}, "text/html", nil)
	result := awaitResult(t, results)
	if !errors.Is(result.Err, ErrUnexpectedMIME) {
		t.Fatalf("error = %v, want ErrUnexpectedMIME", result.Err)
	}
	if source.cancelCount != 1 {
		t.Fatalf("cancel calls = %d, want 1", source.cancelCount)
	}
}

func TestAsyncReaderHandlesEmptyAndReadErrors(t *testing.T) {
	openErr := errors.New("read finish failed")
	chunkErr := errors.New("intermediate read failed")
	tests := []struct {
		name    string
		source  *fakeAsyncSource
		want    string
		wantErr error
	}{
		{name: "empty payload", source: &fakeAsyncSource{stream: &fakeAsyncStream{}}, want: ""},
		{name: "read finish error", source: &fakeAsyncSource{openErr: openErr}, wantErr: openErr},
		{name: "intermediate read error", source: &fakeAsyncSource{stream: &fakeAsyncStream{reads: []fakeRead{{chunk: []byte("part")}, {err: chunkErr}}}}, wantErr: chunkErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := NewAsyncReader(ReadLimits{PayloadBytes: 32, CumulativeBytes: 64, ChunkBytes: 4})
			results := make(chan ReadResult, 1)
			reader.Read([]string{"text/plain"}, tt.source, func(result ReadResult) { results <- result })
			result := awaitResult(t, results)
			if !errors.Is(result.Err, tt.wantErr) || string(result.Data) != tt.want {
				t.Fatalf("result = %+v, want data %q error %v", result, tt.want, tt.wantErr)
			}
		})
	}
}

func TestAsyncReaderEnforcesPayloadAndCumulativeLimits(t *testing.T) {
	t.Run("payload", func(t *testing.T) {
		reader := NewAsyncReader(ReadLimits{PayloadBytes: 5, CumulativeBytes: 20, ChunkBytes: 4})
		source := &fakeAsyncSource{stream: &fakeAsyncStream{reads: []fakeRead{{chunk: []byte("abcd")}, {chunk: []byte("ef")}}}}
		results := make(chan ReadResult, 1)
		reader.Read([]string{"text/plain"}, source, func(result ReadResult) { results <- result })
		assertTooLarge(t, awaitResult(t, results), LimitPayload, 5, 6)
	})

	t.Run("cumulative across payloads", func(t *testing.T) {
		reader := NewAsyncReader(ReadLimits{PayloadBytes: 10, CumulativeBytes: 5, ChunkBytes: 4})
		read := func(payload string) ReadResult {
			source := &fakeAsyncSource{stream: &fakeAsyncStream{reads: []fakeRead{{chunk: []byte(payload)}, {}}}}
			results := make(chan ReadResult, 1)
			reader.Read([]string{"text/plain"}, source, func(result ReadResult) { results <- result })
			return awaitResult(t, results)
		}
		if result := read("abc"); result.Err != nil {
			t.Fatalf("first payload: %v", result.Err)
		}
		assertTooLarge(t, read("def"), LimitCumulative, 5, 6)
	})
}

func assertTooLarge(t *testing.T, result ReadResult, scope LimitScope, limit, size int) {
	t.Helper()
	var tooLarge *TooLargeError
	if !errors.As(result.Err, &tooLarge) {
		t.Fatalf("error = %v, want *TooLargeError", result.Err)
	}
	if tooLarge.Scope != scope || tooLarge.Limit != limit || tooLarge.Size != size {
		t.Fatalf("too-large error = %+v", tooLarge)
	}
	if result.Data != nil {
		t.Fatalf("oversize result retained data: %q", result.Data)
	}
}

func TestAsyncReaderCancellationInvalidationAndDetachAreExactlyOnce(t *testing.T) {
	tests := []struct {
		name      string
		terminate func(*AsyncReader, uint64)
		wantErr   error
	}{
		{name: "cancel", terminate: func(r *AsyncReader, generation uint64) { r.Cancel(generation) }, wantErr: ErrCanceled},
		{name: "stale generation", terminate: func(r *AsyncReader, generation uint64) { r.Invalidate(generation) }, wantErr: ErrStaleGeneration},
		{name: "detach", terminate: func(r *AsyncReader, _ uint64) { r.Detach() }, wantErr: ErrDetached},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := NewAsyncReader(ReadLimits{})
			source := &controlledSource{}
			results := make(chan ReadResult, 4)
			generation := reader.Read([]string{"text/plain"}, source, func(result ReadResult) { results <- result })
			stream := &controlledStream{}
			source.completeOpen(stream, nil)
			tt.terminate(reader, generation)
			result := awaitResult(t, results)
			if !errors.Is(result.Err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", result.Err, tt.wantErr)
			}
			// The platform may still deliver the canceled read callback more than once.
			stream.complete(0, []byte("late"), nil)
			stream.complete(0, nil, errors.New("late error"))
			reader.Cancel(generation)
			assertNoResult(t, results)
			if source.cancelCount != 1 {
				t.Fatalf("cancel calls = %d, want 1", source.cancelCount)
			}
		})
	}
}

func TestAsyncReaderIgnoresLateAndDoubleReadCallbacks(t *testing.T) {
	reader := NewAsyncReader(ReadLimits{})
	source := &controlledSource{}
	stream := &controlledStream{}
	results := make(chan ReadResult, 4)
	reader.Read([]string{"text/plain"}, source, func(result ReadResult) { results <- result })
	source.completeOpen(stream, nil)
	source.completeOpen(stream, nil) // duplicate ReadFinish callback

	stream.complete(0, []byte("a"), nil)
	stream.complete(0, []byte("wrong"), nil) // duplicate callback for the old request
	stream.complete(1, nil, nil)
	stream.complete(1, nil, nil) // duplicate EOF

	result := awaitResult(t, results)
	if result.Err != nil || string(result.Data) != "a" {
		t.Fatalf("result = %+v", result)
	}
	assertNoResult(t, results)
}

func TestAsyncReaderConcurrentCancelAndCallbackTerminatesOnce(t *testing.T) {
	for i := 0; i < 100; i++ {
		reader := NewAsyncReader(ReadLimits{})
		source := &controlledSource{}
		stream := &controlledStream{}
		results := make(chan ReadResult, 3)
		generation := reader.Read([]string{"text/plain"}, source, func(result ReadResult) { results <- result })
		source.completeOpen(stream, nil)

		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); reader.Cancel(generation) }()
		go func() { defer wg.Done(); stream.complete(0, nil, nil) }()
		go func() { defer wg.Done(); stream.complete(0, []byte("late"), nil) }()
		wg.Wait()

		result := awaitResult(t, results)
		if result.Err != nil && !errors.Is(result.Err, ErrCanceled) {
			t.Fatalf("iteration %d terminal error = %v", i, result.Err)
		}
		assertNoResult(t, results)
	}
}

func TestAsyncReaderNewGenerationCompletesOldAsStale(t *testing.T) {
	reader := NewAsyncReader(ReadLimits{})
	oldSource, newSource := &controlledSource{}, &controlledSource{}
	oldResults, newResults := make(chan ReadResult, 2), make(chan ReadResult, 2)
	oldGeneration := reader.Read([]string{"text/plain"}, oldSource, func(result ReadResult) { oldResults <- result })
	newGeneration := reader.Read([]string{"text/plain"}, newSource, func(result ReadResult) { newResults <- result })
	if newGeneration <= oldGeneration {
		t.Fatalf("generations did not increase: old=%d new=%d", oldGeneration, newGeneration)
	}
	if result := awaitResult(t, oldResults); !errors.Is(result.Err, ErrStaleGeneration) {
		t.Fatalf("old result error = %v", result.Err)
	}
	oldSource.completeOpen(&controlledStream{}, nil)
	assertNoResult(t, oldResults)

	stream := &controlledStream{}
	newSource.completeOpen(stream, nil)
	stream.complete(0, nil, nil)
	if result := awaitResult(t, newResults); result.Err != nil {
		t.Fatalf("new result error = %v", result.Err)
	}
}

func assertNoResult(t *testing.T, results <-chan ReadResult) {
	t.Helper()
	select {
	case result := <-results:
		t.Fatalf("unexpected extra terminal result: %+v", result)
	case <-time.After(10 * time.Millisecond):
	}
}

func TestAsyncReaderSelectsSupportedMIMEAndRejectsUnsupported(t *testing.T) {
	reader := NewAsyncReader(ReadLimits{
		SupportedMIMEs:  []string{"text/html", "text/plain"},
		PayloadBytes:    32,
		CumulativeBytes: 64,
		ChunkBytes:      4,
	})
	t.Run("selection follows supported preference", func(t *testing.T) {
		source := &fakeAsyncSource{stream: &fakeAsyncStream{}}
		results := make(chan ReadResult, 1)
		reader.Read([]string{"image/png", "text/plain", "text/html"}, source, func(result ReadResult) { results <- result })
		result := awaitResult(t, results)
		if result.Err != nil || result.MIME != "text/html" || source.openedMIME != "text/html" {
			t.Fatalf("unexpected selection: result=%+v opened=%q", result, source.openedMIME)
		}
	})
	t.Run("unsupported", func(t *testing.T) {
		source := &fakeAsyncSource{stream: &fakeAsyncStream{}}
		results := make(chan ReadResult, 1)
		reader.Read([]string{"image/png"}, source, func(result ReadResult) { results <- result })
		result := awaitResult(t, results)
		if !errors.Is(result.Err, ErrUnsupportedMIME) {
			t.Fatalf("error = %v, want ErrUnsupportedMIME", result.Err)
		}
		if source.openedMIME != "" {
			t.Fatalf("unsupported MIME was opened: %q", source.openedMIME)
		}
	})
}
