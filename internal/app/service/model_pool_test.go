package service

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeModel struct {
	name   string
	closed int32
}

func (f *fakeModel) Close() error   { atomic.AddInt32(&f.closed, 1); return nil }
func (f *fakeModel) isClosed() bool { return atomic.LoadInt32(&f.closed) > 0 }

func TestPoolPinLoadsOnceAndCaches(t *testing.T) {
	p := newModelPool(0, 0)
	var opens int32
	open := func() (loadedModel, error) {
		atomic.AddInt32(&opens, 1)
		return &fakeModel{name: "ecapa"}, nil
	}
	m1, err := p.pin("ecapa", open)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := p.pin("ecapa", open)
	if err != nil {
		t.Fatal(err)
	}
	if m1 != m2 {
		t.Error("pin should return the cached instance")
	}
	if opens != 1 {
		t.Errorf("open called %d times, want 1", opens)
	}
}

func TestPoolPinNotEvictedBySweep(t *testing.T) {
	now := time.Now()
	p := newModelPool(time.Minute, 0)
	p.now = func() time.Time { return now }
	m, _ := p.pin("ecapa", func() (loadedModel, error) { return &fakeModel{}, nil })
	now = now.Add(time.Hour) // long past the TTL
	if p.sweep() != 0 {
		t.Error("pinned model must never be swept")
	}
	if m.(*fakeModel).isClosed() {
		t.Error("pinned model must not be closed by sweep")
	}
}

func TestPoolAcquireReleaseAndIdleEvict(t *testing.T) {
	now := time.Now()
	p := newModelPool(90*time.Second, 0)
	p.now = func() time.Time { return now }
	var opens int32
	open := func() (loadedModel, error) {
		atomic.AddInt32(&opens, 1)
		return &fakeModel{name: "parakeet"}, nil
	}
	m, release, err := p.acquire("parakeet", 1<<30, open)
	if err != nil {
		t.Fatal(err)
	}

	// While referenced, the evictor must not unload it even past the TTL.
	now = now.Add(time.Hour)
	if p.sweep() != 0 {
		t.Fatal("must not evict a model with refs>0")
	}
	if m.(*fakeModel).isClosed() {
		t.Fatal("referenced model closed during sweep")
	}

	// Acquire again (reuse, no second open), then release both.
	m2, release2, _ := p.acquire("parakeet", 1<<30, open)
	if m2 != m {
		t.Error("acquire should reuse the resident model")
	}
	if opens != 1 {
		t.Errorf("open called %d times, want 1", opens)
	}
	release()
	release2()

	// Now unreferenced + idle → swept and closed.
	now = now.Add(2 * time.Minute)
	if p.sweep() != 1 {
		t.Fatal("idle unreferenced model should be evicted")
	}
	if !m.(*fakeModel).isClosed() {
		t.Error("evicted model should be Close()d")
	}

	// A fresh acquire reloads it.
	if _, rel, _ := p.acquire("parakeet", 1<<30, open); rel != nil {
		rel()
	}
	if opens != 2 {
		t.Errorf("expected reload, open called %d times", opens)
	}
}

func TestPoolReleaseIdempotent(t *testing.T) {
	p := newModelPool(time.Second, 0)
	_, release, _ := p.acquire("m", 1, func() (loadedModel, error) { return &fakeModel{}, nil })
	release()
	release() // must not drive refs negative
	p.mu.Lock()
	refs := p.entries["m"].refs
	p.mu.Unlock()
	if refs != 0 {
		t.Errorf("refs = %d, want 0", refs)
	}
}

func TestPoolMaxResidentEvictsLRU(t *testing.T) {
	now := time.Now()
	p := newModelPool(0, 2<<30) // budget = 2 GiB, no idle eviction
	p.now = func() time.Time { return now }
	open := func(name string) func() (loadedModel, error) {
		return func() (loadedModel, error) { return &fakeModel{name: name}, nil }
	}
	// Load A (1 GiB) and B (1 GiB) — both fit.
	a, relA, _ := p.acquire("A", 1<<30, open("A"))
	relA()
	now = now.Add(time.Second)
	b, relB, _ := p.acquire("B", 1<<30, open("B"))
	relB()
	now = now.Add(time.Second)
	// Loading C (1 GiB) would exceed 2 GiB → LRU (A) is evicted.
	_, relC, _ := p.acquire("C", 1<<30, open("C"))
	relC()

	if !a.(*fakeModel).isClosed() {
		t.Error("LRU model A should have been evicted to make room for C")
	}
	if b.(*fakeModel).isClosed() {
		t.Error("B is newer than A and should remain resident")
	}
}

func TestPoolMaxResidentNeverEvictsReferenced(t *testing.T) {
	p := newModelPool(0, 1<<30)
	open := func(name string) func() (loadedModel, error) {
		return func() (loadedModel, error) { return &fakeModel{name: name}, nil }
	}
	// A is held (refs=1) and fills the budget.
	a, _, _ := p.acquire("A", 1<<30, open("A")) // intentionally not released
	// Loading B would exceed budget, but A is referenced and can't be evicted;
	// the budget is best-effort, so B still loads.
	_, relB, err := p.acquire("B", 1<<30, open("B"))
	if err != nil {
		t.Fatal(err)
	}
	relB()
	if a.(*fakeModel).isClosed() {
		t.Error("referenced model A must never be evicted by the budget")
	}
}

func TestPoolOpenErrorPropagates(t *testing.T) {
	p := newModelPool(time.Second, 0)
	_, _, err := p.acquire("bad", 1, func() (loadedModel, error) { return nil, errors.New("boom") })
	if err == nil {
		t.Fatal("expected open error")
	}
	if _, err := p.pin("bad", func() (loadedModel, error) { return nil, errors.New("boom") }); err == nil {
		t.Fatal("expected pin open error")
	}
}

func TestPoolCloseAll(t *testing.T) {
	p := newModelPool(time.Minute, 0)
	m1, _ := p.pin("a", func() (loadedModel, error) { return &fakeModel{}, nil })
	m2, rel, _ := p.acquire("b", 1, func() (loadedModel, error) { return &fakeModel{}, nil })
	rel()
	p.closeAll()
	if !m1.(*fakeModel).isClosed() || !m2.(*fakeModel).isClosed() {
		t.Error("closeAll must close every model")
	}
	if len(p.entries) != 0 {
		t.Error("closeAll must empty the pool")
	}
}

func TestPoolConcurrentAcquireSafe(t *testing.T) {
	p := newModelPool(time.Minute, 0)
	var opens int32
	open := func() (loadedModel, error) {
		atomic.AddInt32(&opens, 1)
		return &fakeModel{}, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, rel, err := p.acquire("shared", 1, open)
			if err == nil && rel != nil {
				rel()
			}
		}()
	}
	wg.Wait()
	// All concurrent acquires must share one resident model (open races are
	// possible but the cache must converge to a single live entry).
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.entries) != 1 {
		t.Errorf("entries = %d, want 1", len(p.entries))
	}
}
