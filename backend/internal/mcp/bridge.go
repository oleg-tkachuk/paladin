// Package mcp wires the Paladin three-plane Connect API into a Model Context
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
	"google.golang.org/protobuf/types/known/fieldmaskpb"

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
	OKey     adminv1connect.CollectionServiceClient
	Policy   adminv1connect.PolicyServiceClient
	Quota    adminv1connect.QuotaServiceClient
	Audit    adminv1connect.AuditLogServiceClient
	EventSub adminv1connect.EventSubscriptionServiceClient
	CEL      adminv1connect.CELServiceClient
	System   adminv1connect.SystemServiceClient

	Object        datav1connect.ObjectServiceClient
	Multipart     datav1connect.MultipartUploadServiceClient
	Presign       datav1connect.PresignServiceClient
	ObjectTag     datav1connect.ObjectTagServiceClient
	Batch         datav1connect.BatchServiceClient
	DataOperation datav1connect.OperationServiceClient

	Auth  iamv1connect.AuthServiceClient
	Users iamv1connect.UserServiceClient
}

// NewClients constructs a Clients bundle. Bearer is the access token the MCP
// service-account holds; it is injected into every outbound Connect request.
func NewClients(httpc *http.Client, adminURL, dataURL, iamURL, bearer string) *Clients {
	return NewClientsWithCapability(httpc, adminURL, dataURL, iamURL, bearer, "")
}

// NewClientsWithCapability is the cap-aware constructor. capabilityToken
// (when non-empty) is forwarded as `X-Paladin-Capability` on every outbound
// Connect call so the destination plane's auth.CapabilityInterceptor
// sees and stamps it on the request context.
//
// The two tokens stack: bearer is the JWT auth (admin / iam audience),
// capabilityToken is the optional capability that grants fine-grained
// caveats. Either or both may be present. Empty values are not sent.
//
// MCP HTTP transport forwards the capability via the same X-Paladin-Capability
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
					req.Header().Set("X-Paladin-Capability", capabilityToken)
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
		OKey:     adminv1connect.NewCollectionServiceClient(httpc, adminURL, authInjector),
		Policy:   adminv1connect.NewPolicyServiceClient(httpc, adminURL, authInjector),
		Quota:    adminv1connect.NewQuotaServiceClient(httpc, adminURL, authInjector),
		Audit:    adminv1connect.NewAuditLogServiceClient(httpc, adminURL, authInjector),
		EventSub: adminv1connect.NewEventSubscriptionServiceClient(httpc, adminURL, authInjector),
		CEL:      adminv1connect.NewCELServiceClient(httpc, adminURL, authInjector),
		System:   adminv1connect.NewSystemServiceClient(httpc, adminURL, authInjector),

		Object:        datav1connect.NewObjectServiceClient(httpc, dataURL, authInjector),
		Multipart:     datav1connect.NewMultipartUploadServiceClient(httpc, dataURL, authInjector),
		Presign:       datav1connect.NewPresignServiceClient(httpc, dataURL, authInjector),
		ObjectTag:     datav1connect.NewObjectTagServiceClient(httpc, dataURL, authInjector),
		Batch:         datav1connect.NewBatchServiceClient(httpc, dataURL, authInjector),
		DataOperation: datav1connect.NewOperationServiceClient(httpc, dataURL, authInjector),

		Auth:  iamv1connect.NewAuthServiceClient(httpc, iamURL, authInjector),
		Users: iamv1connect.NewUserServiceClient(httpc, iamURL, authInjector),
	}
}

// NewServer constructs an MCP server with the Paladin tool / resource / prompt
// catalog wired up. The ToolFilter selects which tools are registered;
// see internal/mcp/profile.go for the YAML-driven gating model.
func NewServer(name, version string, c *Clients, filter *ToolFilter) *mcpsdk.Server {
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: name, Version: version}, &mcpsdk.ServerOptions{
		// An empty (non-nil) ServerCapabilities suppresses the SDK's nil
		// default of {"logging":{}}, which it advertises "for historical
		// reasons". We implement no logging handler, and the feature is
		// deprecated as of protocol version 2026-07-28 (SEP-2577) along with
		// roots and sampling — so advertising it told clients we support a
		// dying feature we never had. Tools / resources / prompts are still
		// inferred from the registrations below; only the unset fields are
		// inferred, so leaving them nil here is what we want.
		Capabilities: &mcpsdk.ServerCapabilities{},
	})
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
type listCollectionsArgs struct {
	// `tenant_id_or_slug` accepted as input — the field name stays
	// `tenant_id` for MCP-client backwards compat, but the resolver
	// downstream (apiutil.ParseTenantNameRef) accepts both forms,
	// matching the UI's slug-first URL behaviour.
	TenantID string `json:"tenant_id" jsonschema:"tenant UUID or slug (e.g. 'platform')"`
	PageSize int32  `json:"page_size,omitempty" jsonschema:"page size; default 50, max 1000"`
}
type queryObjectsArgs struct {
	TenantID   string `json:"tenant_id" jsonschema:"tenant UUID or slug"`
	Collection string `json:"collection" jsonschema:"collection (namespace) name"`
	Filter     string `json:"filter,omitempty" jsonschema:"optional CEL filter, e.g. tags['type']=='invoice'"`
	PageSize   int32  `json:"page_size,omitempty" jsonschema:"page size; default 100, max 1000"`
}
type getQuotaArgs struct {
	Name string `json:"name" jsonschema:"Quota resource name (tenants/{tenant_id_or_slug}/quota | storageBackends/{b}/buckets/{n}/quota)"`
}
type validatePolicyArgs struct {
	CedarPolicy string `json:"cedar_policy" jsonschema:"Cedar policy text"`
}
type listVersionsArgs struct {
	ObjectName string `json:"object_name" jsonschema:"Object resource name (tenants/{tenant_id_or_slug}/collections/{ok}/objects/{id})"`
	PageSize   int32  `json:"page_size,omitempty" jsonschema:"page size; default 50, max 1000"`
}
type getVersionArgs struct {
	VersionName string `json:"version_name" jsonschema:".../objects/{id}/versions/{ver}"`
}
type getEffectivePolicyArgs struct {
	ResourceName string `json:"resource_name" jsonschema:"Any resource name; tenants/{tenant_id_or_slug}/collections/{ok} works for namespace-level"`
}
type simulateAuthzArgs struct {
	PrincipalSubject  string   `json:"principal_subject" jsonschema:"subject (user_id or service-account ref)"`
	PrincipalTenantID string   `json:"principal_tenant_id,omitempty" jsonschema:"optional tenant UUID or slug for the simulated principal"`
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
		Name:        "paladin_list_collections",
		Description: "List collections (logical namespaces) within a tenant.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listCollectionsArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.OKey.ListCollections(ctx, connect.NewRequest(&adminv1.ListCollectionsRequest{
			Parent: "tenants/" + in.TenantID,
			Page:   &commonv1.PageRequest{PageSize: in.PageSize},
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_query_objects",
		Description: "List objects within a collection, optionally filtered by CEL.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in queryObjectsArgs) (*mcpsdk.CallToolResult, any, error) {
		parent := fmt.Sprintf("tenants/%s/collections/%s", in.TenantID, in.Collection)
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
		Description: "Type-check a Cedar policy against the Paladin schema.",
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
		Description: "Return the merged Cedar policy stack the engine compiles for a resource (tenant + collection layers).",
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
		Name:        "paladin_get_collection",
		Description: "Fetch a single collection (tenants/{tenant_id_or_slug}/collections/{ok}).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getNameArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.OKey.GetCollection(ctx, connect.NewRequest(&adminv1.GetCollectionRequest{Name: in.Name})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_get_object",
		Description: "Fetch object metadata (NOT the body — body is fetched via paladin_presign_download).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getNameArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Object.GetObject(ctx, connect.NewRequest(&datav1.GetObjectRequest{Name: in.Name})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_lookup_object",
		Description: "Resolve an object by its human key within a collection (the inverse of having the object_id). Returns the same metadata as paladin_get_object.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in lookupObjectArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Object.LookupObject(ctx, connect.NewRequest(&datav1.LookupObjectRequest{
			Parent: in.Parent,
			Key:    in.Key,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_count_objects",
		Description: "Count objects under a collection, optionally narrowed by a CEL filter. Cheaper than paging paladin_query_objects when only the total is needed.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in countObjectsArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Object.CountObjects(ctx, connect.NewRequest(&datav1.CountObjectsRequest{
			Parent: in.Parent,
			Filter: in.Filter,
		})))
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
		Name:        "paladin_get_subscription",
		Description: "Read a single event subscription by resource name.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getNameArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.EventSub.GetSubscription(ctx, connect.NewRequest(&adminv1.GetSubscriptionRequest{Name: in.Name})))
	})

	// CEL expression validator. Same trust posture as PolicyService.Validate
	// — admin audience, no DB, no audit. Lets the agent type-check a CEL
	// expression against a named Paladin schema (Object | Collection |
	// AuditLogEntry | EventEnvelope) before passing it into a list-RPC
	// query, lifecycle.match, or eventsub.filter.
	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_validate_cel",
		Description: "Compile-check a CEL expression against a Paladin schema. Returns {valid, message, line, column}. Empty expression always validates.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in validateCELArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.CEL.Validate(ctx, connect.NewRequest(&adminv1.ValidateCELRequest{
			Schema:     in.Schema,
			Expression: in.Expression,
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

	// Tag / multipart read surface (data plane).

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_list_distinct_tags",
		Description: "List the distinct tag keys/values currently in use under a collection — useful before filtering or tagging.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listChildrenArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.ObjectTag.ListDistinctTags(ctx, connect.NewRequest(&datav1.ListDistinctTagsRequest{Parent: in.Parent})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_list_parts",
		Description: "List the parts uploaded so far for an in-progress multipart upload.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listPartsArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Multipart.ListParts(ctx, connect.NewRequest(&datav1.ListPartsRequest{
			ObjectName: in.ObjectName,
			UploadId:   in.UploadID,
			Page:       &commonv1.PageRequest{PageSize: in.PageSize},
		})))
	})

	// Admin discovery gaps.

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_get_audit_entry",
		Description: "Read a single audit-log entry by its id (the granular companion to paladin_audit_recent).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in auditEntryArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Audit.GetAuditLogEntry(ctx, connect.NewRequest(&adminv1.GetAuditLogEntryRequest{EntryId: in.EntryID})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_system_config",
		Description: "Read the platform's effective runtime configuration (admin-profile only; not exposed to agent_safe).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.System.GetConfig(ctx, connect.NewRequest(&adminv1.GetConfigRequest{})))
	})
}

// ─── Mutating tool argument types ───────────────────────────────────────────

type createCollectionArgs struct {
	TenantID    string `json:"tenant_id" jsonschema:"tenant UUID or slug"`
	Collection  string `json:"collection" jsonschema:"collection path: kebab-case segments joined by '/' (e.g. 'assets-prod' or 'invoices/2026/q1')"`
	Bucket      string `json:"bucket" jsonschema:"bucket resource name (storageBackends/{b}/buckets/{n})"`
	DisplayName string `json:"display_name,omitempty" jsonschema:"optional display label"`
	CedarPolicy string `json:"cedar_policy,omitempty" jsonschema:"optional Cedar policy"`
}
type grantUserScopesArgs struct {
	UserName string   `json:"user_name" jsonschema:"tenants/{tenant_id_or_slug}/users/{u}"`
	Scopes   []string `json:"scopes,omitempty" jsonschema:"scope strings of form type:value (tenant:.., backend:.., bucket:.., collection:..) or '*'"`
}
type restoreVersionArgs struct {
	VersionName string `json:"version_name" jsonschema:".../objects/{id}/versions/{ver}"`
}
type createUserArgs struct {
	TenantID        string   `json:"tenant_id" jsonschema:"tenant UUID or slug"`
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
	ResourceVersion string             `json:"resource_version" jsonschema:"OCC guard; required — take it from a prior GetBucket"`
	Rules           []lifecycleRuleArg `json:"rules,omitempty" jsonschema:"rule list; pass empty to clear all rules"`
}

// CEL validator args. Schema is one of the registered names exposed by
// internal/filter/cel.SchemaByName; the handler returns
// CodeInvalidArgument on anything else.
type validateCELArgs struct {
	Schema     string `json:"schema" jsonschema:"one of Object | Collection | AuditLogEntry | EventEnvelope"`
	Expression string `json:"expression,omitempty" jsonschema:"CEL source; empty validates as 'match all'"`
}

// EventSubscription mutating args. Sink is a discriminated union: only
// one of {http_url, kafka_brokers+kafka_topic, sqs_queue_url+sqs_region}
// should be populated per call. The CEL filter is validated server-side
// against EventEnvelope; empty = receive all events for the tenant.
type createSubscriptionArgs struct {
	TenantID string `json:"tenant_id" jsonschema:"tenant UUID or slug"`
	Filter   string `json:"filter,omitempty" jsonschema:"CEL filter against EventEnvelope; empty = all events"`
	Disabled bool   `json:"disabled,omitempty" jsonschema:"true to create in disabled state"`

	// HTTP sink
	HTTPURL         string `json:"http_url,omitempty" jsonschema:"HTTPS endpoint receiving signed POSTs"`
	HTTPSecretRef   string `json:"http_signing_secret_ref,omitempty" jsonschema:"Secret name holding the HMAC-SHA256 signing key; empty = unsigned"`
	HTTPMaxAttempts int32  `json:"http_max_attempts,omitempty" jsonschema:"retry cap; 1..10, defaults server-side when 0"`

	// Kafka sink
	KafkaBrokers string `json:"kafka_brokers,omitempty" jsonschema:"comma-separated bootstrap.servers"`
	KafkaTopic   string `json:"kafka_topic,omitempty" jsonschema:"topic name"`

	// SQS sink
	SQSQueueURL string `json:"sqs_queue_url,omitempty" jsonschema:"AWS SQS queue URL"`
	SQSRegion   string `json:"sqs_region,omitempty" jsonschema:"AWS region; e.g. us-east-1"`
}

// updateSubscriptionArgs reuses createSubscriptionArgs verbatim for the
// payload and adds Name + ResourceVersion for OCC.
type updateSubscriptionArgs struct {
	createSubscriptionArgs
	Name            string `json:"name" jsonschema:"tenants/{tenant_id_or_slug}/eventSubscriptions/{id}"`
	ResourceVersion string `json:"resource_version" jsonschema:"OCC guard; required — take it from a prior list/get"`
}

type deleteSubscriptionArgs struct {
	Name            string `json:"name" jsonschema:"tenants/{tenant_id_or_slug}/eventSubscriptions/{id}"`
	ResourceVersion string `json:"resource_version" jsonschema:"OCC guard; required — take it from a prior list/get"`
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
		Name:        "paladin_create_collection",
		Description: "Create a collection (logical namespace) under a tenant + bucket binding.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in createCollectionArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.OKey.CreateCollection(ctx, connect.NewRequest(&adminv1.CreateCollectionRequest{
			Parent:     "tenants/" + in.TenantID,
			Collection: in.Collection,
			CollectionResource: &adminv1.Collection{
				DisplayName: in.DisplayName,
				Bucket:      in.Bucket,
				CedarPolicy: in.CedarPolicy,
			},
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_grant_user_scopes",
		Description: "Add scopes to a user (e.g. bucket:foo, collection:bar/baz).",
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

	// EventSubscription mutating surface. The Test variant is non-
	// destructive but lives next to the rest for cohesion. Create / Update
	// / Delete are gated as destructive — an agent that gets `ops=manage`
	// could redirect production webhooks if uncaught.
	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_create_subscription",
		Description: "Create an event subscription for a tenant. Sink is one of HTTP / Kafka / SQS. CEL filter is validated against EventEnvelope; empty = all events.",
		Annotations: &destructive,
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in createSubscriptionArgs) (*mcpsdk.CallToolResult, any, error) {
		sub, err := buildEventSubscription(in)
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(c.EventSub.CreateSubscription(ctx, connect.NewRequest(&adminv1.CreateSubscriptionRequest{
			Parent:       "tenants/" + in.TenantID,
			Subscription: sub,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_update_subscription",
		Description: "Replace filter / sink / disabled on an existing subscription. Resource name from paladin_list_subscriptions; pass resource_version from the prior fetch for OCC.",
		Annotations: &destructive,
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in updateSubscriptionArgs) (*mcpsdk.CallToolResult, any, error) {
		sub, err := buildEventSubscription(in.createSubscriptionArgs)
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(c.EventSub.UpdateSubscription(ctx, connect.NewRequest(&adminv1.UpdateSubscriptionRequest{
			Name:            in.Name,
			ResourceVersion: in.ResourceVersion,
			UpdateMask:      &fieldmaskpb.FieldMask{Paths: []string{"filter", "sink", "disabled"}},
			Subscription:    sub,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_delete_subscription",
		Description: "Delete an event subscription. Existing in-flight deliveries continue; no events sent after this point.",
		Annotations: &destructive,
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in deleteSubscriptionArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.EventSub.DeleteSubscription(ctx, connect.NewRequest(&adminv1.DeleteSubscriptionRequest{
			Name:            in.Name,
			ResourceVersion: in.ResourceVersion,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_test_subscription",
		Description: "Deliver a synthetic event to the configured sink. Returns {delivered, status_code, error_message}. Safe — does not mutate any state.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getNameArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.EventSub.TestSubscription(ctx, connect.NewRequest(&adminv1.TestSubscriptionRequest{Name: in.Name})))
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
	// Lets the agent move object bytes WITHOUT routing them through Paladin
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

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_delete_object",
		Description: "Delete an object. Default is a soft delete (recoverable via paladin_restore_version); set `permanent` to purge it irrecoverably. Locked objects refuse deletion unless the caller holds GOVERNANCE bypass.",
		Annotations: &destructive,
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in deleteObjectArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Object.DeleteObject(ctx, connect.NewRequest(&datav1.DeleteObjectRequest{
			Name:            in.Name,
			ResourceVersion: in.ResourceVersion,
			Permanent:       in.Permanent,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_copy_object",
		Description: "Server-side copy of an object to a destination collection + key. The source is left in place (use paladin_delete_object after for a move).",
		Annotations: &destructive,
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in copyObjectArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Object.CopyObject(ctx, connect.NewRequest(&datav1.CopyObjectRequest{
			SourceName:            in.SourceName,
			DestinationCollection: in.DestinationCollection,
			DestinationKey:        in.DestinationKey,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_batch_delete",
		Description: "Asynchronously delete many objects under one collection. Select either by explicit `names` (≤100) or a CEL `filter`. Returns a long-running Operation; poll it via paladin_get_operation.",
		Annotations: &destructive,
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in batchDeleteArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Batch.BatchDeleteObjects(ctx, connect.NewRequest(&datav1.BatchDeleteObjectsRequest{
			Parent: in.Parent,
			Selector: &datav1.ObjectSelector{
				Names:  in.Names,
				Filter: in.Filter,
			},
			Permanent: in.Permanent,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_batch_copy",
		Description: "Asynchronously server-side-copy many objects into a destination collection. Select sources by `names` (≤100) or CEL `filter`; each destination key is computed by `destination_key_template` (a CEL expression over the source Object). Returns an Operation; poll via paladin_get_operation.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in batchCopyArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Batch.BatchCopyObjects(ctx, connect.NewRequest(&datav1.BatchCopyObjectsRequest{
			SourceParent: in.SourceParent,
			Selector: &datav1.ObjectSelector{
				Names:  in.Names,
				Filter: in.Filter,
			},
			DestinationCollection:  in.DestinationCollection,
			DestinationKeyTemplate: in.DestinationKeyTemplate,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_batch_restore",
		Description: "Asynchronously restore many soft-deleted objects under one collection. Select by `names` (≤100) or CEL `filter`. Returns an Operation; poll via paladin_get_operation.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in batchRestoreArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Batch.BatchRestoreObjects(ctx, connect.NewRequest(&datav1.BatchRestoreObjectsRequest{
			Parent: in.Parent,
			Selector: &datav1.ObjectSelector{
				Names:  in.Names,
				Filter: in.Filter,
			},
		})))
	})

	// Object / tag metadata mutations (data plane).

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_update_object",
		Description: "Patch an object's mutable metadata. Only the fields named in `update_mask` are changed (e.g. \"metadata,tags,content_type\"); omit the mask to replace all of them. Does not touch the object body.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in updateObjectArgs) (*mcpsdk.CallToolResult, any, error) {
		req := &datav1.UpdateObjectRequest{
			Name:            in.Name,
			ResourceVersion: in.ResourceVersion,
			Metadata:        in.Metadata,
			Tags:            in.Tags,
			ContentType:     in.ContentType,
			ExternalRef:     in.ExternalRef,
		}
		if len(in.UpdateMask) > 0 {
			req.UpdateMask = &fieldmaskpb.FieldMask{Paths: in.UpdateMask}
		}
		return jsonResult(c.Object.UpdateObject(ctx, connect.NewRequest(req)))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_delete_object_tags",
		Description: "Remove specific tag keys from an object. Pass the keys to drop in `keys`; the rest are left intact. (Use paladin_set_object_tags with an empty map to clear all tags at once.)",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in deleteObjectTagsArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.ObjectTag.DeleteObjectTags(ctx, connect.NewRequest(&datav1.DeleteObjectTagsRequest{
			Name:            in.Name,
			ResourceVersion: in.ResourceVersion,
			Keys:            in.Keys,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_batch_update_tags",
		Description: "Asynchronously merge (or, with `replace`, overwrite) a tag map across many objects under one collection. Select by `names` (≤100) or CEL `filter`. Returns an Operation; poll via paladin_get_operation.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in batchUpdateTagsArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Batch.BatchUpdateTags(ctx, connect.NewRequest(&datav1.BatchUpdateTagsRequest{
			Parent: in.Parent,
			Selector: &datav1.ObjectSelector{
				Names:  in.Names,
				Filter: in.Filter,
			},
			Tags:    in.Tags,
			Replace: in.Replace,
		})))
	})

	// Upload-flow surface: single-shot re-sign + the multipart lifecycle.

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_regenerate_upload_url",
		Description: "Re-mint a presigned PUT URL for an object whose upload was initiated but not completed (e.g. the first URL expired). Does not create a new object.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in regenerateUploadURLArgs) (*mcpsdk.CallToolResult, any, error) {
		req := &datav1.RegenerateUploadUrlRequest{Name: in.Name}
		if in.TtlSeconds > 0 {
			req.Ttl = durationpb.New(time.Duration(in.TtlSeconds) * time.Second)
		}
		return jsonResult(c.Presign.RegenerateUploadUrl(ctx, connect.NewRequest(req)))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_initiate_multipart_upload",
		Description: "Begin a multipart upload for a large object. Returns an upload_id + object name; presign each part with paladin_presign_part, then finalise with paladin_complete_multipart_upload.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in initiateMultipartArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Multipart.InitiateMultipartUpload(ctx, connect.NewRequest(&datav1.InitiateMultipartUploadRequest{
			Parent:      in.Parent,
			Key:         in.Key,
			ContentType: in.ContentType,
			SizeBytes:   in.SizeBytes,
			Metadata:    in.Metadata,
			Tags:        in.Tags,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_presign_part",
		Description: "Mint a presigned PUT URL for one part (1-based `part_number`) of an in-progress multipart upload. The agent PUTs the bytes to S3 and keeps the returned ETag for completion.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in presignPartArgs) (*mcpsdk.CallToolResult, any, error) {
		req := &datav1.PresignPartRequest{
			ObjectName: in.ObjectName,
			UploadId:   in.UploadID,
			PartNumber: in.PartNumber,
		}
		if in.TtlSeconds > 0 {
			req.Ttl = durationpb.New(time.Duration(in.TtlSeconds) * time.Second)
		}
		return jsonResult(c.Multipart.PresignPart(ctx, connect.NewRequest(req)))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_complete_multipart_upload",
		Description: "Finalise a multipart upload by committing the ordered part list (each {part_number, etag} from paladin_presign_part). Returns the committed Object.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in completeMultipartArgs) (*mcpsdk.CallToolResult, any, error) {
		parts := make([]*datav1.CompletedPart, 0, len(in.Parts))
		for _, p := range in.Parts {
			parts = append(parts, &datav1.CompletedPart{
				PartNumber:    p.PartNumber,
				Etag:          p.Etag,
				ChecksumValue: p.ChecksumValue,
			})
		}
		return jsonResult(c.Multipart.CompleteMultipartUpload(ctx, connect.NewRequest(&datav1.CompleteMultipartUploadRequest{
			ObjectName: in.ObjectName,
			UploadId:   in.UploadID,
			Parts:      parts,
		})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_abort_multipart_upload",
		Description: "Abort an in-progress multipart upload and discard its uploaded parts. Safe — it never touches a committed object.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in abortMultipartArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Multipart.AbortMultipartUpload(ctx, connect.NewRequest(&datav1.AbortMultipartUploadRequest{
			ObjectName: in.ObjectName,
			UploadId:   in.UploadID,
		})))
	})

	// Operation control (data plane) + quota maintenance (admin).

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_cancel_operation",
		Description: "Request cancellation of a running long-running operation (e.g. a batch job). Returns the operation's updated state.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getNameArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.DataOperation.CancelOperation(ctx, connect.NewRequest(&datav1.CancelOperationRequest{Name: in.Name})))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_reset_usage",
		Description: "Reset the accumulated usage counters on a tenant- or bucket-scoped quota (admin-profile only). Does not change the quota limits.",
		Annotations: &destructive,
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getNameArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Quota.ResetUsage(ctx, connect.NewRequest(&adminv1.ResetUsageRequest{Name: in.Name})))
	})
}

// ─── Presign / tag mutating arg types ───────────────────────────────────────

type uploadObjectArgs struct {
	Parent         string            `json:"parent" jsonschema:"tenants/{tenant_id_or_slug}/collections/{ok}"`
	Key            string            `json:"key,omitempty" jsonschema:"object key under the namespace; empty = server uses the new object_id as key"`
	ContentType    string            `json:"content_type" jsonschema:"MIME type (e.g. application/pdf)"`
	SizeHintBytes  int64             `json:"size_hint_bytes,omitempty" jsonschema:"optional client-reported size"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty" jsonschema:"replay-safe key — same value returns the cached response"`
}
type completeObjectArgs struct {
	Name          string `json:"name" jsonschema:"tenants/{tenant_id_or_slug}/collections/{ok}/objects/{id}"`
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
type deleteObjectArgs struct {
	Name            string `json:"name" jsonschema:"object resource name"`
	ResourceVersion string `json:"resource_version" jsonschema:"optimistic-concurrency token; required — take it from a prior list/get"`
	Permanent       bool   `json:"permanent,omitempty" jsonschema:"true = irrecoverable purge; default false = soft delete"`
}
type copyObjectArgs struct {
	SourceName            string `json:"source_name" jsonschema:"source object resource name"`
	DestinationCollection string `json:"destination_collection" jsonschema:"destination Collection resource name (tenants/{t}/collections/{ok})"`
	DestinationKey        string `json:"destination_key" jsonschema:"destination key (path) under that collection"`
}
type batchDeleteArgs struct {
	Parent    string   `json:"parent" jsonschema:"tenants/{tenant_id_or_slug}/collections/{ok}"`
	Names     []string `json:"names,omitempty" jsonschema:"explicit object resource names; ≤100. Use filter for larger sets."`
	Filter    string   `json:"filter,omitempty" jsonschema:"CEL filter over Object, evaluated lazily in the worker"`
	Permanent bool     `json:"permanent,omitempty" jsonschema:"true = irrecoverable purge; default false = soft delete"`
}
type batchCopyArgs struct {
	SourceParent           string   `json:"source_parent" jsonschema:"source Collection: tenants/{tenant_id_or_slug}/collections/{ok}"`
	Names                  []string `json:"names,omitempty" jsonschema:"explicit source object resource names; ≤100. Use filter for larger sets."`
	Filter                 string   `json:"filter,omitempty" jsonschema:"CEL filter over the source Object"`
	DestinationCollection  string   `json:"destination_collection" jsonschema:"destination Collection resource name"`
	DestinationKeyTemplate string   `json:"destination_key_template" jsonschema:"CEL expression over the source Object that computes each destination key (e.g. 'object.key')"`
}
type batchRestoreArgs struct {
	Parent string   `json:"parent" jsonschema:"tenants/{tenant_id_or_slug}/collections/{ok}"`
	Names  []string `json:"names,omitempty" jsonschema:"explicit object resource names; ≤100. Use filter for larger sets."`
	Filter string   `json:"filter,omitempty" jsonschema:"CEL filter over Object, evaluated lazily in the worker"`
}
type lookupObjectArgs struct {
	Parent string `json:"parent" jsonschema:"collection the key lives under: tenants/{tenant_id_or_slug}/collections/{ok}"`
	Key    string `json:"key" jsonschema:"the object's human key (path) within that collection"`
}
type countObjectsArgs struct {
	Parent string `json:"parent" jsonschema:"tenants/{tenant_id_or_slug}/collections/{ok}"`
	Filter string `json:"filter,omitempty" jsonschema:"optional CEL filter over Object"`
}

// ─── Coverage-fill arg types (ADR/BACKLOG: MCP tool-coverage gaps) ───────────

type updateObjectArgs struct {
	Name            string            `json:"name" jsonschema:"object resource name"`
	ResourceVersion string            `json:"resource_version" jsonschema:"OCC guard; required — take it from a prior list/get"`
	UpdateMask      []string          `json:"update_mask,omitempty" jsonschema:"field paths to change (metadata, tags, content_type, external_ref); empty = replace all of them"`
	Metadata        map[string]string `json:"metadata,omitempty" jsonschema:"replacement user metadata"`
	Tags            map[string]string `json:"tags,omitempty" jsonschema:"replacement tag map"`
	ContentType     string            `json:"content_type,omitempty" jsonschema:"replacement MIME type"`
	ExternalRef     string            `json:"external_ref,omitempty" jsonschema:"replacement external reference"`
}
type deleteObjectTagsArgs struct {
	Name            string   `json:"name" jsonschema:"object resource name"`
	ResourceVersion string   `json:"resource_version" jsonschema:"OCC guard; required — take it from a prior list/get"`
	Keys            []string `json:"keys" jsonschema:"tag keys to remove; others are left intact"`
}
type batchUpdateTagsArgs struct {
	Parent  string            `json:"parent" jsonschema:"tenants/{tenant_id_or_slug}/collections/{ok}"`
	Names   []string          `json:"names,omitempty" jsonschema:"explicit object resource names; ≤100. Use filter for larger sets."`
	Filter  string            `json:"filter,omitempty" jsonschema:"CEL filter over Object, evaluated lazily in the worker"`
	Tags    map[string]string `json:"tags" jsonschema:"tag map to apply to each selected object"`
	Replace bool              `json:"replace,omitempty" jsonschema:"true = overwrite the whole tag map; default false = merge"`
}
type regenerateUploadURLArgs struct {
	Name       string `json:"name" jsonschema:"object resource name of the pending (not-yet-completed) upload"`
	TtlSeconds int64  `json:"ttl_seconds,omitempty" jsonschema:"optional override; capped server-side by cfg.Limits.Presign.put_ttl"`
}
type initiateMultipartArgs struct {
	Parent      string            `json:"parent" jsonschema:"tenants/{tenant_id_or_slug}/collections/{ok}"`
	Key         string            `json:"key,omitempty" jsonschema:"object key under the namespace; empty = server uses the new object_id as key"`
	ContentType string            `json:"content_type" jsonschema:"MIME type (e.g. application/octet-stream)"`
	SizeBytes   int64             `json:"size_bytes,omitempty" jsonschema:"optional total size hint"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
}
type presignPartArgs struct {
	ObjectName string `json:"object_name" jsonschema:"object resource name returned by paladin_initiate_multipart_upload"`
	UploadID   string `json:"upload_id" jsonschema:"upload id from paladin_initiate_multipart_upload"`
	PartNumber int32  `json:"part_number" jsonschema:"1-based part index"`
	TtlSeconds int64  `json:"ttl_seconds,omitempty" jsonschema:"optional override; capped server-side"`
}
type completedPartArg struct {
	PartNumber    int32  `json:"part_number" jsonschema:"1-based part index"`
	Etag          string `json:"etag" jsonschema:"ETag the S3 endpoint returned on the part PUT"`
	ChecksumValue string `json:"checksum_value,omitempty" jsonschema:"hex-encoded checksum, if computed"`
}
type completeMultipartArgs struct {
	ObjectName string             `json:"object_name" jsonschema:"object resource name returned by paladin_initiate_multipart_upload"`
	UploadID   string             `json:"upload_id" jsonschema:"upload id from paladin_initiate_multipart_upload"`
	Parts      []completedPartArg `json:"parts" jsonschema:"ordered list of uploaded parts"`
}
type abortMultipartArgs struct {
	ObjectName string `json:"object_name" jsonschema:"object resource name returned by paladin_initiate_multipart_upload"`
	UploadID   string `json:"upload_id" jsonschema:"upload id from paladin_initiate_multipart_upload"`
}
type listPartsArgs struct {
	ObjectName string `json:"object_name" jsonschema:"object resource name of the in-progress multipart upload"`
	UploadID   string `json:"upload_id" jsonschema:"upload id from paladin_initiate_multipart_upload"`
	PageSize   int32  `json:"page_size,omitempty" jsonschema:"page size; default 50, max 1000"`
}
type auditEntryArgs struct {
	EntryID string `json:"entry_id" jsonschema:"audit-log entry id"`
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
			{Name: "tenant_id", Description: "tenant UUID or slug", Required: true},
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
						"1) Use paladin_list_collections to enumerate the namespaces.\n"+
						"2) For each, fetch its Cedar policy via the Paladin admin API.\n"+
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
		case "collection":
			sc.Type = commonv1.ScopeType_SCOPE_TYPE_OBJECT_KEY
		default:
			return nil, fmt.Errorf("unknown scope type %q", s[:idx])
		}
		sc.Value = s[idx+1:]
		out = append(out, &sc)
	}
	return out, nil
}

// buildEventSubscription assembles an *adminv1.EventSubscription from the
// flat MCP arg shape. The sink is a discriminated union — exactly one of
// {http_url, kafka_*, sqs_*} must be set; the helper rejects the call
// otherwise, so a malformed agent request fails before reaching the
// admin handler. Used by both create and update tools.
func buildEventSubscription(in createSubscriptionArgs) (*adminv1.EventSubscription, error) {
	hasHTTP := in.HTTPURL != ""
	hasKafka := in.KafkaBrokers != "" || in.KafkaTopic != ""
	hasSQS := in.SQSQueueURL != "" || in.SQSRegion != ""
	count := 0
	if hasHTTP {
		count++
	}
	if hasKafka {
		count++
	}
	if hasSQS {
		count++
	}
	if count != 1 {
		return nil, fmt.Errorf("exactly one sink required (http_url | kafka_* | sqs_*); got %d configured", count)
	}
	sink := &adminv1.EventSink{}
	switch {
	case hasHTTP:
		sink.Target = &adminv1.EventSink_Http{Http: &adminv1.HttpSink{
			Url:              in.HTTPURL,
			SigningSecretRef: in.HTTPSecretRef,
			MaxAttempts:      in.HTTPMaxAttempts,
		}}
	case hasKafka:
		if in.KafkaBrokers == "" || in.KafkaTopic == "" {
			return nil, fmt.Errorf("kafka sink requires both kafka_brokers and kafka_topic")
		}
		sink.Target = &adminv1.EventSink_Kafka{Kafka: &adminv1.KafkaSink{
			Brokers: in.KafkaBrokers,
			Topic:   in.KafkaTopic,
		}}
	case hasSQS:
		if in.SQSQueueURL == "" || in.SQSRegion == "" {
			return nil, fmt.Errorf("sqs sink requires both sqs_queue_url and sqs_region")
		}
		sink.Target = &adminv1.EventSink_Sqs{Sqs: &adminv1.SqsSink{
			QueueUrl: in.SQSQueueURL,
			Region:   in.SQSRegion,
		}}
	}
	return &adminv1.EventSubscription{
		TenantId: in.TenantID,
		Filter:   in.Filter,
		Sink:     sink,
		Disabled: in.Disabled,
	}, nil
}
