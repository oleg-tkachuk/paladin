//go:build integration

// The folders list_object_prefixes answers, held to a reference computed in
// Go over the same keys: whatever plan the function takes, it must name every
// folder under the prefix once, bytewise, cut at the limit.
package components

import (
	"context"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// walkAlphabet is what the random keys are made of: the delimiter, LIKE's
// wildcards and escape, characters either side of the delimiter in byte
// order, multi-byte ones, and the last code point before the surrogates and
// the very last, whose successors are the edge cases of the walk.
var walkAlphabet = []string{"/", "a", "b", "_", "%", `\`, ".", "0", "é", "€", "😀", "퟿", "\U0010FFFF"}

// The shape of the random collection.
const (
	walkKeys       = 400
	walkMaxRunes   = 7
	walkSeed       = 20261102
	walkCollection = "walk"
)

// The object states the walk mixes, and the trashed one live listings skip.
const (
	stateAvailable = "AVAILABLE"
	statePending   = "PENDING"
	stateDeleted   = "DELETED"
)

type walkObject struct{ key, state string }

// referenceFolders is the folders one level under prefix, as 089 defines
// them: prefix + the first segment after it + the delimiter, for each key
// under prefix with a delimiter after it, in byte order, at most limit.
func referenceFolders(objs []walkObject, prefix, delim string, state *string, liveOnly bool, limit int) []string {
	seen := map[string]bool{}
	for _, o := range objs {
		if !strings.HasPrefix(o.key, prefix) || (state != nil && o.state != *state) || (liveOnly && o.state == stateDeleted) {
			continue
		}
		seg, _, found := strings.Cut(o.key[len(prefix):], delim)
		if found {
			seen[prefix+seg+delim] = true
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	slices.Sort(out) // Go compares strings bytewise, as COLLATE "C" does
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func TestListObjectPrefixes_MatchesReference(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	other, _ := mkTenant(t, ctx, admin, "shared")
	seedBucketRow(t, ctx, admin)
	seedCollectionFor(t, ctx, admin, tenant, walkCollection)
	seedCollectionFor(t, ctx, admin, other, walkCollection)

	rng := rand.New(rand.NewPCG(walkSeed, walkSeed))
	states := []string{stateAvailable, stateAvailable, statePending, stateDeleted}
	byKey := map[string]walkObject{}
	for len(byKey) < walkKeys {
		var b strings.Builder
		for range 1 + rng.IntN(walkMaxRunes) {
			b.WriteString(walkAlphabet[rng.IntN(len(walkAlphabet))])
		}
		byKey[b.String()] = walkObject{key: b.String(), state: states[rng.IntN(len(states))]}
	}
	objs := make([]walkObject, 0, len(byKey))
	for _, o := range byKey {
		objs = append(objs, o)
		mustExec(t, ctx, admin, `
			INSERT INTO objects (tenant_id, collection_id, path, state, content_type)
			SELECT $1, c.id, $2, $3::object_state, 'text/plain'
			  FROM collections c WHERE c.tenant_id = $1 AND c.name = $4`, tenant, o.key, o.state, walkCollection)
		// The same keys under another tenant, live, so a leak shows as an
		// extra folder.
		mustExec(t, ctx, admin, `
			INSERT INTO objects (tenant_id, collection_id, path, state, content_type)
			SELECT $1, c.id, $2 || 'leak/', 'AVAILABLE', 'text/plain'
			  FROM collections c WHERE c.tenant_id = $1 AND c.name = $3`, other, o.key, walkCollection)
	}

	pool := rlsPool(t, ctx, admin)
	as := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenant})
	// Every prefix a key begins with, so the walk starts inside, between and
	// past folders, and the prefixes that end on an edge-case character.
	prefixes := map[string]bool{"": true}
	for _, o := range objs {
		runes := []rune(o.key)
		for i := range runes {
			prefixes[string(runes[:i])] = true
		}
	}
	available, deleted := stateAvailable, stateDeleted
	for _, tc := range []struct {
		name     string
		delim    string
		state    *string
		liveOnly bool
		limit    int
	}{
		{"live", "/", nil, true, walkKeys},
		{"everything", "/", nil, false, walkKeys},
		{"available", "/", &available, false, walkKeys},
		{"trash", "/", &deleted, false, walkKeys},
		{"cut at two", "/", nil, true, 2},
		{"on a multi-byte delimiter", "€", nil, true, walkKeys},
		{"on the last code point", "\U0010FFFF", nil, false, walkKeys},
		{"on the one before the surrogates", "퟿", nil, false, walkKeys},
	} {
		for prefix := range prefixes {
			want := referenceFolders(objs, prefix, tc.delim, tc.state, tc.liveOnly, tc.limit)
			got := callPrefixes(t, as, pool, tenant, prefix, tc.delim, tc.state, tc.liveOnly, tc.limit)
			if !slices.Equal(got, want) {
				t.Fatalf("%s under %q: got %q, want %q", tc.name, prefix, got, want)
			}
		}
	}
}

func callPrefixes(t *testing.T, ctx context.Context, p *pgxpool.Pool, tenant uuid.UUID,
	prefix, delim string, state *string, liveOnly bool, limit int) []string {
	t.Helper()
	rows, err := p.Query(ctx, `SELECT list_object_prefixes($1, $2, $3, $4, $5, $6, $7)`,
		tenant, walkCollection, prefix, delim, state, liveOnly, limit)
	if err != nil {
		t.Fatalf("list_object_prefixes(%q): %v", prefix, err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list_object_prefixes(%q): %v", prefix, err)
	}
	return out
}

// paladin_key_successor is the least key greater than every key beginning
// with its argument, or NULL when no key is.
func TestKeySuccessor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	for _, tc := range []struct {
		in   string
		want *string
	}{
		{"a/", ptr("a0")},
		{"a", ptr("b")},
		{"é", ptr("ê")},
		{"x퟿", ptr("x")},
		{"x\U0010FFFF", ptr("y")},
		{"x\U0010FFFF\U0010FFFF", ptr("y")},
		{"\U0010FFFF", nil},
		{"", nil},
	} {
		var got *string
		if err := admin.QueryRow(ctx, `SELECT paladin_key_successor($1)`, tc.in).Scan(&got); err != nil {
			t.Fatalf("paladin_key_successor(%q): %v", tc.in, err)
		}
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("paladin_key_successor(%q) = %q, want %q", tc.in, deref(got), deref(tc.want))
		}
	}
}
