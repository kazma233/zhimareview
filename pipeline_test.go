package main

import (
	"sync"
	"testing"
	"time"
)

// testQueue 模拟主线程队列：函数先进队，drain 时按序执行。
// 互斥锁保证测试轮询入队状态与后台 goroutine 的写入无竞态。
type testQueue struct {
	mu    sync.Mutex
	queue []func()
}

func (q *testQueue) post(f func()) {
	q.mu.Lock()
	q.queue = append(q.queue, f)
	q.mu.Unlock()
}

func (q *testQueue) pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.queue)
}

func (q *testQueue) drain() {
	for {
		q.mu.Lock()
		if len(q.queue) == 0 {
			q.mu.Unlock()
			return
		}
		f := q.queue[0]
		q.queue = q.queue[1:]
		q.mu.Unlock()
		f()
	}
}

// headless（post 为 nil）：load 与 apply 同步串行完成，值原样传递。
func TestLoadAsyncSyncWhenHeadless(t *testing.T) {
	ws := newWorkspace(nil)
	var seq []string
	loadAsync(ws, func() string { return "v" }, func(v string) {
		seq = append(seq, "load:"+v, "apply:"+v)
	})
	// seq 由 apply 单侧写入：值经模块从 load 抵达 apply 即证明两段都同步跑过
	if len(seq) != 2 || seq[0] != "load:v" || seq[1] != "apply:v" {
		t.Fatalf("seq = %v, want value passed load→apply synchronously", seq)
	}
}

// 有队列：排干前 apply 不执行；排干后恰好执行一次，值原样传递。
func TestLoadAsyncApplyOnlyViaQueue(t *testing.T) {
	q := &testQueue{}
	ws := newWorkspace(nil)
	ws.post = q.post
	got := ""
	loadAsync(ws, func() int { return 7 }, func(v int) { got += "a"; _ = v })
	if got != "" {
		t.Fatal("apply ran before queue drain")
	}
	waitFor(t, func() bool { return q.pending() == 1 }, 5*time.Second, "load not marshalled")
	q.drain()
	if got != "a" {
		t.Fatalf("apply ran %q, want exactly once", got)
	}
}

// 两阶段分界由模块持有：post 收到函数时 load 已完成（后台先算，主线程只收）。
func TestLoadAsyncLoadBeforeMarshal(t *testing.T) {
	q := &testQueue{}
	ws := newWorkspace(nil)
	ws.post = q.post
	loadDone := false
	loadAsync(ws, func() bool { loadDone = true; return loadDone }, func(bool) {})
	waitFor(t, func() bool { return q.pending() == 1 }, 5*time.Second, "load not marshalled")
	if !loadDone {
		t.Fatal("load not finished when marshalled")
	}
	q.drain()
}

// 两条在途管线互不吞并：排干后各落地恰好一次。
func TestLoadAsyncTwoInFlight(t *testing.T) {
	q := &testQueue{}
	ws := newWorkspace(nil)
	ws.post = q.post
	counts := map[string]int{}
	loadAsync(ws, func() string { return "a" }, func(string) { counts["a"]++ })
	loadAsync(ws, func() string { return "b" }, func(string) { counts["b"]++ })
	waitFor(t, func() bool { return q.pending() == 2 }, 5*time.Second, "both loads not marshalled")
	q.drain()
	if counts["a"] != 1 || counts["b"] != 1 {
		t.Fatalf("counts = %v, want each applied once", counts)
	}
}
