package gscli

import (
	"runtime"
	"sync"
)

func checkoutWorkerCount(jobs int) int {
	if jobs <= 0 {
		return 0
	}

	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	if jobs < workers {
		return jobs
	}
	return workers
}

func runCheckoutJobs[T any](workerCount int, jobs []T, handler func(T)) {
	if workerCount < 1 {
		workerCount = 1
	}

	jobCh := make(chan T)
	var wg sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				handler(job)
			}
		}()
	}

	for _, job := range jobs {
		jobCh <- job
	}
	close(jobCh)
	wg.Wait()
}
