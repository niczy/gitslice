package searchindex

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"sort"
	"unicode/utf8"
)

const CurrentBlobVersion uint32 = 2

var ErrNonIndexableText = errors.New("content is not indexable text")

type SparseMode int

const (
	SparseModeAll SparseMode = iota
	SparseModeCovering
)

type BigramWeighter interface {
	WeightPair(a, b byte) uint32
}

type HashBigramWeighter struct{}

func (HashBigramWeighter) WeightPair(a, b byte) uint32 {
	return crc32.ChecksumIEEE([]byte{a, b})
}

func DefaultWeighter() BigramWeighter {
	return HashBigramWeighter{}
}

func SearchContentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func BuildContentNGrams(content []byte) []string {
	if len(content) < 2 {
		return nil
	}
	values := make([]string, 0, len(content)-1)
	for i := 0; i < len(content)-1; i++ {
		values = append(values, string(content[i:i+2]))
	}
	return uniqueSortedStrings(values)
}

func IsBinaryContent(content []byte) bool {
	for _, b := range content {
		if b == 0 {
			return true
		}
	}
	return false
}

func IsIndexableText(content []byte) bool {
	if len(content) == 0 {
		return true
	}
	if IsBinaryContent(content) {
		return false
	}
	return utf8.Valid(content)
}

func BuildLineOffsets(content []byte) []uint32 {
	offsets := []uint32{0}
	for i, b := range content {
		if b == '\n' && i+1 <= len(content) {
			offsets = append(offsets, uint32(i+1))
		}
	}
	return offsets
}

func uniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	out := values[:0]
	var last string
	for i, value := range values {
		if i > 0 && value == last {
			continue
		}
		out = append(out, value)
		last = value
	}
	return out
}
