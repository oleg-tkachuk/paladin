package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// allowedModules are the modules the console's binary may link beside the
// standard library: its own, and the logger. The console serves files and
// relays one form; anything more is weight and attack surface on every start,
// and above all nothing of the backend's (ADR-0049).
var allowedModules = []string{
	"github.com/oleg-tkachuk/paladin/frontend/server",
	"go.uber.org/zap",
	"go.uber.org/multierr",
}

func TestTheBinaryLinksOnlyWhatItNeeds(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps",
		"-f", "{{if not .Standard}}{{with .Module}}{{.Path}}{{end}}{{end}}", ".").Output()
	require.NoError(t, err)

	allowed := map[string]bool{}
	for _, m := range allowedModules {
		allowed[m] = true
	}
	for _, mod := range strings.Fields(string(out)) {
		assert.True(t, allowed[mod], "the console links %s", mod)
	}
}
