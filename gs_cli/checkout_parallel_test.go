package gscli

import (
	"sync/atomic"
	"testing"
)

func TestRunCheckoutJobsParallelizes(t *testing.T) {
	jobs := []int{1, 2, 3, 4}
	workerCount := 2

	var maxConcurrent int64
	var current int64
	ready := make(chan struct{}, len(jobs))
	gate := make(chan struct{})
	done := make(chan struct{})

	handler := func(_ int) {
		newCurrent := atomic.AddInt64(&current, 1)
		for {
			previous := atomic.LoadInt64(&maxConcurrent)
			if newCurrent <= previous {
				break
			}
			if atomic.CompareAndSwapInt64(&maxConcurrent, previous, newCurrent) {
				break
			}
		}

		ready <- struct{}{}
		<-gate
		atomic.AddInt64(&current, -1)
	}

	go func() {
		runCheckoutJobs(workerCount, jobs, handler)
		close(done)
	}()

	<-ready
	<-ready
	close(gate)
	<-done

	if atomic.LoadInt64(&maxConcurrent) < 2 {
		t.Fatalf("expected at least two concurrent workers, got %d", maxConcurrent)
	}
}
