package searchindex

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

var fileBlobMagic = [8]byte{'g', 's', 'i', 'd', 'x', 'f', 'b', '1'}

type FileBlob struct {
	Version           uint32
	SearchContentHash string
	ByteSize          uint64
	LineOffsets       []uint32
	NGrams            []string
}

func BuildFileBlob(content []byte, weighter BigramWeighter, mode SparseMode) (*FileBlob, error) {
	if !IsIndexableText(content) {
		return nil, ErrNonIndexableText
	}
	return &FileBlob{
		Version:           CurrentBlobVersion,
		SearchContentHash: SearchContentHash(content),
		ByteSize:          uint64(len(content)),
		LineOffsets:       BuildLineOffsets(content),
		NGrams:            UniqueNGramValues(BuildSparseNGrams(content, weighter, mode)),
	}, nil
}

func EncodeFileBlob(blob *FileBlob) ([]byte, error) {
	if blob == nil {
		return nil, fmt.Errorf("blob is nil")
	}

	var buf bytes.Buffer
	if _, err := buf.Write(fileBlobMagic[:]); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, blob.Version); err != nil {
		return nil, err
	}
	if err := writeString(&buf, blob.SearchContentHash); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, blob.ByteSize); err != nil {
		return nil, err
	}
	if err := writeUint32Slice(&buf, blob.LineOffsets); err != nil {
		return nil, err
	}
	if err := writeStringSlice(&buf, blob.NGrams); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func DecodeFileBlob(data []byte) (*FileBlob, error) {
	reader := bytes.NewReader(data)
	var magic [8]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil {
		return nil, err
	}
	if magic != fileBlobMagic {
		return nil, fmt.Errorf("invalid file blob magic")
	}

	blob := &FileBlob{}
	if err := binary.Read(reader, binary.BigEndian, &blob.Version); err != nil {
		return nil, err
	}
	var err error
	blob.SearchContentHash, err = readString(reader)
	if err != nil {
		return nil, err
	}
	if err := binary.Read(reader, binary.BigEndian, &blob.ByteSize); err != nil {
		return nil, err
	}
	blob.LineOffsets, err = readUint32Slice(reader)
	if err != nil {
		return nil, err
	}
	blob.NGrams, err = readStringSlice(reader)
	if err != nil {
		return nil, err
	}
	return blob, nil
}

func writeString(buf *bytes.Buffer, value string) error {
	if err := binary.Write(buf, binary.BigEndian, uint32(len(value))); err != nil {
		return err
	}
	_, err := buf.WriteString(value)
	return err
}

func readString(r io.Reader) (string, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return "", err
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return "", err
	}
	return string(data), nil
}

func writeUint32Slice(buf *bytes.Buffer, values []uint32) error {
	if err := binary.Write(buf, binary.BigEndian, uint32(len(values))); err != nil {
		return err
	}
	for _, value := range values {
		if err := binary.Write(buf, binary.BigEndian, value); err != nil {
			return err
		}
	}
	return nil
}

func readUint32Slice(r io.Reader) ([]uint32, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, err
	}
	values := make([]uint32, n)
	for i := uint32(0); i < n; i++ {
		if err := binary.Read(r, binary.BigEndian, &values[i]); err != nil {
			return nil, err
		}
	}
	return values, nil
}

func writeStringSlice(buf *bytes.Buffer, values []string) error {
	if err := binary.Write(buf, binary.BigEndian, uint32(len(values))); err != nil {
		return err
	}
	for _, value := range values {
		if err := writeString(buf, value); err != nil {
			return err
		}
	}
	return nil
}

func readStringSlice(r io.Reader) ([]string, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, err
	}
	values := make([]string, n)
	for i := uint32(0); i < n; i++ {
		value, err := readString(r)
		if err != nil {
			return nil, err
		}
		values[i] = value
	}
	return values, nil
}
