package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
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
// The bridge holds short-lived references — token rotation is the caller's
// concern (re-build Clients on rotate).
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
// service-account holds. Pass adminURL/dataURL/iamURL of the PALADIN server.
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

// RegisterDefaults installs the standard tool/resource/prompt set onto the
// MCP server. Mutation-tools require explicit env opt-in (`PALADIN_MCP_ALLOW_WRITE=1`)
// — by default the LLM gets a read-only view to avoid accidental damage.
func RegisterDefaults(s *Server, c *Clients, allowWrite bool) {
	registerReadTools(s, c)
	if allowWrite {
		registerWriteTools(s, c)
	}
	registerResources(s, c)
	registerPrompts(s)
}

// ─── Read-only tools ────────────────────────────────────────────────────────

func registerReadTools(s *Server, c *Clients) {
	s.Tools.Register(Tool{
		Name:        "paladin_list_backends",
		Description: "List all storage backends. Read-only.",
		InputSchema: schemaObject(map[string]any{
			"page_size": schemaIntDefault(50, 1, 1000),
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				PageSize int32 `json:"page_size"`
			}
			_ = json.Unmarshal(args, &p)
			req := &adminv1.ListBackendsRequest{
				Page: &commonv1.PageRequest{PageSize: p.PageSize},
			}
			resp, err := c.Backend.ListBackends(ctx, connect.NewRequest(req))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})

	s.Tools.Register(Tool{
		Name:        "paladin_list_buckets",
		Description: "List buckets, optionally filtered to one storage backend.",
		InputSchema: schemaObject(map[string]any{
			"backend_id": schemaString("optional backend id; empty = all"),
			"page_size":  schemaIntDefault(50, 1, 1000),
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				BackendID string `json:"backend_id"`
				PageSize  int32  `json:"page_size"`
			}
			_ = json.Unmarshal(args, &p)
			req := &adminv1.ListBucketsRequest{
				Page: &commonv1.PageRequest{PageSize: p.PageSize},
			}
			if p.BackendID != "" {
				req.Parent = "storageBackends/" + p.BackendID
			}
			resp, err := c.Bucket.ListBuckets(ctx, connect.NewRequest(req))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})

	s.Tools.Register(Tool{
		Name:        "paladin_list_tenants",
		Description: "List all tenants. Platform-admin only.",
		InputSchema: schemaObject(map[string]any{
			"page_size": schemaIntDefault(50, 1, 1000),
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				PageSize int32 `json:"page_size"`
			}
			_ = json.Unmarshal(args, &p)
			resp, err := c.Tenant.ListTenants(ctx, connect.NewRequest(&adminv1.ListTenantsRequest{
				Page: &commonv1.PageRequest{PageSize: p.PageSize},
			}))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})

	s.Tools.Register(Tool{
		Name:        "paladin_list_object_keys",
		Description: "List object keys (logical namespaces) within a tenant.",
		InputSchema: schemaObject(map[string]any{
			"tenant_id": schemaStringRequired("required tenant UUID"),
			"page_size": schemaIntDefault(50, 1, 1000),
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				TenantID string `json:"tenant_id"`
				PageSize int32  `json:"page_size"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			if p.TenantID == "" {
				return nil, fmt.Errorf("tenant_id is required")
			}
			resp, err := c.OKey.ListObjectKeys(ctx, connect.NewRequest(&adminv1.ListObjectKeysRequest{
				Parent: "tenants/" + p.TenantID,
				Page:   &commonv1.PageRequest{PageSize: p.PageSize},
			}))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})

	s.Tools.Register(Tool{
		Name:        "paladin_query_objects",
		Description: "List objects within an object_key, optionally filtered by CEL.",
		InputSchema: schemaObject(map[string]any{
			"tenant_id":  schemaStringRequired("tenant UUID"),
			"object_key": schemaStringRequired("object key (namespace) name"),
			"filter":     schemaString("optional CEL filter, e.g. tags['type']=='invoice'"),
			"page_size":  schemaIntDefault(100, 1, 1000),
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				TenantID  string `json:"tenant_id"`
				ObjectKey string `json:"object_key"`
				Filter    string `json:"filter"`
				PageSize  int32  `json:"page_size"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			parent := fmt.Sprintf("tenants/%s/objectKeys/%s", p.TenantID, p.ObjectKey)
			resp, err := c.Object.ListObjects(ctx, connect.NewRequest(&datav1.ListObjectsRequest{
				Parent: parent,
				Filter: p.Filter,
				Page:   &commonv1.PageRequest{PageSize: p.PageSize},
			}))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})

	s.Tools.Register(Tool{
		Name:        "paladin_get_quota",
		Description: "Inspect a tenant or bucket quota.",
		InputSchema: schemaObject(map[string]any{
			"name": schemaStringRequired("Quota resource name (tenants/{t}/quota | storageBackends/{b}/buckets/{n}/quota)"),
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			resp, err := c.Quota.GetQuota(ctx, connect.NewRequest(&adminv1.GetQuotaRequest{Name: p.Name}))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})

	s.Tools.Register(Tool{
		Name:        "paladin_validate_policy",
		Description: "Type-check a Cedar policy against the PALADIN schema.",
		InputSchema: schemaObject(map[string]any{
			"cedar_policy": schemaStringRequired("Cedar policy text"),
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				CedarPolicy string `json:"cedar_policy"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			resp, err := c.Policy.Validate(ctx, connect.NewRequest(&adminv1.ValidateRequest{
				CedarPolicy: p.CedarPolicy,
			}))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})

	s.Tools.Register(Tool{
		Name:        "paladin_list_versions",
		Description: "List the version history of an object (newest first). Empty when bucket versioning is off.",
		InputSchema: schemaObject(map[string]any{
			"object_name": schemaStringRequired("Object resource name (tenants/{t}/objectKeys/{ok}/objects/{id})"),
			"page_size":   schemaIntDefault(50, 1, 1000),
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				ObjectName string `json:"object_name"`
				PageSize   int32  `json:"page_size"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			resp, err := c.Object.ListObjectVersions(ctx, connect.NewRequest(&datav1.ListObjectVersionsRequest{
				Parent: p.ObjectName,
				Page:   &commonv1.PageRequest{PageSize: p.PageSize},
			}))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})

	s.Tools.Register(Tool{
		Name:        "paladin_get_version",
		Description: "Fetch metadata for a specific object version.",
		InputSchema: schemaObject(map[string]any{
			"version_name": schemaStringRequired(".../objects/{id}/versions/{ver}"),
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				VersionName string `json:"version_name"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			resp, err := c.Object.GetObjectVersion(ctx, connect.NewRequest(&datav1.GetObjectVersionRequest{
				Name: p.VersionName,
			}))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})

	s.Tools.Register(Tool{
		Name:        "paladin_get_effective_policy",
		Description: "Return the merged Cedar policy stack the engine compiles for a resource (tenant + object_key layers).",
		InputSchema: schemaObject(map[string]any{
			"resource_name": schemaStringRequired("Any resource name; tenants/{t}/objectKeys/{ok} works for namespace-level"),
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				ResourceName string `json:"resource_name"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			resp, err := c.Policy.GetEffectivePolicy(ctx, connect.NewRequest(&adminv1.GetEffectivePolicyRequest{
				ResourceName: p.ResourceName,
			}))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})

	s.Tools.Register(Tool{
		Name:        "paladin_simulate_authz",
		Description: "Dry-run authorization: would `principal_subject` (with the given roles) be allowed to perform `action` on `resource_name`?",
		InputSchema: schemaObject(map[string]any{
			"principal_subject":   schemaStringRequired("subject (user_id or service-account ref)"),
			"principal_tenant_id": schemaString("optional tenant UUID for the simulated principal"),
			"principal_roles":     schemaArrayOfStrings("roles the simulated principal carries"),
			"action":              schemaStringRequired("Cedar action name (e.g. PutObject, ManageBucket)"),
			"resource_name":       schemaStringRequired("Resource name to authorize against"),
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				PrincipalSubject  string   `json:"principal_subject"`
				PrincipalTenantID string   `json:"principal_tenant_id"`
				PrincipalRoles    []string `json:"principal_roles"`
				Action            string   `json:"action"`
				ResourceName      string   `json:"resource_name"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			resp, err := c.Policy.SimulateAuthz(ctx, connect.NewRequest(&adminv1.SimulateAuthzRequest{
				PrincipalSubject:  p.PrincipalSubject,
				PrincipalTenantId: p.PrincipalTenantID,
				PrincipalRoles:    p.PrincipalRoles,
				Action:            p.Action,
				ResourceName:      p.ResourceName,
			}))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})

	s.Tools.Register(Tool{
		Name:        "paladin_audit_recent",
		Description: "Fetch the most recent audit log entries.",
		InputSchema: schemaObject(map[string]any{
			"page_size": schemaIntDefault(50, 1, 200),
			"filter":    schemaString("optional CEL over AuditLogEntry"),
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				PageSize int32  `json:"page_size"`
				Filter   string `json:"filter"`
			}
			_ = json.Unmarshal(args, &p)
			resp, err := c.Audit.ListAuditLog(ctx, connect.NewRequest(&adminv1.ListAuditLogRequest{
				Page:   &commonv1.PageRequest{PageSize: p.PageSize},
				Filter: p.Filter,
			}))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})
}

// ─── Mutating tools (write) ─────────────────────────────────────────────────

func registerWriteTools(s *Server, c *Clients) {
	s.Tools.Register(Tool{
		Name:        "paladin_create_object_key",
		Description: "Create an object key (logical namespace) under a tenant + bucket binding.",
		InputSchema: schemaObject(map[string]any{
			"tenant_id":    schemaStringRequired("tenant UUID"),
			"object_key":   schemaStringRequired("kebab-case namespace name"),
			"bucket":       schemaStringRequired("bucket resource name (storageBackends/{b}/buckets/{n})"),
			"display_name": schemaString("optional display label"),
			"cedar_policy": schemaString("optional Cedar policy"),
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				TenantID    string `json:"tenant_id"`
				ObjectKey   string `json:"object_key"`
				Bucket      string `json:"bucket"`
				DisplayName string `json:"display_name"`
				CedarPolicy string `json:"cedar_policy"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			resp, err := c.OKey.CreateObjectKey(ctx, connect.NewRequest(&adminv1.CreateObjectKeyRequest{
				Parent:    "tenants/" + p.TenantID,
				ObjectKey: p.ObjectKey,
				ObjectKeyResource: &adminv1.ObjectKey{
					DisplayName: p.DisplayName,
					Bucket:      p.Bucket,
					CedarPolicy: p.CedarPolicy,
				},
			}))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})

	s.Tools.Register(Tool{
		Name:        "paladin_grant_user_scopes",
		Description: "Add scopes to a user (e.g. bucket:foo, object_key:bar/baz).",
		InputSchema: schemaObject(map[string]any{
			"user_name": schemaStringRequired("tenants/{t}/users/{u}"),
			"scopes":    schemaArrayOfStrings("scope strings of form type:value"),
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				UserName string   `json:"user_name"`
				Scopes   []string `json:"scopes"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			scopes, err := stringScopesToProto(p.Scopes)
			if err != nil {
				return nil, err
			}
			resp, err := c.Users.GrantScopes(ctx, connect.NewRequest(&iamv1.GrantScopesRequest{
				Name:   p.UserName,
				Scopes: scopes,
			}))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})

	s.Tools.Register(Tool{
		Name:        "paladin_restore_version",
		Description: "Make the named version `current` again (versioning must be enabled on the parent bucket).",
		InputSchema: schemaObject(map[string]any{
			"version_name": schemaStringRequired(".../objects/{id}/versions/{ver}"),
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				VersionName string `json:"version_name"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			resp, err := c.Object.RestoreObjectVersion(ctx, connect.NewRequest(&datav1.RestoreObjectVersionRequest{
				Name: p.VersionName,
			}))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})

	s.Tools.Register(Tool{
		Name:        "paladin_create_user",
		Description: "Create a new user under a tenant. Initial password is returned only via secret channels — do NOT echo it back to the LLM transcript.",
		InputSchema: schemaObject(map[string]any{
			"tenant_id":        schemaStringRequired("tenant UUID"),
			"subject":          schemaStringRequired("login subject (email-style)"),
			"display_name":     schemaString("display label"),
			"initial_password": schemaStringRequired("≥12 chars; rotated by user on first login (slice 12+)"),
			"roles":            schemaArrayOfStrings("roles to grant; e.g. tenant.user"),
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				TenantID        string   `json:"tenant_id"`
				Subject         string   `json:"subject"`
				DisplayName     string   `json:"display_name"`
				InitialPassword string   `json:"initial_password"`
				Roles           []string `json:"roles"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			resp, err := c.Users.CreateUser(ctx, connect.NewRequest(&iamv1.CreateUserRequest{
				Parent:          "tenants/" + p.TenantID,
				Subject:         p.Subject,
				DisplayName:     p.DisplayName,
				InitialPassword: p.InitialPassword,
				Roles:           p.Roles,
			}))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})

	s.Tools.Register(Tool{
		Name:        "paladin_set_lifecycle_rules",
		Description: "Replace bucket lifecycle rules (expiration only in v2). Each rule: id, enabled, match (CEL — reserved), expiration_after (Go duration like '720h'). Pass rules=[] to clear.",
		InputSchema: schemaObject(map[string]any{
			"bucket_name":      schemaStringRequired("Bucket resource name (storageBackends/{b}/buckets/{n})"),
			"resource_version": schemaString("OCC guard from prior GetBucket"),
			"rules": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":               map[string]any{"type": "string"},
						"enabled":          map[string]any{"type": "boolean"},
						"match":            map[string]any{"type": "string", "description": "CEL filter (reserved)"},
						"expiration_after": map[string]any{"type": "string", "description": "Go duration; e.g. '720h' = 30 days"},
					},
					"required": []string{"id", "expiration_after"},
				},
			},
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				BucketName      string `json:"bucket_name"`
				ResourceVersion string `json:"resource_version"`
				Rules           []struct {
					ID              string `json:"id"`
					Enabled         bool   `json:"enabled"`
					Match           string `json:"match"`
					ExpirationAfter string `json:"expiration_after"`
				} `json:"rules"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			pbRules := make([]*adminv1.LifecycleRule, 0, len(p.Rules))
			for _, r := range p.Rules {
				if r.ExpirationAfter == "" {
					return nil, fmt.Errorf("rule %q: expiration_after required", r.ID)
				}
				dur, err := time.ParseDuration(r.ExpirationAfter)
				if err != nil {
					return nil, fmt.Errorf("rule %q: invalid expiration_after %q: %w", r.ID, r.ExpirationAfter, err)
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
			resp, err := c.Bucket.SetLifecycleRules(ctx, connect.NewRequest(&adminv1.SetLifecycleRulesRequest{
				Name:            p.BucketName,
				ResourceVersion: p.ResourceVersion,
				Rules:           pbRules,
			}))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})

	s.Tools.Register(Tool{
		Name:        "paladin_revoke_api_key",
		Description: "Revoke an API key. The key stays in the table for audit but stops authenticating.",
		InputSchema: schemaObject(map[string]any{
			"name": schemaStringRequired("tenants/{t}/apiKeys/{id}"),
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			resp, err := c.ApiKey.RevokeApiKey(ctx, connect.NewRequest(&iamv1.RevokeApiKeyRequest{
				Name: p.Name,
			}))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})

	s.Tools.Register(Tool{
		Name:        "paladin_set_quota",
		Description: "Set tenant or bucket usage caps. Provide caps in bytes / counts.",
		InputSchema: schemaObject(map[string]any{
			"name":              schemaStringRequired("Quota resource name"),
			"max_total_bytes":   schemaInt("hard cap on bytes stored"),
			"max_object_count":  schemaInt("hard cap on number of objects"),
			"max_bytes_per_day": schemaInt("daily upload byte budget"),
		}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				Name             string `json:"name"`
				MaxTotalBytes    int64  `json:"max_total_bytes"`
				MaxObjectCount   int64  `json:"max_object_count"`
				MaxBytesPerDay   int64  `json:"max_bytes_per_day"`
				MaxObjectsPerDay int64  `json:"max_objects_per_day"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			resp, err := c.Quota.SetQuota(ctx, connect.NewRequest(&adminv1.SetQuotaRequest{
				Name: p.Name,
				Quota: &adminv1.Quota{
					MaxTotalBytes:    p.MaxTotalBytes,
					MaxObjectCount:   p.MaxObjectCount,
					MaxBytesPerDay:   p.MaxBytesPerDay,
					MaxObjectsPerDay: p.MaxObjectsPerDay,
				},
			}))
			if err != nil {
				return nil, err
			}
			return JSONResult(resp.Msg)
		},
	})
}

// ─── Resources ──────────────────────────────────────────────────────────────

func registerResources(s *Server, c *Clients) {
	s.Resources.Register(Resource{
		URI:         "paladin://backends",
		Name:        "Storage backends",
		Description: "All registered storage backends as JSON.",
		MimeType:    "application/json",
		Reader: func(ctx context.Context, _ string) (string, error) {
			resp, err := c.Backend.ListBackends(ctx, connect.NewRequest(&adminv1.ListBackendsRequest{
				Page: &commonv1.PageRequest{PageSize: 200},
			}))
			if err != nil {
				return "", err
			}
			b, err := json.MarshalIndent(resp.Msg, "", "  ")
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	})

	s.Resources.Register(Resource{
		URI:         "paladin://buckets",
		Name:        "Buckets",
		Description: "All buckets across all backends.",
		MimeType:    "application/json",
		Reader: func(ctx context.Context, _ string) (string, error) {
			resp, err := c.Bucket.ListBuckets(ctx, connect.NewRequest(&adminv1.ListBucketsRequest{
				Page: &commonv1.PageRequest{PageSize: 200},
			}))
			if err != nil {
				return "", err
			}
			b, err := json.MarshalIndent(resp.Msg, "", "  ")
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	})

	s.Resources.Register(Resource{
		URI:         "paladin://tenants",
		Name:        "Tenants",
		Description: "All tenants.",
		MimeType:    "application/json",
		Reader: func(ctx context.Context, _ string) (string, error) {
			resp, err := c.Tenant.ListTenants(ctx, connect.NewRequest(&adminv1.ListTenantsRequest{
				Page: &commonv1.PageRequest{PageSize: 200},
			}))
			if err != nil {
				return "", err
			}
			b, err := json.MarshalIndent(resp.Msg, "", "  ")
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	})
}

// ─── Prompts ────────────────────────────────────────────────────────────────

func registerPrompts(s *Server) {
	s.Prompts.Register(Prompt{
		Name:        "audit_access_for_tenant",
		Description: "Audit who has access to what within a tenant.",
		Arguments: []PromptArgument{
			{Name: "tenant_id", Description: "tenant UUID", Required: true},
		},
		Renderer: func(ctx context.Context, args map[string]string) ([]PromptMessage, error) {
			t := args["tenant_id"]
			if t == "" {
				return nil, fmt.Errorf("tenant_id required")
			}
			return []PromptMessage{
				{
					Role: "user",
					Content: fmt.Sprintf(
						"Audit access for tenant %s.\n"+
							"1) Use paladin_list_object_keys to enumerate the namespaces.\n"+
							"2) For each, fetch its Cedar policy via the PALADIN admin API.\n"+
							"3) Identify which user_ids (via paladin_list_users) hold scopes that match.\n"+
							"4) Summarise findings with concrete principal→action→resource bindings.\n",
						t,
					),
				},
			}, nil
		},
	})

	s.Prompts.Register(Prompt{
		Name:        "rotate_backend_credentials",
		Description: "Plan a credential rotation for a storage backend.",
		Arguments: []PromptArgument{
			{Name: "backend_id", Description: "backend id to rotate", Required: true},
		},
		Renderer: func(ctx context.Context, args map[string]string) ([]PromptMessage, error) {
			b := args["backend_id"]
			if b == "" {
				return nil, fmt.Errorf("backend_id required")
			}
			return []PromptMessage{
				{
					Role: "user",
					Content: fmt.Sprintf(
						"Plan a safe credential rotation for backend %s.\n"+
							"Steps to consider:\n"+
							"  • Confirm the new secret_ref is provisioned in the secret manager.\n"+
							"  • Call paladin_test_backend to verify reachability before swap.\n"+
							"  • Decide a grace_period that exceeds the max presign TTL in use.\n"+
							"  • Flag any presigned URLs minted before rotation that may break.\n",
						b,
					),
				},
			}, nil
		},
	})
}

// ─── Schema helpers ─────────────────────────────────────────────────────────

func schemaObject(props map[string]any) map[string]any {
	required := make([]string, 0)
	for k, v := range props {
		if m, ok := v.(map[string]any); ok && m["__required"] == true {
			required = append(required, k)
			delete(m, "__required")
		}
	}
	out := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func schemaString(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func schemaStringRequired(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "__required": true}
}

func schemaInt(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func schemaIntDefault(def, min, max int32) map[string]any {
	return map[string]any{
		"type":        "integer",
		"description": fmt.Sprintf("default %d, range [%d,%d]", def, min, max),
		"minimum":     min,
		"maximum":     max,
		"default":     def,
	}
}

func schemaArrayOfStrings(desc string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": desc,
		"items":       map[string]any{"type": "string"},
	}
}

// stringScopesToProto decodes "type:value" strings into commonv1.Scope.
func stringScopesToProto(in []string) ([]*commonv1.Scope, error) {
	out := make([]*commonv1.Scope, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		var sc commonv1.Scope
		// Format: type:value (e.g. bucket:paladin-archive); "*" alone for wildcard.
		switch {
		case s == "*":
			// Wildcard — leave type unspecified.
			sc.Value = "*"
		default:
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
		}
		out = append(out, &sc)
	}
	return out, nil
}
