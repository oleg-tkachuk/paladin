package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// configSource turns the --config flag and PALADIN_CONFIG_OVERLAYS into the paths
// fx loads. Overlay ordering decides which value wins, so a dropped or
// misparsed entry means the process boots on the wrong config — visible only
// as behaviour nobody expected, never as an error.

func withConfigPath(t *testing.T, p string) {
	t.Helper()
	prev := configPath
	configPath = p
	t.Cleanup(func() { configPath = prev })
}

func TestConfigSourceMakesPathsAbsolute(t *testing.T) {
	withConfigPath(t, "configs/config.yaml")
	t.Setenv("PALADIN_CONFIG_OVERLAYS", "")

	got := configSource()
	if !filepath.IsAbs(got.Path) {
		t.Errorf("Path = %q, want absolute — a relative path resolves against the "+
			"working directory, which differs between a container and a laptop", got.Path)
	}
}

func TestConfigSourceNoOverlaysByDefault(t *testing.T) {
	withConfigPath(t, "configs/config.yaml")
	t.Setenv("PALADIN_CONFIG_OVERLAYS", "")

	if got := configSource(); len(got.Overlays) != 0 {
		t.Errorf("Overlays = %v, want none when the env var is empty", got.Overlays)
	}
}

func TestConfigSourceParsesOverlaysInOrder(t *testing.T) {
	withConfigPath(t, "configs/config.yaml")
	t.Setenv("PALADIN_CONFIG_OVERLAYS", "a.yaml:b.yaml:c.yaml")

	got := configSource()
	if len(got.Overlays) != 3 {
		t.Fatalf("got %d overlays, want 3: %v", len(got.Overlays), got.Overlays)
	}
	// Order is the whole semantics of an overlay list — later wins.
	for i, want := range []string{"a.yaml", "b.yaml", "c.yaml"} {
		if !strings.HasSuffix(got.Overlays[i], want) {
			t.Errorf("overlay[%d] = %q, want it to end in %q", i, got.Overlays[i], want)
		}
	}
}

// A trailing colon, a doubled separator or a stray space are what an operator
// actually produces when composing this in a Helm template. Each must be
// skipped rather than becoming an empty path that silently loads nothing.
func TestConfigSourceSkipsEmptyAndTrimsEntries(t *testing.T) {
	withConfigPath(t, "configs/config.yaml")
	t.Setenv("PALADIN_CONFIG_OVERLAYS", " a.yaml : : b.yaml ::")

	got := configSource()
	if len(got.Overlays) != 2 {
		t.Fatalf("got %d overlays, want 2 (empties skipped): %v", len(got.Overlays), got.Overlays)
	}
	for _, o := range got.Overlays {
		if strings.TrimSpace(o) != o {
			t.Errorf("overlay %q retains surrounding whitespace", o)
		}
		if !filepath.IsAbs(o) {
			t.Errorf("overlay %q is not absolute", o)
		}
	}
}

func TestConfigSourceAllOverlaysAbsolute(t *testing.T) {
	withConfigPath(t, "configs/config.yaml")
	t.Setenv("PALADIN_CONFIG_OVERLAYS", "rel/one.yaml:rel/two.yaml")

	for _, o := range configSource().Overlays {
		if !filepath.IsAbs(o) {
			t.Errorf("overlay %q must be absolute for the same reason the base path is", o)
		}
	}
}

// buildMeta is the version stamp the /system endpoints and the startup log
// report. It is pure, so pin that it carries the ldflags-injected values
// rather than dropping one.
func TestBuildMetaCarriesTheStamp(t *testing.T) {
	withConfigPath(t, "/etc/paladin/config.yaml")

	got := buildMeta()
	if got.ConfigPath != "/etc/paladin/config.yaml" {
		t.Errorf("ConfigPath = %q", got.ConfigPath)
	}
	// version/commit/buildTime are ldflags-injected and empty in tests; what
	// matters is that the fields are wired, not what they contain here.
	if got.Version != version || got.Commit != commit || got.BuildTime != buildTime {
		t.Error("buildMeta must forward the injected stamp verbatim")
	}
}
