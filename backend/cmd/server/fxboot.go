package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/oleg-tkachuk/paladin-private/internal/app"
)

// configSource resolves the CLI config path + PALADIN_CONFIG_OVERLAYS chain into
// the value the fx ConfigProvider consumes. Mirrors the resolution boot() does
// (abs paths + colon-separated overlays, later wins); the two share the same
// semantics so an fx and a non-fx role load config identically.
func configSource() app.ConfigSource {
	if abs, err := filepath.Abs(configPath); err == nil {
		configPath = abs
	}
	var overlays []string
	if o := os.Getenv("PALADIN_CONFIG_OVERLAYS"); o != "" {
		for _, p := range strings.Split(o, ":") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if abs, err := filepath.Abs(p); err == nil {
				p = abs
			}
			overlays = append(overlays, p)
		}
	}
	return app.ConfigSource{Path: configPath, Overlays: overlays}
}

// buildMeta carries the version-stamp into the fx graph.
func buildMeta() app.BuildMeta {
	return app.BuildMeta{
		Version:    version,
		Commit:     commit,
		BuildTime:  buildTime,
		ConfigPath: configPath,
	}
}
