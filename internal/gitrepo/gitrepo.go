package gitrepo

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type File struct {
	Path          string
	Content       []byte
	Executable    bool
	SymlinkTarget string
}

func execGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return out, nil
}

func ProviderForURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.Contains(trimmed, "github.com") {
		return "github"
	}
	return "git"
}

func Clone(ctx context.Context, repoURL, branch, token string) (string, string, string, func(), error) {
	parentDir, err := os.MkdirTemp("", "gitslice-repo-")
	if err != nil {
		return "", "", "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(parentDir) }

	cloneDir := filepath.Join(parentDir, "repo")
	authURL := withToken(repoURL, token)
	args := []string{"clone", "--depth", "1"}
	if strings.TrimSpace(branch) != "" {
		args = append(args, "--branch", strings.TrimSpace(branch))
	}
	args = append(args, authURL, cloneDir)
	if _, err := execGit(ctx, parentDir, args...); err != nil {
		cleanup()
		return "", "", "", func() {}, err
	}

	resolvedBranch := strings.TrimSpace(branch)
	if resolvedBranch == "" {
		out, err := execGit(ctx, cloneDir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
		if err == nil {
			resolvedBranch = strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/")
		}
	}
	if resolvedBranch == "" {
		out, err := execGit(ctx, cloneDir, "rev-parse", "--abbrev-ref", "HEAD")
		if err == nil {
			resolvedBranch = strings.TrimSpace(string(out))
		}
	}
	head, err := HeadCommit(ctx, cloneDir)
	if err != nil {
		cleanup()
		return "", "", "", func() {}, err
	}
	return cloneDir, resolvedBranch, head, cleanup, nil
}

func HeadCommit(ctx context.Context, repoDir string) (string, error) {
	out, err := execGit(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func SnapshotWorktree(root string) ([]File, error) {
	files := make([]File, 0)
	err := filepath.WalkDir(root, func(absPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if absPath == root {
			return nil
		}
		relPath, err := filepath.Rel(root, absPath)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)
		if relPath == ".git" || strings.HasPrefix(relPath, ".git/") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := os.Lstat(absPath)
		if err != nil {
			return err
		}
		mode := info.Mode()
		switch {
		case mode&os.ModeSymlink != 0:
			target, err := os.Readlink(absPath)
			if err != nil {
				return err
			}
			files = append(files, File{
				Path:          relPath,
				Content:       []byte(target),
				SymlinkTarget: target,
			})
			return nil
		case d.IsDir():
			return nil
		default:
			content, err := os.ReadFile(absPath)
			if err != nil {
				return err
			}
			files = append(files, File{
				Path:       relPath,
				Content:    content,
				Executable: mode.Perm()&0o111 != 0,
			})
			return nil
		}
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func ResetWorktree(repoDir string) error {
	entries, err := os.ReadDir(repoDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(repoDir, name)); err != nil {
			return err
		}
	}
	return nil
}

func WriteFiles(root string, files []File) error {
	for _, file := range files {
		targetPath := filepath.Join(root, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		if err := os.RemoveAll(targetPath); err != nil {
			return err
		}
		if file.SymlinkTarget != "" {
			if err := os.Symlink(file.SymlinkTarget, targetPath); err != nil {
				return err
			}
			continue
		}
		mode := os.FileMode(0o644)
		if file.Executable {
			mode = 0o755
		}
		if err := os.WriteFile(targetPath, file.Content, mode); err != nil {
			return err
		}
	}
	return nil
}

func HasChanges(ctx context.Context, repoDir string) (bool, error) {
	out, err := execGit(ctx, repoDir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func CommitAndPush(ctx context.Context, repoDir, branch, message, username, token string) (string, bool, error) {
	if _, err := execGit(ctx, repoDir, "config", "user.name", username); err != nil {
		return "", false, err
	}
	if _, err := execGit(ctx, repoDir, "config", "user.email", fmt.Sprintf("%s@gitslice.local", username)); err != nil {
		return "", false, err
	}
	if _, err := execGit(ctx, repoDir, "add", "-A"); err != nil {
		return "", false, err
	}
	changed, err := HasChanges(ctx, repoDir)
	if err != nil {
		return "", false, err
	}
	if !changed {
		head, err := HeadCommit(ctx, repoDir)
		return head, false, err
	}
	if _, err := execGit(ctx, repoDir, "commit", "-m", message); err != nil {
		return "", false, err
	}

	if strings.TrimSpace(token) != "" {
		authURL, err := execGit(ctx, repoDir, "remote", "get-url", "origin")
		if err != nil {
			return "", false, err
		}
		if _, err := execGit(ctx, repoDir, "remote", "set-url", "origin", withToken(strings.TrimSpace(string(authURL)), token)); err != nil {
			return "", false, err
		}
	}
	if _, err := execGit(ctx, repoDir, "push", "origin", "HEAD:"+branch); err != nil {
		return "", false, err
	}
	head, err := HeadCommit(ctx, repoDir)
	if err != nil {
		return "", false, err
	}
	return head, true, nil
}

func withToken(rawURL, token string) string {
	trimmedURL := strings.TrimSpace(rawURL)
	trimmedToken := strings.TrimSpace(token)
	if trimmedURL == "" || trimmedToken == "" {
		return trimmedURL
	}
	if !strings.HasPrefix(trimmedURL, "https://") {
		if strings.HasPrefix(trimmedURL, "git@github.com:") {
			repoPath := strings.TrimPrefix(trimmedURL, "git@github.com:")
			return "https://x-access-token:" + trimmedToken + "@github.com/" + repoPath
		}
		return trimmedURL
	}
	if !strings.Contains(trimmedURL, "github.com/") {
		return trimmedURL
	}
	return strings.Replace(trimmedURL, "https://", "https://x-access-token:"+trimmedToken+"@", 1)
}
