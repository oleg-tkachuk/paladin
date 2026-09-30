package backendh

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/apiutil"
)

// ListBackends, UpdateBackend and DeleteBackend were three of the thirteen
// handlers at 0.0% across unit and both integration suites (BACKLOG: "Half the
// admin API's RPCs have no behavioural test").
//
// The list path is the interesting one. GetBackend's per-role credential
// redaction has a test; List does the same redaction over a page, on TWO
// fields rather than one, and had none — so the secret that GetBackend is
// careful about could have walked out through the other door without any test
// disagreeing.

// pageRepo returns a two-row page with real credential refs, one of which is
// mid-rotation and therefore carries the previous ref as well.
type pageRepo struct {
	fakeBackendRepo
	updated    *admindomain.StorageBackend
	updateMask []string
	updateVer  int64
	updateErr  error
	deletedID  string
	deletedVer int64
	deleteErr  error
	listErr    error
}

func (r *pageRepo) List(context.Context, int32, string, string) ([]admindomain.StorageBackend, string, error) {
	if r.listErr != nil {
		return nil, "", r.listErr
	}
	return []admindomain.StorageBackend{
		{
			BackendID:            "primary",
			Kind:                 "s3-compatible",
			CredentialsSecretRef: "vault://kv/paladin/primary",
		},
		{
			BackendID:                    "secondary",
			Kind:                         "s3-compatible",
			CredentialsSecretRef:         "vault://kv/paladin/secondary",
			PreviousCredentialsSecretRef: "vault://kv/paladin/secondary-old",
		},
	}, "next-cursor", nil
}

func (r *pageRepo) Update(_ context.Context, b admindomain.StorageBackend, ver int64, mask []string) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	cp := b
	r.updated, r.updateVer, r.updateMask = &cp, ver, mask
	return nil
}

func (r *pageRepo) Delete(_ context.Context, id string, ver int64) error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	r.deletedID, r.deletedVer = id, ver
	return nil
}

func TestListBackendsRedactsBothCredentialRefsBelowPlatformAdmin(t *testing.T) {
	h := NewHandler(&pageRepo{}, allowAuthorizer{})

	out, next, err := h.ListBackends(ctxWithRoles("tenant.admin"), 50, "", "")
	if err != nil {
		t.Fatalf("ListBackends: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d rows, want 2", len(out))
	}
	for _, b := range out {
		if b.CredentialsSecretRef != "[REDACTED]" {
			t.Errorf("%s: CredentialsSecretRef = %q, want redacted", b.BackendID, b.CredentialsSecretRef)
		}
	}
	// The rotation field is the one a list-shaped redaction is most likely to
	// forget: it is empty on most rows, so a bug here hides until a backend is
	// mid-rotation — exactly when the old credential is still live.
	if out[1].PreviousCredentialsSecretRef != "[REDACTED]" {
		t.Errorf("PreviousCredentialsSecretRef = %q, want redacted — the superseded credential is still a credential",
			out[1].PreviousCredentialsSecretRef)
	}
	if next != "next-cursor" {
		t.Errorf("next = %q, want the repo's cursor carried through", next)
	}
}

func TestListBackendsUnredactedForPlatformAdmin(t *testing.T) {
	h := NewHandler(&pageRepo{}, allowAuthorizer{})

	out, _, err := h.ListBackends(ctxWithRoles("platform.admin"), 50, "", "")
	if err != nil {
		t.Fatalf("ListBackends: %v", err)
	}
	if out[0].CredentialsSecretRef != "vault://kv/paladin/primary" {
		t.Errorf("platform.admin saw %q, want the real ref", out[0].CredentialsSecretRef)
	}
	if out[1].PreviousCredentialsSecretRef != "vault://kv/paladin/secondary-old" {
		t.Errorf("platform.admin saw %q for the previous ref, want the real one", out[1].PreviousCredentialsSecretRef)
	}
}

func TestListBackendsRoleGuard(t *testing.T) {
	h := NewHandler(&pageRepo{}, allowAuthorizer{})

	// Read access is wider than write: three roles may list.
	for _, role := range []string{"platform.admin", "bucket.admin", "tenant.admin"} {
		if _, _, err := h.ListBackends(ctxWithRoles(role), 50, "", ""); err != nil {
			t.Errorf("%s was refused the list: %v", role, err)
		}
	}
	if _, _, err := h.ListBackends(ctxWithRoles("agent"), 50, "", ""); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("an unlisted role: code = %v, want PermissionDenied", connect.CodeOf(err))
	}
}

func TestListBackendsRejectsAnUncompilableFilter(t *testing.T) {
	h := NewHandler(&pageRepo{}, allowAuthorizer{})

	_, _, err := h.ListBackends(ctxWithRoles("platform.admin"), 50, "", "not a CEL expression")
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

func TestUpdateBackendPassesVersionAndMaskThrough(t *testing.T) {
	repo := &pageRepo{}
	h := NewHandler(repo, allowAuthorizer{})

	got, err := h.UpdateBackend(ctxWithRoles("platform.admin"),
		admindomain.StorageBackend{BackendID: "primary", Region: "eu-central-1"},
		7, []string{"region"})
	if err != nil {
		t.Fatalf("UpdateBackend: %v", err)
	}
	// The OCC guard and the mask are the whole contract of this call: losing
	// either turns a narrow, guarded edit into a blind overwrite.
	if repo.updateVer != 7 {
		t.Errorf("expected_version reached the repo as %d, want 7", repo.updateVer)
	}
	if len(repo.updateMask) != 1 || repo.updateMask[0] != "region" {
		t.Errorf("mask = %v, want [region]", repo.updateMask)
	}
	// The response is re-read rather than echoed, so it carries server-side
	// fields the caller did not send.
	if got.Kind != "s3-compatible" {
		t.Errorf("Kind = %q — the response was echoed back instead of re-read", got.Kind)
	}
}

func TestUpdateBackendRequiresPlatformAdmin(t *testing.T) {
	h := NewHandler(&pageRepo{}, allowAuthorizer{})

	// Wider than list on purpose: tenant.admin may see backends, not edit them.
	_, err := h.UpdateBackend(ctxWithRoles("tenant.admin"),
		admindomain.StorageBackend{BackendID: "primary"}, 1, []string{"region"})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", connect.CodeOf(err))
	}
}

func TestUpdateBackendMapsAVersionConflict(t *testing.T) {
	h := NewHandler(&pageRepo{updateErr: apiutil.ErrConflict}, allowAuthorizer{})

	_, err := h.UpdateBackend(ctxWithRoles("platform.admin"),
		admindomain.StorageBackend{BackendID: "primary"}, 1, []string{"region"})
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("code = %v, want Aborted — a stale version is a retryable conflict, not Internal", connect.CodeOf(err))
	}
}

func TestDeleteBackendPassesTheVersionThrough(t *testing.T) {
	repo := &pageRepo{}
	h := NewHandler(repo, allowAuthorizer{})

	if err := h.DeleteBackend(ctxWithRoles("platform.admin"), "primary", 4); err != nil {
		t.Fatalf("DeleteBackend: %v", err)
	}
	if repo.deletedID != "primary" || repo.deletedVer != 4 {
		t.Errorf("repo saw (%q, %d), want (primary, 4)", repo.deletedID, repo.deletedVer)
	}
}

func TestDeleteBackendRequiresPlatformAdmin(t *testing.T) {
	h := NewHandler(&pageRepo{}, allowAuthorizer{})

	err := h.DeleteBackend(ctxWithRoles("bucket.admin"), "primary", 1)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", connect.CodeOf(err))
	}
}

func TestDeleteBackendMapsRepoSentinels(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want connect.Code
	}{
		{"absent backend", apiutil.ErrNotFound, connect.CodeNotFound},
		{"stale version", apiutil.ErrConflict, connect.CodeAborted},
		// Buckets still reference it: the database refuses, and the caller
		// needs to hear a precondition rather than an internal error.
		{"still referenced", apiutil.ErrFailedPrecondition, connect.CodeFailedPrecondition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandler(&pageRepo{deleteErr: tc.err}, allowAuthorizer{})
			err := h.DeleteBackend(ctxWithRoles("platform.admin"), "primary", 1)
			if connect.CodeOf(err) != tc.want {
				t.Fatalf("code = %v, want %v", connect.CodeOf(err), tc.want)
			}
		})
	}
}
