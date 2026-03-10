package rootpromote

import (
	"context"
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
			SliceID:    "home.alice",
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
