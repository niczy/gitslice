package main

import "strings"

func consumeBoolFlag(args []string, name string) ([]string, bool) {
	flagName := "--" + strings.TrimSpace(name)
	if flagName == "--" {
		return append([]string(nil), args...), false
	}

	remaining := make([]string, 0, len(args))
	found := false
	for _, arg := range args {
		if arg == flagName {
			found = true
			continue
		}
		remaining = append(remaining, arg)
	}
	return remaining, found
}
