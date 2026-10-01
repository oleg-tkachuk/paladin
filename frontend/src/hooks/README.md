# Data hooks — error contract

Every data hook in this directory exposes async functions that talk to the
admin/object Connect APIs. They follow **one** error convention so callers
never have to guess which channel an error arrives on.

## The rule

| Kind         | Examples                                                      | On failure                                                                                                                                       |
| ------------ | ------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Query**    | `fetch*`, `refresh`, `loadMore`                               | **State-only.** Calls `setError(msg)`, returns a safe sentinel (empty list / unchanged state). **Never throws.**                                 |
| **Mutation** | `create*`, `update*`, `delete*`, `restore*`, `purge*`, `set*` | **Throw-only.** Throws the original error; **does not** touch the shared `error` state. May toast itself, but the canonical signal is the throw. |

### Why split this way

- A query failure is a **render** concern: the page shows the `{ data,
loading, error }` triad. Wrapping a query in `try/catch` is redundant, and
  re-throwing forced every caller to handle two channels — some toasted, some
  swallowed, some relied on the throw. That inconsistency is what this
  contract removes.
- A mutation failure is an **imperative** concern: the caller `await`ed an
  action and must react (toast, keep the dialog open, …). It should not light
  up the list's error banner — a failed _create_ is not a failed _list_.

## Caller patterns

```ts
// QUERY — read state, never try/catch:
const { tenants, loading, error, fetchTenants } = useTenants();
useEffect(() => {
  void fetchTenants();
}, [fetchTenants]);
// render the failure, never the empty state, when the list did not load:
//   error ? <ListLoadError what="Tenants" reason={error} onRetry={fetchTenants} />
//         : <Table rows={tenants} />

// MUTATION — await in try/catch, surface via errorMessage():
import { errorMessage } from "@/hooks/errorContract";
try {
  await createTenant(slug);
  showNotification({ type: "success", title: "Tenant created" });
} catch (err) {
  showNotification({
    type: "error",
    title: "Create failed",
    message: errorMessage(err),
  });
}
```

`errorMessage(err, fallback?)` (in `errorContract.ts`) is the shared unwrap —
it pulls `ConnectError.rawMessage` (or `Error.message`) so callers don't
re-implement it.

Two gates keep the contract from drifting, both reading the source rather
than trusting review:

- `queryErrorContract.test.ts` — every call site that takes a list from one
  of these hooks also takes its `error`, or is listed with the reason its
  emptiness claims nothing.
- `src/lib/queryErrorsRead.test.ts` — every `useQuery` outside the hooks has
  its `error` read somewhere in the same file, so a failed read is rendered
  (`ListLoadError`) instead of falling through to "no items yet".

`errorContract.test.ts` pins `errorMessage`'s unwrap and fallback; the hooks'
own `*.test.tsx` files exercise the behaviour with `renderHook`.
