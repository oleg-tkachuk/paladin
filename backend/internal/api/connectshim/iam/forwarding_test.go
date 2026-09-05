package iam

import (
	"context"
	"runtime"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/iam/v1/authh"
	"github.com/oleg-tkachuk/paladin/internal/api/iam/v1/usersettingsh"

	commonpb "github.com/oleg-tkachuk/paladin/internal/api/pb/common/v1"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"
)

// userIDFromName is the only thing standing between a caller-supplied string
// and a uuid.Parse on parts[3]. Its three conditions are joined by `||`, which
// short-circuits; joining the first two with `&&` instead both ACCEPTS
// "bogus/x/users/{uuid}" and PANICS on "tenants/x", because parts[2] is then
// evaluated on a two-element slice. Nothing held that, so this table does: it
// is the shape of the check that matters, not just that valid names work.
func TestGetUser_RejectsMalformedNames(t *testing.T) {
	for _, name := range []string{
		"",
		"tenants/" + uuid.NewString(), // too short — indexing parts[2] would panic
		"bogus/" + uuid.NewString() + "/users/" + uuid.NewString(), // wrong collection prefix
		"tenants/" + uuid.NewString() + "/groups/" + uuid.NewString(),
		"tenants/" + uuid.NewString() + "/users/" + uuid.NewString() + "/extra",
		"tenants/" + uuid.NewString() + "/users/not-a-uuid",
	} {
		t.Run(name, func(t *testing.T) {
			srv := &UserServer{H: failingUser{}}
			_, err := srv.GetUser(context.Background(), connect.NewRequest(&pb.GetUserRequest{
				Name: name,
			}))
			if err == nil {
				t.Fatal("a malformed user name was accepted")
			}
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Errorf("expected InvalidArgument, got %v", err)
			}
		})
	}
}

// The tenant hint arrives as a header, not a request field, and is dropped
// silently when unparseable. A hint that never reaches the handler means the
// login resolves against the wrong tenant — or none — while still answering
// with a token.
type recordingAuth struct {
	failingAuth
	login authh.LoginInput
	next  string
}

func (r *recordingAuth) Login(_ context.Context, in authh.LoginInput) (*authh.LoginOutput, error) {
	r.login = in
	return &authh.LoginOutput{}, nil
}

func (r *recordingAuth) ListMyMemberships(context.Context, authh.ListMembershipsInput) ([]authh.Membership, string, error) {
	return nil, r.next, nil
}

func TestLogin_ForwardsTheTenantHintHeader(t *testing.T) {
	tenantID := uuid.New()

	t.Run("a parseable hint reaches the handler", func(t *testing.T) {
		h := &recordingAuth{}
		srv := &AuthServer{H: h}
		req := connect.NewRequest(&pb.LoginRequest{Subject: "tester", Password: "pw"})
		req.Header().Set("X-Tenant-Id", tenantID.String())

		if _, err := srv.Login(context.Background(), req); err != nil {
			t.Fatalf("login: %v", err)
		}
		if h.login.TenantHint != tenantID {
			t.Errorf("tenant hint reached the handler as %v, want %v — the login "+
				"resolves against the wrong tenant and still answers with a token",
				h.login.TenantHint, tenantID)
		}
	})

	t.Run("an unparseable hint is dropped, not passed on", func(t *testing.T) {
		h := &recordingAuth{}
		srv := &AuthServer{H: h}
		req := connect.NewRequest(&pb.LoginRequest{Subject: "tester", Password: "pw"})
		req.Header().Set("X-Tenant-Id", "not-a-uuid")

		if _, err := srv.Login(context.Background(), req); err != nil {
			t.Fatalf("login: %v", err)
		}
		if h.login.TenantHint != uuid.Nil {
			t.Errorf("an unparseable hint became %v", h.login.TenantHint)
		}
	})
}

// A next-page token the shim does not surface is a listing that looks complete
// while more rows exist.
func TestListMyMemberships_SurfacesTheNextPageToken(t *testing.T) {
	h := &recordingAuth{next: "cursor-2"}
	srv := &AuthServer{H: h}

	resp, err := srv.ListMyMemberships(context.Background(),
		connect.NewRequest(&pb.ListMyMembershipsRequest{}))
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if resp.Msg.GetPage().GetNextPageToken() != "cursor-2" {
		t.Errorf("next page token surfaced as %q — the caller stops reading and "+
			"believes it has seen every membership",
			resp.Msg.GetPage().GetNextPageToken())
	}
}

// ResetPassword returns the generated secret ONLY when the caller supplied
// none. Inverting that both withholds a password nobody else will ever learn
// and echoes one the caller already knows.
func TestResetPassword_ReturnsTheGeneratedSecretOnlyWhenItGeneratedOne(t *testing.T) {
	name := "tenants/" + uuid.NewString() + "/users/" + uuid.NewString()

	t.Run("no password supplied — the generated one comes back", func(t *testing.T) {
		srv := &UserServer{H: resettingUser{generated: "s3cret"}}
		resp, err := srv.ResetPassword(context.Background(),
			connect.NewRequest(&pb.ResetPasswordRequest{Name: name}))
		if err != nil {
			t.Fatalf("reset: %v", err)
		}
		if resp.Msg.GetGeneratedPassword() != "s3cret" {
			t.Errorf("generated password came back as %q — the account was reset to "+
				"a secret nobody can read", resp.Msg.GetGeneratedPassword())
		}
	})

	t.Run("password supplied — nothing is echoed", func(t *testing.T) {
		srv := &UserServer{H: resettingUser{generated: "s3cret"}}
		resp, err := srv.ResetPassword(context.Background(),
			connect.NewRequest(&pb.ResetPasswordRequest{Name: name, NewPassword: "chosen"}))
		if err != nil {
			t.Fatalf("reset: %v", err)
		}
		if resp.Msg.GetGeneratedPassword() != "" {
			t.Errorf("the response carried %q for a caller-chosen password",
				resp.Msg.GetGeneratedPassword())
		}
	})
}

type resettingUser struct {
	failingUser
	generated string
}

func (r resettingUser) ResetPassword(context.Context, uuid.UUID, string) (string, error) {
	return r.generated, nil
}

// The settings conversion can fail on stored preferences that are not valid
// JSON, and both read paths check it. Inverting either check turns every
// SUCCESSFUL read into CodeInternal, which no failing double can reveal.
type okUserSettings struct {
	failingUserSettings
	pageSize int32
}

func (o *okUserSettings) GetMine(context.Context) (*usersettingsh.Settings, error) {
	return &usersettingsh.Settings{UserID: uuid.New(), TenantID: uuid.New()}, nil
}

func (o *okUserSettings) UpdateMine(context.Context, usersettingsh.UpdateMineInput) (*usersettingsh.Settings, error) {
	return &usersettingsh.Settings{UserID: uuid.New(), TenantID: uuid.New()}, nil
}

func (o *okUserSettings) ListByTenant(_ context.Context, _ uuid.UUID, pageSize int32) ([]usersettingsh.Settings, error) {
	o.pageSize = pageSize
	return nil, nil
}

func TestUserSettings_ReadPathsSucceed(t *testing.T) {
	srv := &UserSettingsServer{H: &okUserSettings{}}
	ctx := context.Background()

	if _, err := srv.GetMine(ctx, connect.NewRequest(&pb.GetMineRequest{})); err != nil {
		t.Errorf("GetMine on well-formed settings: %v", err)
	}
	if _, err := srv.UpdateMine(ctx, connect.NewRequest(&pb.UpdateMineRequest{Theme: "dark"})); err != nil {
		t.Errorf("UpdateMine on well-formed settings: %v", err)
	}
}

func TestListByTenant_ForwardsThePageSize(t *testing.T) {
	h := &okUserSettings{}
	srv := &UserSettingsServer{H: h}

	_, err := srv.ListByTenant(context.Background(), connect.NewRequest(&pb.ListByTenantRequest{
		Parent: "tenants/" + uuid.NewString(),
		Page:   &commonpb.PageRequest{PageSize: 25},
	}))
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if h.pageSize != 25 {
		t.Errorf("page size reached the handler as %d — the caller's limit is "+
			"ignored and the response size is whatever the handler defaults to",
			h.pageSize)
	}
}

// SystemServer has no handler to substitute; these two branches are held by
// calling it directly.
func TestGetVersion_FillsAnEmptyGoVersion(t *testing.T) {
	t.Run("empty — filled from the runtime", func(t *testing.T) {
		srv := &SystemServer{}
		resp, err := srv.GetVersion(context.Background(), connect.NewRequest(&pb.GetVersionRequest{}))
		if err != nil {
			t.Fatalf("version: %v", err)
		}
		if resp.Msg.GetGoVersion() != runtime.Version() {
			t.Errorf("go_version came back %q, want the runtime's %q",
				resp.Msg.GetGoVersion(), runtime.Version())
		}
	})

	t.Run("set at build time — kept as given", func(t *testing.T) {
		srv := &SystemServer{GoVersion: "go1.99.0"}
		resp, err := srv.GetVersion(context.Background(), connect.NewRequest(&pb.GetVersionRequest{}))
		if err != nil {
			t.Fatalf("version: %v", err)
		}
		if resp.Msg.GetGoVersion() != "go1.99.0" {
			t.Errorf("the build's go_version was overwritten with %q", resp.Msg.GetGoVersion())
		}
	})
}

// With no health handler registered the server reports healthy rather than
// dereferencing a nil one. The production wiring always passes a real handler,
// so this guard is what keeps every test binary and local `go run` from
// panicking on the first health probe.
func TestGetHealth_WithoutAHandlerReportsHealthy(t *testing.T) {
	srv := &SystemServer{Role: "core"}
	resp, err := srv.GetHealth(context.Background(), connect.NewRequest(&pb.GetHealthRequest{}))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if resp.Msg.GetStatus() != pb.ComponentStatus_COMPONENT_STATUS_HEALTHY {
		t.Errorf("status %v with no checks registered", resp.Msg.GetStatus())
	}
	if resp.Msg.GetRole() != "core" {
		t.Errorf("role %q", resp.Msg.GetRole())
	}
}
