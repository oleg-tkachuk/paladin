package mcp

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// Lifecycle tools: storage backends, buckets and collections, and the
// subscription redrive. All of them are named outside the read patterns of
// read_only and agent_safe, so only the admin profile offers the writes.
//
// What is left off on purpose: creating, re-pointing or re-crediting a
// backend (CreateBackend, UpdateBackend, RotateCredentials) is storage an
// operator provisions; bucket and collection Cedar policy is the policy
// rewrite DefaultAlwaysDeny refuses; object lock and replication stay with
// the console. No tool here takes a cedar_policy, a bucket owner or
// public_read.

// Resource-name prefixes the admin plane parses.
const (
	backendNamePrefix = "storageBackends/"
	tenantNamePrefix  = "tenants/"
)

// Field-mask paths the update tools send; the plane's own spelling.
const (
	maskDisplayName = "display_name"
	maskLabels      = "labels"
)

var errNothingToUpdate = errors.New("name at least one field to change")

type backendNameArgs struct {
	Name string `json:"name" jsonschema:"backend resource name (storageBackends/{backend_id})"`
}

type backendToggleArgs struct {
	Name            string `json:"name" jsonschema:"backend resource name (storageBackends/{backend_id})"`
	ResourceVersion string `json:"resource_version" jsonschema:"OCC guard; required — take it from paladin_get_backend"`
	Value           bool   `json:"value" jsonschema:"the new setting"`
}

type deleteVersionedArgs struct {
	Name            string `json:"name" jsonschema:"resource name"`
	ResourceVersion string `json:"resource_version" jsonschema:"OCC guard; required — take it from a prior get"`
}

type listAccessibleBucketsArgs struct {
	TenantID string `json:"tenant_id" jsonschema:"tenant UUID or slug"`
	PageSize int32  `json:"page_size,omitempty" jsonschema:"page size; default 50, max 1000"`
}

type createBucketArgs struct {
	BackendID          string            `json:"backend_id" jsonschema:"backend the bucket lives on"`
	BucketID           string            `json:"bucket_id" jsonschema:"bucket name: 3-63 chars of a-z, 0-9, '.' and '-'"`
	DisplayName        string            `json:"display_name,omitempty" jsonschema:"optional display label"`
	Region             string            `json:"region,omitempty" jsonschema:"optional region"`
	OwnerTenantID      string            `json:"owner_tenant_id" jsonschema:"UUID of the tenant that owns the bucket; others reach it through a grant"`
	Labels             map[string]string `json:"labels,omitempty" jsonschema:"optional labels"`
	ProvisionOnBackend bool              `json:"provision_on_backend,omitempty" jsonschema:"also create the bucket on the storage backend"`
}

// updateBucketArgs names only the fields to change; a pointer or a nil map
// is "leave it".
type updateBucketArgs struct {
	Name            string            `json:"name" jsonschema:"bucket resource name (storageBackends/{b}/buckets/{n})"`
	ResourceVersion string            `json:"resource_version" jsonschema:"OCC guard; required — take it from paladin_get_bucket"`
	DisplayName     *string           `json:"display_name,omitempty" jsonschema:"new display label"`
	Labels          map[string]string `json:"labels,omitempty" jsonschema:"replacement label set"`
}

type deleteBucketArgs struct {
	Name            string `json:"name" jsonschema:"bucket resource name (storageBackends/{b}/buckets/{n})"`
	ResourceVersion string `json:"resource_version" jsonschema:"OCC guard; required — take it from paladin_get_bucket"`
	DeleteOnBackend bool   `json:"delete_on_backend,omitempty" jsonschema:"also delete the bucket on the storage backend"`
}

type setBucketVersioningArgs struct {
	Name               string `json:"name" jsonschema:"bucket resource name (storageBackends/{b}/buckets/{n})"`
	ResourceVersion    string `json:"resource_version" jsonschema:"OCC guard; required — take it from paladin_get_bucket"`
	Enabled            bool   `json:"enabled" jsonschema:"versioning on or off"`
	KeepDeletesForever bool   `json:"keep_deletes_forever,omitempty" jsonschema:"with versioning on, soft-deletes never reclaim storage"`
}

type updateCollectionArgs struct {
	Name            string `json:"name" jsonschema:"collection resource name (tenants/{t}/collections/{c})"`
	ResourceVersion string `json:"resource_version" jsonschema:"OCC guard; required — take it from paladin_get_collection"`
	DisplayName     string `json:"display_name" jsonschema:"new display label"`
}

type bindCollectionArgs struct {
	Name            string `json:"name" jsonschema:"collection resource name (tenants/{t}/collections/{c})"`
	ResourceVersion string `json:"resource_version" jsonschema:"OCC guard; required — take it from paladin_get_collection"`
	Bucket          string `json:"bucket" jsonschema:"bucket resource name (storageBackends/{b}/buckets/{n})"`
}

func registerLifecycleTools(s *mcpsdk.Server, c *Clients, filter *ToolFilter) {
	registerBackendTools(s, c, filter)
	registerBucketTools(s, c, filter)
	registerCollectionTools(s, c, filter)

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_redrive_failed_deliveries",
		Description: "Re-queue a subscription's failed deliveries. Returns how many were requeued.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getNameArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.EventSub.RedriveFailedDeliveries(ctx, &adminv1.RedriveFailedDeliveriesRequest{Name: in.Name}))
	})
}

func registerBackendTools(s *mcpsdk.Server, c *Clients, filter *ToolFilter) {
	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_get_backend",
		Description: "Read one storage backend: endpoint, health, enabled / read-only / maintenance state and resource_version.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in backendNameArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Backend.GetBackend(ctx, &adminv1.GetBackendRequest{Name: in.Name}))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_test_backend",
		Description: "Probe a storage backend: reachability, latency and the features it supports. Records the outcome as the backend's health and features.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in backendNameArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Backend.TestBackend(ctx, &adminv1.TestBackendRequest{Name: in.Name}))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_set_backend_enabled",
		Description: "Enable or disable a storage backend.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in backendToggleArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Backend.SetBackendEnabled(ctx, &adminv1.SetBackendEnabledRequest{
			Name: in.Name, Enabled: in.Value, ResourceVersion: in.ResourceVersion,
		}))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_set_backend_read_only",
		Description: "Put a storage backend in or out of read-only mode.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in backendToggleArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Backend.SetBackendReadOnly(ctx, &adminv1.SetBackendReadOnlyRequest{
			Name: in.Name, ReadOnly: in.Value, ResourceVersion: in.ResourceVersion,
		}))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_set_backend_maintenance",
		Description: "Put a storage backend in or out of maintenance.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in backendToggleArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Backend.SetBackendMaintenance(ctx, &adminv1.SetBackendMaintenanceRequest{
			Name: in.Name, Maintenance: in.Value, ResourceVersion: in.ResourceVersion,
		}))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_delete_backend",
		Description: "Deregister a storage backend. The plane refuses while buckets still reference it.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in deleteVersionedArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Backend.DeleteBackend(ctx, &adminv1.DeleteBackendRequest{
			Name: in.Name, ResourceVersion: in.ResourceVersion,
		}))
	})
}

func registerBucketTools(s *mcpsdk.Server, c *Clients, filter *ToolFilter) {
	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_list_accessible_buckets",
		Description: "List the buckets a tenant can use: the ones it owns and the ones granted to it.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listAccessibleBucketsArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Bucket.ListAccessibleBuckets(ctx, &adminv1.ListAccessibleBucketsRequest{
			Tenant: tenantNamePrefix + in.TenantID,
			Page:   &commonv1.PageRequest{PageSize: in.PageSize},
		}))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_create_bucket",
		Description: "Register a bucket on a storage backend, optionally creating it there too.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in createBucketArgs) (*mcpsdk.CallToolResult, any, error) {
		// Every bucket has an owner (ADR-0032); say what is wrong before the
		// plane does.
		if _, err := uuid.Parse(in.OwnerTenantID); err != nil {
			return nil, nil, fmt.Errorf("owner_tenant_id %q is not a tenant UUID: %w", in.OwnerTenantID, err)
		}
		return jsonResult(c.Bucket.CreateBucket(ctx, &adminv1.CreateBucketRequest{
			Parent:   backendNamePrefix + in.BackendID,
			BucketId: in.BucketID,
			Bucket: &adminv1.Bucket{
				DisplayName:   in.DisplayName,
				Region:        in.Region,
				OwnerTenantId: in.OwnerTenantID,
				Labels:        in.Labels,
			},
			ProvisionOnBackend: in.ProvisionOnBackend,
		}))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_update_bucket",
		Description: "Change a bucket's display name or replace its labels. Name only the fields to change.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in updateBucketArgs) (*mcpsdk.CallToolResult, any, error) {
		b := &adminv1.Bucket{Labels: in.Labels}
		var paths []string
		if in.DisplayName != nil {
			b.DisplayName = *in.DisplayName
			paths = append(paths, maskDisplayName)
		}
		if in.Labels != nil {
			paths = append(paths, maskLabels)
		}
		if len(paths) == 0 {
			return nil, nil, errNothingToUpdate
		}
		return jsonResult(c.Bucket.UpdateBucket(ctx, &adminv1.UpdateBucketRequest{
			Name:            in.Name,
			ResourceVersion: in.ResourceVersion,
			UpdateMask:      &fieldmaskpb.FieldMask{Paths: paths},
			Bucket:          b,
		}))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_delete_bucket",
		Description: "Deregister a bucket, optionally deleting it on the storage backend too.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in deleteBucketArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Bucket.DeleteBucket(ctx, &adminv1.DeleteBucketRequest{
			Name:            in.Name,
			ResourceVersion: in.ResourceVersion,
			DeleteOnBackend: in.DeleteOnBackend,
		}))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_set_bucket_versioning",
		Description: "Turn object versioning on or off for a bucket.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in setBucketVersioningArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.Bucket.SetVersioning(ctx, &adminv1.SetVersioningRequest{
			Name:            in.Name,
			ResourceVersion: in.ResourceVersion,
			Versioning: &adminv1.BucketVersioning{
				Enabled:            in.Enabled,
				KeepDeletesForever: in.KeepDeletesForever,
			},
		}))
	})
}

func registerCollectionTools(s *mcpsdk.Server, c *Clients, filter *ToolFilter) {
	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_update_collection",
		Description: "Rename a collection's display label.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in updateCollectionArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.OKey.UpdateCollection(ctx, &adminv1.UpdateCollectionRequest{
			Name:               in.Name,
			ResourceVersion:    in.ResourceVersion,
			UpdateMask:         &fieldmaskpb.FieldMask{Paths: []string{maskDisplayName}},
			CollectionResource: &adminv1.Collection{DisplayName: in.DisplayName},
		}))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_bind_collection_to_bucket",
		Description: "Move a collection's binding to another bucket.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in bindCollectionArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.OKey.BindCollectionToBucket(ctx, &adminv1.BindCollectionToBucketRequest{
			Name:            in.Name,
			ResourceVersion: in.ResourceVersion,
			Bucket:          in.Bucket,
		}))
	})

	addTool(s, filter, &mcpsdk.Tool{
		Name:        "paladin_delete_collection",
		Description: "Delete a collection. The plane refuses while objects remain in it.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in deleteVersionedArgs) (*mcpsdk.CallToolResult, any, error) {
		return jsonResult(c.OKey.DeleteCollection(ctx, &adminv1.DeleteCollectionRequest{
			Name:            in.Name,
			ResourceVersion: in.ResourceVersion,
		}))
	})
}
