package oauth

// authenticate() resolves the resource owner behind the consent form, and the
// refresh path re-checks that owner on every rotation. Both are guarded by
// disjunctions where each arm refuses a different way of being ineligible,
// and dropping any one arm hands out a token to someone who should not have
// one — with a 200, not an error.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/internal/config"
)

// multiUsers resolves subjects against a list, so the ambiguity branches of
// the global lookup are reachable. memUsers only ever holds one.
type multiUsers struct {
	users []authstore.User
	err   error
}

func (m multiUsers) GetByID(_ context.Context, id uuid.UUID) (authstore.User, error) {
	for _, u := range m.users {
		if u.UserID == id {
			return u, nil
		}
	}
	return authstore.User{}, authstore.ErrNotFound
}

func (m multiUsers) GetBySubject(_ context.Context, tenant uuid.UUID, subject string) (authstore.User, error) {
	for _, u := range m.users {
		if u.Subject == subject && u.TenantID == tenant {
			return u, nil
		}
	}
	return authstore.User{}, authstore.ErrNotFound
}

func (m multiUsers) FindBySubjectGlobal(_ context.Context, subject string) ([]authstore.User, error) {
	if m.err != nil {
		return nil, m.err
	}
	var out []authstore.User
	for _, u := range m.users {
		if u.Subject == subject {
			out = append(out, u)
		}
	}
	return out, nil
}

func handlerWithUsers(t *testing.T, store Store, users UserResolver) *Handler {
	t.Helper()
	h := testHandler(t, store, &memRefresh{}, sampleUser())
	h.users = users
	return h
}

// A subject that exists in more than one tenant is ambiguous, and the server
// must refuse rather than pick. Picking would authenticate the consent form
// as whichever row the query happened to return first — a cross-tenant
// impersonation decided by row order.
func TestAuthenticate_AmbiguousSubjectIsRefused(t *testing.T) {
	pw, err := auth.HashPassword("hunter2hunter2")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	mk := func(subject string) authstore.User {
		return authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: subject, PasswordHash: pw}
	}
	one, two := mk("svc@acme"), mk("svc@acme")

	h := handlerWithUsers(t, newMemStore(), multiUsers{users: []authstore.User{one, two}})

	if _, err := h.authenticate(context.Background(), "svc@acme", "hunter2hunter2", ""); err == nil {
		t.Fatal("a subject present in two tenants authenticated without a tenant hint")
	}

	// With the hint it resolves to exactly that tenant's row, and to no other.
	got, err := h.authenticate(context.Background(), "svc@acme", "hunter2hunter2", one.TenantID.String())
	if err != nil {
		t.Fatalf("tenant-scoped authenticate: %v", err)
	}
	if got.UserID != one.UserID {
		t.Errorf("resolved user = %v, want the hinted tenant's %v", got.UserID, one.UserID)
	}

	// No such subject is refused rather than resolving to a zero user.
	if _, err := h.authenticate(context.Background(), "nobody@acme", "hunter2hunter2", ""); err == nil {
		t.Error("an unknown subject authenticated")
	}
}

// Two ways to be ineligible, one guard. A disabled account must not pass, and
// neither must an account with no password on file — an SSO-only or
// not-yet-provisioned user, for whom the password grant is not a login method
// at all.
func TestAuthenticate_IneligibleUsersAreRefused(t *testing.T) {
	pw, err := auth.HashPassword("hunter2hunter2")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	cases := map[string]authstore.User{
		"disabled":            {UserID: uuid.New(), TenantID: uuid.New(), Subject: "u@acme", PasswordHash: pw, Disabled: true},
		"no password on file": {UserID: uuid.New(), TenantID: uuid.New(), Subject: "u@acme"},
	}
	for name, u := range cases {
		t.Run(name, func(t *testing.T) {
			h := handlerWithUsers(t, newMemStore(), multiUsers{users: []authstore.User{u}})
			if _, err := h.authenticate(context.Background(), "u@acme", "hunter2hunter2", ""); err == nil {
				t.Fatalf("a %s user authenticated", name)
			}
		})
	}

	// The eligible one still works, so the cases above fail for their own
	// reason rather than because nothing can authenticate.
	ok := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: "u@acme", PasswordHash: pw}
	h := handlerWithUsers(t, newMemStore(), multiUsers{users: []authstore.User{ok}})
	if _, err := h.authenticate(context.Background(), "u@acme", "hunter2hunter2", ""); err != nil {
		t.Fatalf("an eligible user was refused: %v", err)
	}
}

// HTTP Basic is the other way a confidential client presents its secret, and
// the username in it must be the client. As a disjunction any Basic header at
// all would overwrite the form secret with its password — so a caller could
// supply the right secret under someone else's client id, or clobber a
// correct form secret with a wrong Basic one.
func TestToken_BasicAuthUsernameMustMatchTheClient(t *testing.T) {
	const secret = "correct-horse-battery-staple"
	verifier := strings.Repeat("a", 50)
	u := sampleUser()

	newFixture := func(t *testing.T) *Handler {
		t.Helper()
		store := newMemStore()
		c := confidentialClient(t, secret)
		store.clients[c.ClientID] = c
		h := testHandler(t, store, &memRefresh{}, u)
		seedCode(t, store, "basic", c.ClientID, c.RedirectURIs[0], verifier, u)
		return h
	}
	form := func() url.Values {
		return url.Values{
			"grant_type": {"authorization_code"}, "code": {"basic"},
			"client_id": {"svc-client"}, "redirect_uri": {"https://svc.example/cb"},
			"code_verifier": {verifier},
		}
	}

	t.Run("matching username is accepted", func(t *testing.T) {
		rec := postFormBasic(newFixture(t), "/oauth/token", form(), "svc-client", secret)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("mismatched username does not supply the secret", func(t *testing.T) {
		// The password is correct; only the username is wrong. With the guard
		// the Basic header is ignored and no form secret was sent, so this is
		// refused.
		rec := postFormBasic(newFixture(t), "/oauth/token", form(), "someone-else", secret)
		if rec.Code == http.StatusOK {
			t.Fatal("a Basic header naming another client supplied the secret")
		}
	})
}

// Dynamic registration decides at registration time whether a client will
// ever have to prove itself. Inverted, a client asking for
// client_secret_basic is registered public — and every later token request
// from it skips authentication entirely.
func TestRegister_AuthMethodDecidesPublic(t *testing.T) {
	cases := map[string]struct {
		method     string
		wantPublic bool
	}{
		"omitted":             {"", true},
		"none":                {"none", true},
		"client_secret_basic": {"client_secret_basic", false},
		"client_secret_post":  {"client_secret_post", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := newMemStore()
			h := testHandlerCfg(t, store, &memRefresh{}, sampleUser(), config.OAuthAS{
				Enabled: true, DynamicRegistration: true, AuthorizationCodeTTL: time.Minute,
				AllowedRedirectSchemes: []string{"https"},
			})

			body := `{"client_name":"x","redirect_uris":["https://x.example/cb"]`
			if tc.method != "" {
				body += `,"token_endpoint_auth_method":"` + tc.method + `"`
			}
			body += `}`

			rec := postJSON(h, "/oauth/register", body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("register status = %d body = %s", rec.Code, rec.Body.String())
			}
			if len(store.clients) != 1 {
				t.Fatalf("registered %d clients, want 1", len(store.clients))
			}
			for _, c := range store.clients {
				if c.Public != tc.wantPublic {
					t.Errorf("Public = %v, want %v — this decides whether the client ever authenticates",
						c.Public, tc.wantPublic)
				}
				if !tc.wantPublic && len(c.SecretHash) == 0 {
					t.Error("a confidential client was registered with no secret to check")
				}
			}
		})
	}
}

// postFormBasic is postForm with an HTTP Basic credential attached.
func postFormBasic(h *Handler, path string, form url.Values, user, pass string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetBasicAuth(user, pass)
	h.handleToken(rec, r)
	return rec
}

// postJSON posts a raw JSON body to the registration endpoint.
func postJSON(h *Handler, path string, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	h.handleRegister(rec, r)
	return rec
}

// mintRefresh produces a validly-signed IAM refresh token the decoder
// accepts, carrying whatever user and tenant the caller names — which is the
// point: the claims and the stored user are two separate sources, and the
// rotation guard is what makes them agree.
func mintRefresh(t *testing.T, userID, tenantID uuid.UUID) string {
	t.Helper()
	iss, err := issuer.New(issuer.Config{
		Issuer: "paladin", SigningKey: []byte(testSigningKey),
		AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: time.Hour, ScopedTokenMaxTTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	rt, _, err := iss.MintRefresh(issuer.RefreshClaims{
		Subject: userID.String(), TenantID: tenantID, UserID: userID,
		TokenID: uuid.Must(uuid.NewV7()),
	})
	if err != nil {
		t.Fatalf("mint refresh: %v", err)
	}
	return rt
}

// Rotation re-checks the owner on every refresh, and the check is a
// three-armed disjunction. The tenant arm is the one that matters most: the
// tenant comes from the token's claims and the user from the database, and
// without that comparison a validly-signed refresh naming another tenant
// mints a fresh access token scoped to a tenant its owner does not belong to.
//
// Deactivation has to bite here too — a refresh token outlives the session,
// so a disabled user who never logs out keeps rotating indefinitely.
func TestToken_RefreshRechecksTheOwner(t *testing.T) {
	newFixture := func(t *testing.T, u authstore.User) *Handler {
		t.Helper()
		store := newMemStore()
		store.clients[publicClient().ClientID] = publicClient()
		return testHandler(t, store, &memRefresh{}, u)
	}
	exchange := func(h *Handler, rt string) int {
		return postForm(h, "/oauth/token", url.Values{
			"grant_type": {"refresh_token"}, "client_id": {"claude-desktop"}, "refresh_token": {rt},
		}).Code
	}

	t.Run("matching owner rotates", func(t *testing.T) {
		u := sampleUser()
		if got := exchange(newFixture(t, u), mintRefresh(t, u.UserID, u.TenantID)); got != http.StatusOK {
			t.Fatalf("status = %d, want 200 — a valid refresh must rotate", got)
		}
	})

	t.Run("a tenant the user does not belong to is refused", func(t *testing.T) {
		u := sampleUser()
		other := uuid.New()
		if got := exchange(newFixture(t, u), mintRefresh(t, u.UserID, other)); got == http.StatusOK {
			t.Fatal("a refresh naming another tenant rotated; the access token would be scoped to it")
		}
	})

	t.Run("a disabled owner is refused", func(t *testing.T) {
		u := sampleUser()
		u.Disabled = true
		if got := exchange(newFixture(t, u), mintRefresh(t, u.UserID, u.TenantID)); got == http.StatusOK {
			t.Fatal("a disabled user rotated a refresh token")
		}
	})

	t.Run("an unknown owner is refused", func(t *testing.T) {
		u := sampleUser()
		if got := exchange(newFixture(t, u), mintRefresh(t, uuid.New(), u.TenantID)); got == http.StatusOK {
			t.Fatal("a refresh naming a user that does not exist rotated")
		}
	})
}
