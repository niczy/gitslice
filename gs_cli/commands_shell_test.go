package gscli

import "testing"

func TestParseFilesystemShellEchoRedirect(t *testing.T) {
	content, filePath, ok, err := parseFilesystemShellEchoRedirect(`echo "hello world" > notes/todo.txt`)
	if err != nil {
		t.Fatalf("parseFilesystemShellEchoRedirect failed: %v", err)
	}
	if !ok {
		t.Fatal("expected echo redirect to be detected")
	}
	if content != "hello world" {
		t.Fatalf("unexpected content: %q", content)
	}
	if filePath != "notes/todo.txt" {
		t.Fatalf("unexpected file path: %q", filePath)
	}
}

func TestResolveFilesystemShellPath(t *testing.T) {
	tests := []struct {
		name    string
		cwd     string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "relative from root", cwd: "/testuser", raw: "src/main.go", want: "/testuser/src/main.go"},
		{name: "relative from cwd", cwd: "/testuser/src", raw: "utils.py", want: "/testuser/src/utils.py"},
		{name: "absolute", cwd: "/testuser/src", raw: "/testuser/README.md", want: "/testuser/README.md"},
		{name: "parent directory", cwd: "/testuser/src/nested", raw: "../main.go", want: "/testuser/src/main.go"},
		{name: "root", cwd: "/testuser/src", raw: "/", want: "/"},
		{name: "missing path", cwd: "/testuser", raw: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveFilesystemShellPath(tt.cwd, tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveFilesystemShellPath failed: %v", err)
			}
			if got != tt.want {
				t.Fatalf("unexpected path: got=%q want=%q", got, tt.want)
			}
		})
	}
}

func TestShellDisplayPath(t *testing.T) {
	if got := shellDisplayPath(""); got != "/" {
		t.Fatalf("unexpected root display path: %q", got)
	}
	if got := shellDisplayPath("/testuser/src/utils"); got != "/testuser/src/utils" {
		t.Fatalf("unexpected nested display path: %q", got)
	}
}
