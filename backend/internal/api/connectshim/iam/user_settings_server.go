// Package iam — UserSettings Connect-RPC server.
//
// Replaces the earlier hand-written JSON shim that mounted under the same
// `/paladin.iam.v1.UserSettingsService/{Method}` path. The wire path is
// unchanged — clients keep their existing URLs and request bodies, just
// with proper Connect framing now.
package iam

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/iam/v1/usersettingsh"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// UserSettingsServer satisfies the generated Connect handler interface.
// Authentication is enforced by the IAM mux's interceptor stack before
// the request lands here; we read auth.Principal via the domain handler.
type UserSettingsServer struct {
	paladiniamv1connect.UnimplementedUserSettingsServiceHandler
	H userSettingsHandler
}

func NewUserSettingsServer(h *usersettingsh.Handler) *UserSettingsServer {
	return &UserSettingsServer{H: h}
}

var _ paladiniamv1connect.UserSettingsServiceHandler = (*UserSettingsServer)(nil)

// ─── RPC methods ────────────────────────────────────────────────────────────

func (s *UserSettingsServer) GetMine(
	ctx context.Context,
	_ *connect.Request[pb.GetMineRequest],
) (*connect.Response[pb.UserSettings], error) {
	out, err := s.H.GetMine(ctx)
	if err != nil {
		return nil, err
	}
	msg, err := settingsToProto(out)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(msg), nil
}

func (s *UserSettingsServer) UpdateMine(
	ctx context.Context,
	req *connect.Request[pb.UpdateMineRequest],
) (*connect.Response[pb.UserSettings], error) {
	prefs, err := structToBytes(req.Msg.GetPreferences())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.UpdateMine(ctx, usersettingsh.UpdateMineInput{
		UpdateMask:  paths(req.Msg.GetUpdateMask()),
		Timezone:    req.Msg.GetTimezone(),
		Locale:      req.Msg.GetLocale(),
		Theme:       req.Msg.GetTheme(),
		Preferences: prefs,
	})
	if err != nil {
		return nil, err
	}
	msg, err := settingsToProto(out)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(msg), nil
}

func (s *UserSettingsServer) GetForUser(
	ctx context.Context,
	req *connect.Request[pb.GetForUserRequest],
) (*connect.Response[pb.UserSettings], error) {
	uid, err := parseUserResourceName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.GetForUser(ctx, uid)
	if err != nil {
		return nil, err
	}
	msg, err := settingsToProto(out)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(msg), nil
}

func (s *UserSettingsServer) ListByTenant(
	ctx context.Context,
	req *connect.Request[pb.ListByTenantRequest],
) (*connect.Response[pb.ListByTenantResponse], error) {
	tid, err := parseTenantParent(req.Msg.GetParent())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	pageSize := int32(0)
	if p := req.Msg.GetPage(); p != nil {
		pageSize = p.GetPageSize()
	}
	out, err := s.H.ListByTenant(ctx, tid, pageSize)
	if err != nil {
		return nil, err
	}
	items := make([]*pb.UserSettings, 0, len(out))
	for _, s := range out {
		msg, err := settingsToProto(&s)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		items = append(items, msg)
	}
	return connect.NewResponse(&pb.ListByTenantResponse{
		Settings: items,
		// Domain handler doesn't paginate yet — reflect that with an empty
		// next_page_token. When it learns to, swap in the real cursor.
	}), nil
}

func (s *UserSettingsServer) DeleteForUser(
	ctx context.Context,
	req *connect.Request[pb.DeleteForUserRequest],
) (*connect.Response[pb.DeleteForUserResponse], error) {
	uid, err := parseUserResourceName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := s.H.DeleteForUser(ctx, uid); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DeleteForUserResponse{}), nil
}

// ─── conversions ────────────────────────────────────────────────────────────

func settingsToProto(s *usersettingsh.Settings) (*pb.UserSettings, error) {
	prefs, err := bytesToStruct(s.Preferences)
	if err != nil {
		return nil, err
	}
	return &pb.UserSettings{
		Name:            "users/" + s.UserID.String() + "/settings",
		UserId:          s.UserID.String(),
		TenantId:        s.TenantID.String(),
		Timezone:        s.Timezone,
		Locale:          s.Locale,
		Theme:           s.Theme,
		Preferences:     prefs,
		ResourceVersion: itoa64(s.ResourceVersion),
		CreatedAt:       timestamppb.New(s.CreatedAt),
		UpdatedAt:       timestamppb.New(s.UpdatedAt),
	}, nil
}

// bytesToStruct turns the domain layer's raw JSON preferences into a
// google.protobuf.Struct. Empty / nil → nil (the field stays unset).
func bytesToStruct(b []byte) (*structpb.Struct, error) {
	if len(b) == 0 {
		return nil, nil
	}
	out := &structpb.Struct{}
	if err := out.UnmarshalJSON(b); err != nil {
		return nil, err
	}
	return out, nil
}

// structToBytes is the inverse — nil → nil so callers can distinguish
// "no preferences set" from "preferences set to {}".
func structToBytes(p *structpb.Struct) ([]byte, error) {
	if p == nil {
		return nil, nil
	}
	return p.MarshalJSON()
}

func paths(m *fieldmaskpb.FieldMask) []string {
	if m == nil {
		return nil
	}
	return m.GetPaths()
}

// parseUserResourceName accepts both "users/{uuid}" and
// "users/{uuid}/settings" since the proto's Get and Delete use the
// trailing /settings shape on different fields.
func parseUserResourceName(name string) (uuid.UUID, error) {
	const prefix = "users/"
	if len(name) <= len(prefix) || name[:len(prefix)] != prefix {
		return uuid.Nil, errors.New("name must start with users/")
	}
	body := name[len(prefix):]
	if i := indexOf(body, "/settings"); i >= 0 {
		body = body[:i]
	}
	return uuid.Parse(body)
}

// parseTenantParent — UUID-only for now; slug rollout is tracked in
// BACKLOG ("Tenant slug — Phase 3"). Same constraint as the prior shim.
func parseTenantParent(parent string) (uuid.UUID, error) {
	const prefix = "tenants/"
	if len(parent) <= len(prefix) || parent[:len(prefix)] != prefix {
		return uuid.Nil, errors.New("parent must be tenants/{tenant_id}")
	}
	id, err := uuid.Parse(parent[len(prefix):])
	if err != nil {
		return uuid.Nil, errors.New("parent: tenant slug not yet supported on this RPC; use UUID")
	}
	return id, nil
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// itoa64 — small dependency-free formatter matching the proto-emitted
// resource_version shape (decimal string). Mirrors what the JSON shim used.
func itoa64(i int64) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
