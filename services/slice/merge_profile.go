package sliceservice

import (
	"fmt"
	"log"
	"strings"
	"time"
)

type mergeProfile struct {
	changesetID       string
	sliceID           string
	modifiedFiles     int
	startedAt         time.Time
	conflictDuration  time.Duration
	revertDuration    time.Duration
	finalizeDuration  time.Duration
	promotionDuration time.Duration
	configDuration    time.Duration
	totalDuration     time.Duration
	conflictsFound    int
}

func newMergeProfile(changesetID, sliceID string, modifiedFiles int) *mergeProfile {
	return &mergeProfile{
		changesetID:   strings.TrimSpace(changesetID),
		sliceID:       strings.TrimSpace(sliceID),
		modifiedFiles: modifiedFiles,
		startedAt:     time.Now(),
	}
}

func (p *mergeProfile) markConflictCheck(conflicts int, duration time.Duration) {
	if p == nil {
		return
	}
	p.conflictsFound = conflicts
	p.conflictDuration = duration
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

func (p *mergeProfile) markPromotion(duration time.Duration) {
	if p == nil {
		return
	}
	p.promotionDuration = duration
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
		"Merge profile: changeset_id=%s slice_id=%s modified_files=%d conflicts=%d conflict_ms=%d revert_ms=%d finalize_ms=%d promotion_ms=%d config_ms=%d total_ms=%d",
		p.changesetID,
		p.sliceID,
		p.modifiedFiles,
		p.conflictsFound,
		p.conflictDuration.Milliseconds(),
		p.revertDuration.Milliseconds(),
		p.finalizeDuration.Milliseconds(),
		p.promotionDuration.Milliseconds(),
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
	log.Print(p.summary())
}
