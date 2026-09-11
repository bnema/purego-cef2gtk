package gl

import (
	"errors"
	"time"
)

// GL sync object constants. A fence is inserted into the current command stream
// and signals once the commands before it have completed, including any read of
// a buffer they performed. That is what makes it usable as a completion proof.
const (
	SyncGpuCommandsComplete uint32 = 0x9117
	SyncFlushCommandsBit    uint32 = 0x0001

	AlreadySignaled    uint32 = 0x911A
	TimeoutExpired     uint32 = 0x911B
	ConditionSatisfied uint32 = 0x911C
	WaitFailed         uint32 = 0x911D
)

var (
	// ErrFenceUnsupported means the driver exposes no fence entry points, so a
	// caller cannot prove that a submitted read has completed.
	ErrFenceUnsupported = errors.New("gl fences unavailable")
	// ErrFenceWaitFailed means glClientWaitSync returned WAIT_FAILED.
	ErrFenceWaitFailed = errors.New("gl client wait failed")
	// ErrFenceTimeout means the fence did not signal within the bound.
	ErrFenceTimeout = errors.New("gl fence timed out")
)

// FenceSupported reports whether fence entry points were resolved.
func (l *Loader) FenceSupported() bool {
	return l != nil && l.fenceSync != nil && l.clientWaitSync != nil && l.deleteSync != nil
}

// Fence inserts a fence into the current command stream and returns its handle,
// or ErrFenceUnsupported. The caller owns the handle and must delete it.
func (l *Loader) Fence() (uintptr, error) {
	if !l.FenceSupported() {
		return 0, ErrFenceUnsupported
	}
	sync := l.fenceSync(SyncGpuCommandsComplete, 0)
	if sync == 0 {
		return 0, ErrFenceUnsupported
	}
	return sync, nil
}

// WaitFence blocks until the fence signals or the bound elapses. A true result
// means every command ahead of the fence, including source reads, has completed
// from this context's point of view.
//
// The bound is required: an unbounded wait on the GTK thread would stall the
// whole application when the driver never signals.
func (l *Loader) WaitFence(sync uintptr, bound time.Duration) (bool, error) {
	if !l.FenceSupported() || sync == 0 {
		return false, ErrFenceUnsupported
	}
	timeoutNS := uint64(bound.Nanoseconds())
	if bound <= 0 {
		timeoutNS = 0
	}
	status := l.clientWaitSync(sync, SyncFlushCommandsBit, timeoutNS)
	switch status {
	case AlreadySignaled, ConditionSatisfied:
		return true, nil
	case TimeoutExpired:
		return false, ErrFenceTimeout
	default:
		return false, ErrFenceWaitFailed
	}
}

// DeleteFence releases a fence handle. Safe to call with a nil loader or a zero
// handle so callers can defer it unconditionally.
func (l *Loader) DeleteFence(sync uintptr) {
	if l == nil || l.deleteSync == nil || sync == 0 {
		return
	}
	l.deleteSync(sync)
}
