package main

import (
	"github.com/spf13/cobra"
	"go.uber.org/fx"

	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// serveAPICmd runs the data Connect listener on Uber fx. No workers; the IAM
// plane is `serve iam`.
//
// fx owns the lifecycle: it builds the dependency graph (config → logger →
// OTel → DB → SharedDeps → listeners → App), starts the listeners, and blocks
// on SIGINT/SIGTERM, then runs the graceful shutdown.
var serveAPICmd = &cobra.Command{
	Use:   "api",
	Short: "Run the data Connect listener",
	Run: func(cmd *cobra.Command, args []string) {
		fx.New(
			fx.Supply(configSource(config.RoleAPI)),
			fx.Supply(buildMeta()),
			app.APIModule,
		).Run()
	},
}
