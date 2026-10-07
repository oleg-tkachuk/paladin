package objecth

import (
	"context"
	"errors"
	"slices"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/capability"
)

type taintRepoStub struct {
	stored   []string
	setCalls int
	at       map[string][]string // "collection/key" → signals
}

func (s *taintRepoStub) SetTaint(_ context.Context, _, _ uuid.UUID, signals []string) ([]string, error) {
	s.setCalls++
	s.stored = signals
	return signals, nil
}

func (s *taintRepoStub) TaintAtPath(_ context.Context, _ uuid.UUID, collection, key string) ([]string, error) {
	return s.at[collection+"/"+key], nil
}

func newTaintHandler(policy cedar.Authorizer) (*TaintHandler, *taintRepoStub, uuid.UUID) {
	tenantID := uuid.New()
	objects := &lockObjectStub{obj: Object{ObjectID: uuid.New(), TenantID: tenantID, Collection: "docs", Key: "a.pdf"}}
	repo := &taintRepoStub{at: map[string][]string{"docs/a.pdf": {TaintPII}}}
	return NewTaintHandler(objects, repo, policy), repo, tenantID
}

func TestSetTaintNormalisesAndAsksItsOwnAction(t *testing.T) {
	policy := &actionRecorder{}
	h, repo, tenantID := newTaintHandler(policy)

	obj, err := h.SetTaint(lockCtx(tenantID), "docs", uuid.NewString(), []string{TaintSecrets, TaintPII, TaintSecrets})
	if err != nil {
		t.Fatalf("SetTaint: %v", err)
	}
	if want := []string{TaintPII, TaintSecrets}; !slices.Equal(repo.stored, want) || !slices.Equal(obj.Taint, want) {
		t.Errorf("stored %v, returned %v; want the deduplicated, sorted %v", repo.stored, obj.Taint, want)
	}
	if policy.last != cedar.ActionSetObjectTaint {
		t.Errorf("Cedar was asked about %q, want %q", policy.last, cedar.ActionSetObjectTaint)
	}

	// Clearing is an empty list, and goes through the same gate.
	if _, err := h.SetTaint(lockCtx(tenantID), "docs", uuid.NewString(), nil); err != nil || len(repo.stored) != 0 {
		t.Fatalf("clear: stored %v, err %v", repo.stored, err)
	}
}

func TestSetTaintRefusals(t *testing.T) {
	h, repo, tenantID := newTaintHandler(&actionRecorder{})
	if _, err := h.SetTaint(lockCtx(tenantID), "docs", uuid.NewString(), []string{"TAINT_SIGNAL_UNSPECIFIED"}); connect.CodeOf(err) != connect.CodeInvalidArgument || !errors.Is(err, ErrUnknownTaintSignal) {
		t.Errorf("unknown signal: err = %v, want InvalidArgument / ErrUnknownTaintSignal", err)
	}

	denied, _, tenantID2 := newTaintHandler(denyAll{})
	if _, err := denied.SetTaint(lockCtx(tenantID2), "docs", uuid.NewString(), []string{TaintPII}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("Cedar deny: err = %v, want PermissionDenied", err)
	}

	// A capability needs OpManage on the object to change its flags.
	reader := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{Ops: []capability.Op{capability.OpGet}}}
	if _, err := h.SetTaint(auth.WithCapability(lockCtx(tenantID), reader), "docs", uuid.NewString(), nil); !errors.Is(err, capability.ErrOpNotAllowed) {
		t.Errorf("capability without OpManage: err = %v, want ErrOpNotAllowed", err)
	}
	if repo.setCalls != 0 {
		t.Errorf("a refused request still wrote taint %d times", repo.setCalls)
	}
}

func TestTaintedLooksUpTheObjectTheURINames(t *testing.T) {
	h, _, tenantID := newTaintHandler(nil)
	ctx := context.Background()
	cases := map[string]bool{
		CapabilityObjectURI(tenantID, "docs", "a.pdf"):     true,
		CapabilityObjectURI(tenantID, "docs", "b.pdf"):     false,
		capabilityCollectionURI(tenantID, "docs"):          false, // a prefix names no object
		"object://not-a-uuid/docs/a.pdf":                   false,
		"something-else":                                   false,
		CapabilityObjectURI(tenantID, "docs", "dir/a.pdf"): false,
	}
	for uri, want := range cases {
		got, err := h.Tainted(ctx, uri)
		if err != nil || got != want {
			t.Errorf("Tainted(%q) = %v, %v; want %v", uri, got, err, want)
		}
	}
}

func TestParseCapabilityObjectURIKeepsSlashesInTheKey(t *testing.T) {
	tenantID := uuid.New()
	gotTenant, collection, key, ok := parseCapabilityObjectURI(CapabilityObjectURI(tenantID, "docs", "2026/q3/report.pdf"))
	if !ok || gotTenant != tenantID || collection != "docs" || key != "2026/q3/report.pdf" {
		t.Fatalf("parse = %v %q %q %v", gotTenant, collection, key, ok)
	}
}
