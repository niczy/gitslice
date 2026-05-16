package benchmarksuite

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/storage"
)

type benchmarkPostgresPoolStatsProvider interface {
	PostgresPoolStats() storage.PostgresPoolStats
}

type benchmarkPostgresPoolObserver struct {
	provider benchmarkPostgresPoolStatsProvider
	start    storage.PostgresPoolStats
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once

	mu                  sync.Mutex
	maxAcquiredConns    int32
	maxIdleConns        int32
	maxTotalConns       int32
	maxConstructingConn int32
}

type benchmarkPostgresPoolReport struct {
	start               storage.PostgresPoolStats
	end                 storage.PostgresPoolStats
	maxAcquiredConns    int32
	maxIdleConns        int32
	maxTotalConns       int32
	maxConstructingConn int32
}

func startBenchmarkPostgresPoolObserver() *benchmarkPostgresPoolObserver {
	return startBenchmarkPostgresPoolObserverForStorage(benchStorage)
}

func startBenchmarkProjectionPostgresPoolObserver() *benchmarkPostgresPoolObserver {
	return startBenchmarkPostgresPoolObserverForStorage(benchProjectionStorage)
}

func startBenchmarkPostgresPoolObserverForStorage(st storage.Storage) *benchmarkPostgresPoolObserver {
	provider, ok := st.(benchmarkPostgresPoolStatsProvider)
	if !ok || provider == nil {
		return nil
	}
	observer := &benchmarkPostgresPoolObserver{
		provider: provider,
		start:    provider.PostgresPoolStats(),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	observer.observe(observer.start)
	go observer.run(50 * time.Millisecond)
	return observer
}

func (o *benchmarkPostgresPoolObserver) run(interval time.Duration) {
	defer close(o.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			o.observe(o.provider.PostgresPoolStats())
		case <-o.stop:
			return
		}
	}
}

func (o *benchmarkPostgresPoolObserver) stopAndReport() (benchmarkPostgresPoolReport, bool) {
	if o == nil {
		return benchmarkPostgresPoolReport{}, false
	}
	o.once.Do(func() {
		close(o.stop)
		<-o.done
	})
	end := o.provider.PostgresPoolStats()
	o.observe(end)

	o.mu.Lock()
	defer o.mu.Unlock()
	return benchmarkPostgresPoolReport{
		start:               o.start,
		end:                 end,
		maxAcquiredConns:    o.maxAcquiredConns,
		maxIdleConns:        o.maxIdleConns,
		maxTotalConns:       o.maxTotalConns,
		maxConstructingConn: o.maxConstructingConn,
	}, true
}

func (o *benchmarkPostgresPoolObserver) observe(stats storage.PostgresPoolStats) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.maxAcquiredConns = maxInt32(o.maxAcquiredConns, stats.AcquiredConns)
	o.maxIdleConns = maxInt32(o.maxIdleConns, stats.IdleConns)
	o.maxTotalConns = maxInt32(o.maxTotalConns, stats.TotalConns)
	o.maxConstructingConn = maxInt32(o.maxConstructingConn, stats.ConstructingConns)
}

func logBenchmarkPostgresPoolReport(t *testing.T, label string, report benchmarkPostgresPoolReport, ok bool) {
	t.Helper()
	if !ok {
		return
	}
	delta := subtractPostgresPoolStats(report.end, report.start)
	t.Logf("%s Postgres pool current: acquired=%d idle=%d total=%d constructing=%d max_conns=%d",
		label,
		report.end.AcquiredConns,
		report.end.IdleConns,
		report.end.TotalConns,
		report.end.ConstructingConns,
		report.end.MaxConns,
	)
	t.Logf("%s Postgres pool observed max: acquired=%d idle=%d total=%d constructing=%d",
		label,
		report.maxAcquiredConns,
		report.maxIdleConns,
		report.maxTotalConns,
		report.maxConstructingConn,
	)
	t.Logf("%s Postgres pool cumulative delta: acquire_count=%d empty_acquire_count=%d canceled_acquire_count=%d acquire_duration=%s empty_acquire_wait=%s new_conns=%d lifetime_destroy=%d idle_destroy=%d",
		label,
		delta.AcquireCount,
		delta.EmptyAcquireCount,
		delta.CanceledAcquireCount,
		delta.AcquireDuration,
		delta.EmptyAcquireWaitTime,
		delta.NewConnsCount,
		delta.MaxLifetimeDestroyCount,
		delta.MaxIdleDestroyCount,
	)
}

func subtractPostgresPoolStats(end, start storage.PostgresPoolStats) storage.PostgresPoolStats {
	return storage.PostgresPoolStats{
		AcquireCount:            end.AcquireCount - start.AcquireCount,
		AcquireDuration:         end.AcquireDuration - start.AcquireDuration,
		CanceledAcquireCount:    end.CanceledAcquireCount - start.CanceledAcquireCount,
		EmptyAcquireCount:       end.EmptyAcquireCount - start.EmptyAcquireCount,
		EmptyAcquireWaitTime:    end.EmptyAcquireWaitTime - start.EmptyAcquireWaitTime,
		NewConnsCount:           end.NewConnsCount - start.NewConnsCount,
		MaxLifetimeDestroyCount: end.MaxLifetimeDestroyCount - start.MaxLifetimeDestroyCount,
		MaxIdleDestroyCount:     end.MaxIdleDestroyCount - start.MaxIdleDestroyCount,
	}
}

func drainBenchmarkProjections(timeout time.Duration) (time.Duration, bool, error) {
	if benchProjectionWaiter == nil {
		return 0, false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	startedAt := time.Now()
	err := benchProjectionWaiter.WaitForQueuedProjections(ctx)
	return time.Since(startedAt), true, err
}

func maxInt32(left, right int32) int32 {
	if right > left {
		return right
	}
	return left
}
