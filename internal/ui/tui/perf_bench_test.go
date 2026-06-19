package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// BenchmarkContent measures one full frame build (what View does on a dirty
// frame). It's the cost we must NOT pay on every motion event.
func BenchmarkContent(b *testing.B) {
	m := testRootModel()
	m.content() // prime frameHits
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.content()
	}
}

// BenchmarkMotionSameCell is the steady-state firehose: the pointer rests or
// jitters within one element, so the hovered id never changes. AllMotion
// delivers a flood of these, and the runtime calls View after each — they must
// be served from cache (~no allocs, no rebuild), not rebuild the whole frame.
func BenchmarkMotionSameCell(b *testing.B) {
	m := testRootModel()
	_ = m.View() // prime the cache
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(tea.MouseMotionMsg{X: 1, Y: 1, Button: tea.MouseNone})
		_ = m.View()
	}
}

// A motion that leaves the hovered element unchanged must not dirty the frame:
// View then serves the cached content untouched, instead of re-rendering every
// screen, chip and hit region for a flood of AllMotion events that change
// nothing on screen. This is the regression guard for the render cache.
func TestMotionWithoutHoverChangeReusesFrame(t *testing.T) {
	m := testRootModel()
	first := m.View().Content // builds + caches, clears dirty

	// Two motions over the same dead cell: the hovered id can't change, so the
	// frame must stay clean and the next View must return the identical string.
	m.Update(tea.MouseMotionMsg{X: 0, Y: 0, Button: tea.MouseNone})
	m.Update(tea.MouseMotionMsg{X: 0, Y: 0, Button: tea.MouseNone})
	if m.dirty {
		t.Fatal("a no-op motion dirtied the frame; the render cache won't kick in")
	}
	if got := m.View().Content; got != first {
		t.Fatal("View rebuilt the frame for a motion that changed nothing")
	}
}

// Crossing onto a different element must dirty the frame so the new hover
// highlight actually renders — the cache must never swallow a real change.
func TestMotionOntoElementRebuilds(t *testing.T) {
	m := testRootModel()
	_ = m.View() // prime cache, dirty=false

	// Find a cell that resolves to some clickable region (a header nav pill is
	// always present), and move onto it.
	var hit bool
	for x := 0; x < m.width && !hit; x++ {
		if r, ok := m.regionAt(tea.Mouse{X: x, Y: 0}); ok && r.id != "" {
			m.Update(tea.MouseMotionMsg{X: x, Y: 0, Button: tea.MouseNone})
			hit = true
		}
	}
	if !hit {
		t.Skip("no clickable region on the header row to hover")
	}
	if !m.dirty {
		t.Fatal("moving the pointer onto a new element did not dirty the frame; hover wouldn't update")
	}
}
