package app

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/migrations"
)

type fakeStat struct {
	acquired, max, idle int32
	waits               int64
}

func (s fakeStat) AcquiredConns() int32     { return s.acquired }
func (s fakeStat) MaxConns() int32          { return s.max }
func (s fakeStat) IdleConns() int32         { return s.idle }
func (s fakeStat) EmptyAcquireCount() int64 { return s.waits }

func TestPoolDetails(t *testing.T) {
	got := poolDetails(fakeStat{acquired: 3, max: 20, idle: 2, waits: 7})
	want := []health.Detail{
		{Name: detailConnections, Value: "3 of 20 in use"},
		{Name: detailIdle, Value: "2"},
		{Name: detailNoIdle, Value: "7 times since start"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("poolDetails = %v, want %v", got, want)
	}
}

// fakeVersions is a database at applied, or failing with err, under a build
// carrying migrations 1..wanted.
type fakeVersions struct {
	applied, wanted int64
	err             error
}

func (f fakeVersions) GetDBVersion(context.Context) (int64, error) { return f.applied, f.err }
func (f fakeVersions) ListSources() []*goose.Source {
	out := make([]*goose.Source, 0, f.wanted)
	for v := range f.wanted {
		out = append(out, &goose.Source{Version: v + 1})
	}
	return out
}

// A schema behind the build is the migrate Job not having run: the pod must
// not serve. Ahead is a rollout in progress, and fine.
func TestSchemaComponent(t *testing.T) {
	cases := []struct {
		name        string
		v           fakeVersions
		want        health.ComponentStatus
		wantMessage string
		details     []health.Detail
	}{
		{"current", fakeVersions{applied: 56, wanted: 56}, health.StatusHealthy, "",
			[]health.Detail{{Name: detailSchemaApplied, Value: "56"}, {Name: detailSchemaWanted, Value: "56"}}},
		{"ahead, mid-rollout", fakeVersions{applied: 57, wanted: 56}, health.StatusHealthy, "",
			[]health.Detail{{Name: detailSchemaApplied, Value: "57"}, {Name: detailSchemaWanted, Value: "56"}}},
		{"behind", fakeVersions{applied: 55, wanted: 56}, health.StatusUnhealthy, "schema at 55, this build needs 56",
			[]health.Detail{{Name: detailSchemaApplied, Value: "55"}, {Name: detailSchemaWanted, Value: "56"}}},
		{"unreadable", fakeVersions{wanted: 56, err: errors.New("permission denied")}, health.StatusUnhealthy, "permission denied",
			[]health.Detail{{Name: detailSchemaWanted, Value: "56"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := schemaCheck(tc.v)
			if !c.Critical {
				t.Error("the schema component is not critical")
			}
			h := &health.Handler{Ready: []health.Probe{c}}
			got := h.Snapshot(context.Background(), "api")
			comp := got.Components[0]
			if comp.Status != tc.want || !strings.Contains(comp.Message, tc.wantMessage) {
				t.Errorf("component = %+v, want %s with %q", comp, tc.want, tc.wantMessage)
			}
			if !reflect.DeepEqual(comp.Details, tc.details) {
				t.Errorf("details = %v, want %v", comp.Details, tc.details)
			}
		})
	}
}

// The version this build needs is its newest embedded migration, read by
// goose from the same files the migrate Job applies.
func TestSchemaWantedIsTheNewestEmbeddedMigration(t *testing.T) {
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var newest int64
	for _, e := range entries {
		v, err := goose.NumericComponent(e.Name())
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		newest = max(newest, v)
	}
	// goose wants a database handle; nothing here connects through it.
	cc, err := pgx.ParseConfig("postgres://localhost/none")
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDB(*cc), migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	c := schemaCheck(p)
	want := health.Detail{Name: detailSchemaWanted, Value: strconv.FormatInt(newest, 10)}
	if got := c.Describe(context.Background()); len(got) != 1 || got[0] != want {
		t.Errorf("details before a check = %v, want only %v", got, want)
	}
}
