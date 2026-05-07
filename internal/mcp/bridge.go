// Package mcp wires the PALADIN three-plane Connect API into a Model Context
// Protocol server.
//
// Built on top of github.com/modelcontextprotocol/go-sdk: tool schemas are
// inferred from typed input structs (one struct per tool), and the SDK
// handles JSON-RPC framing, transport (stdio / streamable HTTP), session
// state, schema validation, and notification fan-out for us. The bridge
// itself is just a glue layer that owns the Connect clients and translates
// each MCP tool call into a single Connect RPC.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/types/known/durationpb"

	adminv1 "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	adminv1connect "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
	commonv1 "github.com/oleg-tkachuk/paladin/internal/api/pb/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
	datav1connect "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1/paladindatav1connect"
	iamv1 "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"
	iamv1connect "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1/paladiniamv1connect"
)

// Clients is the bundle of Connect clients the MCP bridge dispatches to.
type Clients struct {
	HTTP        *http.Client
	AdminURL    string
	DataURL     string
	IAMURL      string
	BearerToken string

	Backend  adminv1connect.BackendServiceClient
	Bucket   adminv1connect.BucketServiceClient
	Tenant   adminv1connect.TenantServiceClient
	OKey     adminv1connect.ObjectKeyServiceClient
	Policy   adminv1connect.PolicyServiceClient
	Quota    adminv1connect.QuotaServiceClient
	Audit    adminv1connect.AuditLogServiceClient
	EventSub adminv1connect.EventSubscriptionServiceClient

	Object        datav1connect.ObjectServiceClient
	Multipart     datav1connect.MultipartUploadServiceClient
	Presign       datav1connect.PresignServiceClient
	ObjectTag     datav1connect.ObjectTagServiceClient
	DataOperation datav1connect.OperationServiceClient

	Auth   iamv1connect.AuthServiceClient
	Users  iamv1connect.UserServiceClient
	ApiKey iamv1connect.ApiKeyServiceClient
}

// NewClients constructs a Clients bundle. Bearer is the access token the MCP
// service-account holds; it is injected into every outbound Connect request.
func NewClients(httpc *http.Client, adminURL, dataURL, iamURL, bearer string) *Clients {
	return NewClientsWithCapability(httpc, adminURL, dataURL, iamURL, bearer, "")
}

// NewClientsWithCapability is the cap-aware constructor. capabilityToken
// (when non-empty) is forwarded as `X-PALADIN-Capability` on every outbound
// Connect call so the destination plane's auth.CapabilityInterceptor
// sees and stamps it on the request context.
//
// The two tokens stack: bearer is the JWT auth (admin / iam audience),
// capabilityToken is the optional capability that grants fine-grained
// caveats. Either or both may be present. Empty values are not sent.
//
// MCP HTTP transport forwards the capability via the same X-PALADIN-Capability
// header the streamable-HTTP getServer hook reads — see cmd/server/serve_mcp.go.
func NewClientsWithCapability(httpc *http.Client, adminURL, dataURL, iamURL, bearer, capabilityToken string) *Clients {
	if httpc == nil {
		httpc = http.DefaultClient
	}
	authInjector := connect.WithInterceptors(connect.UnaryInterceptorFunc(
		func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				if bearer != "" {
					req.Header().Set("Authorization", "Bearer "+bearer)
				}
				if capabilityToken != "" {
					req.Header().Set("X-PALADIN-Capability", capabilityToken)
				}
				return next(ctx, req)
			}
		},
	))
	return &Clients{
		HTTP:        httpc,
		AdminURL:    adminURL,
		DataURL:     dataURL,
		IAMURL:      iamURL,
		BearerToken: bearer,

		Backend:  adminv1connect.NewBackendServiceClient(httpc, adminURL, authInjector),
		Bucket:   adminv1connect.NewBucketServiceClient(httpc, adminURL, authInjector),
		Tenant:   adminv1connect.NewTenantServiceClient(httpc, adminURL, authInjector),
		OKey:     adminv1connect.NewObjectKeyServiceClient(httpc, adminURL, authInjector),
		Policy:   adminv1connect.NewPolicyServiceClient(httpc, adminURL, authInjector),
		Quota:    adminv1connect.NewQuotaServiceClient(httpc, adminURL, authInjector),
		Audit:    adminv1connect.NewAuditLogServiceClient(httpc, adminURL, authInjector),
		EventSub: adminv1connect.NewEventSubscriptionServiceClient(httpc, adminURL, authInjector),

		Object:        datav1connect.NewObjectServiceClient(httpc, dataURL, authInjector),
		Multipart:     datav1connect.NewMultipartUploadServiceClient(httpc, dataURL, authInjector),
		Presign:       datav1connect.NewPresignServiceClient(httpc, dataURL, authInjector),
		ObjectTag:     datav1connect.NewObjectTagServiceClient(httpc, dataURL, authInjector),
		DataOperation: datav1connect.NewOperationServiceClient(httpc, dataURL, authInjector),

		Auth:   iamv1connect.NewAuthServiceClient(httpc, iamURL, authInjector),
		Users:  iamv1connect.NewUserServiceClient(httpc, iamURL, authInjector),
		ApiKey: iamv1connect.NewApiKeyServiceClient(httpc, iamURL, authInjector),
	}
}

// NewServer constructs an MCP server with the PALADIN tool / resource / prompt
// catalog wired up. The ToolFilter selects which tools are registered;
// see internal/mcp/profile.go for the YAML-driven gating model.
func NewServer(name, version string, c *Clients, filter *ToolFilter) *mcpsdk.Server {
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: name, Version: version}, nil)
	registerReadTools(srv, c, filter)
	registerWriteTools(srv, c, filter)
	registerResources(srv, c)
	registerPrompts(srv)
	return srv
}

// addTool wraps mcpsdk.AddTool with the active ToolFilter so the
// registration call sites stay tight. When the filter denies a name
// the call is a no-op — the tool simply doesn't appear in ListTools.
func addTool[Args any](
	srv *mcpsdk.Server,
	filter *ToolFilter,
	tool *mcpsdk.Tool,
	handler func(context.Context, *mcpsdk.CallToolRequest, Args) (*mcpsdk.CallToolResult, any, error),
) {
	if filter != nil && !filter.Allow(tool.Name) {
		return
	}
	mcpsdk.AddTool(srv, tool, handler)
}

// jsonResult is the universal "Connect response → MCP tool result" adapter.
// Errors from the Connect call surface as protocol errors (bubble up as JSON-RPC
// error responses); successful payloads land as a single text-content block of
// indented JSON so the LLM can read fields by name.
func jsonResult[T any](resp *connect.Response[T], err error) (*mcpsdk.CallToolResult, any, error) {
	if err != nil {
		return nil, nil, err
	}
	b, mErr := json.MarshalIndent(resp.Msg, "", "  ")
	if mErr != nil {
		return nil, nil, fmt.Errorf("marshal response: %w", mErr)
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
	}, nil, nil
}

// ─── Read-only tool argument types ──────────────────────────────────────────

type listBackendsArgs struct {
	PageSize int32 `json:"page_size,omitempty" jsonschema:"page size; default 50, max 1000"`
}
type listBucketsArgs struct {
	BackendID string `json:"backend_id,omitempty" jsonschema:"optional backend id; empty = all"`
	PageSize  int32  `json:"page_size,omitempty" jsonschema:"page size; default 50, max 1000"`
}
type listTenantsArgs struct {
	PageSize int32 `json:"page_size,omitempty" jsonschema:"page size; default 50, max 1000"`
}
type listObjectKeysArgs struct {
	TenantID string `json:"tenant_id" jsonschema:"required tenant UUID"`
	PageSize int32  `json:"page_size,omitempty" jsonschema:"page size; default 50, max 1000"`
}
type queryObjectsArgs struct {
	TenantID  string `json:"tenant_id" jsonschema:"tenant UUID"`
	ObjectKey string `json:"object_key" jsonschema:"object key (namespace) name"`
	Filter    string `json:"filter,omitempty" jsonschema:"optional CEL filter, e.g. tags['type']=='invoice'"`
	PageSize  int32  `json:"page_size,omitempty" jsonschema:"page size; default 100, max 1000"`
}
type getQuotaArgs struct {
	Name string `json:"name" jsonschema:"Quota resource name (tenants/{tenant_id_or_slug}/quota | storageBackends/{b}/buckets/{n}/quota)"`
}
type validatePolicyArgs struct {
	CedarPolicy string `json:"cedar_policy" jsonschema:"Cedar policy text"`
}
type listVersionsArgs struct {
	ObjectName string `json:"object_name" jsonschema:"Object resource name (tenants/{tenant_id_or_slug}/objectKeys/{ok}/objects/{id})"`
	PageSize   int32  `json:"page_size,omitempty" jsonschema:"page size; default 50, max 1000"`
}
type getVersionArgs struct {
	VersionName string `json:"version_name" jsonschema:".../objects/{id}/versions/{ver}"`
}
type getEffectivePolicyArgs struct {
	ResourceName string `json:"resource_name" jsonschema:"Any resource name; tenants/{tenant_id_or_slug}/objectKeys/{ok} works for namespace-level"`
}
type simulateAuthzArgs struct {
	PrincipalSubject  string   `json:"principal_subject" jsonschema:"subject (user_id or service-account ref)"`
	PrincipalTenantID string   `json:"principal_tenant_id,omitempty" jsonschema:"optional tenant UUID for the simulated principal"`
	PrincipalRoles    []string `json:"principal_roles,omitempty" jsonschema:"roles the simulated principal carries"`
	Action            string   `json:"action" jsonschema:"Cedar action name (e.g. PutObject, ManageBucket)"`
	ResourceName      string   `json:"resource_name" jsonschema:"Resource name to authorize against"`
}
type auditRecentArgs struct {
	PageSize int32  `json:"page_size,omitempty" jsonschema:"page size; default 50, max 200"`
	Filter   string `json:"filter,omitempty" jsonschema:"CEL filter over AuditLogEntry — fields: actor_subject, actor_tenant_id, actor_audience, action, resource_name, request_id, source_ip, at, is_error"`
}

// getNameArgs is the canonical "fetch one resource by name" shape — used
// by every Get* tool below. Resource name format is service-specific
// and documented per tool.
type getNameArgs struct {
	Name string `json:"name" jsonschema:"resource name"`
}

// listChildrenArgs is the canonical "list under parent" shape for Get
// tools that take a parent + page size.
type listChildrenArgs struct {
	Parent   string `json:"parent,omitempty" jsonschema:"optional parent resource name"`
	PageSize int32  `json:"page_size,omitempty" jsonschema:"page size; default 50, max 1000"`
}

type listOperationsArgs struct {
	PageSize int32 `json:"page_size,omitempty" jsonschema:"page size; default 50, max 1000"`
}

// ─── Read-only tool registration ────────────────────────────────────────────

func registerReadTools(s *mcpsdk.Server, c *Clients, filter *ToolFilter) {
	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_list_backends",
		Description: "List all storage backends. Read-only.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listBackendsArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Backend.ListBackends(ctx, connect.NewRequest(&adminv1.ListBackendsRequest{
			Page: &commonv1.PageRequest{PageSize: in.PageSize},
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_list_buckets",
		Description: "List buckets, optionally filtered to one storage backend.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listBucketsArgs) (*mcpsdk.CallToolResult, any, error) {
		req := &adminv1.ListBucketsRequest{Page: &commonv1.PageRequest{PageSize: in.PageSize}}
		if in.BackendID != "" {
			req.Parent = "storageBackends/" + in.BackendID
		}
		return jsonResult(c.Bucket.ListBuckets(ctx, connect.NewRequest(req)))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_list_tenants",
		Description: "List all tenants. Platform-admin only.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listTenantsArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Tenant.ListTenants(ctx, connect.NewRequest(&adminv1.ListTenantsRequest{
			Page: &commonv1.PageRequest{PageSize: in.PageSize},
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_list_object_keys",
		Description: "List object keys (logical namespaces) within a tenant.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listObjectKeysArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.OKey.ListObjectKeys(ctx, connect.NewRequest(&adminv1.ListObjectKeysRequest{
			Parent: "tenants/" + in.TenantID,
			Page:   &commonv1.PageRequest{PageSize: in.PageSize},
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_query_objects",
		Description: "List objects within an object_key, optionally filtered by CEL.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in queryObjectsArgs) (*mcpsdk.CallToolResult, any, error) {
		parent := fmt.Sprintf("tenants/%s/objectKeys/%s", in.TenantID, in.ObjectKey)
		return jsonResult(c.Object.ListObjects(ctx, connect.NewRequest(&datav1.ListObjectsRequest{
			Parent: parent,
			Filter: in.Filter,
			Page:   &commonv1.PageRequest{PageSize: in.PageSize},
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_get_quota",
		Description: "Inspect a tenant or bucket quota.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getQuotaArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Quota.GetQuota(ctx, connect.NewRequest(&adminv1.GetQuotaRequest{Name: in.Name})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_validate_policy",
		Description: "Type-check a Cedar policy against the PALADIN schema.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in validatePolicyArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Policy.Validate(ctx, connect.NewRequest(&adminv1.ValidateRequest{
			CedarPolicy: in.CedarPolicy,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_list_versions",
		Description: "List the version history of an object (newest first). Empty when bucket versioning is off.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listVersionsArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Object.ListObjectVersions(ctx, connect.NewRequest(&datav1.ListObjectVersionsRequest{
			Parent: in.ObjectName,
			Page:   &commonv1.PageRequest{PageSize: in.PageSize},
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_get_version",
		Description: "Fetch metadata for a specific object version.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getVersionArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Object.GetObjectVersion(ctx, connect.NewRequest(&datav1.GetObjectVersionRequest{
			Name: in.VersionName,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_get_effective_policy",
		Description: "Return the merged Cedar policy stack the engine compiles for a resource (tenant + object_key layers).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getEffectivePolicyArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Policy.GetEffectivePolicy(ctx, connect.NewRequest(&adminv1.GetEffectivePolicyRequest{
			ResourceName: in.ResourceName,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_simulate_authz",
		Description: "Dry-run authorization: would `principal_subject` (with the given roles) be allowed to perform `action` on `resource_name`?",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in simulateAuthzArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Policy.SimulateAuthz(ctx, connect.NewRequest(&adminv1.SimulateAuthzRequest{
			PrincipalSubject:  in.PrincipalSubject,
			PrincipalTenantId: in.PrincipalTenantID,
			PrincipalRoles:    in.PrincipalRoles,
			Action:            in.Action,
			ResourceName:      in.ResourceName,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_audit_recent",
		Description: "Fetch the most recent audit log entries.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in auditRecentArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Audit.ListAuditLog(ctx, connect.NewRequest(&adminv1.ListAuditLogRequest{
			Page:   &commonv1.PageRequest{PageSize: in.PageSize},
			Filter: in.Filter,
		})))
	})

	// ─── Single-resource Get tools ─────────────────────────────────────
	// These complete the read surface that the List/Query tools imply:
	// once an agent sees a name in a list result, it can fetch that
	// specific record without having to filter the list again.

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_get_tenant",
		Description: "Fetch a single tenant by resource name.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getNameArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Tenant.GetTenant(ctx, connect.NewRequest(&adminv1.GetTenantRequest{Name: in.Name})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_get_bucket",
		Description: "Fetch a single bucket (storageBackends/{b}/buckets/{n}).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getNameArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Bucket.GetBucket(ctx, connect.NewRequest(&adminv1.GetBucketRequest{Name: in.Name})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_get_object_key",
		Description: "Fetch a single object_key (tenants/{tenant_id_or_slug}/objectKeys/{ok}).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getNameArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.OKey.GetObjectKey(ctx, connect.NewRequest(&adminv1.GetObjectKeyRequest{Name: in.Name})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_get_object",
		Description: "Fetch object metadata (NOT the body — body is fetched via paladin_presign_download).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getNameArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Object.GetObject(ctx, connect.NewRequest(&datav1.GetObjectRequest{Name: in.Name})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_get_object_tags",
		Description: "Read the tag map for a single object.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getNameArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.ObjectTag.GetObjectTags(ctx, connect.NewRequest(&datav1.GetObjectTagsRequest{Name: in.Name})))
	})

	// Subscription / operations introspection.

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_list_subscriptions",
		Description: "List event subscriptions, optionally for one bucket parent.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listChildrenArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.EventSub.ListSubscriptions(ctx, connect.NewRequest(&adminv1.ListSubscriptionsRequest{
			Parent: in.Parent,
			Page:   &commonv1.PageRequest{PageSize: in.PageSize},
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_list_operations",
		Description: "List long-running operations (BatchDelete / BatchCopy / …) for the active tenant.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listOperationsArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.DataOperation.ListOperations(ctx, connect.NewRequest(&datav1.ListOperationsRequest{
			Page: &commonv1.PageRequest{PageSize: in.PageSize},
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_get_operation",
		Description: "Fetch a single long-running operation (state + response payload).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getNameArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.DataOperation.GetOperation(ctx, connect.NewRequest(&datav1.GetOperationRequest{Name: in.Name})))
	})
}

// ─── Mutating tool argument types ───────────────────────────────────────────

type createObjectKeyArgs struct {
	TenantID    string `json:"tenant_id" jsonschema:"tenant UUID"`
	ObjectKey   string `json:"object_key" jsonschema:"kebab-case namespace name"`
	Bucket      string `json:"bucket" jsonschema:"bucket resource name (storageBackends/{b}/buckets/{n})"`
	DisplayName string `json:"display_name,omitempty" jsonschema:"optional display label"`
	CedarPolicy string `json:"cedar_policy,omitempty" jsonschema:"optional Cedar policy"`
}
type grantUserScopesArgs struct {
	UserName string   `json:"user_name" jsonschema:"tenants/{tenant_id_or_slug}/users/{u}"`
	Scopes   []string `json:"scopes,omitempty" jsonschema:"scope strings of form type:value (tenant:.., backend:.., bucket:.., object_key:..) or '*'"`
}
type restoreVersionArgs struct {
	VersionName string `json:"version_name" jsonschema:".../objects/{id}/versions/{ver}"`
}
type createUserArgs struct {
	TenantID        string   `json:"tenant_id" jsonschema:"tenant UUID"`
	Subject         string   `json:"subject" jsonschema:"login subject (email-style)"`
	DisplayName     string   `json:"display_name,omitempty" jsonschema:"display label"`
	InitialPassword string   `json:"initial_password" jsonschema:"≥12 chars; rotated by user on first login"`
	Roles           []string `json:"roles,omitempty" jsonschema:"roles to grant; e.g. tenant.user"`
}
type lifecycleRuleArg struct {
	ID              string `json:"id" jsonschema:"rule id (unique within bucket)"`
	Enabled         bool   `json:"enabled,omitempty" jsonschema:"whether the rule is active"`
	Match           string `json:"match,omitempty" jsonschema:"CEL filter (reserved)"`
	ExpirationAfter string `json:"expiration_after" jsonschema:"Go duration; e.g. '720h' = 30 days"`
}
type setLifecycleRulesArgs struct {
	BucketName      string             `json:"bucket_name" jsonschema:"Bucket resource name (storageBackends/{b}/buckets/{n})"`
	ResourceVersion string             `json:"resource_version,omitempty" jsonschema:"OCC guard from prior GetBucket"`
	Rules           []lifecycleRuleArg `json:"rules,omitempty" jsonschema:"rule list; pass empty to clear all rules"`
}
type revokeApiKeyArgs struct {
	Name string `json:"name" jsonschema:"tenants/{tenant_id_or_slug}/apiKeys/{id}"`
}
type setQuotaArgs struct {
	Name             string `json:"name" jsonschema:"Quota resource name"`
	MaxTotalBytes    int64  `json:"max_total_bytes,omitempty" jsonschema:"hard cap on bytes stored"`
	MaxObjectCount   int64  `json:"max_object_count,omitempty" jsonschema:"hard cap on number of objects"`
	MaxBytesPerDay   int64  `json:"max_bytes_per_day,omitempty" jsonschema:"daily upload byte budget"`
	MaxObjectsPerDay int64  `json:"max_objects_per_day,omitempty" jsonschema:"daily object-count budget"`
}
type auditExportArgs struct {
	Filter      string `json:"filter,omitempty" jsonschema:"CEL filter over AuditLogEntry — fields: actor_subject, actor_tenant_id, actor_audience, action, resource_name, request_id, source_ip, at, is_error"`
	Destination string `json:"destination,omitempty" jsonschema:"Advisory tag — recorded in result envelope; not yet acted upon"`
}

// ─── Mutating tool registration ─────────────────────────────────────────────

func registerWriteTools(s *mcpsdk.Server, c *Clients, filter *ToolFilter) {
	destructive := mcpsdk.ToolAnnotations{DestructiveHint: ptrTrue()}

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_create_object_key",
		Description: "Create an object key (logical namespace) under a tenant + bucket binding.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in createObjectKeyArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.OKey.CreateObjectKey(ctx, connect.NewRequest(&adminv1.CreateObjectKeyRequest{
			Parent:    "tenants/" + in.TenantID,
			ObjectKey: in.ObjectKey,
			ObjectKeyResource: &adminv1.ObjectKey{
				DisplayName: in.DisplayName,
				Bucket:      in.Bucket,
				CedarPolicy: in.CedarPolicy,
			},
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_grant_user_scopes",
		Description: "Add scopes to a user (e.g. bucket:foo, object_key:bar/baz).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in grantUserScopesArgs) (*mcpsdk.CallToolResult, any, error) {
		scopes, err := stringScopesToProto(in.Scopes)
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(c.Users.GrantScopes(ctx, connect.NewRequest(&iamv1.GrantScopesRequest{
			Name:   in.UserName,
			Scopes: scopes,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_restore_version",
		Description: "Make the named version `current` again (versioning must be enabled on the parent bucket).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in restoreVersionArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Object.RestoreObjectVersion(ctx, connect.NewRequest(&datav1.RestoreObjectVersionRequest{
			Name: in.VersionName,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_create_user",
		Description: "Create a new user under a tenant. Initial password is returned only via secret channels — do NOT echo it back to the LLM transcript.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in createUserArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Users.CreateUser(ctx, connect.NewRequest(&iamv1.CreateUserRequest{
			Parent:          "tenants/" + in.TenantID,
			Subject:         in.Subject,
			DisplayName:     in.DisplayName,
			InitialPassword: in.InitialPassword,
			Roles:           in.Roles,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_set_lifecycle_rules",
		Description: "Replace bucket lifecycle rules (expiration only in v2). Pass rules=[] to clear.",
		Annotations: &destructive,
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in setLifecycleRulesArgs) (*mcpsdk.CallToolResult, any, error) {
		pbRules := make([]*adminv1.LifecycleRule, 0, len(in.Rules))
		for _, r := range in.Rules {
			if r.ExpirationAfter == "" {
				return nil, nil, fmt.Errorf("rule %q: expiration_after required", r.ID)
			}
			dur, err := time.ParseDuration(r.ExpirationAfter)
			if err != nil {
				return nil, nil, fmt.Errorf("rule %q: invalid expiration_after %q: %w", r.ID, r.ExpirationAfter, err)
			}
			pbRules = append(pbRules, &adminv1.LifecycleRule{
				Id:      r.ID,
				Enabled: r.Enabled,
				Match:   r.Match,
				Action: &adminv1.LifecycleRule_Expiration{
					Expiration: &adminv1.LifecycleExpiration{After: durationpb.New(dur)},
				},
			})
		}
		return jsonResult(c.Bucket.SetLifecycleRules(ctx, connect.NewRequest(&adminv1.SetLifecycleRulesRequest{
			Name:            in.BucketName,
			ResourceVersion: in.ResourceVersion,
			Rules:           pbRules,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_revoke_api_key",
		Description: "Revoke an API key. The key stays in the table for audit but stops authenticating.",
		Annotations: &destructive,
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in revokeApiKeyArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.ApiKey.RevokeApiKey(ctx, connect.NewRequest(&iamv1.RevokeApiKeyRequest{
			Name: in.Name,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_set_quota",
		Description: "Set tenant or bucket usage caps. Provide caps in bytes / counts.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in setQuotaArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Quota.SetQuota(ctx, connect.NewRequest(&adminv1.SetQuotaRequest{
			Name: in.Name,
			Quota: &adminv1.Quota{
				MaxTotalBytes:    in.MaxTotalBytes,
				MaxObjectCount:   in.MaxObjectCount,
				MaxBytesPerDay:   in.MaxBytesPerDay,
				MaxObjectsPerDay: in.MaxObjectsPerDay,
			},
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_audit_export",
		Description: "Export a point-in-time JSON snapshot of audit-log entries (for SOC-2 / ISO-27001 evidence). Returns an Operation with the materialised dump in `response`. Capped at 10k rows — `truncated=true` signals the caller should narrow `filter`.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in auditExportArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Audit.ExportAuditLog(ctx, connect.NewRequest(&adminv1.ExportAuditLogRequest{
			Filter:      in.Filter,
			Destination: in.Destination,
		})))
	})

	// ─── Presign trio ──────────────────────────────────────────────────
	// Lets the agent move object bytes WITHOUT routing them through PALADIN
	// itself. Flow:
	//
	//   paladin_upload_object(parent, key, content_type, size_hint) →
	//       { upload_url, headers, object_id }   (PENDING in DB)
	//   <agent uses HTTP-fetch / curl / equivalent to PUT bytes to S3>
	//   paladin_complete_object(name, etag, checksum)              → AVAILABLE
	//
	//   paladin_presign_download(name, ttl) → { url } → agent GETs from S3.
	//
	// Presign URLs already pass through Cedar + capability caveats on
	// issuance — agent without `ops=put` caveat is rejected before any
	// URL is minted. The TTL on the URL is bounded by cfg.Limits.Presign.

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_upload_object",
		Description: "Reserve a new object slot and return a presigned PUT URL. Agent uploads bytes directly to S3, then calls paladin_complete_object to promote the row to AVAILABLE.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in uploadObjectArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Object.UploadObject(ctx, connect.NewRequest(&datav1.UploadObjectRequest{
			Parent:         in.Parent,
			Key:            in.Key,
			ContentType:    in.ContentType,
			SizeHintBytes:  in.SizeHintBytes,
			Metadata:       in.Metadata,
			Tags:           in.Tags,
			IdempotencyKey: in.IdempotencyKey,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_complete_object",
		Description: "Promote a PENDING object to AVAILABLE after the agent finished its presigned PUT. Etag from the S3 response goes into `etag`; checksum is optional.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in completeObjectArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Object.CompleteObject(ctx, connect.NewRequest(&datav1.CompleteObjectRequest{
			Name:          in.Name,
			Etag:          in.Etag,
			ChecksumValue: in.ChecksumValue,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_presign_download",
		Description: "Issue a presigned GET URL for an existing AVAILABLE object. Agent fetches bytes directly from S3.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in presignDownloadArgs) (*mcpsdk.CallToolResult, any, error) {
		req := &datav1.PresignDownloadRequest{
			Name:               in.Name,
			ContentDisposition: in.ContentDisposition,
		}
		if in.TtlSeconds > 0 {
			req.Ttl = durationpb.New(time.Duration(in.TtlSeconds) * time.Second)
		}
		return jsonResult(c.Presign.PresignDownload(ctx, connect.NewRequest(req)))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_set_object_tags",
		Description: "Replace the tag map on an existing object. Pass an empty `tags` map to clear all tags.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in setObjectTagsArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.ObjectTag.PutObjectTags(ctx, connect.NewRequest(&datav1.PutObjectTagsRequest{
			Name: in.Name,
			Tags: in.Tags,
		})))
	})
}

// ─── Presign / tag mutating arg types ───────────────────────────────────────

type uploadObjectArgs struct {
	Parent         string            `json:"parent" jsonschema:"tenants/{tenant_id_or_slug}/objectKeys/{ok}"`
	Key            string            `json:"key,omitempty" jsonschema:"object key under the namespace; empty = server uses the new object_id as key"`
	ContentType    string            `json:"content_type" jsonschema:"MIME type (e.g. application/pdf)"`
	SizeHintBytes  int64             `json:"size_hint_bytes,omitempty" jsonschema:"optional client-reported size"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty" jsonschema:"replay-safe key — same value returns the cached response"`
}
type completeObjectArgs struct {
	Name          string `json:"name" jsonschema:"tenants/{tenant_id_or_slug}/objectKeys/{ok}/objects/{id}"`
	Etag          string `json:"etag" jsonschema:"etag the S3 endpoint returned on the PUT"`
	ChecksumValue string `json:"checksum_value,omitempty" jsonschema:"hex-encoded checksum"`
}
type presignDownloadArgs struct {
	Name               string `json:"name" jsonschema:"object resource name"`
	TtlSeconds         int64  `json:"ttl_seconds,omitempty" jsonschema:"optional override; capped server-side by cfg.Limits.Presign.get_ttl"`
	ContentDisposition string `json:"content_disposition,omitempty" jsonschema:"e.g. 'attachment; filename=\"report.pdf\"' to force browser download"`
}
type setObjectTagsArgs struct {
	Name string            `json:"name" jsonschema:"object resource name"`
	Tags map[string]string `json:"tags" jsonschema:"replacement tag map; empty = clear"`
}

// ─── Resources ──────────────────────────────────────────────────────────────

func registerResources(s *mcpsdk.Server, c *Clients) {
	addJSONResource(s, c, "paladin://backends", "Storage backends", "All registered storage backends as JSON.",
		func(ctx context.Context) (any, error) {
			r, err := c.Backend.ListBackends(ctx, connect.NewRequest(&adminv1.ListBackendsRequest{
				Page: &commonv1.PageRequest{PageSize: 200},
			}))
			if err != nil {
				return nil, err
			}
			return r.Msg, nil
		})
	addJSONResource(s, c, "paladin://buckets", "Buckets", "All buckets across all backends.",
		func(ctx context.Context) (any, error) {
			r, err := c.Bucket.ListBuckets(ctx, connect.NewRequest(&adminv1.ListBucketsRequest{
				Page: &commonv1.PageRequest{PageSize: 200},
			}))
			if err != nil {
				return nil, err
			}
			return r.Msg, nil
		})
	addJSONResource(s, c, "paladin://tenants", "Tenants", "All tenants.",
		func(ctx context.Context) (any, error) {
			r, err := c.Tenant.ListTenants(ctx, connect.NewRequest(&adminv1.ListTenantsRequest{
				Page: &commonv1.PageRequest{PageSize: 200},
			}))
			if err != nil {
				return nil, err
			}
			return r.Msg, nil
		})
}

func addJSONResource(s *mcpsdk.Server, _ *Clients, uri, name, desc string, fetch func(ctx context.Context) (any, error)) {
	s.AddResource(&mcpsdk.Resource{
		URI:         uri,
		Name:        name,
		Description: desc,
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		v, err := fetch(ctx)
		if err != nil {
			return nil, err
		}
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return nil, err
		}
		return &mcpsdk.ReadResourceResult{
			Contents: []*mcpsdk.ResourceContents{{
				URI:      req.Params.URI,
				MIMEType: "application/json",
				Text:     string(b),
			}},
		}, nil
	})
}

// ─── Prompts ────────────────────────────────────────────────────────────────

func registerPrompts(s *mcpsdk.Server) {
	s.AddPrompt(&mcpsdk.Prompt{
		Name:        "audit_access_for_tenant",
		Description: "Audit who has access to what within a tenant.",
		Arguments: []*mcpsdk.PromptArgument{
			{Name: "tenant_id", Description: "tenant UUID", Required: true},
		},
	}, func(_ context.Context, req *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
		t := req.Params.Arguments["tenant_id"]
		if t == "" {
			return nil, fmt.Errorf("tenant_id required")
		}
		return &mcpsdk.GetPromptResult{
			Messages: []*mcpsdk.PromptMessage{{
				Role: "user",
				Content: &mcpsdk.TextContent{Text: fmt.Sprintf(
					"Audit access for tenant %s.\n"+
						"1) Use paladin_list_object_keys to enumerate the namespaces.\n"+
						"2) For each, fetch its Cedar policy via the PALADIN admin API.\n"+
						"3) Identify which user_ids hold scopes that match.\n"+
						"4) Summarise findings with concrete principal→action→resource bindings.\n",
					t,
				)},
			}},
		}, nil
	})

	s.AddPrompt(&mcpsdk.Prompt{
		Name:        "rotate_backend_credentials",
		Description: "Plan a credential rotation for a storage backend.",
		Arguments: []*mcpsdk.PromptArgument{
			{Name: "backend_id", Description: "backend id to rotate", Required: true},
		},
	}, func(_ context.Context, req *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
		b := req.Params.Arguments["backend_id"]
		if b == "" {
			return nil, fmt.Errorf("backend_id required")
		}
		return &mcpsdk.GetPromptResult{
			Messages: []*mcpsdk.PromptMessage{{
				Role: "user",
				Content: &mcpsdk.TextContent{Text: fmt.Sprintf(
					"Plan a safe credential rotation for backend %s.\n"+
						"Steps to consider:\n"+
						"  • Confirm the new secret_ref is provisioned in the secret manager.\n"+
						"  • Call paladin_test_backend to verify reachability before swap.\n"+
						"  • Decide a grace_period that exceeds the max presign TTL in use.\n"+
						"  • Flag any presigned URLs minted before rotation that may break.\n",
					b,
				)},
			}},
		}, nil
	})
}

func ptrTrue() *bool { v := true; return &v }

// stringScopesToProto decodes "type:value" strings into commonv1.Scope. The
// LLM-friendly text form keeps the tool input flat — no nested object array.
func stringScopesToProto(in []string) ([]*commonv1.Scope, error) {
	out := make([]*commonv1.Scope, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		var sc commonv1.Scope
		if s == "*" {
			sc.Value = "*"
			out = append(out, &sc)
			continue
		}
		idx := -1
		for i := 0; i < len(s); i++ {
			if s[i] == ':' {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, fmt.Errorf("scope %q must be type:value", s)
		}
		switch s[:idx] {
		case "tenant":
			sc.Type = commonv1.ScopeType_SCOPE_TYPE_TENANT
		case "backend":
			sc.Type = commonv1.ScopeType_SCOPE_TYPE_BACKEND
		case "bucket":
			sc.Type = commonv1.ScopeType_SCOPE_TYPE_BUCKET
		case "object_key":
			sc.Type = commonv1.ScopeType_SCOPE_TYPE_OBJECT_KEY
		default:
			return nil, fmt.Errorf("unknown scope type %q", s[:idx])
		}
		sc.Value = s[idx+1:]
		out = append(out, &sc)
	}
	return out, nil
}
