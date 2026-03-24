package searchindex

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

type testWeighter map[[2]byte]uint32

func (t testWeighter) WeightPair(a, b byte) uint32 {
	if weight, ok := t[[2]byte{a, b}]; ok {
		return weight
	}
	return 1
}

func TestSearchContentHashDependsOnBytes(t *testing.T) {
	first := SearchContentHash([]byte("hello\n"))
	second := SearchContentHash([]byte("hello\n"))
	third := SearchContentHash([]byte("hello!\n"))

	if first != second {
		t.Fatalf("expected identical hashes for identical bytes")
	}
	if first == third {
		t.Fatalf("expected different hashes for different bytes")
	}
}

func TestIsIndexableText(t *testing.T) {
	if !IsIndexableText([]byte("plain text\n")) {
		t.Fatalf("expected utf-8 text to be indexable")
	}
	if IsIndexableText([]byte("bad\x00text")) {
		t.Fatalf("expected binary content to be rejected")
	}
	if IsIndexableText([]byte{0xff, 0xfe, 0xfd}) {
		t.Fatalf("expected invalid utf-8 to be rejected")
	}
}

func TestBuildLineOffsets(t *testing.T) {
	got := BuildLineOffsets([]byte("one\ntwo\nthree"))
	want := []uint32{0, 4, 8}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected line offsets:\n got %#v\nwant %#v", got, want)
	}
}

func TestBuildSparseNGramsAll(t *testing.T) {
	weighter := testWeighter{
		{'a', 'b'}: 9,
		{'b', 'c'}: 1,
		{'c', 'd'}: 1,
		{'d', 'e'}: 1,
		{'e', 'f'}: 9,
	}
	got := UniqueNGramValues(BuildSparseNGrams([]byte("abcdef"), weighter, SparseModeAll))
	want := []string{"ab", "abc", "abcdef", "bc", "bcd", "cd", "cde", "de", "def", "ef"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected sparse n-grams:\n got %#v\nwant %#v", got, want)
	}
}

func TestBuildSparseNGramsCovering(t *testing.T) {
	weighter := testWeighter{
		{'a', 'b'}: 9,
		{'b', 'c'}: 1,
		{'c', 'd'}: 1,
		{'d', 'e'}: 1,
		{'e', 'f'}: 9,
	}
	got := UniqueNGramValues(BuildSparseNGrams([]byte("abcdef"), weighter, SparseModeCovering))
	want := []string{"abcdef"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected covering sparse n-grams:\n got %#v\nwant %#v", got, want)
	}
}

func TestBuildRegexQuery(t *testing.T) {
	query, err := BuildRegexQuery(`foo(bar|baz).*qux`, DefaultWeighter(), SparseModeCovering)
	if err != nil {
		t.Fatalf("BuildRegexQuery failed: %v", err)
	}
	if got, want := query.String(), `AND(TERM("foo"),TERM("ba"),TERM("qux"))`; got != want {
		t.Fatalf("unexpected query tree:\n got %s\nwant %s", got, want)
	}
}

func TestBuildRegexQueryCaseInsensitiveLiteralFallsBack(t *testing.T) {
	query, err := BuildRegexQuery(`(?i)hello`, DefaultWeighter(), SparseModeCovering)
	if err != nil {
		t.Fatalf("BuildRegexQuery failed: %v", err)
	}
	if query.Kind != QueryNodeTrue {
		t.Fatalf("expected case-insensitive literal to fall back to TRUE, got %+v", query)
	}
}

func TestBuildFileBlobRoundTrip(t *testing.T) {
	content := []byte("alpha\nbeta\ngamma\n")
	blob, err := BuildFileBlob(content, DefaultWeighter(), SparseModeCovering)
	if err != nil {
		t.Fatalf("BuildFileBlob failed: %v", err)
	}
	data, err := EncodeFileBlob(blob)
	if err != nil {
		t.Fatalf("EncodeFileBlob failed: %v", err)
	}
	decoded, err := DecodeFileBlob(data)
	if err != nil {
		t.Fatalf("DecodeFileBlob failed: %v", err)
	}
	if !reflect.DeepEqual(decoded, blob) {
		t.Fatalf("decoded blob mismatch:\n got %#v\nwant %#v", decoded, blob)
	}
}

func TestBuildFileBlobRejectsBinary(t *testing.T) {
	_, err := BuildFileBlob([]byte("bad\x00text"), DefaultWeighter(), SparseModeCovering)
	if !errors.Is(err, ErrNonIndexableText) {
		t.Fatalf("expected ErrNonIndexableText, got %v", err)
	}
}

func TestDecodeFileBlobRejectsInvalidMagic(t *testing.T) {
	_, err := DecodeFileBlob([]byte("not-a-blob"))
	if err == nil {
		t.Fatalf("expected invalid magic error")
	}
}

func TestBuildFileBlobStableHash(t *testing.T) {
	content := []byte("stable\n")
	blob, err := BuildFileBlob(content, DefaultWeighter(), SparseModeCovering)
	if err != nil {
		t.Fatalf("BuildFileBlob failed: %v", err)
	}
	if blob.SearchContentHash != SearchContentHash(content) {
		t.Fatalf("expected blob hash to match search content hash")
	}
}

func TestUniqueNGramValuesSortedAndDeduped(t *testing.T) {
	values := UniqueNGramValues([]SparseNGram{
		{Value: "def"},
		{Value: "abc"},
		{Value: "abc"},
	})
	if !reflect.DeepEqual(values, []string{"abc", "def"}) {
		t.Fatalf("unexpected unique values: %#v", values)
	}
}

func TestBuildFileBlobHasLineOffsets(t *testing.T) {
	blob, err := BuildFileBlob([]byte("a\nb\n"), DefaultWeighter(), SparseModeCovering)
	if err != nil {
		t.Fatalf("BuildFileBlob failed: %v", err)
	}
	if !reflect.DeepEqual(blob.LineOffsets, []uint32{0, 2, 4}) {
		t.Fatalf("unexpected line offsets: %#v", blob.LineOffsets)
	}
}

func TestEncodeFileBlobDeterministic(t *testing.T) {
	blob, err := BuildFileBlob([]byte("deterministic\n"), DefaultWeighter(), SparseModeCovering)
	if err != nil {
		t.Fatalf("BuildFileBlob failed: %v", err)
	}
	first, err := EncodeFileBlob(blob)
	if err != nil {
		t.Fatalf("first EncodeFileBlob failed: %v", err)
	}
	second, err := EncodeFileBlob(blob)
	if err != nil {
		t.Fatalf("second EncodeFileBlob failed: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("expected deterministic encoding")
	}
}
