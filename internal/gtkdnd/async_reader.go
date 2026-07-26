package gtkdnd

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	ErrUnsupportedMIME = errors.New("drop has no supported MIME type")
	ErrCanceled        = errors.New("asynchronous drop read canceled")
	ErrDetached        = errors.New("asynchronous drop reader detached")
	ErrStaleGeneration = errors.New("asynchronous drop read generation is stale")
	ErrNilStream       = errors.New("asynchronous drop read returned no stream")
	ErrUnexpectedMIME  = errors.New("drop read returned an unexpected MIME type")
)

// LimitScope identifies which bounded-read budget was exceeded.
type LimitScope string

const (
	LimitPayload    LimitScope = "payload"
	LimitCumulative LimitScope = "cumulative"
)

// TooLargeError is returned before an oversized chunk is retained.
type TooLargeError struct {
	Scope LimitScope
	Limit int
	Size  int
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("drop %s too large: %d bytes exceeds %d-byte limit", e.Scope, e.Size, e.Limit)
}

// AsyncSource opens one advertised MIME payload without blocking the caller.
type AsyncSource interface {
	// The callback MIME is the actual type returned by the platform's finish call.
	OpenAsync(mime string, callback func(AsyncStream, string, error))
	Cancel()
}

// AsyncStream reads one chunk without blocking the caller. An empty chunk is EOF.
type AsyncStream interface {
	ReadAsync(maxBytes int, callback func([]byte, error))
}

// ReadLimits configures MIME preference and bounded asynchronous reads. A byte
// limit of zero disables that limit. ChunkBytes defaults to 64 KiB.
type ReadLimits struct {
	SupportedMIMEs  []string
	PayloadBytes    int
	CumulativeBytes int
	ChunkBytes      int
}

// ReadResult is the exactly-once terminal result of a read operation.
type ReadResult struct {
	Generation uint64
	MIME       string
	Data       []byte
	Err        error
}

// AsyncReader is a platform-independent callback state machine. It never calls
// a synchronous read method or starts a goroutine; the platform adapter owns
// scheduling each OpenAsync and ReadAsync operation.
type AsyncReader struct {
	mu         sync.Mutex
	limits     ReadLimits
	generation uint64
	cumulative int
	detached   bool
	active     *readOperation
}

type readOperation struct {
	generation  uint64
	mime        string
	source      AsyncSource
	stream      AsyncStream
	done        func(ReadResult)
	data        []byte
	openPending bool
	readPending bool
	readToken   uint64
	terminal    bool
}

func NewAsyncReader(limits ReadLimits) *AsyncReader {
	limits.SupportedMIMEs = append([]string(nil), limits.SupportedMIMEs...)
	if limits.ChunkBytes <= 0 {
		limits.ChunkBytes = 64 * 1024
	}
	return &AsyncReader{limits: limits}
}

// Read starts a new generation. Starting a read makes any prior generation
// stale and completes it before opening the new source.
func (r *AsyncReader) Read(advertised []string, source AsyncSource, done func(ReadResult)) uint64 {
	mime, supported := selectMIME(advertised, r.limits.SupportedMIMEs)

	r.mu.Lock()
	r.generation++
	generation := r.generation
	old := r.active
	if old != nil && !old.terminal {
		old.terminal = true
		r.active = nil
	}
	if r.detached {
		r.mu.Unlock()
		finishOutside(old, ErrStaleGeneration, true)
		done(ReadResult{Generation: generation, MIME: mime, Err: ErrDetached})
		return generation
	}
	if !supported {
		r.mu.Unlock()
		finishOutside(old, ErrStaleGeneration, true)
		done(ReadResult{Generation: generation, Err: ErrUnsupportedMIME})
		return generation
	}
	op := &readOperation{
		generation:  generation,
		mime:        mime,
		source:      source,
		done:        done,
		openPending: true,
	}
	r.active = op
	r.mu.Unlock()

	finishOutside(old, ErrStaleGeneration, true)
	source.OpenAsync(mime, func(stream AsyncStream, actualMIME string, err error) {
		r.openComplete(op, stream, actualMIME, err)
	})
	return generation
}

// Cancel completes the matching generation and asks the platform operation to
// cancel. Late callbacks are harmless.
func (r *AsyncReader) Cancel(generation uint64) {
	r.terminate(generation, ErrCanceled, true)
}

// Invalidate marks the matching generation stale without detaching the reader.
func (r *AsyncReader) Invalidate(generation uint64) {
	r.terminate(generation, ErrStaleGeneration, true)
}

// Detach permanently invalidates the active generation.
func (r *AsyncReader) Detach() {
	r.mu.Lock()
	r.detached = true
	r.generation++
	op := r.active
	if op != nil && !op.terminal {
		op.terminal = true
		r.active = nil
	} else {
		op = nil
	}
	r.mu.Unlock()
	finishOutside(op, ErrDetached, true)
}

func (r *AsyncReader) openComplete(op *readOperation, stream AsyncStream, actualMIME string, err error) {
	r.mu.Lock()
	if r.active != op || op.terminal || !op.openPending {
		r.mu.Unlock()
		return
	}
	op.openPending = false
	if err == nil && !strings.EqualFold(strings.TrimSpace(actualMIME), strings.TrimSpace(op.mime)) {
		err = ErrUnexpectedMIME
	}
	if err == nil && stream == nil {
		err = ErrNilStream
	}
	if err != nil {
		op.terminal = true
		r.active = nil
		r.mu.Unlock()
		finishOutside(op, err, errors.Is(err, ErrUnexpectedMIME))
		return
	}
	op.stream = stream
	r.mu.Unlock()
	r.requestRead(op)
}

func (r *AsyncReader) requestRead(op *readOperation) {
	r.mu.Lock()
	if r.active != op || op.terminal || op.readPending {
		r.mu.Unlock()
		return
	}
	op.readPending = true
	op.readToken++
	token := op.readToken
	stream := op.stream
	chunkBytes := r.limits.ChunkBytes
	r.mu.Unlock()

	stream.ReadAsync(chunkBytes, func(chunk []byte, err error) {
		r.readComplete(op, token, chunk, err)
	})
}

func (r *AsyncReader) readComplete(op *readOperation, token uint64, chunk []byte, err error) {
	r.mu.Lock()
	if r.active != op || op.terminal || !op.readPending || op.readToken != token {
		r.mu.Unlock()
		return
	}
	op.readPending = false
	if err != nil {
		op.terminal = true
		r.active = nil
		r.mu.Unlock()
		finishOutside(op, err, false)
		return
	}
	if len(chunk) == 0 {
		op.terminal = true
		r.active = nil
		result := ReadResult{Generation: op.generation, MIME: op.mime, Data: append([]byte(nil), op.data...)}
		r.mu.Unlock()
		op.done(result)
		return
	}

	payloadSize := len(op.data) + len(chunk)
	if limit := r.limits.PayloadBytes; limit > 0 && payloadSize > limit {
		op.terminal = true
		r.active = nil
		r.mu.Unlock()
		finishOutside(op, &TooLargeError{Scope: LimitPayload, Limit: limit, Size: payloadSize}, true)
		return
	}
	cumulativeSize := r.cumulative + len(chunk)
	if limit := r.limits.CumulativeBytes; limit > 0 && cumulativeSize > limit {
		op.terminal = true
		r.active = nil
		r.mu.Unlock()
		finishOutside(op, &TooLargeError{Scope: LimitCumulative, Limit: limit, Size: cumulativeSize}, true)
		return
	}
	op.data = append(op.data, chunk...)
	r.cumulative = cumulativeSize
	r.mu.Unlock()
	r.requestRead(op)
}

func (r *AsyncReader) terminate(generation uint64, err error, cancel bool) {
	r.mu.Lock()
	op := r.active
	if op == nil || op.generation != generation || op.terminal {
		r.mu.Unlock()
		return
	}
	op.terminal = true
	r.active = nil
	r.mu.Unlock()
	finishOutside(op, err, cancel)
}

func finishOutside(op *readOperation, err error, cancel bool) {
	if op == nil {
		return
	}
	if cancel {
		op.source.Cancel()
	}
	op.done(ReadResult{Generation: op.generation, MIME: op.mime, Err: err})
}

func selectMIME(advertised, supported []string) (string, bool) {
	if len(supported) == 0 && len(advertised) > 0 {
		return advertised[0], true
	}
	for _, preferred := range supported {
		for _, offered := range advertised {
			if strings.EqualFold(strings.TrimSpace(offered), strings.TrimSpace(preferred)) {
				return offered, true
			}
		}
	}
	return "", false
}
