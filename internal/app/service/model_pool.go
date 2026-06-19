package service

import (
	"context"
	"sync"
	"time"
)

// loadedModel is anything the pool owns the lifetime of — an ONNX-session-
// holding provider (ECAPA today; Parakeet/diarizer next). Close frees the
// native session.
type loadedModel interface {
	Close() error
}

// poolEntry tracks one loaded model and the bookkeeping the evictor needs.
type poolEntry struct {
	id       string
	model    loadedModel
	refs     int   // in-flight acquire()s; the evictor never unloads while >0
	pinned   bool  // pinned models are cached for the process lifetime
	bytes    int64 // estimated footprint, for the maxResident budget
	lastUsed time.Time
}

// modelPool gives multi-GB local models a lazy-load + idle-evict lifecycle so
// they don't all sit pinned in RAM at once. It layers ON TOP of each provider's
// own internal mutex around sess.Run: the pool only manages *when a model is
// resident*, never serializes inference itself.
//
//   - pin(id, open)             — load once, cache forever (tiny models, e.g. ECAPA)
//   - acquire(id, bytes, open)  — ref-counted + idle-evictable (heavy STT/diar models)
//
// The refs interlock is the safety property: a model is never Close()d while a
// transcribe holds it. Set idleTTL<=0 (NOTO_MODEL_KEEP_WARM) to disable idle
// eviction for an always-hot server; set maxBytes>0 to keep only one heavy
// model resident on a small machine (evict-LRU before loading another).
type modelPool struct {
	mu       sync.Mutex
	idleTTL  time.Duration
	maxBytes int64
	entries  map[string]*poolEntry
	now      func() time.Time // injectable clock for tests
}

func newModelPool(idleTTL time.Duration, maxBytes int64) *modelPool {
	return &modelPool{
		idleTTL:  idleTTL,
		maxBytes: maxBytes,
		entries:  map[string]*poolEntry{},
		now:      time.Now,
	}
}

// pin loads a model once and caches it for the process lifetime (never evicted).
// Used for small always-on models like ECAPA.
func (p *modelPool) pin(id string, open func() (loadedModel, error)) (loadedModel, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.entries[id]; ok && e.model != nil {
		e.lastUsed = p.now()
		e.pinned = true
		return e.model, nil
	}
	m, err := open()
	if err != nil {
		return nil, err
	}
	p.entries[id] = &poolEntry{id: id, model: m, pinned: true, lastUsed: p.now()}
	return m, nil
}

// acquire returns an evictable model (loading it on miss) and increments its
// ref count. The returned release MUST be called once the caller is done — it
// decrements the ref count so the idle evictor may later reclaim the model.
func (p *modelPool) acquire(id string, bytes int64, open func() (loadedModel, error)) (loadedModel, func(), error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.entries[id]; ok && e.model != nil {
		e.refs++
		e.lastUsed = p.now()
		return e.model, p.releaser(id), nil
	}
	// Make room under the resident budget before loading a new heavy model.
	p.evictForLocked(bytes)
	m, err := open()
	if err != nil {
		return nil, nil, err
	}
	p.entries[id] = &poolEntry{id: id, model: m, refs: 1, bytes: bytes, lastUsed: p.now()}
	return m, p.releaser(id), nil
}

func (p *modelPool) releaser(id string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			if e, ok := p.entries[id]; ok && e.refs > 0 {
				e.refs--
				e.lastUsed = p.now()
			}
		})
	}
}

// sweep evicts idle, unreferenced, unpinned models. Returns the count evicted.
func (p *modelPool) sweep() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idleTTL <= 0 {
		return 0
	}
	now := p.now()
	n := 0
	for id, e := range p.entries {
		if e.pinned || e.refs > 0 || e.model == nil {
			continue
		}
		if now.Sub(e.lastUsed) > p.idleTTL {
			_ = e.model.Close()
			delete(p.entries, id)
			n++
		}
	}
	return n
}

// runEvictor sweeps every idleTTL/2 until ctx is done. A no-op when idle
// eviction is disabled (idleTTL<=0).
func (p *modelPool) runEvictor(ctx context.Context) {
	if p.idleTTL <= 0 {
		return
	}
	interval := p.idleTTL / 2
	if interval <= 0 {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.sweep()
		}
	}
}

// closeAll unloads everything (called on service shutdown).
func (p *modelPool) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, e := range p.entries {
		if e.model != nil {
			_ = e.model.Close()
		}
		delete(p.entries, id)
	}
}

// --- locked helpers (caller holds p.mu) ---

func (p *modelPool) residentBytesLocked() int64 {
	var sum int64
	for _, e := range p.entries {
		if e.model != nil {
			sum += e.bytes
		}
	}
	return sum
}

// evictForLocked frees LRU evictable entries until `want` more bytes fit under
// maxBytes. No-op when maxBytes<=0 (unlimited).
func (p *modelPool) evictForLocked(want int64) {
	if p.maxBytes <= 0 {
		return
	}
	for p.residentBytesLocked()+want > p.maxBytes {
		victim := p.lruEvictableLocked()
		if victim == "" {
			return // nothing left to evict; let the load proceed (best-effort budget)
		}
		if e := p.entries[victim]; e != nil && e.model != nil {
			_ = e.model.Close()
		}
		delete(p.entries, victim)
	}
}

func (p *modelPool) lruEvictableLocked() string {
	oldest := ""
	var oldestT time.Time
	for id, e := range p.entries {
		if e.pinned || e.refs > 0 || e.model == nil {
			continue
		}
		if oldest == "" || e.lastUsed.Before(oldestT) {
			oldest, oldestT = id, e.lastUsed
		}
	}
	return oldest
}
