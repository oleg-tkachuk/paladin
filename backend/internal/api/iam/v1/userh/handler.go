// Package userh implements the IAM UserService — admin-side CRUD over users.
// Requires role iam.admin or platform.admin (enforced at audience interceptor
// + Cedar). Tenant admins may manage users only within their own tenant.
package userh

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"github.com/oleg-tkachuk/limes"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	celpkg "github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
)

type Handler struct {
	users  authstore.UserRepository
	policy cedar.Authorizer
	// cel compiles and caches List filters (program cache only).
	cel *celpkg.Evaluator
	// grants lists and ends the OAuth grants users gave clients. Nil
	// answers both RPCs Unimplemented.
	grants authstore.OAuthGrantStore
	// sessions revokes the capabilities exchanged under a grant's session
	// when the grant ends. Nil while the capability subsystem is off.
	sessions *auth.BulkRevocation
	// capabilities lists the capabilities acting on users' behalf. Nil
	// answers ListUserCapabilities Unimplemented.
	capabilities limes.RecordLister
	now          func() time.Time
}

// WithCapabilities lets the handler list the capabilities acting on users'
// behalf.
func (h *Handler) WithCapabilities(c limes.RecordLister) *Handler {
	h.capabilities = c
	return h
}

// WithSessionRevoker lets ending a grant end the capabilities exchanged
// under its session.
func (h *Handler) WithSessionRevoker(s *auth.BulkRevocation) *Handler {
	h.sessions = s
	return h
}

// WithOAuthGrants lets the handler list and end users' OAuth grants.
func (h *Handler) WithOAuthGrants(g authstore.OAuthGrantStore) *Handler {
	h.grants = g
	return h
}

func NewHandler(u authstore.UserRepository, policy cedar.Authorizer) *Handler {
	if policy == nil {
		panic("userh: policy authorizer is required")
	}
	return &Handler{cel: celpkg.NewEvaluator(), users: u, policy: policy, now: time.Now}
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
// `users` has carried a tenant policy since 017_users_rls.sql, so this flag is
// a real widening: the authorization gate after each load (and load's own
// check that the user belongs to the tenant named) is what keeps a caller to
// the users it may see.
func readCtx(ctx context.Context) context.Context {
	return auth.WithCrossTenantRead(ctx)
}

func (h *Handler) authorize(ctx context.Context, action cedar.Action, target authstore.User) error {
	_, err := apiutil.Authorize(ctx, h.policy, action, &cedar.Resource{
		TenantID:      target.TenantID,
		TargetUserID:  target.UserID,
		TargetSubject: target.Subject,
	})
	return err
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
	if in.TenantID == uuid.Nil() {
		in.TenantID = caller
	}
	// Tenant admins limited to their own tenant — code-level guard.
	p, _ := auth.PrincipalFromContext(ctx)
	if !hasPlatformAdmin(p) && in.TenantID != caller {
		return nil, connect.NewError(connect.CodePermissionDenied,
			"cannot create user in foreign tenant")
	}
	// Input first: without a subject the request names no user, and Cedar
	// would be asked about the Tenant instead.
	if in.Subject == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "subject required")
	}
	if in.InitialPassword == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "initial_password required")
	}
	if err := h.authorize(ctx, cedar.ActionManageUser,
		authstore.User{TenantID: in.TenantID, Subject: in.Subject}); err != nil {
		return nil, err
	}
	hash, err := auth.HashPassword(in.InitialPassword)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
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

// UserRef names a user as its resource name does: its tenant and its id.
type UserRef struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
}

// errUserNotFound answers a user that does not exist under the tenant named.
var errUserNotFound = errors.New("user not found")

// load reads the user ref names, and refuses one that is not the named
// tenant's as not found: no user exists at that name. The tenant in the name
// is what the tenant freeze judges a call by, so a name pairing a live tenant
// with another tenant's user would otherwise act on a trashed tenant's user
// past the freeze.
func (h *Handler) load(ctx context.Context, ref UserRef) (authstore.User, error) {
	u, err := h.users.GetByID(readCtx(ctx), ref.UserID)
	if err != nil {
		return authstore.User{}, err
	}
	if u.TenantID != ref.TenantID {
		return authstore.User{}, connect.NewError(connect.CodeNotFound, errUserNotFound.Error()).WithCause(errUserNotFound)
	}
	return u, nil
}

// notFound maps a failed load: its own NotFound as it is, a store error as
// not found.
func notFound(err error) error {
	var cerr *connect.Error
	if errors.As(err, &cerr) {
		return cerr
	}
	return connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
}

// ─── Read ───────────────────────────────────────────────────────────────────

func (h *Handler) GetUser(ctx context.Context, ref UserRef) (*authstore.User, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	u, err := h.load(ctx, ref)
	if err != nil {
		if cerr := new(connect.Error); errors.As(err, &cerr) {
			return nil, cerr
		}
		return nil, apiutil.MapError(err)
	}
	p, _ := auth.PrincipalFromContext(ctx)
	if !hasPlatformAdmin(p) && u.TenantID != caller {
		return nil, connect.NewError(connect.CodeNotFound, "user not found")
	}
	if err := h.authorize(ctx, cedar.ActionReadUser, u); err != nil {
		return nil, err
	}
	return &u, nil
}

// ─── Update ─────────────────────────────────────────────────────────────────

type UpdateUserInput struct {
	User            UserRef
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
	current, err := h.load(ctx, in.User)
	if err != nil {
		return nil, notFound(err)
	}
	p, _ := auth.PrincipalFromContext(ctx)
	if !hasPlatformAdmin(p) && current.TenantID != caller {
		return nil, connect.NewError(connect.CodePermissionDenied, "cross-tenant update denied")
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

func (h *Handler) DeleteUser(ctx context.Context, ref UserRef, expectedVersion int64) error {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return err
	}
	u, err := h.load(ctx, ref)
	if err != nil {
		return notFound(err)
	}
	p, _ := auth.PrincipalFromContext(ctx)
	if !hasPlatformAdmin(p) && u.TenantID != caller {
		return connect.NewError(connect.CodePermissionDenied, "cross-tenant delete denied")
	}
	if err := h.authorize(ctx, cedar.ActionManageUser, u); err != nil {
		return err
	}
	if err := h.users.Delete(auth.WithActingTenant(ctx, u.TenantID), u.UserID, expectedVersion); err != nil {
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
	if scope == uuid.Nil() {
		// Cross-tenant listing is platform-admin only.
		if !hasPlatformAdmin(p) {
			scope = caller
		}
	} else if !hasPlatformAdmin(p) && scope != caller {
		return nil, "", connect.NewError(connect.CodePermissionDenied, "cross-tenant list denied")
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
	if authzScope == uuid.Nil() {
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
	case scope == uuid.Nil():
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
		return nil, "", rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
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

func (h *Handler) GrantScopes(ctx context.Context, ref UserRef, scopes []auth.Scope) (*authstore.User, error) {
	current, err := h.load(ctx, ref)
	if err != nil {
		return nil, notFound(err)
	}
	if err := h.authorize(ctx, cedar.ActionGrantScopes, current); err != nil {
		return nil, err
	}
	current.Scopes = mergeScopes(current.Scopes, scopes)
	updated, err := h.users.Update(auth.WithActingTenant(ctx, current.TenantID), current, 0)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &updated, nil
}

func (h *Handler) RevokeScopes(ctx context.Context, ref UserRef, scopes []auth.Scope) (*authstore.User, error) {
	current, err := h.load(ctx, ref)
	if err != nil {
		return nil, notFound(err)
	}
	if err := h.authorize(ctx, cedar.ActionGrantScopes, current); err != nil {
		return nil, err
	}
	current.Scopes = removeScopes(current.Scopes, scopes)
	updated, err := h.users.Update(auth.WithActingTenant(ctx, current.TenantID), current, 0)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &updated, nil
}

// ─── ResetPassword ──────────────────────────────────────────────────────────

func (h *Handler) ResetPassword(ctx context.Context, ref UserRef, newPassword string) (string, error) {
	current, err := h.load(ctx, ref)
	if err != nil {
		return "", notFound(err)
	}
	if err := h.authorize(ctx, cedar.ActionResetPassword, current); err != nil {
		return "", err
	}
	if newPassword == "" {
		newPassword = generatedPassword()
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return "", connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	if err := h.users.UpdatePasswordHash(auth.WithActingTenant(ctx, current.TenantID), current.UserID, hash); err != nil {
		return "", rpcerr.New(connect.CodeInternal, fmt.Errorf("reset password: %w", err))
	}
	return newPassword, nil
}

// ─── OAuth grants ───────────────────────────────────────────────────────────

// errNoGrantStore answers the grant RPCs on a handler built without a store.
var errNoGrantStore = errors.New("oauth grants are not served here")

// ListOAuthGrants returns the OAuth grants the user gave clients: the user's
// own, or for a tenant admin a user of the tenant. Only the live ones unless
// includeInactive, which adds those revoked or expired whose rows remain.
func (h *Handler) ListOAuthGrants(ctx context.Context, ref UserRef, includeInactive bool) ([]authstore.UserOAuthGrant, error) {
	u, err := h.grantTarget(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionReadOAuthGrants, u); err != nil {
		return nil, err
	}
	grants, err := h.grants.ListOAuthGrants(auth.WithActingTenant(ctx, u.TenantID), u.UserID, h.now(), includeInactive)
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal, fmt.Errorf("list oauth grants: %w", err))
	}
	return grants, nil
}

// callerSubject is the subject of the principal ctx carries, empty for none.
func callerSubject(ctx context.Context) string {
	if p, err := auth.PrincipalFromContext(ctx); err == nil && p != nil {
		return p.Subject
	}
	return ""
}

// noteGrantRevokedByOperator is why a session's capabilities were revoked
// when an operator ended its grant from the console.
const noteGrantRevokedByOperator = "an operator revoked the OAuth grant"

// RevokeOAuthGrant ends one of the user's grants: the client's refresh
// tokens stop working at once, and so do the capabilities exchanged under
// the grant's session; an access token it holds lasts its short lifetime.
// A grant that is not the user's is not found. One already ended is ended
// again, so a call that failed between the two steps is finished by a retry.
func (h *Handler) RevokeOAuthGrant(ctx context.Context, ref UserRef, grantID uuid.UUID) error {
	u, err := h.grantTarget(ctx, ref)
	if err != nil {
		return err
	}
	if err := h.authorize(ctx, cedar.ActionRevokeOAuthGrant, u); err != nil {
		return err
	}
	ctx = auth.WithActingTenant(ctx, u.TenantID)
	// The grant is checked to be the user's, ended or not, before anything
	// is revoked: the session named is one the caller is authorized over.
	grants, err := h.grants.ListOAuthGrants(ctx, u.UserID, h.now(), true)
	if err != nil {
		return rpcerr.New(connect.CodeInternal, fmt.Errorf("list oauth grants: %w", err))
	}
	if !slices.ContainsFunc(grants, func(g authstore.UserOAuthGrant) bool { return g.ID == grantID }) {
		return connect.NewError(connect.CodeNotFound, "oauth grant not found")
	}
	// The refresh tokens first, so no new capability is exchanged under the
	// session while its capabilities are revoked.
	if _, err := h.grants.RevokeOAuthGrant(ctx, u.UserID, grantID); err != nil {
		return rpcerr.New(connect.CodeInternal, fmt.Errorf("revoke oauth grant: %w", err))
	}
	var revoked int64
	if h.sessions != nil {
		if revoked, err = h.sessions.RevokeSession(ctx, u.TenantID, grantID, noteGrantRevokedByOperator, callerSubject(ctx)); err != nil {
			return connect.NewError(connect.CodeUnavailable, "the grant's capabilities could not be revoked; retry").WithCause(err)
		}
	}
	logger.FromContext(ctx).Info("oauth grant revoked",
		zap.String("user_id", u.UserID.String()), zap.String("grant_id", grantID.String()),
		zap.Int64(auth.LogKeyCapabilitiesRevoked, revoked))
	return nil
}

// grantTarget loads the user whose grants are asked about, as ownedTarget
// does.
func (h *Handler) grantTarget(ctx context.Context, ref UserRef) (authstore.User, error) {
	if h.grants == nil {
		return authstore.User{}, connect.NewError(connect.CodeUnimplemented, errNoGrantStore.Error())
	}
	return h.ownedTarget(ctx, ref)
}

// ownedTarget loads the user whose own things — grants, capabilities — are
// asked about. Another tenant's user reads as not found to all but a
// platform admin, as GetUser does; the caller then authorizes its action on
// the user.
func (h *Handler) ownedTarget(ctx context.Context, ref UserRef) (authstore.User, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return authstore.User{}, err
	}
	u, err := h.load(ctx, ref)
	if err != nil {
		return authstore.User{}, notFound(err)
	}
	p, _ := auth.PrincipalFromContext(ctx)
	if !hasPlatformAdmin(p) && u.TenantID != caller {
		return authstore.User{}, connect.NewError(connect.CodeNotFound, errUserNotFound.Error())
	}
	return u, nil
}

// ─── Capabilities acting for a user ─────────────────────────────────────────

// errNoCapabilityLister answers ListUserCapabilities on a handler built
// without a lister: the capability subsystem is off.
var errNoCapabilityLister = errors.New("capabilities are not served here")

// logKeyCapabilitiesListed is how many capabilities a listing returned.
const logKeyCapabilitiesListed = "capabilities_listed"

// UserCapabilitiesPage asks for a page of the capabilities acting on a
// user's behalf.
type UserCapabilitiesPage struct {
	// Dispositions lists the capabilities of any of these; empty, the
	// active ones.
	Dispositions []limes.RecordDisposition
	// Size is the page size; zero, the store's default.
	Size int
	// Token is the one the previous page returned; empty for the first.
	Token string
}

// ListUserCapabilities returns a page of the capabilities acting on the
// user's behalf — exchanged under the user's OAuth grants, or delegated from
// those — newest first: the user's own, or for a tenant admin a user of the
// tenant.
func (h *Handler) ListUserCapabilities(ctx context.Context, ref UserRef, page UserCapabilitiesPage) (limes.Page[limes.RecordListing], error) {
	if h.capabilities == nil {
		return limes.Page[limes.RecordListing]{}, connect.NewError(connect.CodeUnimplemented, errNoCapabilityLister.Error())
	}
	u, err := h.ownedTarget(ctx, ref)
	if err != nil {
		return limes.Page[limes.RecordListing]{}, err
	}
	if err := h.authorize(ctx, cedar.ActionReadUserCapabilities, u); err != nil {
		return limes.Page[limes.RecordListing]{}, err
	}
	dispositions := page.Dispositions
	if len(dispositions) == 0 {
		dispositions = []limes.RecordDisposition{limes.RecordDispositionActive}
	}
	out, err := h.capabilities.ListRecords(auth.WithActingTenant(ctx, u.TenantID), limes.ListRecordsRequest{
		TenantID:     u.TenantID,
		OnBehalfOf:   u.UserID.String(),
		Dispositions: dispositions,
		Order:        limes.ListOrderDescending,
		Cursor:       page.Token,
		Limit:        page.Size,
	})
	if err != nil {
		return limes.Page[limes.RecordListing]{}, auth.AnswerCapabilityError(err, auth.ListExceptions)
	}
	logger.FromContext(ctx).Debug("user capabilities listed",
		zap.String("user_id", u.UserID.String()), zap.String("tenant_id", u.TenantID.String()),
		zap.Int(logKeyCapabilitiesListed, len(out.Items)))
	return out, nil
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
