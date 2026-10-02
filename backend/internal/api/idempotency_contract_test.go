package api_test

// The guard behind `option idempotency_level = NO_SIDE_EFFECTS`.
//
// That option is a WIRE-level promise: buf classifies a change to it as
// breaking, and gRPC intermediaries read it to decide whether they may retry a
// call on their own. Declaring it on an RPC that writes is therefore not a
// documentation slip — it invites a proxy to duplicate the write.
//
// Which is a live risk here, not a theoretical one. The proto comment on
// TestBackend says "Read-only"; the handler persists the probe result via
// SetHealth. A classification built from names, or from the existing comments,
// gets that wrong — and an earlier draft of this work did, twice over.
//
// So the audit runs on every build instead of once. For every RPC the
// descriptors declare NO_SIDE_EFFECTS, this parses the handler and fails if it
// reaches a write — directly, or through a helper in the same package.
//
// What it does NOT prove: writes reached through an interface whose
// implementation lives elsewhere, or more than one hop away. It is a tripwire
// for the common shape, not a proof of purity. The annotations it guards were
// each read by hand first; this keeps them true afterwards.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// writeVerb matches a method name that persists something. Deliberately broad:
// a false positive costs one line in the exceptions list below and a moment's
// thought, while a false negative publishes a wrong wire contract.
var writeVerb = regexp.MustCompile(
	`^(Create|Insert|Update|Set|Delete|Touch|Record|Upsert|Mark|Purge|Revoke|Rotate|` +
		`Bind|Clear|Increment|Reset|Save|Put|Add|Remove|Dispatch|` +
		// Second batch, and the reason the first was not enough: a probe over the
		// 52 unannotated RPCs reported RenameTenantSlug, Delegate, Issue and
		// MigrateTenantStorageLayout as writing NOTHING. They call Rename,
		// Delegate, Mint and Migrate. Four handlers that plainly write, and the
		// detector said clean — a false negative is exactly the answer that gets
		// believed, because it agrees with the annotation you were hoping for.
		`Rename|Migrate|Initiate|Restore|Mint|Issue|Delegate|Store|Write|Enqueue|` +
		`Publish|Emit|Send|Apply|Attach|Detach|Grant|Assign|Enable|Disable|Abort|Cancel|` +
		// Third batch, found the same way and worth the same note: BatchUpdateTags
		// reaches Submit and ChargeRequest through a helper, MigrateTenantStorageLayout
		// calls StartStorageMigration, TestSubscription calls DeliverOne. The one-hop
		// walk reached all three — the list simply had no word for what they do.
		//
		// Three rounds of this is the honest measure of a name-based detector: it
		// finds what someone thought to name. That is why every annotation is read
		// by hand first and this only has to keep it true afterwards.
		`Start|Stop|Submit|Charge|Deliver|Commit|Begin|Flush|Import|Move|Upload|` +
		`Drain|Promote|Demote|Approve|Reject|Complete|Finish|Trigger|Schedule|Exec)` +
		// The verb must END there — at the end of the name or at the next
		// capital. Without this it is a bare prefix match, and `Dispatch` swallowed
		// `DispatcherStats`: an HTTP GET for the dispatcher's stats page, reported
		// as a write, which is how a genuine read stays undeclared for looking
		// guilty. `DispatchTx` still matches, because Tx is a new word.
		`([A-Z]|$)`)

// noSideEffectRPCs collects every method the descriptors declare NO_SIDE_EFFECTS.
func noSideEffectRPCs(t *testing.T) []string {
	t.Helper()
	var out []string
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(string(fd.Package()), "paladin.") {
			return true
		}
		svcs := fd.Services()
		for i := 0; i < svcs.Len(); i++ {
			ms := svcs.Get(i).Methods()
			for j := 0; j < ms.Len(); j++ {
				m := ms.Get(j)
				opts, _ := m.Options().(*descriptorpb.MethodOptions)
				if opts.GetIdempotencyLevel() == descriptorpb.MethodOptions_NO_SIDE_EFFECTS {
					out = append(out, string(m.Name()))
				}
			}
		}
		return true
	})
	return out
}

// ONE index across every layer, but a DISCIPLINED one about which call it
// follows.
//
// It used to be per-package, which encoded an assumption that turned out to be
// false: that an RPC is implemented by a handler method of the same name in one
// package. The connectshim layer breaks that twice over — QuotaService.GetQuota
// is a method on *QuotaServer that dispatches into H.GetTenantQuota, a
// different name in a different package — and fourteen read RPCs were
// consequently invisible to this file.
//
// The first attempt at the fix simply followed every declaration sharing a
// name, and that was too coarse to be worth having: `authorize` has nineteen
// bodies in this tree, so every RPC appeared to reach whatever any of the
// nineteen touched, and three declared reads were accused of writes they have
// no path to. Resolution is therefore: a call resolves inside the caller's own
// package, and reaches outside it only when the name is unique in the whole
// tree — which is the only case where a name identifies a body. An ambiguous
// name that is not local is not followed, and that limit is stated in the test
// output rather than hidden.
type decl struct {
	fn  *ast.FuncDecl
	dir string
}

type pkgIndex struct {
	rpc    map[string][]decl // methods on a *…Handler or *…Server
	byName map[string][]decl // every func, for resolving one call
}

func indexHandlers(t *testing.T) *pkgIndex {
	t.Helper()
	idx := &pkgIndex{rpc: map[string][]decl{}, byName: map[string][]decl{}}

	// EVERY non-test .go file under the handler trees, not `handler*.go`.
	//
	// That glob was a prefix match, so v1/object/lock_handler.go and
	// version_handler.go were never read at all — the widened receiver predicate
	// could not have saved them, because their file was not being opened. Two
	// ways to miss the same handler, each hiding the other.
	var files []string
	for _, pat := range []string{"v1/*/*.go", "*/v1/*/*.go", "connectshim/*/*.go"} {
		matched, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range matched {
			if !strings.HasSuffix(f, "_test.go") {
				files = append(files, f)
			}
		}
	}
	if len(files) == 0 {
		t.Fatal("indexed no handler files — the glob is wrong, and an empty index " +
			"would make every assertion below vacuous")
	}
	fset := token.NewFileSet()
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			e := decl{fn: fd, dir: filepath.Dir(path)}
			idx.byName[fd.Name.Name] = append(idx.byName[fd.Name.Name], e)
			if fd.Recv != nil && isHandlerRecv(fd.Recv) {
				idx.rpc[fd.Name.Name] = append(idx.rpc[fd.Name.Name], e)
			}
		}
	}
	return idx
}

// resolve returns the bodies a call to `name` from `dir` may mean.
//
// Local first: a declaration in the caller's own package is the call, full
// stop. Leaving another package is allowed only for a DISPATCH — `s.H.Method`,
// a field on the receiver — and only to a name that is unique in the tree.
//
// Both halves of that were learned by getting it wrong. Following every body
// sharing a name accused three declared reads of writes, because `authorize`
// has nineteen bodies here. Narrowing to unique names alone still accused two,
// because `connect.NewError`'s neighbour `errors.New` matched the single `New`
// declared in admin/v1/systemh — a name being unique among indexed packages
// says nothing about the hundreds of packages that are not indexed, and every
// `pkg.Func(...)` call looks exactly like a local one to an AST walk.
//
// `s.H.GetTenantQuota` does not look like that. Its receiver is itself a
// selector — a field reached through the handler — which is what dispatch into
// another layer of THIS tree looks like and what `errors.New` never does.
func (idx *pkgIndex) resolve(name, dir string, dispatch bool) []decl {
	var local []decl
	for _, d := range idx.byName[name] {
		if d.dir == dir {
			local = append(local, d)
		}
	}
	if len(local) > 0 {
		return local
	}
	if all := idx.byName[name]; dispatch && len(all) == 1 {
		return all
	}
	return nil
}

// A receiver whose type name ends in Handler or Server.
//
// Not `*Handler` alone: ObjectService.GetVersion hangs off *VersionHandler and
// QuotaService.GetQuota off *QuotaServer, and the narrow predicate skipped both
// in silence — the annotation would have been published with nothing checking
// it. That is the failure mode this whole file exists to prevent, reproduced
// inside the file itself.
func isHandlerRecv(fl *ast.FieldList) bool {
	if len(fl.List) == 0 {
		return false
	}
	star, ok := fl.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := star.X.(*ast.Ident)
	if !ok {
		return false
	}
	return strings.HasSuffix(id.Name, "Handler") || strings.HasSuffix(id.Name, "Server")
}

// writesIn reports the write-shaped calls reachable from d, following up to
// `depth` further calls that resolve unambiguously.
//
// Two hops rather than one, because connectshim added a layer: the Connect
// server is hop zero, the domain handler it dispatches into is hop one, and
// that handler's own helper is hop two. `seen` keeps mutual recursion from
// walking forever.
func writesIn(d decl, idx *pkgIndex, depth int, seen map[*ast.FuncDecl]bool) []string {
	if seen[d.fn] {
		return nil
	}
	seen[d.fn] = true
	var found []string
	follow := func(name string, dispatch bool) {
		if depth <= 0 {
			return
		}
		for _, next := range idx.resolve(name, d.dir, dispatch) {
			found = append(found, writesIn(next, idx, depth-1, seen)...)
		}
	}
	ast.Inspect(d.fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch f := call.Fun.(type) {
		case *ast.SelectorExpr:
			if writeVerb.MatchString(f.Sel.Name) {
				found = append(found, f.Sel.Name)
			}
			// `s.H.Method` — a field on the receiver — is dispatch into another
			// layer. `pkg.Func` is not, whatever the tree happens to contain.
			//
			// A name already excused as not-a-write is not followed either.
			// `req.Header.Set` wears the dispatch shape exactly, and `Set` is
			// unique among the indexed packages because TenantBudgetServer
			// declares one — so setting an HTTP header appeared to reach
			// SetTenantBudget. Having decided a name means nothing here, walking
			// into a body that happens to share it means less.
			_, dispatch := f.X.(*ast.SelectorExpr)
			if !notReallyWrites[f.Sel.Name] {
				follow(f.Sel.Name, dispatch)
			}
		case *ast.Ident:
			follow(f.Name, false)
		}
		return true
	})
	return found
}

// Some write-shaped names are not writes. Each entry is a decision, not a
// silencer — if one stops being true the RPC's annotation is wrong.
var notReallyWrites = map[string]bool{
	"SetHeader":   true, // connect response metadata
	"SetTrailer":  true,
	"Set":         true, // http.Header.Set and friends
	"Add":         true,
	"MarshalJSON": true,
	"PutTx":       false, // deliberately NOT excused: a real write
}

func TestNoSideEffectRPCsDoNotWrite(t *testing.T) {
	declared := noSideEffectRPCs(t)
	if len(declared) == 0 {
		t.Skip("no RPC declares NO_SIDE_EFFECTS yet — nothing to guard")
	}
	idx := indexHandlers(t)

	checked := 0
	var unmatched []string
	for _, name := range declared {
		decls := idx.rpc[name]
		if len(decls) == 0 {
			unmatched = append(unmatched, name)
			continue
		}
		for _, fn := range decls {
			checked++
			var bad []string
			for _, w := range writesIn(fn, idx, 2, map[*ast.FuncDecl]bool{}) {
				if notReallyWrites[w] {
					continue
				}
				bad = append(bad, w)
			}
			if len(bad) > 0 {
				t.Errorf("%s declares NO_SIDE_EFFECTS but its handler calls %v — "+
					"a proxy reading that option may retry it, duplicating the write",
					name, uniq(bad))
			}
		}
	}
	// An RPC whose handler this cannot find is UNGUARDED, and an unguarded
	// annotation is the thing being guarded against. Failing here rather than
	// passing quietly is the difference between a check and a decoration.
	if len(unmatched) > 0 {
		t.Errorf("declared NO_SIDE_EFFECTS but no handler found, so nothing "+
			"checked them: %v — either the index misses their receiver shape, or "+
			"they should not carry the annotation", uniq(unmatched))
	}
	// `checked` counts handler bodies, not RPCs: a method name that exists on
	// two services (GetOperation, List, Validate …) is verified on both, so
	// this can legitimately exceed len(declared).
	t.Logf("checked %d handler bodies for %d declared NO_SIDE_EFFECTS RPCs",
		checked, len(declared))
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ─── the IDEMPOTENT half ────────────────────────────────────────────────────

// NO_SIDE_EFFECTS is guarded above by reading the handler. IDEMPOTENT needs a
// different claim — "repeating this is safe" — and a different check, because
// nothing about a handler's shape proves it.
//
// What does prove it, for this API, is one of two structures:
//
//   - the request carries `resource_version`, so a repeat either writes the
//     same value or loses the OCC check and fails Aborted; or
//   - the RPC removes something, and removing what is already gone is a no-op.
//
// An RPC declared IDEMPOTENT with neither is either misannotated or relies on
// a third argument nobody wrote down. The exceptions map below is where that
// third argument goes, one line of reasoning each — not a silencer.
//
// This matters because IDEMPOTENT is a wire promise like the other: a proxy
// may retry on it. Declaring it on RotateCredentials — which an early draft of
// this work did — invites a retry that mints a second credential pair.
var idempotentByArgument = map[string]string{
	// name: why repeating is safe without OCC and without being a removal
	"EnsureTenantStorage":     "converges on a target state; the name is the contract",
	"BindCollectionToBucket":  "an upsert of one binding row keyed by (tenant, collection)",
	"SetTenantDefaultBinding": "upserts the single binding row for a tenant",
	"RegenerateUploadUrl":     "re-signs a URL for an EXISTING pending row; creates nothing",
	"PutObjectTags":           "a PUT replaces the whole tag set with the one supplied",
	"ResetUsage":              "zeroes a counter; zeroing twice lands on zero",

	// Read out of the SQL rather than inferred from the verb, because the verb
	// is what made RotateCredentials look safe.
	"SetObjectLegalHold": "ON CONFLICT DO UPDATE SET legal_hold = EXCLUDED.legal_hold — " +
		"an upsert of one flag to the value supplied",
	"SetObjectRetention": "the same upsert; a repeat of the same retain_until satisfies " +
		"the `EXCLUDED.retain_until >= ol.retain_until` guard by equality",
	"GrantScopes": "mergeScopes dedups by Scope.String(), so this is a set union — " +
		"granting a scope already held changes nothing",
	"ResetPassword": "sets the password to the one supplied. The bcrypt hash differs " +
		"per call because the salt does; WHICH PASSWORD WORKS does not, and that is " +
		"the observable state",
	"TestBackend": "probes the backend and overwrites the health row with the result. " +
		"The row holds the latest probe either way — and note this is exactly the RPC " +
		"whose proto comment says \"Read-only\" while it writes, which is why it is " +
		"here and not among the NO_SIDE_EFFECTS",
	"UpdateMine": "INSERT … ON CONFLICT (user_id) DO UPDATE of one settings row from " +
		"the masked request",
	"RedriveFailedDeliveries": "UPDATE … SET status = 'pending' WHERE status = 'failed': " +
		"a repeat finds none of the rows the first call moved, only ones that failed " +
		"since — which is what a redrive is for",
}

var removalVerb = regexp.MustCompile(`^(Delete|Clear|Abort|Cancel|Revoke|Purge|Remove)`)

func TestIdempotentRPCsAreActuallyRepeatable(t *testing.T) {
	var checked, declared int
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(string(fd.Package()), "paladin.") {
			return true
		}
		svcs := fd.Services()
		for i := 0; i < svcs.Len(); i++ {
			ms := svcs.Get(i).Methods()
			for j := 0; j < ms.Len(); j++ {
				m := ms.Get(j)
				opts, _ := m.Options().(*descriptorpb.MethodOptions)
				if opts.GetIdempotencyLevel() != descriptorpb.MethodOptions_IDEMPOTENT {
					continue
				}
				declared++
				name := string(m.Name())
				if removalVerb.MatchString(name) {
					checked++
					continue
				}
				if _, ok := idempotentByArgument[name]; ok {
					checked++
					continue
				}
				if hasField(m.Input(), "resource_version") {
					checked++
					continue
				}
				t.Errorf("%s.%s declares IDEMPOTENT but is neither a removal, nor "+
					"OCC-guarded (no resource_version on %s), nor listed in "+
					"idempotentByArgument with a reason — a proxy may retry it",
					svcs.Get(i).Name(), name, m.Input().Name())
			}
		}
		return true
	})
	if declared == 0 {
		t.Skip("no RPC declares IDEMPOTENT yet")
	}
	t.Logf("checked %d of %d declared IDEMPOTENT RPCs", checked, declared)
}

func hasField(md protoreflect.MessageDescriptor, name string) bool {
	fs := md.Fields()
	for i := 0; i < fs.Len(); i++ {
		if string(fs.Get(i).Name()) == name {
			return true
		}
	}
	return false
}

// ─── the census ─────────────────────────────────────────────────────────────

// What is still UNDECLARED, printed on every run.
//
// This started as a throwaway probe and earns its place because of what it
// found: fourteen read RPCs invisible to the guard, four handlers the write
// detector called clean while they call Rename, Delegate, Mint and Migrate, and
// two false accusations from an over-eager index. None of that was visible from
// the annotations themselves — the gap had to be enumerated to be seen.
//
// It asserts nothing about the count. An RPC left undeclared is allowed;
// IDEMPOTENCY_UNKNOWN is the honest answer for most writes. What it refuses to
// allow is the gap being INVISIBLE, which is how "not yet checked" quietly
// becomes "checked and fine". A `CLEAN` row here is a candidate to read by
// hand, never an annotation to apply on this test's say-so.
func TestUndeclaredRPCCensus(t *testing.T) {
	idx := indexHandlers(t)
	type row struct{ name, svc, state string }
	var rows []row
	var clean int
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(string(fd.Package()), "paladin.") {
			return true
		}
		svcs := fd.Services()
		for i := 0; i < svcs.Len(); i++ {
			ms := svcs.Get(i).Methods()
			for j := 0; j < ms.Len(); j++ {
				m := ms.Get(j)
				opts, _ := m.Options().(*descriptorpb.MethodOptions)
				if opts.GetIdempotencyLevel() != descriptorpb.MethodOptions_IDEMPOTENCY_UNKNOWN {
					continue
				}
				name := string(m.Name())
				decls := idx.rpc[name]
				state := "no handler indexed"
				if len(decls) > 0 {
					var ws []string
					for _, d := range decls {
						for _, w := range writesIn(d, idx, 2, map[*ast.FuncDecl]bool{}) {
							if !notReallyWrites[w] {
								ws = append(ws, w)
							}
						}
					}
					if len(ws) == 0 {
						state, clean = "CLEAN — read by hand before annotating", clean+1
					} else {
						state = "writes " + strings.Join(uniq(ws), ",")
					}
				}
				rows = append(rows, row{name, string(svcs.Get(i).Name()), state})
			}
		}
		return true
	})
	sort.Slice(rows, func(a, b int) bool { return rows[a].name < rows[b].name })
	for _, r := range rows {
		t.Logf("%-28s %-26s %s", r.name, r.svc, r.state)
	}
	t.Logf("%d RPCs undeclared, %d of them with no write the detector can see",
		len(rows), clean)
}
