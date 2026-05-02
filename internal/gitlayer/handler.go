package gitlayer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/niczy/gitslice/internal/authresolver"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/gitrepo"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultBranch      = "main"
	sourceHeadFilename = ".gitslice-source-head"
	gitHeadFilename    = ".gitslice-git-head"
)

type Handler struct {
	st       storage.Storage
	cacheDir string
	mu       sync.Mutex
}

type route struct {
	sliceRef string
	suffix   string
}

func NewHandler(st storage.Storage, cacheDir string) *Handler {
	cacheDir = strings.TrimSpace(cacheDir)
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "gitslice-git-cache")
	}
	return &Handler{
		st:       st,
		cacheDir: cacheDir,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parsed, err := parseRoute(r.URL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	service := gitService(r, parsed.suffix)
	switch service {
	case "git-upload-pack":
	case "git-receive-pack":
	default:
		http.Error(w, "unsupported git service", http.StatusBadRequest)
		return
	}

	slice, err := h.resolveSlice(r.Context(), parsed.sliceRef)
	if err != nil {
		writeStatusError(w, err)
		return
	}
	id, err := authresolver.OptionalHTTPIdentity(r.Context(), h.st, r)
	if err != nil {
		writeGitAuthError(w, err)
		return
	}
	if !canReadSlice(slice, id) {
		if id == nil || strings.TrimSpace(id.Username) == "" {
			writeGitAuthChallenge(w, "authentication required")
			return
		}
		http.Error(w, "not authorized for slice", http.StatusForbidden)
		return
	}
	if service == "git-receive-pack" && !canWriteSlice(slice, id) {
		if id == nil || strings.TrimSpace(id.Username) == "" {
			writeGitAuthChallenge(w, "authentication required")
			return
		}
		http.Error(w, "not authorized to modify slice", http.StatusForbidden)
		return
	}

	if service == "git-receive-pack" {
		if err := h.serveReceivePack(w, r, slice, id, parsed.suffix); err != nil {
			log.Printf("gitlayer: git receive-pack failed for %s: %v", slice.ID, err)
			http.Error(w, "git push failed", http.StatusInternalServerError)
		}
		return
	}

	repoPath, repoName, err := h.ensureBareRepo(r.Context(), slice)
	if err != nil {
		log.Printf("gitlayer: failed to materialize slice %s: %v", slice.ID, err)
		http.Error(w, "failed to prepare git repository", http.StatusInternalServerError)
		return
	}
	_ = repoPath

	if err := h.serveBackend(w, r, repoName, parsed.suffix); err != nil {
		log.Printf("gitlayer: git http-backend failed for %s: %v", slice.ID, err)
		http.Error(w, "git backend failed", http.StatusInternalServerError)
		return
	}
}

func parseRoute(u *url.URL) (*route, error) {
	if u == nil {
		return nil, fmt.Errorf("invalid git URL")
	}
	rel := strings.TrimPrefix(u.EscapedPath(), "/git/")
	if rel == u.EscapedPath() || rel == "" {
		return nil, fmt.Errorf("git repository path is required")
	}
	idx := strings.LastIndex(rel, ".git")
	if idx < 0 {
		return nil, fmt.Errorf("git repository path must end with .git")
	}
	rawName := rel[:idx]
	rawSuffix := rel[idx+len(".git"):]
	if rawName == "" {
		return nil, fmt.Errorf("git repository name is required")
	}
	name, err := url.PathUnescape(rawName)
	if err != nil {
		return nil, fmt.Errorf("invalid git repository name")
	}
	suffix, err := url.PathUnescape(rawSuffix)
	if err != nil {
		return nil, fmt.Errorf("invalid git repository suffix")
	}
	name = strings.Trim(strings.TrimSpace(name), "/")
	if name == "" || strings.Contains(name, "\\") {
		return nil, fmt.Errorf("invalid git repository name")
	}
	parts := strings.Split(name, "/")
	if len(parts) > 2 {
		return nil, fmt.Errorf("invalid git repository name")
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" || part == "." || part == ".." {
			return nil, fmt.Errorf("invalid git repository name")
		}
	}
	if suffix == "" {
		suffix = "/"
	}
	if !strings.HasPrefix(suffix, "/") {
		suffix = "/" + suffix
	}
	return &route{sliceRef: name, suffix: suffix}, nil
}

func gitService(r *http.Request, suffix string) string {
	if r == nil {
		return ""
	}
	service := strings.TrimSpace(r.URL.Query().Get("service"))
	if service != "" {
		return service
	}
	if strings.Contains(suffix, "git-upload-pack") {
		return "git-upload-pack"
	}
	if strings.Contains(suffix, "git-receive-pack") {
		return "git-receive-pack"
	}
	return ""
}

func (h *Handler) resolveSlice(ctx context.Context, ref string) (*models.Slice, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, status.Error(codes.InvalidArgument, "slice is required")
	}
	if slice, err := h.st.GetSliceBySlug(ctx, ref); err == nil {
		return slice, nil
	} else if err != storage.ErrSliceNotFound {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load slice by slug: %v", err))
	}
	slice, err := h.st.GetSlice(ctx, ref)
	if err != nil {
		if err == storage.ErrSliceNotFound {
			return nil, status.Error(codes.NotFound, "slice not found")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load slice: %v", err))
	}
	return slice, nil
}

func canReadSlice(slice *models.Slice, id *authresolver.Identity) bool {
	if slice == nil {
		return false
	}
	if slice.Visibility == models.VisibilityPublic {
		return true
	}
	username := ""
	if id != nil {
		username = strings.TrimSpace(id.Username)
	}
	if username == "" {
		return false
	}
	if slice.IsRoot {
		return true
	}
	if slice.CreatedBy == username {
		return true
	}
	for _, owner := range slice.Owners {
		if owner == username {
			return true
		}
	}
	return false
}

func canWriteSlice(slice *models.Slice, id *authresolver.Identity) bool {
	username := ""
	if id != nil {
		username = strings.TrimSpace(id.Username)
	}
	if slice == nil || username == "" || slice.IsRoot {
		return false
	}
	if slice.CreatedBy == username {
		return true
	}
	for _, owner := range slice.Owners {
		if owner == username {
			return true
		}
	}
	return false
}

func (h *Handler) ensureBareRepo(ctx context.Context, slice *models.Slice) (string, string, error) {
	if slice == nil {
		return "", "", fmt.Errorf("slice is nil")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ensureBareRepoLocked(ctx, slice)
}

func (h *Handler) ensureBareRepoLocked(ctx context.Context, slice *models.Slice) (string, string, error) {
	if err := os.MkdirAll(h.cacheDir, 0o755); err != nil {
		return "", "", err
	}
	meta, err := h.st.GetSliceMetadata(ctx, slice.ID)
	if err != nil {
		return "", "", err
	}

	repoName := cacheRepoName(slice.ID)
	repoPath := filepath.Join(h.cacheDir, repoName)
	sourceHead := strings.TrimSpace(meta.HeadCommitHash)
	if sourceHead == "" {
		sourceHead = "empty"
	}
	if cachedHead, readErr := os.ReadFile(filepath.Join(repoPath, sourceHeadFilename)); readErr == nil && strings.TrimSpace(string(cachedHead)) == sourceHead {
		return repoPath, repoName, nil
	}

	files, err := collectSliceFiles(ctx, h.st, slice.ID)
	if err != nil {
		return "", "", err
	}
	parentDir, err := os.MkdirTemp(h.cacheDir, "materialize-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(parentDir)

	worktree := filepath.Join(parentDir, "worktree")
	bareTmp := filepath.Join(parentDir, repoName)
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		return "", "", err
	}
	if _, err := runGit(ctx, worktree, "init", "-b", defaultBranch); err != nil {
		return "", "", err
	}
	if _, err := runGit(ctx, worktree, "config", "user.name", "gitslice"); err != nil {
		return "", "", err
	}
	if _, err := runGit(ctx, worktree, "config", "user.email", "git@gitslice.local"); err != nil {
		return "", "", err
	}
	if err := gitrepo.WriteFiles(worktree, files); err != nil {
		return "", "", err
	}
	if _, err := runGit(ctx, worktree, "add", "-A"); err != nil {
		return "", "", err
	}
	if _, err := runGit(ctx, worktree, "commit", "--allow-empty", "-m", fmt.Sprintf("materialize slice %s", slice.ID)); err != nil {
		return "", "", err
	}
	if _, err := runGit(ctx, parentDir, "init", "--bare", bareTmp); err != nil {
		return "", "", err
	}
	if _, err := runGit(ctx, worktree, "remote", "add", "origin", bareTmp); err != nil {
		return "", "", err
	}
	if _, err := runGit(ctx, worktree, "push", "origin", defaultBranch); err != nil {
		return "", "", err
	}
	if _, err := runGit(ctx, "", "--git-dir="+bareTmp, "symbolic-ref", "HEAD", "refs/heads/"+defaultBranch); err != nil {
		return "", "", err
	}
	if _, err := runGit(ctx, "", "--git-dir="+bareTmp, "config", "http.receivepack", "true"); err != nil {
		return "", "", err
	}
	gitHead, err := gitHead(ctx, bareTmp)
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(bareTmp, sourceHeadFilename), []byte(sourceHead+"\n"), 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(bareTmp, gitHeadFilename), []byte(gitHead+"\n"), 0o644); err != nil {
		return "", "", err
	}

	if err := os.RemoveAll(repoPath); err != nil {
		return "", "", err
	}
	if err := os.Rename(bareTmp, repoPath); err != nil {
		return "", "", err
	}
	return repoPath, repoName, nil
}

func (h *Handler) serveBackend(w http.ResponseWriter, r *http.Request, repoName, suffix string) error {
	payload, err := h.runBackend(r, repoName, suffix)
	if err != nil {
		return err
	}
	return writeCGIResponse(w, payload)
}

func (h *Handler) serveReceivePack(w http.ResponseWriter, r *http.Request, slice *models.Slice, id *authresolver.Identity, suffix string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	repoPath, repoName, err := h.ensureBareRepoLocked(r.Context(), slice)
	if err != nil {
		return fmt.Errorf("prepare bare repo: %w", err)
	}
	before, _ := readCachedGitHead(repoPath)
	if before == "" {
		before, _ = gitHead(r.Context(), repoPath)
	}

	payload, err := h.runBackend(r, repoName, suffix)
	if err != nil {
		return err
	}
	statusCode := cgiStatusCode(payload)
	if statusCode >= 200 && statusCode < 300 && r.Method == http.MethodPost {
		after, headErr := gitHead(r.Context(), repoPath)
		if headErr != nil {
			return fmt.Errorf("resolve pushed head: %w", headErr)
		}
		if strings.TrimSpace(after) != "" && after != before {
			if err := h.importBareRepo(r.Context(), slice, repoPath, after, id.Username); err != nil {
				return err
			}
			meta, err := h.st.GetSliceMetadata(r.Context(), slice.ID)
			if err != nil {
				return fmt.Errorf("load imported slice metadata: %w", err)
			}
			if err := os.WriteFile(filepath.Join(repoPath, sourceHeadFilename), []byte(strings.TrimSpace(meta.HeadCommitHash)+"\n"), 0o644); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(repoPath, gitHeadFilename), []byte(after+"\n"), 0o644); err != nil {
				return err
			}
		}
	}
	return writeCGIResponse(w, payload)
}

func (h *Handler) runBackend(r *http.Request, repoName, suffix string) ([]byte, error) {
	pathInfo := "/" + repoName
	if suffix != "/" {
		pathInfo += suffix
	}

	cmd := exec.CommandContext(r.Context(), "git", "http-backend")
	cmd.Stdin = r.Body
	cmd.Env = append(os.Environ(),
		"GIT_PROJECT_ROOT="+h.cacheDir,
		"GIT_HTTP_EXPORT_ALL=1",
		"REQUEST_METHOD="+r.Method,
		"PATH_INFO="+pathInfo,
		"SCRIPT_NAME=/git",
		"QUERY_STRING="+r.URL.RawQuery,
		"CONTENT_TYPE="+r.Header.Get("Content-Type"),
		"CONTENT_LENGTH="+r.Header.Get("Content-Length"),
		"REMOTE_USER=gitslice",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func cgiStatusCode(payload []byte) int {
	reader := bufio.NewReader(bytes.NewReader(payload))
	tp := textproto.NewReader(reader)
	headers, err := tp.ReadMIMEHeader()
	if err != nil {
		return http.StatusInternalServerError
	}
	if statusValue := strings.TrimSpace(headers.Get("Status")); statusValue != "" {
		parts := strings.Fields(statusValue)
		if len(parts) > 0 {
			if parsed, parseErr := strconv.Atoi(parts[0]); parseErr == nil {
				return parsed
			}
		}
	}
	return http.StatusOK
}

func writeCGIResponse(w http.ResponseWriter, payload []byte) error {
	reader := bufio.NewReader(bytes.NewReader(payload))
	tp := textproto.NewReader(reader)
	headers, err := tp.ReadMIMEHeader()
	if err != nil {
		return err
	}
	statusCode := http.StatusOK
	if statusValue := strings.TrimSpace(headers.Get("Status")); statusValue != "" {
		parts := strings.Fields(statusValue)
		if len(parts) > 0 {
			if parsed, parseErr := strconv.Atoi(parts[0]); parseErr == nil {
				statusCode = parsed
			}
		}
		headers.Del("Status")
	}
	for key, values := range headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(statusCode)
	_, err = io.Copy(w, reader)
	return err
}

func collectSliceFiles(ctx context.Context, st storage.Storage, sliceID string) ([]gitrepo.File, error) {
	entries, err := collectEntries(ctx, st, sliceID)
	if err != nil {
		return nil, err
	}
	files := make([]gitrepo.File, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.Type != "file" {
			continue
		}
		content, err := storage.ReadSliceFileContent(ctx, st, sliceID, entry.Path)
		if err != nil {
			return nil, err
		}
		files = append(files, gitrepo.File{
			Path:          entry.Path,
			Content:       content.Content,
			Executable:    entry.Executable,
			SymlinkTarget: entry.SymlinkTarget,
		})
	}
	return files, nil
}

func collectEntries(ctx context.Context, st storage.Storage, sliceID string) ([]*models.DirectoryEntry, error) {
	result := make([]*models.DirectoryEntry, 0)
	seen := make(map[string]struct{})
	var walk func(parentID string) error
	walk = func(parentID string) error {
		children, err := st.ListEntries(ctx, sliceID, parentID)
		if err != nil {
			return err
		}
		for _, child := range children {
			if child == nil {
				continue
			}
			key := child.ID + "|" + child.Path
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, child)
			if child.Type == "directory" {
				if err := walk(child.ID); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(sliceID); err != nil {
		return nil, err
	}
	return result, nil
}

func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return out, nil
}

func gitHead(ctx context.Context, bareRepo string) (string, error) {
	out, err := runGit(ctx, "", "--git-dir="+bareRepo, "rev-parse", "refs/heads/"+defaultBranch)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func readCachedGitHead(repoPath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repoPath, gitHeadFilename))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func cacheRepoName(sliceID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(sliceID)))
	return "slice-" + hex.EncodeToString(sum[:12]) + ".git"
}

func cleanGitPath(raw string) (string, error) {
	cleaned := common.CleanRelativePath(strings.ReplaceAll(raw, "\\", "/"))
	if cleaned == "" {
		return "", nil
	}
	if path.IsAbs(cleaned) || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("invalid path %q", raw)
	}
	if err := common.ValidateFilePath(cleaned); err != nil {
		return "", err
	}
	return cleaned, nil
}

func writeStatusError(w http.ResponseWriter, err error) {
	code := status.Code(err)
	switch code {
	case codes.InvalidArgument:
		http.Error(w, status.Convert(err).Message(), http.StatusBadRequest)
	case codes.NotFound:
		http.Error(w, status.Convert(err).Message(), http.StatusNotFound)
	case codes.Unauthenticated:
		http.Error(w, status.Convert(err).Message(), http.StatusUnauthorized)
	case codes.PermissionDenied:
		http.Error(w, status.Convert(err).Message(), http.StatusForbidden)
	default:
		http.Error(w, status.Convert(err).Message(), http.StatusInternalServerError)
	}
}

func writeGitAuthError(w http.ResponseWriter, err error) {
	if status.Code(err) == codes.Unauthenticated {
		writeGitAuthChallenge(w, "authentication required")
		return
	}
	writeStatusError(w, err)
}

func writeGitAuthChallenge(w http.ResponseWriter, message string) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Gitslice Git"`)
	http.Error(w, message, http.StatusUnauthorized)
}
