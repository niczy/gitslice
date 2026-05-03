package common

import "testing"

func TestValidateFilePathAllowsProjectDirectoryNames(t *testing.T) {
	for _, path := range []string{
		"dev/dev-servers.sh",
		"etc/config.yaml",
		"root/docs/readme.md",
		"procfile",
	} {
		if err := ValidateFilePath(path); err != nil {
			t.Fatalf("ValidateFilePath(%q) failed: %v", path, err)
		}
	}
}

func TestValidateFilePathRejectsUnsafePaths(t *testing.T) {
	for _, path := range []string{
		"",
		"/etc/passwd",
		"../secrets.txt",
		"docs/../../secrets.txt",
		"~/secrets.txt",
		"docs/\x00secret.txt",
	} {
		if err := ValidateFilePath(path); err == nil {
			t.Fatalf("ValidateFilePath(%q) succeeded, want error", path)
		}
	}
}
