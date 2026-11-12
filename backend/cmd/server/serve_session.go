package main

import (
	"github.com/spf13/cobra"
	"go.uber.org/fx"

	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

var serveSessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Run the console's session role: its session cookie and the calls it forwards",
	Run: func(cmd *cobra.Command, args []string) {
		fx.New(
			fx.Supply(configSource(config.RoleSession)),
			fx.Supply(buildMeta()),
			app.SessionModule,
		).Run()
	},
}
