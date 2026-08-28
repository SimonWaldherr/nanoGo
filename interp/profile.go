// interp/profile.go
package interp

import "sync"

// LineProfile counts how many times execution reached each source line's
// statements during one or more runs — the "how often" counterpart to
// Tracer's point-in-time event log. It is cheap to collect (one map
// increment per statement, see evalStmt in evaluator.go) and is meant for
// rendering a per-line execution heatmap: darker/hotter for lines hit more
// often. A single LineProfile can be shared across several Run/RunContext
// calls (e.g. once per benchmark iteration) to accumulate a combined count
// across all of them — see interp/loader or a host's own benchmark loop.
type LineProfile struct {
	mu     sync.Mutex
	counts map[int]uint64
}

// NewLineProfile creates an empty profile ready to be attached to an
// Interpreter via SetLineProfile.
func NewLineProfile() *LineProfile {
	return &LineProfile{counts: make(map[int]uint64)}
}

// hit records one statement execution at line. A nil receiver or a
// non-positive line (no resolvable position) is a silent no-op, matching
// how emitTrace already tolerates a nil tracer.
func (p *LineProfile) hit(line int) {
	if p == nil || line <= 0 {
		return
	}
	p.mu.Lock()
	p.counts[line]++
	p.mu.Unlock()
}

// Counts returns a snapshot copy of line -> hit count. Safe to call while
// the profile is still being written to by an active or concurrent run.
func (p *LineProfile) Counts() map[int]uint64 {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[int]uint64, len(p.counts))
	for line, count := range p.counts {
		out[line] = count
	}
	return out
}

// Reset clears all recorded counts so the same LineProfile instance can be
// reused for a fresh run without reattaching a new one.
func (p *LineProfile) Reset() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.counts = make(map[int]uint64)
	p.mu.Unlock()
}

// SetLineProfile installs (or removes with nil) the line-execution profiler
// for subsequent statement evaluations. Like SetTracer, this only affects
// evaluation from the point it's called — install it before Run/RunContext.
func (vm *Interpreter) SetLineProfile(profile *LineProfile) {
	vm.lineProfile.Store(profile)
	vm.refreshStmtHooks()
}

// LineProfileActive returns the currently installed line profiler, or nil
// if none is set.
func (vm *Interpreter) LineProfileActive() *LineProfile {
	return vm.lineProfile.Load()
}
