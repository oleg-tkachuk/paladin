//go:build integration

package integration

import (
	"os"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// TestMain stops the Postgres server pgharness shares across the package.
func TestMain(m *testing.M) {
	os.Exit(pgharness.Main(m))
}
