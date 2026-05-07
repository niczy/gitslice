package gscli

import "fmt"

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func versionString() string {
	return fmt.Sprintf("gs %s (commit %s, built %s)", version, commit, buildDate)
}

func printVersion() {
	fmt.Println(versionString())
}
