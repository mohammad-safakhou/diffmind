package indexer

import "sync"

// builderFlight ensures that concurrent EnsureImage calls for the same
// image share a single underlying build. The standard library does
// not ship a singleflight, so we keep a small one here. Total surface
// is one map + one mutex; the data structure is freed as soon as the
// in-flight call returns.
var builderFlight = &flightGroup{m: map[string]*flightCall{}}

type flightGroup struct {
	mu sync.Mutex
	m  map[string]*flightCall
}

type flightCall struct {
	wg  sync.WaitGroup
	val any
	err error
}

func (g *flightGroup) Do(key string, fn func() (any, error)) (any, error, bool) {
	g.mu.Lock()
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err, true
	}
	c := &flightCall{}
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()

	return c.val, c.err, false
}
