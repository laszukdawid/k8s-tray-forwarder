package kube

import (
	"strings"
	"testing"
)

func TestEnvAddsCommonBinDirs(t *testing.T) {
	// Simulate the minimal PATH a launchd/Finder-launched GUI app sees.
	t.Setenv("PATH", "/usr/bin")

	var path string
	for _, e := range Env() {
		if strings.HasPrefix(e, "PATH=") {
			path = strings.TrimPrefix(e, "PATH=")
		}
	}

	if !strings.Contains(path, "/opt/homebrew/bin") {
		t.Fatalf("expected /opt/homebrew/bin to be added to PATH, got %q", path)
	}
	if !strings.Contains(path, "/usr/local/bin") {
		t.Fatalf("expected /usr/local/bin to be added to PATH, got %q", path)
	}
	if !strings.Contains(path, "/usr/bin") {
		t.Fatalf("expected the original /usr/bin to be preserved, got %q", path)
	}
}

func TestEnvNoDuplicatesWhenPresent(t *testing.T) {
	t.Setenv("PATH", "/opt/homebrew/bin:/usr/local/bin:/opt/local/bin:/usr/bin:/bin")
	for _, e := range Env() {
		if strings.HasPrefix(e, "PATH=") {
			path := strings.TrimPrefix(e, "PATH=")
			if strings.Count(path, "/opt/homebrew/bin") != 1 {
				t.Fatalf("PATH should not duplicate existing entries, got %q", path)
			}
		}
	}
}
