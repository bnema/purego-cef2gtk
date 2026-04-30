package gl

import (
	"testing"
	"time"
)

type fakeTimerQueryDriver struct {
	next      uint32
	available map[uint32]bool
	results   map[uint32]uint64
	begun     []uint32
	ended     []uint32
	deleted   []uint32
}

func (f *fakeTimerQueryDriver) TimerQuerySupported() bool { return true }
func (f *fakeTimerQueryDriver) GenQueries(n int32, ids *uint32) {
	f.next++
	*ids = f.next
}
func (f *fakeTimerQueryDriver) DeleteQueries(n int32, ids *uint32) {
	f.deleted = append(f.deleted, *ids)
}
func (f *fakeTimerQueryDriver) BeginQuery(target uint32, id uint32) { f.begun = append(f.begun, id) }
func (f *fakeTimerQueryDriver) EndQuery(target uint32)              { f.ended = append(f.ended, target) }
func (f *fakeTimerQueryDriver) GetQueryObjectuiv(id uint32, pname uint32, params *uint32) {
	if f.available[id] {
		*params = 1
	}
}
func (f *fakeTimerQueryDriver) GetQueryObjectui64v(id uint32, pname uint32, params *uint64) {
	*params = f.results[id]
}

func TestTimerQueryRecorderCollectsAvailableElapsedTime(t *testing.T) {
	driver := &fakeTimerQueryDriver{available: map[uint32]bool{}, results: map[uint32]uint64{}}
	r := NewTimerQueryRecorder(driver)

	q, ok := r.Begin()
	if !ok {
		t.Fatal("Begin returned unsupported")
	}
	r.End(q)
	if got := r.Collect(); len(got) != 0 {
		t.Fatalf("Collect before availability = %v, want empty", got)
	}
	driver.available[1] = true
	driver.results[1] = uint64((2 * time.Millisecond).Nanoseconds())

	got := r.Collect()
	if len(got) != 1 || got[0] != 2*time.Millisecond {
		t.Fatalf("Collect = %v, want [2ms]", got)
	}
	if len(driver.deleted) != 1 || driver.deleted[0] != 1 {
		t.Fatalf("deleted queries = %v, want [1]", driver.deleted)
	}
}

func TestTimerQueryRecorderNoopsWhenUnsupported(t *testing.T) {
	r := NewTimerQueryRecorder(nil)
	if _, ok := r.Begin(); ok {
		t.Fatal("Begin supported with nil driver")
	}
	if got := r.Collect(); len(got) != 0 {
		t.Fatalf("Collect = %v, want empty", got)
	}
}
