package main

import (
	"github.com/spf13/cobra"
	"go.uber.org/fx"

	"github.com/oleg-tkachuk/paladin/backend/internal/app"
)

// serveAdminCmd runs only the admin Connect listener on Uber fx. Sized for low
// replica counts (1–2) and intended to sit behind a separate ingress with mTLS
// and tighter NetworkPolicy than data/iam. fx owns the lifecycle (see
// serve_api.go / internal/app/appfx.go).
var serveAdminCmd = &cobra.Command{
	Use:   "admin",
	Short: "Run admin Connect listener",
	Run: func(cmd *cobra.Command, args []string) {
		fx.New(
			fx.Supply(configSource()),
			fx.Supply(buildMeta()),
			app.AdminModule,
		).Run()
	},
}
