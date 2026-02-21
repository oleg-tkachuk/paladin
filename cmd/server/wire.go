//go:build wireinject
// +build wireinject

//go:generate go run -mod=mod github.com/google/wire/cmd/wire

package main

import (
	"context"
	"sync/atomic"

	"github.com/oleg-tkachuk/paladin/internal/app"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	appwire "github.com/oleg-tkachuk/paladin/internal/wire"

	"github.com/google/wire"
)

func InitializeApp(ctx context.Context, version appwire.Version, commit appwire.Commit, buildTime appwire.BuildTime, configPath appwire.ConfigPath) (*app.App, func(), error) {
	wire.Build(
		provideAppMetadata,
		provideBootstrapLogger,
		provideStartedBool,
		appwire.ProviderSet,
	)
	return nil, nil, nil
}

func provideAppMetadata(v appwire.Version, c appwire.Commit, b appwire.BuildTime) appwire.AppMetadata {
	return appwire.AppMetadata{
		Version:   string(v),
		Commit:    string(c),
		BuildTime: string(b),
	}
}

func provideBootstrapLogger() appwire.BootstrapLogger {
	return appwire.BootstrapLogger(logger.NewBootstrapLogger())
}

func provideStartedBool() *atomic.Bool {
	return &atomic.Bool{}
}
