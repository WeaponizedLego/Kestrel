package thumbnail

import (
	"container/heap"
	"sync"
)

// prefetcher is a goroutine pool that drains a priority queue of
// (path, tier) tasks, reads bytes from the pack, and inserts them
// into the cache. Lower tier numbers win: a TierViewport enqueue
// jumps ahead of a backlog of TierBackground speculation.
//
// The queue lives in this package — the upstream API is
// Provider.Prefetch which just forwards paths and a tier. Workers
// skip paths already cached so re-enqueues during scroll don't
// waste disk reads.
type prefetcher struct {
	provider *TieredProvider
	workers  int

	mu     sync.Mutex
	cond   *sync.Cond
	queue  prefetchHeap
	seq    uint64
	stopCh chan struct{}
	done   sync.WaitGroup
	closed bool
}

// prefetchTask bundles a path with its priority tier and a monotonic
// sequence so the heap is stable across equal priorities (FIFO within
// a tier).
type prefetchTask struct {
	path string
	tier Tier
	seq  uint64
}

// prefetchHeap is a min-heap ordered by (tier asc, seq asc).
type prefetchHeap []prefetchTask

func (h prefetchHeap) Len() int { return len(h) }
func (h prefetchHeap) Less(i, j int) bool {
	if h[i].tier != h[j].tier {
		return h[i].tier < h[j].tier
	}
	return h[i].seq < h[j].seq
}
func (h prefetchHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *prefetchHeap) Push(x any)         { *h = append(*h, x.(prefetchTask)) }
func (h *prefetchHeap) Pop() any {
	old := *h
	n := len(old)
	t := old[n-1]
	*h = old[:n-1]
	return t
}

func newPrefetcher(p *TieredProvider, workers int) *prefetcher {
	pf := &prefetcher{
		provider: p,
		workers:  workers,
		stopCh:   make(chan struct{}),
	}
	pf.cond = sync.NewCond(&pf.mu)
	heap.Init(&pf.queue)
	return pf
}

// start launches the worker goroutines. Call once.
func (pf *prefetcher) start() {
	for i := 0; i < pf.workers; i++ {
		pf.done.Add(1)
		go pf.loop()
	}
}

// stop signals workers to exit and waits for them.
func (pf *prefetcher) stop() {
	pf.mu.Lock()
	if pf.closed {
		pf.mu.Unlock()
		return
	}
	pf.closed = true
	close(pf.stopCh)
	pf.cond.Broadcast()
	pf.mu.Unlock()
	pf.done.Wait()
}

// enqueue pushes every path at the given tier. Already-cached paths
// are filtered inside the worker to keep enqueue cheap under bursts.
func (pf *prefetcher) enqueue(paths []string, tier Tier) {
	if len(paths) == 0 {
		return
	}
	pf.mu.Lock()
	if pf.closed {
		pf.mu.Unlock()
		return
	}
	for _, path := range paths {
		heap.Push(&pf.queue, prefetchTask{path: path, tier: tier, seq: pf.seq})
		pf.seq++
	}
	pf.mu.Unlock()
	pf.cond.Broadcast()
}

// loop is the worker body: pop the highest-priority task, load it,
// insert, repeat. Exits when stop() closes stopCh.
func (pf *prefetcher) loop() {
	defer pf.done.Done()
	for {
		task, ok := pf.next()
		if !ok {
			return
		}
		pf.handle(task)
	}
}

// next blocks until there's a task or the prefetcher is stopped.
func (pf *prefetcher) next() (prefetchTask, bool) {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	for pf.queue.Len() == 0 {
		if pf.closed {
			return prefetchTask{}, false
		}
		pf.cond.Wait()
	}
	t := heap.Pop(&pf.queue).(prefetchTask)
	return t, true
}

// handle loads bytes for the task and inserts them into the cache.
// Cache hits are skipped — re-enqueuing a visible thumbnail during
// scroll is common and must not trigger another pack read.
func (pf *prefetcher) handle(t prefetchTask) {
	if pf.provider.cache.Contains(t.path) {
		return
	}
	data, err := pf.provider.loadFromPack(t.path)
	if err != nil || data == nil {
		return
	}
	pf.provider.insert(t.path, data, t.tier)
}
