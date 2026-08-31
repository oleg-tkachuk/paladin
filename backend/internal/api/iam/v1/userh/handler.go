// Package userh implements the IAM UserService — admin-side CRUD over users.
// Requires role iam.admin or platform.admin (enforced at audience interceptor
// + Cedar). Tenant admins may manage users only within their own tenant.
package userh

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
	celpkg "github.com/oleg-tkachuk/paladin/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

type Handler struct {
	users  authstore.UserRepository
	policy cedar.Authorizer
	// cel compiles and caches List filters (program cache only).
	cel *celpkg.Evaluator
}

func NewHandler(u authstore.UserRepository, policy cedar.Authorizer) *Handler {
	if policy == nil {
		panic("userh: policy authorizer is required")
	}
	return &Handler{cel: celpkg.NewEvaluator(), users: u, policy: policy}
}

// authorize gates a user-management RPC against Cedar. Pre-existing
// hasPlatformAdmin/tenant-isolation checks stay as defense-in-depth at the
// call sites; Cedar adds policy expressivity (tenant.admin permits, etc.)
// on top.
// readCtx widens a lookup that cannot name its tenant yet.
//
// Every admin operation here starts by fetching a user BY ID ALONE — the
// tenant is a property of the row, not of the request — so a tenant-scoped
// read would find nothing before the handler could learn which tenant to
// scope to. The flag widens SELECT only; the authorization gate on the line
// after each read is what bounds it, and the write that follows is pinned to
// the row's own tenant with WithActingTenant.
//
// This does not loosen anything: with no policy on `users` today the read is
// already unrestricted. It keeps that behaviour while letting the writes be
// pinned, which is the half a policy can actually enforce.
func readCtx(ctx context.Context) context.Context {
	return auth.WithCrossTenantRead(ctx)
}

func (h *Handler) authorize(ctx context.Context, action string, target authstore.User) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		action,
		&cedar.Resource{
			TenantID:      target.TenantID,
			TargetUserID:  target.UserID,
			TargetSubject: target.Subject,
		},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return apiutil.MapError(fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied,
			errors.New("denied by policy"))
	}
	return nil
}

// ─── Create ─────────────────────────────────────────────────────────────────

type CreateUserInput struct {
	TenantID        uuid.UUID
	Subject         string
	DisplayName     string
	InitialPassword string
	Roles           []string
	Scopes          []auth.Scope
}

func (h *Handler) CreateUser(ctx context.Context, in CreateUserInput) (*authstore.User, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	// An omitted parent means "the caller's own tenant", not "no tenant":
	// users.tenant_id is NOT NULL and every user belongs to exactly one
	// tenant, the bootstrap admin included. Passing the zero uuid through
	// reached the database and failed there with a constraint violation,
	// which told the operator nothing about what they had left out.
	if in.TenantID == uuid.Nil {
		in.TenantID = caller
	}
	// Tenant admins limited to their own tenant — code-level guard.
	p, _ := auth.PrincipalFromContext(ctx)
	if !hasPlatformAdmin(p) && in.TenantID != caller {
		return nil, connect.NewError(connect.CodePermissionDenied,
			errors.New("cannot create user in foreign tenant"))
	}
	if err := h.authorize(ctx, cedar.ActionManageUser,
		authstore.User{TenantID: in.TenantID, Subject: in.Subject}); err != nil {
		return nil, err
	}
	if in.Subject == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("subject required"))
	}
	if in.InitialPassword == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("initial_password required"))
	}
	hash, err := auth.HashPassword(in.InitialPassword)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	u, err := h.users.Create(auth.WithActingTenant(ctx, in.TenantID), authstore.User{
		TenantID:     in.TenantID,
		Subject:      in.Subject,
		DisplayName:  in.DisplayName,
		PasswordHash: hash,
		Roles:        in.Roles,
		Scopes:       in.Scopes,
	})
	if err != nil {
		return nil, apiutil.MapError(err)
	}
	return &u, nil
}

// ─── Read ───────────────────────────────────────────────────────────────────

func (h *Handler) GetUser(ctx context.Context, id uuid.UUID) (*authstore.User, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	u, err := h.users.GetByID(readCtx(ctx), id)
	if err != nil {
		return nil, apiutil.MapError(err)
	}
	p, _ := auth.PrincipalFromContext(ctx)
	if !hasPlatformAdmin(p) && u.TenantID != caller {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("user not found"))
	}
	if err := h.authorize(ctx, cedar.ActionReadUser, u); err != nil {
		return nil, err
	}
	return &u, nil
}

// ─── Update ─────────────────────────────────────────────────────────────────

type UpdateUserInput struct {
	UserID          uuid.UUID
	ExpectedVersion int64
	UpdateMask      []string
	DisplayName     string
	Disabled        bool
	Roles           []string
}

func (h *Handler) UpdateUser(ctx context.Context, in UpdateUserInput) (*authstore.User, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	current, err := h.users.GetByID(readCtx(ctx), in.UserID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	p, _ := auth.PrincipalFromContext(ctx)
	if !hasPlatformAdmin(p) && current.TenantID != caller {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("cross-tenant update denied"))
	}
	if err := h.authorize(ctx, cedar.ActionManageUser, current); err != nil {
		return nil, err
	}
	for _, field := range in.UpdateMask {
		switch field {
		case "display_name":
			current.DisplayName = in.DisplayName
		case "disabled":
			current.Disabled = in.Disabled
		case "roles":
			current.Roles = in.Roles
		}
	}
	updated, err := h.users.Update(auth.WithActingTenant(ctx, current.TenantID), current, in.ExpectedVersion)
	if err != nil {
		return nil, apiutil.MapError(err)
	}
	return &updated, nil
}

// ─── Delete ─────────────────────────────────────────────────────────────────

func (h *Handler) DeleteUser(ctx context.Context, id uuid.UUID, expectedVersion int64) error {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return err
	}
	u, err := h.users.GetByID(readCtx(ctx), id)
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	p, _ := auth.PrincipalFromContext(ctx)
	if !hasPlatformAdmin(p) && u.TenantID != caller {
		return connect.NewError(connect.CodePermissionDenied, errors.New("cross-tenant delete denied"))
	}
	if err := h.authorize(ctx, cedar.ActionManageUser, u); err != nil {
		return err
	}
	if err := h.users.Delete(auth.WithActingTenant(ctx, u.TenantID), id, expectedVersion); err != nil {
		return apiutil.MapError(err)
	}
	return nil
}

// ─── List ───────────────────────────────────────────────────────────────────

type ListUsersInput struct {
	TenantID  uuid.UUID
	PageSize  int32
	PageToken string
	Filter    string
}

func (h *Handler) ListUsers(ctx context.Context, in ListUsersInput) ([]authstore.User, string, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, "", err
	}
	p, _ := auth.PrincipalFromContext(ctx)
	scope := in.TenantID
	if scope == uuid.Nil {
		// Cross-tenant listing is platform-admin only.
		if !hasPlatformAdmin(p) {
			scope = caller
		}
	} else if !hasPlatformAdmin(p) && scope != caller {
		return nil, "", connect.NewError(connect.CodePermissionDenied, errors.New("cross-tenant list denied"))
	}
	// Cedar authz scope: the default policy template grants
	// ReadUser to members of Tenant::"<their-slug>". When this is a
	// cross-tenant list (scope==Nil), we authorize against the
	// caller's own tenant — platform.admin role on the principal
	// unlocks the broader scan; non-admins already had scope
	// narrowed to `caller` above. Using Nil here would feed Cedar
	// a Resource{TenantID: Nil} that no policy literal matches,
	// resulting in a silent deny even for legitimate admins.
	authzScope := scope
	if authzScope == uuid.Nil {
		authzScope = caller
	}
	if err := h.authorize(ctx, cedar.ActionReadUser, authstore.User{TenantID: authzScope}); err != nil {
		return nil, "", err
	}
	// users carries an RLS policy, so a read outside the caller's own tenant
	// has to say which one it means. Both cross-tenant shapes need saying,
	// and only the first one did:
	//
	//   - scope Nil is the platform-admin listing that spans tenants. The
	//     flag widens SELECT only, and non-admins never reach here with Nil
	//     because it was narrowed to `caller` above.
	//   - a NAMED scope that is not the caller's tenant is the one that was
	//     missing. The role gate above lets a platform admin through, and
	//     then the query ran with the session still pinned to the CALLER's
	//     tenant, so it matched nothing and the RPC answered with an empty
	//     page. Not an error, not a refusal — the tenant simply looked like
	//     it had no users.
	//
	// That is what made the e2e teardown leak: it lists a tenant's users to
	// delete them, got an empty page, deleted nothing, reported success, and
	// left a user behind that then blocked the purge. Those leftovers are
	// what eventually broke Login (see FindUsersBySubjectGlobal).
	//
	// WithActingTenant, not WithCrossTenantRead: the request names one
	// tenant, so pin to it rather than widening to all of them.
	listCtx := ctx
	switch {
	case scope == uuid.Nil:
		listCtx = auth.WithCrossTenantRead(ctx)
	case scope != caller:
		listCtx = auth.WithActingTenant(ctx, scope)
	}
	page, next, err := h.users.List(listCtx, authstore.ListUsersArgs{
		TenantID:  scope,
		PageSize:  in.PageSize,
		PageToken: in.PageToken,
		Filter:    in.Filter,
	})
	if err != nil {
		return nil, "", err
	}
	// Authoritative pass. The store narrows the scan with whatever part of
	// this expression SQL can express, and may only narrow: anything it does
	// not understand is decided here, where the rows are.
	page, err = celpkg.FilterPage(h.cel, celpkg.UserSchema, in.Filter, page, userRow)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("filter: %w", err))
	}
	return page, next, nil
}

// userRow projects a User onto the variables UserSchema declares. The password
// hash and scopes are deliberately absent: a filter must not become an oracle
// for either.
func userRow(u authstore.User) map[string]any {
	roles := make([]any, 0, len(u.Roles))
	for _, r := range u.Roles {
		roles = append(roles, r)
	}
	return map[string]any{
		"user_id":      u.UserID.String(),
		"tenant_id":    u.TenantID.String(),
		"subject":      u.Subject,
		"display_name": u.DisplayName,
		"disabled":     u.Disabled,
		"roles":        roles,
		"created_at":   u.CreatedAt,
	}
}

// ─── GrantScopes / RevokeScopes ─────────────────────────────────────────────

func (h *Handler) GrantScopes(ctx context.Context, id uuid.UUID, scopes []auth.Scope) (*authstore.User, error) {
	current, err := h.users.GetByID(readCtx(ctx), id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if err := h.authorize(ctx, cedar.ActionGrantScopes, current); err != nil {
		return nil, err
	}
	current.Scopes = mergeScopes(current.Scopes, scopes)
	updated, err := h.users.Update(auth.WithActingTenant(ctx, current.TenantID), current, 0)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &updated, nil
}

func (h *Handler) RevokeScopes(ctx context.Context, id uuid.UUID, scopes []auth.Scope) (*authstore.User, error) {
	current, err := h.users.GetByID(readCtx(ctx), id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if err := h.authorize(ctx, cedar.ActionGrantScopes, current); err != nil {
		return nil, err
	}
	current.Scopes = removeScopes(current.Scopes, scopes)
	updated, err := h.users.Update(auth.WithActingTenant(ctx, current.TenantID), current, 0)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &updated, nil
}

// ─── ResetPassword ──────────────────────────────────────────────────────────

func (h *Handler) ResetPassword(ctx context.Context, id uuid.UUID, newPassword string) (string, error) {
	current, err := h.users.GetByID(readCtx(ctx), id)
	if err != nil {
		return "", connect.NewError(connect.CodeNotFound, err)
	}
	if err := h.authorize(ctx, cedar.ActionResetPassword, current); err != nil {
		return "", err
	}
	if newPassword == "" {
		newPassword = generatedPassword()
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return "", connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.users.UpdatePasswordHash(auth.WithActingTenant(ctx, current.TenantID), id, hash); err != nil {
		return "", connect.NewError(connect.CodeInternal, fmt.Errorf("reset password: %w", err))
	}
	return newPassword, nil
}

// ─── helpers ────────────────────────────────────────────────────────────────

func hasPlatformAdmin(p *auth.Principal) bool {
	if p == nil {
		return false
	}
	return p.HasRole("platform.admin")
}

func mergeScopes(existing, added []auth.Scope) []auth.Scope {
	seen := map[string]struct{}{}
	out := make([]auth.Scope, 0, len(existing)+len(added))
	for _, s := range existing {
		if _, ok := seen[s.String()]; !ok {
			seen[s.String()] = struct{}{}
			out = append(out, s)
		}
	}
	for _, s := range added {
		if _, ok := seen[s.String()]; !ok {
			seen[s.String()] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

func removeScopes(existing, removing []auth.Scope) []auth.Scope {
	rm := map[string]struct{}{}
	for _, s := range removing {
		rm[s.String()] = struct{}{}
	}
	out := make([]auth.Scope, 0, len(existing))
	for _, s := range existing {
		if _, ok := rm[s.String()]; !ok {
			out = append(out, s)
		}
	}
	return out
}

func generatedPassword() string {
	// Lightweight password generator. Caller-side bcrypt hashing happens
	// after — collision impossible for the 16-byte random secret.
	secret, _, err := auth.GenerateApiKeySecret()
	if err != nil {
		return time.Now().UTC().Format("20060102T150405.000000000")
	}
	// Trim the prefix; we just want random bytes.
	if i := strings.Index(secret, "_"); i > 0 {
		return secret[i+1:]
	}
	return secret
}
