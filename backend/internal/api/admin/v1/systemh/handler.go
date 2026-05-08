// Package systemh implements the admin/v1 SystemService — operator
// diagnostics that show the running configuration with secrets
// redacted. Distinct from iam/v1.SystemService (open to any
// authenticated caller) because the YAML may shape internal
// architecture an attacker shouldn't see.
package systemh

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"gopkg.in/yaml.v3"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/config"
)

// Handler holds an immutable reference to the loaded config plus the
// on-disk path it came from. Both are set once during process boot —
// SIGHUP-style reload is out of scope, since reloading the config
// safely requires reloading dependent subsystems too.
type Handler struct {
	cfg        config.Config
	sourcePath string
}

// New wires the handler with the live config + the on-disk path it
// was loaded from. Pass an empty path when the process bootstrapped
// from env / flags only — the response renders "—" in that case.
func New(cfg config.Config, sourcePath string) *Handler {
	return &Handler{cfg: cfg, sourcePath: sourcePath}
}

// MarshalRedacted produces the wire payload: the running config as
// YAML with secrets replaced by Config.Obfuscated()'s "***" sentinels,
// alongside the on-disk path the binary loaded it from.
//
// Gated to platform.admin — this method is the only entry-point the
// connectshim uses, so the role check lives here so the connectshim-
// gating coverage test sees it (rather than tucked into the shim).
func (h *Handler) MarshalRedacted(ctx context.Context) (yamlBlob string, sourcePath string, err error) {
	if rerr := apiutil.RequireRole(ctx, apiutil.RolePlatformAdmin); rerr != nil {
		return "", "", connect.NewError(connect.CodePermissionDenied, rerr)
	}
	redacted := h.cfg.Obfuscated()
	out, err := yaml.Marshal(&redacted)
	if err != nil {
		return "", "", connect.NewError(connect.CodeInternal,
			fmt.Errorf("marshal config: %w", err))
	}
	return string(out), h.sourcePath, nil
}
