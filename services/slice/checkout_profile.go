package sliceservice

import (
	"fmt"
	"log"
	"strings"
	"time"
)

type checkoutProfile struct {
	mode             string
	sliceID          string
	requestedCommit  string
	knownHashes      int
	startedAt        time.Time
	prepareDuration  time.Duration
	payloadDuration  time.Duration
	totalDuration    time.Duration
	fileCount        int
	manifestChunks   int
	blockChunks      int
	blockBytes       int64
	filePayloads     int
	filePayloadBytes int64
}

func newCheckoutProfile(mode, sliceID, requestedCommit string, knownHashes int) *checkoutProfile {
	return &checkoutProfile{
		mode:            strings.TrimSpace(mode),
		sliceID:         strings.TrimSpace(sliceID),
		requestedCommit: strings.TrimSpace(requestedCommit),
		knownHashes:     knownHashes,
		startedAt:       time.Now(),
	}
}

func (p *checkoutProfile) markPrepared(fileCount int, duration time.Duration) {
	if p == nil {
		return
	}
	p.fileCount = fileCount
	p.prepareDuration = duration
}

func (p *checkoutProfile) addManifestChunk(fileCount int) {
	if p == nil {
		return
	}
	p.manifestChunks++
	if p.fileCount == 0 {
		p.fileCount = fileCount
	}
}

func (p *checkoutProfile) addBlockPayload(size int) {
	if p == nil {
		return
	}
	p.blockChunks++
	p.blockBytes += int64(size)
}

func (p *checkoutProfile) addFilePayload(size int) {
	if p == nil {
		return
	}
	p.filePayloads++
	p.filePayloadBytes += int64(size)
}

func (p *checkoutProfile) finish(payloadDuration time.Duration) {
	if p == nil {
		return
	}
	p.payloadDuration = payloadDuration
	p.totalDuration = time.Since(p.startedAt)
}

func (p *checkoutProfile) summary() string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf(
		"Checkout profile: mode=%s slice_id=%s requested_commit=%s known_hashes=%d files=%d manifest_chunks=%d block_payloads=%d block_bytes=%d file_payloads=%d file_payload_bytes=%d prepare_ms=%d payload_ms=%d total_ms=%d",
		p.mode,
		p.sliceID,
		p.requestedCommit,
		p.knownHashes,
		p.fileCount,
		p.manifestChunks,
		p.blockChunks,
		p.blockBytes,
		p.filePayloads,
		p.filePayloadBytes,
		p.prepareDuration.Milliseconds(),
		p.payloadDuration.Milliseconds(),
		p.totalDuration.Milliseconds(),
	)
}

func (p *checkoutProfile) logResult(err error) {
	if p == nil {
		return
	}
	if err != nil {
		log.Printf("%s err=%v", p.summary(), err)
		return
	}
	log.Print(p.summary())
}
