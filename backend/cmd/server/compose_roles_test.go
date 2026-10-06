package main

import (
	"os"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

// composeFile is the local stack that is meant to run every role the way the
// chart does: one container per `paladin serve` child.
const composeFile = "../../deploy/docker-compose.yaml"

// The compose stack once ran api, admin, worker, mcp and ingest but no
// dispatcher, so outbox rows piled up and no subscription in a local stack
// ever received an event. A role added to serveCmd without a container is the
// same gap; this holds the two lists together.
func TestComposeRunsEveryServeRole(t *testing.T) {
	raw, err := os.ReadFile(composeFile)
	if err != nil {
		t.Fatalf("read %s: %v", composeFile, err)
	}
	var doc struct {
		// A command is a list or a shell string; only a list can name a
		// `paladin serve` role.
		Services map[string]struct {
			Command any `yaml:"command"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", composeFile, err)
	}

	const serveVerb = "serve"
	inCompose := map[string]bool{}
	for _, svc := range doc.Services {
		argv, ok := svc.Command.([]any)
		if !ok || len(argv) < 2 || argv[0] != serveVerb {
			continue
		}
		if role, ok := argv[1].(string); ok {
			inCompose[role] = true
		}
	}

	var missing []string
	for _, c := range serveCmd.Commands() {
		if !inCompose[c.Name()] {
			missing = append(missing, c.Name())
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%s runs no container for serve %v", composeFile, missing)
	}
}
