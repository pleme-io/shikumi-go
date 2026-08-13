package shikumi

import (
	"path/filepath"
	"strings"
	"testing"
)

// The masked-branch invariant: every environment-derived arm of the discovery
// chain must yield an ABSOLUTE path or be dropped. A non-empty check alone lets
// a relative value join into a path resolved against the current working
// directory, which is a config-injection seam — the tool loads whatever config
// happens to sit under the cwd of whoever launched it.
//
// These tests use t.Setenv (which is why none of them call t.Parallel: the Go
// test framework panics on that combination rather than racing).

// clearPathEnv pins every environment input the chain reads, so a test asserts
// against a known environment rather than the developer's.
func clearPathEnv(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("APP_CONFIG", "")
}

func anyRelative(paths []string) []string {
	var rel []string
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			rel = append(rel, p)
		}
	}
	return rel
}

func TestCandidatesDropRelativeXDGConfigHome(t *testing.T) {
	clearPathEnv(t)
	t.Setenv("XDG_CONFIG_HOME", "rel/x")

	got := New("app").candidates()
	if rel := anyRelative(got); len(rel) != 0 {
		t.Fatalf("relative XDG_CONFIG_HOME leaked cwd-relative candidates: %v", rel)
	}
	for _, p := range got {
		if strings.Contains(p, "rel/x") {
			t.Fatalf("relative XDG_CONFIG_HOME was joined anyway: %q", p)
		}
	}
}

func TestCandidatesDropRelativeHome(t *testing.T) {
	clearPathEnv(t)
	t.Setenv("HOME", "rel/home")

	// Both home-derived arms (.config and the legacy dotfiles) must drop it —
	// os.UserHomeDir returns $HOME verbatim, so neither is guarded for free.
	got := New("app").candidates()
	if rel := anyRelative(got); len(rel) != 0 {
		t.Fatalf("relative $HOME leaked cwd-relative candidates: %v", rel)
	}
	if len(got) != 0 {
		t.Fatalf("expected no candidates from a relative $HOME alone, got %v", got)
	}
}

func TestCandidatesDropRelativeEnvOverride(t *testing.T) {
	clearPathEnv(t)
	t.Setenv("APP_CONFIG", "app.yaml")

	got := New("app").EnvOverride("APP_CONFIG").candidates()
	for _, p := range got {
		if p == "app.yaml" {
			t.Fatal("relative env override was taken verbatim")
		}
	}
	if rel := anyRelative(got); len(rel) != 0 {
		t.Fatalf("relative env override leaked cwd-relative candidates: %v", rel)
	}
}

// The other half of the invariant, and the one the fix must not break: an
// absolute value resolves exactly where it always did.
func TestCandidatesKeepAbsoluteArms(t *testing.T) {
	clearPathEnv(t)
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	t.Setenv("HOME", "/home/u")
	t.Setenv("APP_CONFIG", "/etc/app/explicit.yaml")

	got := New("app").EnvOverride("APP_CONFIG").candidates()

	want := []string{
		"/etc/app/explicit.yaml",
		"/xdg/app/app.yaml",
		"/xdg/app/app.yml",
		"/xdg/app/app.toml",
		"/home/u/.config/app/app.yaml",
		"/home/u/.config/app/app.yml",
		"/home/u/.config/app/app.toml",
		"/home/u/.app",
		"/home/u/.app.yaml",
		"/home/u/.app.yml",
		"/home/u/.app.toml",
	}
	if len(got) != len(want) {
		t.Fatalf("candidate count changed: got %d %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// Dirs() is a caller-supplied seam, not an environment read: a repo-local
// relative dir stays usable. Pinned so a later tightening of the env filter
// does not quietly swallow it too.
func TestCandidatesKeepRelativeCallerDirs(t *testing.T) {
	clearPathEnv(t)

	got := New("app").Dirs("etc/app").candidates()
	if len(got) == 0 {
		t.Fatal("caller-supplied relative dir was dropped")
	}
	if got[0] != filepath.Join("etc/app", "app.yaml") {
		t.Fatalf("caller dir arm changed: got %q", got[0])
	}
}
