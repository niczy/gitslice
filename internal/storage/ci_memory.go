package storage

import (
	"context"
	"sort"
	"strings"
	"time"
)

const defaultCIListLimit = 100

func (s *InMemoryStorage) CreateCIPlan(ctx context.Context, plan *CIPlan) error {
	_ = ctx
	if plan == nil || plan.Run == nil || strings.TrimSpace(plan.Run.ID) == "" {
		return ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	run := cloneCIRun(plan.Run)
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now()
	}
	if _, exists := s.ciRuns[run.ID]; exists {
		return ErrInvalidInput
	}
	s.ciRuns[run.ID] = run

	for _, manifest := range plan.Manifests {
		if manifest == nil || strings.TrimSpace(manifest.ID) == "" || strings.TrimSpace(manifest.RunID) != run.ID {
			return ErrInvalidInput
		}
		copyManifest := cloneCIRunManifest(manifest)
		s.ciRunManifests[copyManifest.ID] = copyManifest
		s.ciRunManifestIDs[run.ID] = append(s.ciRunManifestIDs[run.ID], copyManifest.ID)
	}
	for _, job := range plan.Jobs {
		if job == nil || strings.TrimSpace(job.ID) == "" || strings.TrimSpace(job.RunID) != run.ID {
			return ErrInvalidInput
		}
		copyJob := cloneCIJob(job)
		s.ciJobs[copyJob.ID] = copyJob
		s.ciJobIDsByRun[run.ID] = append(s.ciJobIDsByRun[run.ID], copyJob.ID)
	}
	for _, step := range plan.Steps {
		if step == nil || strings.TrimSpace(step.ID) == "" || strings.TrimSpace(step.JobID) == "" {
			return ErrInvalidInput
		}
		if _, ok := s.ciJobs[step.JobID]; !ok {
			return ErrInvalidInput
		}
		copyStep := cloneCIStep(step)
		s.ciSteps[copyStep.ID] = copyStep
		s.ciStepIDsByJob[copyStep.JobID] = append(s.ciStepIDsByJob[copyStep.JobID], copyStep.ID)
	}
	for _, check := range plan.Checks {
		if check == nil {
			return ErrInvalidInput
		}
		copyCheck := cloneCICheck(check)
		if copyCheck.UpdatedAt.IsZero() {
			copyCheck.UpdatedAt = time.Now()
		}
		s.ciChecks[ciCheckKey(copyCheck)] = copyCheck
	}

	sort.Strings(s.ciRunManifestIDs[run.ID])
	sort.Strings(s.ciJobIDsByRun[run.ID])
	for jobID := range s.ciStepIDsByJob {
		sort.Slice(s.ciStepIDsByJob[jobID], func(i, j int) bool {
			return s.ciSteps[s.ciStepIDsByJob[jobID][i]].StepIndex < s.ciSteps[s.ciStepIDsByJob[jobID][j]].StepIndex
		})
	}
	return nil
}

func (s *InMemoryStorage) GetCIRun(ctx context.Context, runID string) (*CIRun, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.ciRuns[strings.TrimSpace(runID)]
	if !ok {
		return nil, ErrEntryNotFound
	}
	return cloneCIRun(run), nil
}

func (s *InMemoryStorage) ListCIRuns(ctx context.Context, filter CIRunListFilter) ([]*CIRun, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := normalizeCIListLimit(filter.Limit)
	runs := make([]*CIRun, 0)
	for _, run := range s.ciRuns {
		if filter.HomeID != "" && run.HomeID != filter.HomeID {
			continue
		}
		if filter.ChangesetID != "" && run.ChangesetID != filter.ChangesetID {
			continue
		}
		if filter.ChangesetVersionID != "" && run.ChangesetVersionID != filter.ChangesetVersionID {
			continue
		}
		if filter.PlanHash != "" && run.PlanHash != filter.PlanHash {
			continue
		}
		if filter.Status != "" && run.Status != filter.Status {
			continue
		}
		runs = append(runs, cloneCIRun(run))
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
	if len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, nil
}

func (s *InMemoryStorage) UpdateCIRunStatus(ctx context.Context, runID string, status string, finishedAt *time.Time) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.ciRuns[strings.TrimSpace(runID)]
	if !ok {
		return ErrEntryNotFound
	}
	run.Status = strings.TrimSpace(status)
	run.FinishedAt = cloneTimePtr(finishedAt)
	return nil
}

func (s *InMemoryStorage) ListCIRunManifests(ctx context.Context, runID string) ([]*CIRunManifest, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := append([]string(nil), s.ciRunManifestIDs[strings.TrimSpace(runID)]...)
	out := make([]*CIRunManifest, 0, len(ids))
	for _, id := range ids {
		if manifest := s.ciRunManifests[id]; manifest != nil {
			out = append(out, cloneCIRunManifest(manifest))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ManifestPath < out[j].ManifestPath })
	return out, nil
}

func (s *InMemoryStorage) ListCIJobs(ctx context.Context, filter CIJobListFilter) ([]*CIJob, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := normalizeCIListLimit(filter.Limit)
	jobs := make([]*CIJob, 0)
	for _, job := range s.ciJobs {
		if filter.RunID != "" && job.RunID != filter.RunID {
			continue
		}
		if filter.RunnerID != "" && job.RunnerID != filter.RunnerID {
			continue
		}
		if filter.Pool != "" && job.RunnerPool != filter.Pool {
			continue
		}
		if filter.Status != "" && job.Status != filter.Status {
			continue
		}
		jobs = append(jobs, cloneCIJob(job))
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].RunID == jobs[j].RunID {
			if jobs[i].ManifestPath == jobs[j].ManifestPath {
				return jobs[i].JobKey < jobs[j].JobKey
			}
			return jobs[i].ManifestPath < jobs[j].ManifestPath
		}
		return jobs[i].RunID < jobs[j].RunID
	})
	if len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs, nil
}

func (s *InMemoryStorage) ListCISteps(ctx context.Context, jobID string) ([]*CIStep, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := append([]string(nil), s.ciStepIDsByJob[strings.TrimSpace(jobID)]...)
	out := make([]*CIStep, 0, len(ids))
	for _, id := range ids {
		if step := s.ciSteps[id]; step != nil {
			out = append(out, cloneCIStep(step))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StepIndex < out[j].StepIndex })
	return out, nil
}

func (s *InMemoryStorage) UpsertCICheck(ctx context.Context, check *CICheck) error {
	_ = ctx
	if check == nil {
		return ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	copyCheck := cloneCICheck(check)
	if copyCheck.UpdatedAt.IsZero() {
		copyCheck.UpdatedAt = time.Now()
	}
	s.ciChecks[ciCheckKey(copyCheck)] = copyCheck
	return nil
}

func (s *InMemoryStorage) ListCIChecks(ctx context.Context, changesetID string, changesetVersionID string, planHash string) ([]*CICheck, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*CICheck, 0)
	for _, check := range s.ciChecks {
		if changesetID != "" && check.ChangesetID != changesetID {
			continue
		}
		if changesetVersionID != "" && check.ChangesetVersionID != changesetVersionID {
			continue
		}
		if planHash != "" && check.PlanHash != planHash {
			continue
		}
		out = append(out, cloneCICheck(check))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PlanHash == out[j].PlanHash {
			if out[i].ManifestPath == out[j].ManifestPath {
				return out[i].JobKey < out[j].JobKey
			}
			return out[i].ManifestPath < out[j].ManifestPath
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (s *InMemoryStorage) ListCILogChunks(ctx context.Context, filter CILogChunkListFilter) ([]*CILogChunk, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := normalizeCIListLimit(filter.Limit)
	chunks := make([]*CILogChunk, 0)
	for _, chunk := range s.ciLogChunks {
		if filter.JobID != "" && chunk.JobID != filter.JobID {
			continue
		}
		if filter.RunID != "" && chunk.RunID != filter.RunID {
			continue
		}
		if chunk.ChunkIndex < filter.SinceChunk {
			continue
		}
		chunks = append(chunks, cloneCILogChunk(chunk))
	}
	sort.Slice(chunks, func(i, j int) bool {
		if chunks[i].JobID == chunks[j].JobID {
			return chunks[i].ChunkIndex < chunks[j].ChunkIndex
		}
		return chunks[i].CreatedAt.Before(chunks[j].CreatedAt)
	})
	if len(chunks) > limit {
		chunks = chunks[:limit]
	}
	return chunks, nil
}

func (s *InMemoryStorage) CreateCIRunner(ctx context.Context, runner *CIRunner) error {
	_ = ctx
	if runner == nil || strings.TrimSpace(runner.ID) == "" {
		return ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.ciRunners[runner.ID]; exists {
		return ErrInvalidInput
	}
	copyRunner := cloneCIRunner(runner)
	if copyRunner.CreatedAt.IsZero() {
		copyRunner.CreatedAt = time.Now()
	}
	s.ciRunners[copyRunner.ID] = copyRunner
	return nil
}

func (s *InMemoryStorage) GetCIRunner(ctx context.Context, runnerID string) (*CIRunner, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	runner := s.ciRunners[strings.TrimSpace(runnerID)]
	if runner == nil {
		return nil, ErrEntryNotFound
	}
	return cloneCIRunner(runner), nil
}

func (s *InMemoryStorage) ListCIRunners(ctx context.Context, filter CIRunnerListFilter) ([]*CIRunner, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := normalizeCIListLimit(filter.Limit)
	out := make([]*CIRunner, 0)
	for _, runner := range s.ciRunners {
		if filter.HomeID != "" && runner.HomeID != filter.HomeID {
			continue
		}
		if filter.Pool != "" && runner.Pool != filter.Pool {
			continue
		}
		if filter.Status != "" && runner.Status != filter.Status {
			continue
		}
		out = append(out, cloneCIRunner(runner))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStorage) UpdateCIRunnerStatus(ctx context.Context, runnerID string, status string, lastSeenAt *time.Time) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	runner := s.ciRunners[strings.TrimSpace(runnerID)]
	if runner == nil {
		return ErrEntryNotFound
	}
	runner.Status = strings.TrimSpace(status)
	runner.LastSeenAt = cloneTimePtr(lastSeenAt)
	return nil
}

func (s *InMemoryStorage) RevokeCIRunner(ctx context.Context, runnerID string, revokedAt time.Time) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	runner := s.ciRunners[strings.TrimSpace(runnerID)]
	if runner == nil {
		return ErrEntryNotFound
	}
	runner.Status = "revoked"
	runner.DisabledAt = &revokedAt
	runner.TokenHash = ""
	return nil
}

func ciCheckKey(check *CICheck) string {
	if check == nil {
		return ""
	}
	return strings.Join([]string{
		check.ChangesetID,
		check.ChangesetVersionID,
		check.PlanHash,
		check.ManifestPath,
		check.JobKey,
	}, "\x00")
}

func normalizeCIListLimit(limit int) int {
	if limit <= 0 {
		return defaultCIListLimit
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}

func cloneCIRun(src *CIRun) *CIRun {
	if src == nil {
		return nil
	}
	dst := *src
	dst.StartedAt = cloneTimePtr(src.StartedAt)
	dst.FinishedAt = cloneTimePtr(src.FinishedAt)
	return &dst
}

func cloneCIRunManifest(src *CIRunManifest) *CIRunManifest {
	if src == nil {
		return nil
	}
	dst := *src
	dst.MatchedPaths = append([]string(nil), src.MatchedPaths...)
	return &dst
}

func cloneCIJob(src *CIJob) *CIJob {
	if src == nil {
		return nil
	}
	dst := *src
	dst.LeaseExpiresAt = cloneTimePtr(src.LeaseExpiresAt)
	dst.StartedAt = cloneTimePtr(src.StartedAt)
	dst.FinishedAt = cloneTimePtr(src.FinishedAt)
	dst.DependsOnJobIDs = append([]string(nil), src.DependsOnJobIDs...)
	return &dst
}

func cloneCIStep(src *CIStep) *CIStep {
	if src == nil {
		return nil
	}
	dst := *src
	dst.StartedAt = cloneTimePtr(src.StartedAt)
	dst.FinishedAt = cloneTimePtr(src.FinishedAt)
	return &dst
}

func cloneCICheck(src *CICheck) *CICheck {
	if src == nil {
		return nil
	}
	dst := *src
	return &dst
}

func cloneCILogChunk(src *CILogChunk) *CILogChunk {
	if src == nil {
		return nil
	}
	dst := *src
	dst.Payload = append([]byte(nil), src.Payload...)
	return &dst
}

func cloneCIRunner(src *CIRunner) *CIRunner {
	if src == nil {
		return nil
	}
	dst := *src
	dst.Labels = append([]string(nil), src.Labels...)
	dst.LastSeenAt = cloneTimePtr(src.LastSeenAt)
	dst.DisabledAt = cloneTimePtr(src.DisabledAt)
	return &dst
}

func cloneTimePtr(src *time.Time) *time.Time {
	if src == nil {
		return nil
	}
	dst := *src
	return &dst
}
