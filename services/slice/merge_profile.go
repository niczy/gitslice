package sliceservice

import (
	"fmt"
	"log"
	"strings"
	"time"
)

type mergeProfile struct {
	changesetID        string
	sliceID            string
	modifiedFiles      int
	startedAt          time.Time
	revertDuration     time.Duration
	finalizeDuration   time.Duration
	projectionDuration time.Duration
	configDuration     time.Duration
	totalDuration      time.Duration
}

func newMergeProfile(changesetID, sliceID string, modifiedFiles int) *mergeProfile {
	return &mergeProfile{
		changesetID:   strings.TrimSpace(changesetID),
		sliceID:       strings.TrimSpace(sliceID),
		modifiedFiles: modifiedFiles,
		startedAt:     time.Now(),
	}
}

func (p *mergeProfile) markRevertApply(duration time.Duration) {
	if p == nil {
		return
	}
	p.revertDuration = duration
}

func (p *mergeProfile) markFinalize(duration time.Duration) {
	if p == nil {
		return
	}
	p.finalizeDuration = duration
}

func (p *mergeProfile) markProjection(duration time.Duration) {
	if p == nil {
		return
	}
	p.projectionDuration = duration
}

func (p *mergeProfile) markConfig(duration time.Duration) {
	if p == nil {
		return
	}
	p.configDuration = duration
}

func (p *mergeProfile) finish() {
	if p == nil {
		return
	}
	p.totalDuration = time.Since(p.startedAt)
}

func (p *mergeProfile) summary() string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf(
		"Merge profile: changeset_id=%s slice_id=%s modified_files=%d revert_ms=%d finalize_ms=%d projection_ms=%d config_ms=%d total_ms=%d",
		p.changesetID,
		p.sliceID,
		p.modifiedFiles,
		p.revertDuration.Milliseconds(),
		p.finalizeDuration.Milliseconds(),
		p.projectionDuration.Milliseconds(),
		p.configDuration.Milliseconds(),
		p.totalDuration.Milliseconds(),
	)
}

func (p *mergeProfile) logResult(err error) {
	if p == nil {
		return
	}
	if err != nil {
		log.Printf("%s err=%v", p.summary(), err)
		return
	}
	if !shouldLogProfiles() {
		return
	}
	log.Print(p.summary())
}
