package eventsubh

import (
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/sinkkind"
)

// rabbitOff is a deployment with RabbitMQ switched off and the rest on.
var rabbitOff = config.DispatcherSinks{
	HTTP: config.SinkSwitch{Enabled: true},
	NATS: config.SinkSwitch{Enabled: true},
}

func withSinks(repo *fakeRepo) *Handler {
	h := NewHandler(repo, allowAuthorizer{})
	h.SetSinkKinds(rabbitOff)
	return h
}

func wantOffKind(t *testing.T, err error) {
	t.Helper()
	if code(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), config.SinkSwitchKey(sinkkind.RabbitMQ)) {
		t.Fatalf("err = %v, want failed_precondition naming %s", err, config.SinkSwitchKey(sinkkind.RabbitMQ))
	}
}

// A subscription to a kind switched off would fail every delivery; the
// admin API refuses it rather than queue events nothing delivers.
func TestCreate_RefusesAnEnabledSubscriptionOfAnOffKind(t *testing.T) {
	tenant := uuid.New()
	repo := &fakeRepo{}
	_, err := withSinks(repo).Create(ctxAs(tenant, apiutil.RoleTenantAdmin),
		admindomain.EventSubscription{TenantID: tenant, SinkKind: sinkkind.RabbitMQ})
	wantOffKind(t, err)
	if repo.sub.SinkKind != "" {
		t.Error("the refused subscription was stored")
	}
}

func TestCreate_AcceptsAnOnKindOrADisabledSubscription(t *testing.T) {
	tenant := uuid.New()
	for _, s := range []admindomain.EventSubscription{
		{TenantID: tenant, SinkKind: sinkkind.NATS},
		// Delivers nothing, so it may name a kind that is off.
		{TenantID: tenant, SinkKind: sinkkind.RabbitMQ, Disabled: true},
	} {
		if _, err := withSinks(&fakeRepo{}).Create(ctxAs(tenant, apiutil.RoleTenantAdmin), s); err != nil {
			t.Errorf("Create(%s, disabled %v) = %v", s.SinkKind, s.Disabled, err)
		}
	}
}

// An update is judged by the subscription it leaves behind.
func TestUpdate_JudgesTheSubscriptionItLeaves(t *testing.T) {
	tenant := uuid.New()
	parked := admindomain.EventSubscription{TenantID: tenant, SinkKind: sinkkind.RabbitMQ, Disabled: true}
	onNATS := admindomain.EventSubscription{TenantID: tenant, SinkKind: sinkkind.NATS}
	cases := []struct {
		name    string
		stored  admindomain.EventSubscription
		update  admindomain.EventSubscription
		mask    []string
		refused bool
	}{
		{"enabling a parked off-kind subscription", parked,
			admindomain.EventSubscription{}, []string{admindomain.EventSubscriptionPathDisabled}, true},
		{"moving an enabled subscription to an off kind", onNATS,
			admindomain.EventSubscription{SinkKind: sinkkind.RabbitMQ}, []string{admindomain.EventSubscriptionPathSink}, true},
		{"replacing it whole with an enabled off-kind one", onNATS,
			admindomain.EventSubscription{SinkKind: sinkkind.RabbitMQ}, nil, true},
		{"editing the filter of a parked off-kind subscription", parked,
			admindomain.EventSubscription{CELFilter: "true"}, []string{admindomain.EventSubscriptionPathFilter}, false},
		{"moving a parked subscription to an on kind and enabling it", parked,
			admindomain.EventSubscription{SinkKind: sinkkind.NATS}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepo{sub: tc.stored}
			_, err := withSinks(repo).Update(ctxAs(tenant, apiutil.RoleTenantAdmin), tenant, tc.update, 1, tc.mask)
			if tc.refused {
				wantOffKind(t, err)
				if repo.updateCalls != 0 {
					t.Error("the refused update reached the store")
				}
				return
			}
			if err != nil {
				t.Fatalf("Update = %v", err)
			}
		})
	}
}

// A test delivery to an off kind is refused before the dispatcher is asked.
func TestTestSubscription_RefusesAnOffKind(t *testing.T) {
	tenant := uuid.New()
	h := withSinks(&fakeRepo{sub: admindomain.EventSubscription{TenantID: tenant, SinkKind: sinkkind.RabbitMQ, Disabled: true}})
	h.SetDispatcher(failDispatcher{})
	wantOffKind(t, h.TestSubscription(ctxAs(tenant, apiutil.RoleTenantAdmin), tenant, uuid.New()))
}
