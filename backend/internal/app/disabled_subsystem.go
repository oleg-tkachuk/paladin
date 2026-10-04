package app

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
)

// Disabled-subsystem error contract.
//
// When a subsystem is gated off-by-config (capability.enabled=false,
// api_token.enabled=false) we still mount the Connect service so the
// caller hits a real RPC handler instead of a default-mux 404. The
// handler returns CodeUnimplemented — which is the spec-correct gRPC
// code (per AIP-193 and the gRPC docs, UNIMPLEMENTED covers both
// "method does not exist" AND "method is not supported/enabled in this
// service"). What we add on top is structured *metadata* so clients can
// distinguish "the binary lacks this method" from "the operator turned
// this subsystem off":
//
//	X-Paladin-Reason:    subsystem_disabled
//	X-Paladin-Subsystem: capability
//
// Plus a human-readable message naming the config flag the operator
// flips to enable the subsystem. The BFF and admin UI key on the
// X-Paladin-Reason header to render a different toast ("This feature is
// disabled in this deployment — ask your platform admin to enable
// `capability`") than for genuinely unimplemented methods ("This
// version of Paladin does not implement this RPC").
//
// We deliberately do NOT invent a new Connect code (no NotStarted,
// SubsystemDisabled, etc.) — clients that auto-retry on
// CodeUnavailable would mis-handle a custom code, and a
// non-standard code defeats interop with generic dashboards.
const (
	HeaderReason    = "X-Paladin-Reason"
	HeaderSubsystem = "X-Paladin-Subsystem"
	ReasonDisabled  = "subsystem_disabled"
)

// subsystemDisabledError builds the canonical CodeUnimplemented error
// for a disabled subsystem, decorated with the X-Paladin-Reason /
// X-Paladin-Subsystem response headers and a message naming the config
// flag operators flip to enable it.
func subsystemDisabledError(subsystem, configFlag string) error {
	err := connect.NewError(
		connect.CodeUnimplemented,
		fmt.Errorf("%s subsystem is disabled in this deployment (set %s=true to activate)", subsystem, configFlag),
	)
	err.Meta().Set(HeaderReason, ReasonDisabled)
	err.Meta().Set(HeaderSubsystem, subsystem)
	return err
}

// ─── CapabilityService stub ─────────────────────────────────────────────────
//
// Embeds the generated Unimplemented* struct so future-added methods
// fall through to the default Unimplemented behaviour rather than
// failing the build until we update this file. The currently-defined
// methods are overridden to return the rich subsystem-disabled error.

type disabledCapabilityServiceHandler struct {
	paladinadminv1connect.UnimplementedCapabilityServiceHandler
}

func (disabledCapabilityServiceHandler) Issue(context.Context, *connect.Request[adminv1.CapabilityServiceIssueRequest]) (*connect.Response[adminv1.CapabilityServiceIssueResponse], error) {
	return nil, subsystemDisabledError("capability", "config.capability.enabled")
}

func (disabledCapabilityServiceHandler) Delegate(context.Context, *connect.Request[adminv1.CapabilityServiceDelegateRequest]) (*connect.Response[adminv1.CapabilityServiceIssueResponse], error) {
	return nil, subsystemDisabledError("capability", "config.capability.enabled")
}

func (disabledCapabilityServiceHandler) Revoke(context.Context, *connect.Request[adminv1.CapabilityServiceRevokeRequest]) (*connect.Response[adminv1.CapabilityServiceRevokeResponse], error) {
	return nil, subsystemDisabledError("capability", "config.capability.enabled")
}

func (disabledCapabilityServiceHandler) RevokeBiscuit(context.Context, *connect.Request[adminv1.CapabilityServiceRevokeBiscuitRequest]) (*connect.Response[adminv1.CapabilityServiceRevokeBiscuitResponse], error) {
	return nil, subsystemDisabledError("capability", "config.capability.enabled")
}

func (disabledCapabilityServiceHandler) GetBiscuitUsage(context.Context, *connect.Request[adminv1.CapabilityServiceGetBiscuitUsageRequest]) (*connect.Response[adminv1.CapabilityServiceGetBiscuitUsageResponse], error) {
	return nil, subsystemDisabledError("capability", "config.capability.enabled")
}

func (disabledCapabilityServiceHandler) List(context.Context, *connect.Request[adminv1.CapabilityServiceListRequest]) (*connect.Response[adminv1.CapabilityServiceListResponse], error) {
	return nil, subsystemDisabledError("capability", "config.capability.enabled")
}

func (disabledCapabilityServiceHandler) GetUsage(context.Context, *connect.Request[adminv1.CapabilityServiceGetUsageRequest]) (*connect.Response[adminv1.CapabilityServiceGetUsageResponse], error) {
	return nil, subsystemDisabledError("capability", "config.capability.enabled")
}

// ─── APITokenService stub ───────────────────────────────────────────────────

type disabledAPITokenServiceHandler struct {
	paladinadminv1connect.UnimplementedAPITokenServiceHandler
}

func (disabledAPITokenServiceHandler) Create(context.Context, *connect.Request[adminv1.APITokenServiceCreateRequest]) (*connect.Response[adminv1.APITokenServiceCreateResponse], error) {
	return nil, subsystemDisabledError("api_token", "config.api_token.enabled")
}

func (disabledAPITokenServiceHandler) Revoke(context.Context, *connect.Request[adminv1.APITokenServiceRevokeRequest]) (*connect.Response[adminv1.APITokenServiceRevokeResponse], error) {
	return nil, subsystemDisabledError("api_token", "config.api_token.enabled")
}

func (disabledAPITokenServiceHandler) List(context.Context, *connect.Request[adminv1.APITokenServiceListRequest]) (*connect.Response[adminv1.APITokenServiceListResponse], error) {
	return nil, subsystemDisabledError("api_token", "config.api_token.enabled")
}

func (disabledAPITokenServiceHandler) GetSelf(context.Context, *connect.Request[adminv1.APITokenServiceGetSelfRequest]) (*connect.Response[adminv1.APITokenServiceGetSelfResponse], error) {
	return nil, subsystemDisabledError("api_token", "config.api_token.enabled")
}

func (disabledAPITokenServiceHandler) GetUsage(context.Context, *connect.Request[adminv1.APITokenServiceGetUsageRequest]) (*connect.Response[adminv1.APITokenServiceGetUsageResponse], error) {
	return nil, subsystemDisabledError("api_token", "config.api_token.enabled")
}

// Compile-time check: ensure both stubs satisfy the generated handler
// interfaces. Catches drift if a new RPC method is added to a service
// without a matching override here.
var (
	_ paladinadminv1connect.CapabilityServiceHandler = disabledCapabilityServiceHandler{}
	_ paladinadminv1connect.APITokenServiceHandler   = disabledAPITokenServiceHandler{}
)
