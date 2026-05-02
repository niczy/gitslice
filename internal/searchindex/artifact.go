package searchindex

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
)

var sliceArtifactMagic = [8]byte{'g', 's', 'i', 'd', 'x', 's', 'a', '1'}

const CurrentArtifactVersion uint32 = 2

type ArtifactInputFile struct {
	Path              string
	SearchContentHash string
	NGrams            []string
}

type SliceArtifactFile struct {
	Path              string
	SearchContentHash string
}

type SliceArtifactPosting struct {
	NGram       string
	FileIndexes []uint32
}

type SliceArtifact struct {
	Version    uint32
	SliceID    string
	CommitHash string
	Files      []SliceArtifactFile
	Postings   []SliceArtifactPosting
}

func BuildSliceArtifact(sliceID, commitHash string, files []ArtifactInputFile) *SliceArtifact {
	normalized := make([]ArtifactInputFile, 0, len(files))
	for _, file := range files {
		if file.Path == "" || file.SearchContentHash == "" {
			continue
		}
		file.NGrams = uniqueSortedStrings(append([]string(nil), file.NGrams...))
		normalized = append(normalized, file)
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].Path < normalized[j].Path
	})

	postings := make(map[string][]uint32)
	artifactFiles := make([]SliceArtifactFile, 0, len(normalized))
	for idx, file := range normalized {
		artifactFiles = append(artifactFiles, SliceArtifactFile{
			Path:              file.Path,
			SearchContentHash: file.SearchContentHash,
		})
		for _, gram := range file.NGrams {
			postings[gram] = append(postings[gram], uint32(idx))
		}
	}

	postingKeys := make([]string, 0, len(postings))
	for gram := range postings {
		postingKeys = append(postingKeys, gram)
	}
	sort.Strings(postingKeys)

	artifactPostings := make([]SliceArtifactPosting, 0, len(postingKeys))
	for _, gram := range postingKeys {
		artifactPostings = append(artifactPostings, SliceArtifactPosting{
			NGram:       gram,
			FileIndexes: append([]uint32(nil), postings[gram]...),
		})
	}

	return &SliceArtifact{
		Version:    CurrentArtifactVersion,
		SliceID:    sliceID,
		CommitHash: commitHash,
		Files:      artifactFiles,
		Postings:   artifactPostings,
	}
}

func EncodeSliceArtifact(artifact *SliceArtifact) ([]byte, error) {
	if artifact == nil {
		return nil, fmt.Errorf("artifact is nil")
	}

	var buf bytes.Buffer
	if _, err := buf.Write(sliceArtifactMagic[:]); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, artifact.Version); err != nil {
		return nil, err
	}
	if err := writeString(&buf, artifact.SliceID); err != nil {
		return nil, err
	}
	if err := writeString(&buf, artifact.CommitHash); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, uint32(len(artifact.Files))); err != nil {
		return nil, err
	}
	for _, file := range artifact.Files {
		if err := writeString(&buf, file.Path); err != nil {
			return nil, err
		}
		if err := writeString(&buf, file.SearchContentHash); err != nil {
			return nil, err
		}
	}
	if err := binary.Write(&buf, binary.BigEndian, uint32(len(artifact.Postings))); err != nil {
		return nil, err
	}
	for _, posting := range artifact.Postings {
		if err := writeString(&buf, posting.NGram); err != nil {
			return nil, err
		}
		if err := writeUint32Slice(&buf, posting.FileIndexes); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func DecodeSliceArtifact(data []byte) (*SliceArtifact, error) {
	reader := bytes.NewReader(data)
	var magic [8]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil {
		return nil, err
	}
	if magic != sliceArtifactMagic {
		return nil, fmt.Errorf("invalid slice artifact magic")
	}

	artifact := &SliceArtifact{}
	if err := binary.Read(reader, binary.BigEndian, &artifact.Version); err != nil {
		return nil, err
	}
	var err error
	artifact.SliceID, err = readString(reader)
	if err != nil {
		return nil, err
	}
	artifact.CommitHash, err = readString(reader)
	if err != nil {
		return nil, err
	}

	var fileCount uint32
	if err := binary.Read(reader, binary.BigEndian, &fileCount); err != nil {
		return nil, err
	}
	artifact.Files = make([]SliceArtifactFile, 0, fileCount)
	for i := uint32(0); i < fileCount; i++ {
		path, err := readString(reader)
		if err != nil {
			return nil, err
		}
		searchHash, err := readString(reader)
		if err != nil {
			return nil, err
		}
		artifact.Files = append(artifact.Files, SliceArtifactFile{
			Path:              path,
			SearchContentHash: searchHash,
		})
	}

	var postingCount uint32
	if err := binary.Read(reader, binary.BigEndian, &postingCount); err != nil {
		return nil, err
	}
	artifact.Postings = make([]SliceArtifactPosting, 0, postingCount)
	for i := uint32(0); i < postingCount; i++ {
		gram, err := readString(reader)
		if err != nil {
			return nil, err
		}
		indexes, err := readUint32Slice(reader)
		if err != nil {
			return nil, err
		}
		artifact.Postings = append(artifact.Postings, SliceArtifactPosting{
			NGram:       gram,
			FileIndexes: indexes,
		})
	}
	return artifact, nil
}
