package ids

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const RootSliceID = "root"
const ChangesetIDPrefix = "chg_"
const CommitIDPrefix = "cmt_"
const ChangesetVersionIDPrefix = "chgver_"
const ChangesetSnapshotIDPrefix = "chgsnap_"
const FileChangeIDPrefix = "fc_"
const MergeEventIDPrefix = "me_"
const DirectoryMoveIDPrefix = "dmv_"
const AgentRunnerIDPrefix = "agr_"

// GenerateSliceID creates a new opaque custom-slice ID.
func GenerateSliceID() string {
	return "sl_" + strings.ReplaceAll(uuid.New().String(), "-", "")
}

// GenerateCommitID creates an opaque synthetic commit ID.
func GenerateCommitID() string {
	return CommitIDPrefix + strings.ReplaceAll(uuid.New().String(), "-", "")
}

// GenerateInitialCommitID creates a deterministic initial commit ID for a slice.
func GenerateInitialCommitID(sliceID string) string {
	return CommitIDPrefix + "init_" + sanitizeIDPart(sliceID)
}

// IsInitialCommitID reports whether an ID is a deterministic slice-initial marker.
func IsInitialCommitID(commitID string) bool {
	return strings.HasPrefix(strings.TrimSpace(commitID), CommitIDPrefix+"init_")
}

// GenerateChangesetVersionHash creates an opaque version marker for changeset contents.
func GenerateChangesetVersionHash() string {
	return ChangesetVersionIDPrefix + strings.ReplaceAll(uuid.New().String(), "-", "")
}

// GenerateChangesetSnapshotID creates a deterministic ID for one changeset version.
func GenerateChangesetSnapshotID(changesetID string, version int64) string {
	return fmt.Sprintf("%s%s_v%d", ChangesetSnapshotIDPrefix, sanitizeIDPart(changesetID), version)
}

// GenerateFileChangeID creates a stable private ID for a path within one commit.
func GenerateFileChangeID(commitID, filePath string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(commitID) + "\x00" + strings.TrimSpace(filePath)))
	return FileChangeIDPrefix + hex.EncodeToString(sum[:])
}

// GenerateMergeEventID creates an opaque durable merge event ID.
func GenerateMergeEventID() string {
	return MergeEventIDPrefix + strings.ReplaceAll(uuid.New().String(), "-", "")
}

func GenerateDirectoryMoveID() string {
	return DirectoryMoveIDPrefix + strings.ReplaceAll(uuid.New().String(), "-", "")
}

func GenerateAgentRunnerID() string {
	return AgentRunnerIDPrefix + strings.ReplaceAll(uuid.New().String(), "-", "")
}

func sanitizeIDPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		valid := (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9')
		if valid {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "unknown"
	}
	return out
}
