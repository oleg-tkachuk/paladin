package main

import (
	"github.com/spf13/cobra"
	"go.uber.org/fx"

	"github.com/oleg-tkachuk/paladin/backend/internal/app"
)

// serveAPICmd runs the data + iam Connect listeners on Uber fx. No workers.
//
// fx owns the lifecycle: it builds the dependency graph (config → logger →
// OTel → DB → SharedDeps → listeners → App), starts the listeners, and blocks
// on SIGINT/SIGTERM, then runs the graceful shutdown. Two listeners share one
// *health.Handler so SIGTERM flips both /readyz to 503 simultaneously, giving
// kube-proxy a single window to drain in-flight traffic.
var serveAPICmd = &cobra.Command{
	Use:   "api",
	Short: "Run data + iam Connect listeners",
	Run: func(cmd *cobra.Command, args []string) {
		fx.New(
			fx.Supply(configSource()),
			fx.Supply(buildMeta()),
			app.APIModule,
		).Run()
	},
}
