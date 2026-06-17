package kube

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// commonBinDirs are PATH entries that a login/Finder-launched macOS GUI process
// typically does NOT inherit (launchd gives a minimal PATH). kubectl lives in
// one of these, and — crucially — so do the exec-auth helpers kubectl itself
// shells out to (e.g. `aws` for EKS `aws eks get-token`). We both resolve
// kubectl here and prepend these to the PATH of every kubectl subprocess so its
// children resolve too.
var commonBinDirs = []string{
	"/opt/homebrew/bin",
	"/usr/local/bin",
	"/opt/local/bin",
	"/usr/bin",
	"/bin",
}

// kubectlPath is resolved once. exec.LookPath uses the process PATH at startup,
// which is minimal under launchd; the commonBinDirs fallback covers that case.
var kubectlPath = resolveKubectl()

func resolveKubectl() string {
	if p, err := exec.LookPath("kubectl"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	for _, dir := range commonBinDirs {
		candidate := filepath.Join(dir, "kubectl")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return "kubectl" // last resort; produces a clear "not found" error
}

// Binary returns the resolved kubectl executable path.
func Binary() string { return kubectlPath }

// Env returns the current environment with commonBinDirs ensured on PATH, so
// kubectl and its exec-auth helpers are found regardless of how the app was
// launched (Terminal, Finder, or login item).
func Env() []string {
	env := os.Environ()
	current := os.Getenv("PATH")
	have := map[string]bool{}
	for _, d := range filepath.SplitList(current) {
		have[d] = true
	}
	var missing []string
	for _, d := range commonBinDirs {
		if !have[d] {
			missing = append(missing, d)
		}
	}
	if len(missing) == 0 {
		return env
	}
	sep := string(os.PathListSeparator)
	newPath := strings.TrimPrefix(current+sep+strings.Join(missing, sep), sep)

	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			out = append(out, "PATH="+newPath)
			replaced = true
			continue
		}
		out = append(out, e)
	}
	if !replaced {
		out = append(out, "PATH="+newPath)
	}
	return out
}
