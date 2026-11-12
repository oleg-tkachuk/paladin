package main

import (
	"github.com/spf13/cobra"
	"go.uber.org/fx"

	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// serveIAMCmd runs the IAM Connect listener on Uber fx: login and the token
// RPCs, the OAuth authorization server and the session JWKS. It is the one
// role that holds the session signing key (ADR-0047); every other role
// verifies sessions by the JWKS it serves.
var serveIAMCmd = &cobra.Command{
	Use:   "iam",
	Short: "Run the IAM Connect listener",
	Run: func(cmd *cobra.Command, args []string) {
		fx.New(
			fx.Supply(configSource(config.RoleIAM)),
			fx.Supply(buildMeta()),
			app.IAMModule,
		).Run()
	},
}
