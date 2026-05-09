package rootpromote

import (
	"context"
	"hash/fnv"
	"strings"
	"sync"
	"time"
)

type Job struct {
	SliceID            string
	CommitHash         string
	Files              []string
	CommitTime         time.Time
	ShardKey           string
	ProjectionShardID  int32
	ProjectionMergeSeq int64
}

type Processor func(context.Context, []Job) error

const (
	DefaultQueueSize    = 8192
	DefaultBatchWindow  = 100 * time.Millisecond
	DefaultBatchMaxSize = 256
	DefaultWorkerCount  = 1
)

type Queue struct {
	once         sync.Once
	shards       []chan queuedJob
	wg           sync.WaitGroup
	batchWindow  time.Duration
	batchMaxSize int
	workerCount  int
	process      Processor
}

type queuedJob struct {
	job  Job
	done chan error
}

func New(batchWindow time.Duration, batchMaxSize int, process Processor) *Queue {
	return NewWithWorkers(batchWindow, batchMaxSize, DefaultWorkerCount, process)
}

func NewWithWorkers(batchWindow time.Duration, batchMaxSize int, workerCount int, process Processor) *Queue {
	if batchWindow <= 0 {
		batchWindow = DefaultBatchWindow
	}
	if batchMaxSize <= 0 {
		batchMaxSize = DefaultBatchMaxSize
	}
	if workerCount <= 0 {
		workerCount = DefaultWorkerCount
	}
	shards := make([]chan queuedJob, workerCount)
	for i := range shards {
		shards[i] = make(chan queuedJob, DefaultQueueSize)
	}
	return &Queue{
		shards:       shards,
		batchWindow:  batchWindow,
		batchMaxSize: batchMaxSize,
		workerCount:  workerCount,
		process:      process,
	}
}

func (q *Queue) Enqueue(ctx context.Context, job Job) error {
	return q.enqueue(ctx, job, nil)
}

func (q *Queue) EnqueueAndWait(ctx context.Context, job Job) error {
	done := make(chan error, 1)
	if err := q.enqueue(ctx, job, done); err != nil {
		return err
	}

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *Queue) enqueue(ctx context.Context, job Job, done chan error) error {
	if q == nil || q.process == nil {
		return nil
	}

	q.once.Do(func() {
		for i := range q.shards {
			go q.run(q.shards[i])
		}
	})

	q.wg.Add(1)
	queued := queuedJob{job: cloneJob(job), done: done}
	shard := q.shards[q.shardIndex(queued.job)]
	select {
	case shard <- queued:
		return nil
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

func (q *Queue) run(jobs <-chan queuedJob) {
	for {
		current, ok := <-jobs
		if !ok {
			return
		}

		batch := []queuedJob{current}
		timer := time.NewTimer(q.batchWindow)
		collecting := true
		for collecting && len(batch) < q.batchMaxSize {
			select {
			case nextJob, open := <-jobs:
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

		jobs := make([]Job, 0, len(batch))
		for _, queued := range batch {
			jobs = append(jobs, queued.job)
		}
		err := q.process(context.Background(), jobs)
		for _, queued := range batch {
			completeQueuedJob(queued, err)
			q.wg.Done()
		}
	}
}

func (q *Queue) shardIndex(job Job) int {
	if q == nil || len(q.shards) <= 1 {
		return 0
	}
	key := strings.TrimSpace(job.ShardKey)
	if key == "" {
		key = strings.TrimSpace(job.SliceID)
	}
	if key == "" && len(job.Files) > 0 {
		key = strings.TrimSpace(job.Files[0])
	}
	if key == "" {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(len(q.shards)))
}

func completeQueuedJob(queued queuedJob, err error) {
	if queued.done == nil {
		return
	}
	queued.done <- err
}

func cloneJob(job Job) Job {
	return Job{
		SliceID:            job.SliceID,
		CommitHash:         job.CommitHash,
		Files:              append([]string(nil), job.Files...),
		CommitTime:         job.CommitTime,
		ShardKey:           job.ShardKey,
		ProjectionShardID:  job.ProjectionShardID,
		ProjectionMergeSeq: job.ProjectionMergeSeq,
	}
}

var globalMu sync.Mutex

func WithGlobalLock(fn func() error) error {
	globalMu.Lock()
	defer globalMu.Unlock()
	return fn()
}
