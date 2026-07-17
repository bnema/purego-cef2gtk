package integration_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type lifecycleMilestone uint8

const (
	milestoneAcceleratedPaint lifecycleMilestone = iota
	milestoneDMABUFTextureSwap
	milestonePresentation
	milestoneResize
	milestoneClose
	milestoneDone
)

func (m lifecycleMilestone) String() string {
	switch m {
	case milestoneAcceleratedPaint:
		return "accelerated paint"
	case milestoneDMABUFTextureSwap:
		return "DMABUF texture swap"
	case milestonePresentation:
		return "GTK presentation"
	case milestoneResize:
		return "resize"
	case milestoneClose:
		return "browser close"
	default:
		return "completion"
	}
}

// lifecycleWaiter accepts exactly the smoke lifecycle order. It is deliberately
// strict: a duplicate is also out of order, and the first failure is terminal.
type lifecycleWaiter struct {
	mu      sync.Mutex
	next    lifecycleMilestone
	failure error
	changed chan struct{}
}

func newLifecycleWaiter() *lifecycleWaiter {
	return &lifecycleWaiter{changed: make(chan struct{})}
}

func (w *lifecycleWaiter) observe(m lifecycleMilestone) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failure != nil {
		return
	}
	if m != w.next {
		w.failure = fmt.Errorf("lifecycle out of order: expected %s, got %s", w.next, m)
		w.notifyLocked()
		return
	}
	w.next++
	w.notifyLocked()
}

func (w *lifecycleWaiter) fail(err error) {
	if err == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failure == nil {
		w.failure = err
		w.notifyLocked()
	}
}

func (w *lifecycleWaiter) status(target lifecycleMilestone) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failure != nil {
		return false, w.failure
	}
	return w.next > target, nil
}

func (w *lifecycleWaiter) wait(ctx context.Context, target lifecycleMilestone) error {
	for {
		w.mu.Lock()
		if w.failure != nil {
			err := w.failure
			w.mu.Unlock()
			return err
		}
		if w.next > target {
			w.mu.Unlock()
			return nil
		}
		expected := w.next
		changed := w.changed
		w.mu.Unlock()

		select {
		case <-ctx.Done():
			return fmt.Errorf("lifecycle deadline waiting for %s: %w", expected, ctx.Err())
		case <-changed:
		}
	}
}

func (w *lifecycleWaiter) notifyLocked() {
	close(w.changed)
	w.changed = make(chan struct{})
}

func TestLifecycleWaiterAcceptsOrderedLifecycle(t *testing.T) {
	waiter := newLifecycleWaiter()
	for milestone := milestoneAcceleratedPaint; milestone <= milestoneClose; milestone++ {
		waiter.observe(milestone)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waiter.wait(ctx, milestoneClose); err != nil {
		t.Fatalf("wait ordered lifecycle: %v", err)
	}
}

func TestLifecycleWaiterRejectsMissingMilestoneAtDeadline(t *testing.T) {
	waiter := newLifecycleWaiter()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := waiter.wait(ctx, milestoneAcceleratedPaint)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait missing milestone error = %v, want deadline exceeded", err)
	}
	if !strings.Contains(err.Error(), milestoneAcceleratedPaint.String()) {
		t.Fatalf("wait missing milestone error = %q, want milestone name", err)
	}
}

func TestLifecycleWaiterRejectsOutOfOrderAndDuplicateMilestones(t *testing.T) {
	for _, sequence := range [][]lifecycleMilestone{
		{milestoneDMABUFTextureSwap},
		{milestoneAcceleratedPaint, milestoneAcceleratedPaint},
	} {
		waiter := newLifecycleWaiter()
		for _, milestone := range sequence {
			waiter.observe(milestone)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := waiter.wait(ctx, milestoneClose)
		cancel()
		if err == nil || !strings.Contains(err.Error(), "out of order") {
			t.Fatalf("sequence %v error = %v, want out-of-order failure", sequence, err)
		}
	}
}

func TestLifecycleWaiterRejectsRenderError(t *testing.T) {
	waiter := newLifecycleWaiter()
	want := errors.New("render failure")
	waiter.fail(want)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waiter.wait(ctx, milestoneClose); !errors.Is(err, want) {
		t.Fatalf("wait error = %v, want %v", err, want)
	}
}

func TestLifecycleWaiterConcurrentDuplicateIsTerminal(t *testing.T) {
	waiter := newLifecycleWaiter()
	start := make(chan struct{})
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			waiter.observe(milestoneAcceleratedPaint)
		}()
	}
	close(start)
	group.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := waiter.wait(ctx, milestoneClose)
	if err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("concurrent duplicate error = %v, want terminal out-of-order failure", err)
	}
}
