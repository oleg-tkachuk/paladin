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

	Object    datav1connect.ObjectServiceClient
	Multipart datav1connect.MultipartUploadServiceClient
	Presign   datav1connect.PresignServiceClient

	Auth   iamv1connect.AuthServiceClient
	Users  iamv1connect.UserServiceClient
	ApiKey iamv1connect.ApiKeyServiceClient
}

// NewClients constructs a Clients bundle. Bearer is the access token the MCP
// service-account holds; it is injected into every outbound Connect request.
func NewClients(httpc *http.Client, adminURL, dataURL, iamURL, bearer string) *Clients {
	if httpc == nil {
		httpc = http.DefaultClient
	}
	authInjector := connect.WithInterceptors(connect.UnaryInterceptorFunc(
		func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				if bearer != "" {
					req.Header().Set("Authorization", "Bearer "+bearer)
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

		Object:    datav1connect.NewObjectServiceClient(httpc, dataURL, authInjector),
		Multipart: datav1connect.NewMultipartUploadServiceClient(httpc, dataURL, authInjector),
		Presign:   datav1connect.NewPresignServiceClient(httpc, dataURL, authInjector),

		Auth:   iamv1connect.NewAuthServiceClient(httpc, iamURL, authInjector),
		Users:  iamv1connect.NewUserServiceClient(httpc, iamURL, authInjector),
		ApiKey: iamv1connect.NewApiKeyServiceClient(httpc, iamURL, authInjector),
	}
}

// NewServer constructs an MCP server with the PALADIN tool / resource / prompt
// catalog wired up. allowWrite gates the mutating-tool subset.
func NewServer(name, version string, c *Clients, allowWrite bool) *mcpsdk.Server {
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: name, Version: version}, nil)
	registerReadTools(srv, c)
	if allowWrite {
		registerWriteTools(srv, c)
	}
	registerResources(srv, c)
	registerPrompts(srv)
	return srv
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
	Name string `json:"name" jsonschema:"Quota resource name (tenants/{t}/quota | storageBackends/{b}/buckets/{n}/quota)"`
}
type validatePolicyArgs struct {
	CedarPolicy string `json:"cedar_policy" jsonschema:"Cedar policy text"`
}
type listVersionsArgs struct {
	ObjectName string `json:"object_name" jsonschema:"Object resource name (tenants/{t}/objectKeys/{ok}/objects/{id})"`
	PageSize   int32  `json:"page_size,omitempty" jsonschema:"page size; default 50, max 1000"`
}
type getVersionArgs struct {
	VersionName string `json:"version_name" jsonschema:".../objects/{id}/versions/{ver}"`
}
type getEffectivePolicyArgs struct {
	ResourceName string `json:"resource_name" jsonschema:"Any resource name; tenants/{t}/objectKeys/{ok} works for namespace-level"`
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
	Filter   string `json:"filter,omitempty" jsonschema:"optional CEL over AuditLogEntry"`
}

// ─── Read-only tool registration ────────────────────────────────────────────

func registerReadTools(s *mcpsdk.Server, c *Clients) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "paladin_list_backends",
		Description: "List all storage backends. Read-only.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listBackendsArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Backend.ListBackends(ctx, connect.NewRequest(&adminv1.ListBackendsRequest{
			Page: &commonv1.PageRequest{PageSize: in.PageSize},
		})))
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "paladin_list_buckets",
		Description: "List buckets, optionally filtered to one storage backend.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listBucketsArgs) (*mcpsdk.CallToolResult, any, error) {
		req := &adminv1.ListBucketsRequest{Page: &commonv1.PageRequest{PageSize: in.PageSize}}
		if in.BackendID != "" {
			req.Parent = "storageBackends/" + in.BackendID
		}
		return jsonResult(c.Bucket.ListBuckets(ctx, connect.NewRequest(req)))
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "paladin_list_tenants",
		Description: "List all tenants. Platform-admin only.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listTenantsArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Tenant.ListTenants(ctx, connect.NewRequest(&adminv1.ListTenantsRequest{
			Page: &commonv1.PageRequest{PageSize: in.PageSize},
		})))
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "paladin_list_object_keys",
		Description: "List object keys (logical namespaces) within a tenant.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listObjectKeysArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.OKey.ListObjectKeys(ctx, connect.NewRequest(&adminv1.ListObjectKeysRequest{
			Parent: "tenants/" + in.TenantID,
			Page:   &commonv1.PageRequest{PageSize: in.PageSize},
		})))
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
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

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "paladin_get_quota",
		Description: "Inspect a tenant or bucket quota.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getQuotaArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Quota.GetQuota(ctx, connect.NewRequest(&adminv1.GetQuotaRequest{Name: in.Name})))
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "paladin_validate_policy",
		Description: "Type-check a Cedar policy against the PALADIN schema.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in validatePolicyArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Policy.Validate(ctx, connect.NewRequest(&adminv1.ValidateRequest{
			CedarPolicy: in.CedarPolicy,
		})))
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "paladin_list_versions",
		Description: "List the version history of an object (newest first). Empty when bucket versioning is off.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listVersionsArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Object.ListObjectVersions(ctx, connect.NewRequest(&datav1.ListObjectVersionsRequest{
			Parent: in.ObjectName,
			Page:   &commonv1.PageRequest{PageSize: in.PageSize},
		})))
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "paladin_get_version",
		Description: "Fetch metadata for a specific object version.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getVersionArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Object.GetObjectVersion(ctx, connect.NewRequest(&datav1.GetObjectVersionRequest{
			Name: in.VersionName,
		})))
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "paladin_get_effective_policy",
		Description: "Return the merged Cedar policy stack the engine compiles for a resource (tenant + object_key layers).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getEffectivePolicyArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Policy.GetEffectivePolicy(ctx, connect.NewRequest(&adminv1.GetEffectivePolicyRequest{
			ResourceName: in.ResourceName,
		})))
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
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

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "paladin_audit_recent",
		Description: "Fetch the most recent audit log entries.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in auditRecentArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Audit.ListAuditLog(ctx, connect.NewRequest(&adminv1.ListAuditLogRequest{
			Page:   &commonv1.PageRequest{PageSize: in.PageSize},
			Filter: in.Filter,
		})))
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
	UserName string   `json:"user_name" jsonschema:"tenants/{t}/users/{u}"`
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
	Name string `json:"name" jsonschema:"tenants/{t}/apiKeys/{id}"`
}
type setQuotaArgs struct {
	Name             string `json:"name" jsonschema:"Quota resource name"`
	MaxTotalBytes    int64  `json:"max_total_bytes,omitempty" jsonschema:"hard cap on bytes stored"`
	MaxObjectCount   int64  `json:"max_object_count,omitempty" jsonschema:"hard cap on number of objects"`
	MaxBytesPerDay   int64  `json:"max_bytes_per_day,omitempty" jsonschema:"daily upload byte budget"`
	MaxObjectsPerDay int64  `json:"max_objects_per_day,omitempty" jsonschema:"daily object-count budget"`
}
type auditExportArgs struct {
	Filter      string `json:"filter,omitempty" jsonschema:"CEL filter over AuditLogEntry (reserved)"`
	Destination string `json:"destination,omitempty" jsonschema:"Advisory tag — recorded in result envelope; not yet acted upon"`
}

// ─── Mutating tool registration ─────────────────────────────────────────────

func registerWriteTools(s *mcpsdk.Server, c *Clients) {
	destructive := mcpsdk.ToolAnnotations{DestructiveHint: ptrTrue()}

	mcpsdk.AddTool(s, &mcpsdk.Tool{
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

	mcpsdk.AddTool(s, &mcpsdk.Tool{
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

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "paladin_restore_version",
		Description: "Make the named version `current` again (versioning must be enabled on the parent bucket).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in restoreVersionArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Object.RestoreObjectVersion(ctx, connect.NewRequest(&datav1.RestoreObjectVersionRequest{
			Name: in.VersionName,
		})))
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
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

	mcpsdk.AddTool(s, &mcpsdk.Tool{
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

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "paladin_revoke_api_key",
		Description: "Revoke an API key. The key stays in the table for audit but stops authenticating.",
		Annotations: &destructive,
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in revokeApiKeyArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.ApiKey.RevokeApiKey(ctx, connect.NewRequest(&iamv1.RevokeApiKeyRequest{
			Name: in.Name,
		})))
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
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

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "paladin_audit_export",
		Description: "Export a point-in-time JSON snapshot of audit-log entries (for SOC-2 / ISO-27001 evidence). Returns an Operation with the materialised dump in `response`. Capped at 10k rows — `truncated=true` signals the caller should narrow `filter`.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in auditExportArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Audit.ExportAuditLog(ctx, connect.NewRequest(&adminv1.ExportAuditLogRequest{
			Filter:      in.Filter,
			Destination: in.Destination,
		})))
	})
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
