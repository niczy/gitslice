package gscli

import (
	"testing"

	civ1 "github.com/niczy/gitslice/proto/ci"
)

func TestRunnerQueueWarningsDiagnoseCapacityAndImage(t *testing.T) {
	warnings := runnerQueueWarnings(
		"default",
		"golang:1.24",
		[]*civ1.RunnerPool{{
			Name:          "default",
			AllowedImages: []string{"node:22"},
		}},
		nil,
		[]*civ1.Job{{JobId: "job-1", RunnerPool: "default"}},
	)

	seen := map[string]bool{}
	for _, warning := range warnings {
		seen[warning["code"]] = true
	}
	for _, code := range []string{"no_online_runner", "no_registered_runner", "image_not_allowed"} {
		if !seen[code] {
			t.Fatalf("expected warning %s in %#v", code, warnings)
		}
	}
}

func TestRunnerQueueWarningsDetectMissingPool(t *testing.T) {
	warnings := runnerQueueWarnings("gpu", "", nil, nil, []*civ1.Job{{JobId: "job-1", RunnerPool: "gpu"}})
	if len(warnings) != 1 || warnings[0]["code"] != "missing_pool" {
		t.Fatalf("expected missing pool warning, got %#v", warnings)
	}
}
