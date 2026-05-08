package rootpromote

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestQueueBatchesJobs(t *testing.T) {
	var (
		mu      sync.Mutex
		batches [][]Job
	)
	q := New(20*time.Millisecond, 8, func(ctx context.Context, batch []Job) error {
		mu.Lock()
		defer mu.Unlock()
		cloned := make([]Job, 0, len(batch))
		for _, job := range batch {
			cloned = append(cloned, cloneJob(job))
		}
		batches = append(batches, cloned)
		return nil
	})

	for i := 0; i < 3; i++ {
		if err := q.Enqueue(context.Background(), Job{
			SliceID:    "home_alice",
			CommitHash: "c" + string(rune('1'+i)),
			Files:      []string{"alice/file.txt"},
			CommitTime: time.Unix(int64(i+1), 0),
		}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := q.Wait(waitCtx); err != nil {
		t.Fatalf("wait: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 1 {
		t.Fatalf("expected one batch, got %d", len(batches))
	}
	if len(batches[0]) != 3 {
		t.Fatalf("expected three jobs in the batch, got %d", len(batches[0]))
	}
}

func TestQueueEnqueueAndWaitReturnsProcessorError(t *testing.T) {
	expectedErr := errors.New("promotion failed")
	q := New(1*time.Millisecond, 8, func(ctx context.Context, batch []Job) error {
		return expectedErr
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := q.EnqueueAndWait(ctx, Job{
		SliceID:    "slice-a",
		CommitHash: "commit-a",
		Files:      []string{"a.txt"},
		CommitTime: time.Now(),
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected %v, got %v", expectedErr, err)
	}
}

func TestQueueEnqueueAndWaitBatchesConcurrentWaiters(t *testing.T) {
	var (
		mu      sync.Mutex
		batches [][]Job
	)
	q := New(20*time.Millisecond, 8, func(ctx context.Context, batch []Job) error {
		mu.Lock()
		defer mu.Unlock()
		cloned := make([]Job, 0, len(batch))
		for _, job := range batch {
			cloned = append(cloned, cloneJob(job))
		}
		batches = append(batches, cloned)
		return nil
	})

	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			errs <- q.EnqueueAndWait(ctx, Job{
				SliceID:    "slice-a",
				CommitHash: "c" + string(rune('1'+i)),
				Files:      []string{"a.txt"},
				CommitTime: time.Unix(int64(i+1), 0),
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("enqueue and wait: %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 1 {
		t.Fatalf("expected one batch, got %d", len(batches))
	}
	if len(batches[0]) != 3 {
		t.Fatalf("expected three jobs in the batch, got %d", len(batches[0]))
	}
}

func TestQueueProcessesDifferentShardsConcurrently(t *testing.T) {
	q := NewWithWorkers(1*time.Millisecond, 8, 2, nil)
	leftKey, rightKey := differentQueueShardKeys(t, q)

	started := make(chan string, 2)
	release := make(chan struct{})
	q.process = func(ctx context.Context, batch []Job) error {
		if len(batch) == 0 {
			t.Fatal("processor got empty batch")
		}
		started <- batch[0].ShardKey
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, key := range []string{leftKey, rightKey} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			errs <- q.EnqueueAndWait(ctx, Job{
				SliceID:    "slice-" + key,
				CommitHash: "commit-" + key,
				Files:      []string{key + "/file.txt"},
				CommitTime: time.Now(),
				ShardKey:   key,
			})
		}(key)
	}

	seen := make(map[string]struct{}, 2)
	deadline := time.After(time.Second)
	for len(seen) < 2 {
		select {
		case key := <-started:
			seen[key] = struct{}{}
		case <-deadline:
			t.Fatalf("expected both shards to start concurrently, saw %#v", seen)
		}
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("enqueue and wait: %v", err)
		}
	}
}

func differentQueueShardKeys(t *testing.T, q *Queue) (string, string) {
	t.Helper()
	left := "home-a"
	leftShard := q.shardIndex(Job{ShardKey: left})
	for i := 0; i < 100; i++ {
		right := "home-b-" + string(rune('a'+i))
		if q.shardIndex(Job{ShardKey: right}) != leftShard {
			return left, right
		}
	}
	t.Fatal("failed to find two keys on different shards")
	return "", ""
}
