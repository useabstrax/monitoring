package collect

import (
	"sync"
	"time"
)

const defaultStatfsTimeout = 10 * time.Second

// mountProbes measures filesystem capacity away from the sampling goroutine.
// statfs on a hung network mount can block forever in D-state. At most one
// probe per mount is ever outstanding, so a dead filesystem costs one thread
// rather than one more every sample.
type mountProbes struct {
	statfs  StatfsFunc
	timeout time.Duration
	spawn   func(func())

	mu sync.Mutex
	by map[string]*mountProbe
}

type mountProbe struct {
	cap        Capacity
	haveValue  bool
	measuredAt time.Time
	inFlight   bool
	startedAt  time.Time
	failure    string
}

type probeResult struct {
	Capacity
	Have    bool
	Hung    bool
	Waiting time.Duration
}

func newMountProbes(statfs StatfsFunc, timeout time.Duration, spawn func(func())) *mountProbes {
	if statfs == nil {
		statfs = platformStatfs
	}
	if timeout <= 0 {
		timeout = defaultStatfsTimeout
	}
	if spawn == nil {
		spawn = func(f func()) { go f() }
	}
	return &mountProbes{statfs: statfs, timeout: timeout, spawn: spawn, by: map[string]*mountProbe{}}
}

func (p *mountProbes) check(point string, now time.Time) probeResult {
	p.mu.Lock()
	e := p.by[point]
	if e == nil {
		e = &mountProbe{}
		p.by[point] = e
	}
	start := !e.inFlight
	if start {
		e.inFlight = true
		e.startedAt = now
	}
	res := e.snapshot(now, p.timeout)
	p.mu.Unlock()
	if !start {
		return res
	}
	p.spawn(func() { p.run(point, now) })
	p.mu.Lock()
	defer p.mu.Unlock()
	if e = p.by[point]; e == nil {
		return res
	}
	return e.snapshot(now, p.timeout)
}

func (e *mountProbe) snapshot(now time.Time, timeout time.Duration) probeResult {
	r := probeResult{Capacity: e.cap, Have: e.haveValue}
	if e.inFlight {
		r.Waiting = now.Sub(e.startedAt)
		r.Hung = r.Waiting >= timeout
	}
	return r
}

func (p *mountProbes) run(point string, startedAt time.Time) {
	cap, err := p.statfs(point)
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.by[point]
	if e == nil {
		return
	}
	e.inFlight = false
	if err != nil {
		e.failure = err.Error()
		return
	}
	e.failure = ""
	e.cap = cap
	e.haveValue = true
	e.measuredAt = startedAt
}

func (p *mountProbes) retain(live map[string]bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for point, e := range p.by {
		if !live[point] && !e.inFlight {
			delete(p.by, point)
		}
	}
}
