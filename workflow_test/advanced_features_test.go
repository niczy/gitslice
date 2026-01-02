package workflow

import "testing"

func TestAdvancedFeaturePlaceholders(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"stash save", []string{"stash", "save", "WIP"}},
		{"stash list", []string{"stash", "list"}},
		{"stash apply", []string{"stash", "apply", "stash-0"}},
		{"stash drop", []string{"stash", "drop", "stash-0"}},
		{"import from git", []string{"import", "from-git", "./repo"}},
		{"export to git", []string{"export", "to-git"}},
		{"sync with git", []string{"sync", "with-git"}},
		{"hook list", []string{"hook", "list"}},
		{"hook run", []string{"hook", "run", "pre-commit"}},
		{"config show", []string{"config", "show"}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			assertUnsupportedCommand(t, tt.args...)
		})
	}
}
