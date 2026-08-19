package main

import (
	"testing"

	"go.uber.org/fx"

	"github.com/oleg-tkachuk/paladin-private/internal/app"
)

// TestRoleModulesValidate asserts that every serve role's fx dependency graph is
// complete and acyclic — every fx.Provide/Invoke has its inputs satisfied — for
// all 7 role graphs (api, admin, worker, dispatcher, ingest, and both mcp
// modes). fx.ValidateApp builds the graph in validation mode: it does NOT run
// constructors or invocations, so no DB/config file/network is touched. This
// turns a wiring regression (a missing provider, a renamed dependency, a cycle)
// into a fast unit-test failure instead of a pod that crash-loops on boot.
//
// The two process-scoped inputs the CLI supplies at runtime — ConfigSource and
// BuildMeta — are supplied here with zero values; validation only checks the
// TYPES resolve, never reads the values.
func TestRoleModulesValidate(t *testing.T) {
	supplies := fx.Options(
		fx.Supply(app.ConfigSource{}),
		fx.Supply(app.BuildMeta{}),
	)

	modules := map[string]fx.Option{
		"api":          app.APIModule,
		"admin":        app.AdminModule,
		"worker":       workerModule,
		"dispatcher":   dispatcherModule,
		"ingest":       ingestModule,
		"mcp-bridge":   mcpBridgeModule,
		"mcp-embedded": mcpEmbeddedModule,
	}

	for name, mod := range modules {
		t.Run(name, func(t *testing.T) {
			// fx.NopLogger last so a valid graph produces no test-log noise;
			// ValidateApp still surfaces any graph error as the returned err.
			if err := fx.ValidateApp(supplies, mod, fx.NopLogger); err != nil {
				t.Fatalf("%s module graph is not wireable: %v", name, err)
			}
		})
	}
}
