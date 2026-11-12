package main

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
)

const chartValuesPath = "../../deploy/chart/values.yaml"

// minWorkAfterWait is what a one-shot Job must have left once the wait for
// the database has used its whole budget: migrations and bootstrap rows.
const minWorkAfterWait = time.Minute

// The migrate and bootstrap Jobs run boot(), which waits up to
// postgres.OneShotWait.Budget for the database. A Job deadline shorter than
// that kills a wait that would have succeeded, and the hook fails the
// release all the same.
func TestOneShotJobDeadlinesOutlastTheDatabaseWait(t *testing.T) {
	raw, err := os.ReadFile(chartValuesPath)
	require.NoError(t, err)
	var values struct {
		Jobs map[string]struct {
			ActiveDeadlineSeconds int `yaml:"activeDeadlineSeconds"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &values))

	need := postgres.OneShotWait.Budget + minWorkAfterWait
	for _, job := range []string{"migrate", "bootstrap"} {
		got, ok := values.Jobs[job]
		require.True(t, ok, "jobs.%s missing from the chart values", job)
		deadline := time.Duration(got.ActiveDeadlineSeconds) * time.Second
		require.GreaterOrEqual(t, deadline, need,
			"jobs.%s.activeDeadlineSeconds %s leaves no room after a %s database wait",
			job, deadline, postgres.OneShotWait.Budget)
	}
}
