package rootpromote

import (
	"context"
	"sync"
	"time"
)

type Job struct {
	SliceID    string
	CommitHash string
	Files      []string
	CommitTime time.Time
}

type Processor func(context.Context, []Job) error

const (
	DefaultQueueSize    = 8192
	DefaultBatchWindow  = 100 * time.Millisecond
	DefaultBatchMaxSize = 256
)

type Queue struct {
	once         sync.Once
	jobs         chan Job
	wg           sync.WaitGroup
	batchWindow  time.Duration
	batchMaxSize int
	process      Processor
}

func New(batchWindow time.Duration, batchMaxSize int, process Processor) *Queue {
	if batchWindow <= 0 {
		batchWindow = DefaultBatchWindow
	}
	if batchMaxSize <= 0 {
		batchMaxSize = DefaultBatchMaxSize
	}
	return &Queue{
		jobs:         make(chan Job, DefaultQueueSize),
		batchWindow:  batchWindow,
		batchMaxSize: batchMaxSize,
		process:      process,
	}
}

func (q *Queue) Enqueue(ctx context.Context, job Job) error {
	if q == nil || q.process == nil {
		return nil
	}

	q.once.Do(func() {
		go q.run()
	})

	q.wg.Add(1)
	select {
	case q.jobs <- cloneJob(job):
		return nil
	default:
		defer q.wg.Done()
		return q.process(ctx, []Job{cloneJob(job)})
	case <-ctx.Done():
		q.wg.Done()
		return ctx.Err()
	}
}

func (q *Queue) Wait(ctx context.Context) error {
	if q == nil {
		return nil
	}

	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *Queue) run() {
	for {
		current, ok := <-q.jobs
		if !ok {
			return
		}

		batch := []Job{current}
		timer := time.NewTimer(q.batchWindow)
		collecting := true
		for collecting && len(batch) < q.batchMaxSize {
			select {
			case nextJob, open := <-q.jobs:
				if !open {
					collecting = false
					break
				}
				batch = append(batch, nextJob)
			case <-timer.C:
				collecting = false
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}

		_ = q.process(context.Background(), batch)
		for range batch {
			q.wg.Done()
		}
	}
}

func cloneJob(job Job) Job {
	return Job{
		SliceID:    job.SliceID,
		CommitHash: job.CommitHash,
		Files:      append([]string(nil), job.Files...),
		CommitTime: job.CommitTime,
	}
}

var globalMu sync.Mutex

func WithGlobalLock(fn func() error) error {
	globalMu.Lock()
	defer globalMu.Unlock()
	return fn()
}
