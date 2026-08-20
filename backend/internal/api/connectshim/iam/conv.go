// Package iam wires the generated paladin.iam.v1 Connect server stubs onto the
// internal/api/iam/v1/* handler packages. Each *.go file in this package
// maps one service.
package iam

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	commonpb "github.com/oleg-tkachuk/paladin/internal/api/pb/common/v1"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
)

func tsProto(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func tsPtrProto(t *time.Time) *timestamppb.Timestamp {
	if t == nil || t.IsZero() {
		return nil
	}
	return timestamppb.New(*t)
}

func pageResponseProto(nextToken string) *commonpb.PageResponse {
	if nextToken == "" {
		return nil
	}
	return &commonpb.PageResponse{NextPageToken: nextToken}
}

// ─── Wire helpers ───────────────────────────────────────────────────────────

func userToProto(u *authstore.User) *pb.User {
	if u == nil {
		return nil
	}
	return &pb.User{
		Name:            fmt.Sprintf("tenants/%s/users/%s", u.TenantID, u.UserID),
		UserId:          u.UserID.String(),
		TenantId:        u.TenantID.String(),
		Subject:         u.Subject,
		DisplayName:     u.DisplayName,
		Roles:           u.Roles,
		Scopes:          scopesToProto(u.Scopes),
		Disabled:        u.Disabled,
		ResourceVersion: fmt.Sprintf("%d", u.ResourceVersion),
		CreatedAt:       tsProto(u.CreatedAt),
		UpdatedAt:       tsProto(u.UpdatedAt),
		LastLoginAt:     tsPtrProto(u.LastLoginAt),
	}
}

func scopesToProto(s []auth.Scope) []*commonpb.Scope {
	out := make([]*commonpb.Scope, 0, len(s))
	for _, sc := range s {
		out = append(out, &commonpb.Scope{
			Type:  scopeTypeProto(sc.Type),
			Value: sc.Value,
		})
	}
	return out
}

func scopesFromProto(s []*commonpb.Scope) []auth.Scope {
	out := make([]auth.Scope, 0, len(s))
	for _, sc := range s {
		if sc == nil {
			continue
		}
		out = append(out, auth.Scope{
			Type:  scopeTypeFromProto(sc.GetType()),
			Value: sc.GetValue(),
		})
	}
	return out
}

func scopeTypeProto(t auth.ScopeType) commonpb.ScopeType {
	switch t {
	case auth.ScopeTenant:
		return commonpb.ScopeType_SCOPE_TYPE_TENANT
	case auth.ScopeBackend:
		return commonpb.ScopeType_SCOPE_TYPE_BACKEND
	case auth.ScopeBucket:
		return commonpb.ScopeType_SCOPE_TYPE_BUCKET
	case auth.ScopeCollection:
		return commonpb.ScopeType_SCOPE_TYPE_OBJECT_KEY
	}
	return commonpb.ScopeType_SCOPE_TYPE_UNSPECIFIED
}

func scopeTypeFromProto(t commonpb.ScopeType) auth.ScopeType {
	switch t {
	case commonpb.ScopeType_SCOPE_TYPE_TENANT:
		return auth.ScopeTenant
	case commonpb.ScopeType_SCOPE_TYPE_BACKEND:
		return auth.ScopeBackend
	case commonpb.ScopeType_SCOPE_TYPE_BUCKET:
		return auth.ScopeBucket
	case commonpb.ScopeType_SCOPE_TYPE_OBJECT_KEY:
		return auth.ScopeCollection
	}
	return auth.ScopeType("")
}
