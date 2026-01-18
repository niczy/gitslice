package fileservice

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	filev1 "github.com/niczy/gitslice/proto/file"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fileServiceServer struct {
	filev1.UnimplementedFileServiceServer
	storage storage.Storage
}

func newFileServiceServer(st storage.Storage) *fileServiceServer {
	return &fileServiceServer{storage: st}
}

// NewService constructs the file service implementation for use without gRPC.
func NewService(st storage.Storage) filev1.FileServiceServer {
	return newFileServiceServer(st)
}

func (s *fileServiceServer) ListEntries(ctx context.Context, req *filev1.ListEntriesRequest) (*filev1.ListEntriesResponse, error) {
	if req.SliceId == "" {
		return nil, status.Error(codes.InvalidArgument, "slice_id is required")
	}

	slice, err := s.storage.GetSlice(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}

	normalizedPath := cleanPath(req.Path)
	prefix := ""
	if normalizedPath != "" {
		prefix = normalizedPath + "/"
	}

	fileSet := make(map[string]bool, len(slice.Files))
	for _, file := range slice.Files {
		normalized := cleanPath(file)
		if normalized == "" {
			continue
		}
		fileSet[normalized] = true
	}

	contentByPath := fileContentIndex(s.storage, ctx, slice.ID)

	entriesByName := map[string]*filev1.DirectoryEntry{}
	for filePath := range fileSet {
		if prefix != "" && !strings.HasPrefix(filePath, prefix) {
			continue
		}

		remaining := strings.TrimPrefix(filePath, prefix)
		if remaining == "" {
			continue
		}

		parts := strings.Split(remaining, "/")
		name := parts[0]
		entryPath := name
		if normalizedPath != "" {
			entryPath = normalizedPath + "/" + name
		}

		entry, ok := entriesByName[name]
		if !ok {
			entry = &filev1.DirectoryEntry{
				Name: name,
				Path: entryPath,
			}
			entriesByName[name] = entry
		}

		if len(parts) == 1 {
			entry.Type = filev1.EntryType_ENTRY_TYPE_FILE
			entry.HasChildren = false
			if content, ok := contentByPath[filePath]; ok {
				entry.Size = contentSize(content)
			}
		} else {
			entry.Type = filev1.EntryType_ENTRY_TYPE_DIRECTORY
			entry.HasChildren = true
		}
	}

	if len(entriesByName) == 0 && normalizedPath != "" {
		if fileSet[normalizedPath] {
			return nil, status.Error(codes.FailedPrecondition, "path refers to a file")
		}
		return nil, status.Error(codes.NotFound, "path not found")
	}

	entries := make([]*filev1.DirectoryEntry, 0, len(entriesByName))
	for _, entry := range entriesByName {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})

	truncated := false
	if req.Limit > 0 && int(req.Limit) < len(entries) {
		entries = entries[:req.Limit]
		truncated = true
	}

	return &filev1.ListEntriesResponse{
		SliceId:   req.SliceId,
		Path:      normalizedPath,
		Entries:   entries,
		Truncated: truncated,
	}, nil
}

func (s *fileServiceServer) GetFile(ctx context.Context, req *filev1.GetFileRequest) (*filev1.GetFileResponse, error) {
	if req.SliceId == "" {
		return nil, status.Error(codes.InvalidArgument, "slice_id is required")
	}
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	slice, err := s.storage.GetSlice(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}

	normalizedPath := cleanPath(req.Path)
	contentByPath := fileContentIndex(s.storage, ctx, slice.ID)

	content, ok := contentByPath[normalizedPath]
	if !ok {
		if sliceHasPath(slice, normalizedPath) {
			return nil, status.Error(codes.NotFound, "file content not available")
		}
		return nil, status.Error(codes.NotFound, "file not found")
	}

	file := &filev1.File{
		Path:    normalizedPath,
		Content: content.Content,
		Size:    contentSize(content),
		Hash:    content.Hash,
	}

	return &filev1.GetFileResponse{File: file}, nil
}

func cleanPath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	cleaned := path.Clean("/" + trimmed)
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func fileContentIndex(st storage.Storage, ctx context.Context, sliceID string) map[string]*models.FileContent {
	files, err := st.GetSliceFiles(ctx, sliceID)
	if err != nil {
		return map[string]*models.FileContent{}
	}

	result := make(map[string]*models.FileContent, len(files))
	for _, file := range files {
		if file == nil {
			continue
		}
		filePath := cleanPath(file.Path)
		if filePath == "" {
			filePath = cleanPath(file.FileID)
		}
		if filePath == "" {
			continue
		}
		result[filePath] = file
	}
	return result
}

func contentSize(content *models.FileContent) int64 {
	if content == nil {
		return 0
	}
	if content.Size != 0 {
		return content.Size
	}
	return int64(len(content.Content))
}

func sliceHasPath(slice *models.Slice, path string) bool {
	for _, file := range slice.Files {
		if cleanPath(file) == path {
			return true
		}
	}
	return false
}
