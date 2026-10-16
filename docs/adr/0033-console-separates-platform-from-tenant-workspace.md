# ADR-0033: The console separates the platform from a tenant's workspace

- **Status:** Proposed
- **Related:** [ADR-0014](0014-canonical-resource-names.md) (canonical
  resource names — this gives each one a single console URL),
  [ADR-0015](0015-per-tenant-bucket-layout.md) (shared and dedicated
  layouts), [ADR-0022](0022-data-plane-acts-on-the-named-tenant.md) (the
  tenant a platform admin acts on), [ADR-0032](0032-every-bucket-has-a-tenant-owner.md)
  (bucket owners and grants).

- **Context.** The console mixes two hierarchies and never says which one a
  page belongs to:
  - the **platform's infrastructure** — storage backend → bucket;
  - a **tenant's workspace** — tenant → collection → object.

  They meet twice: a bucket has an owner and grantees, and a collection is
  bound to a bucket. What the operator sees as a result:
  - **One bucket, two pages.** `/storage-backends/{b}/buckets/{id}` shows the
    owner by name, the grants and what is stored; `/tenants/{t}/buckets/{b}/{id}`
    shows the owner as a raw UUID and every setting (lifecycle, policy,
    versioning, object lock, replication). The settings of a bucket the
    platform shares with several tenants are edited inside one tenant's
    workspace, with nothing saying the change reaches the others.
  - **The context is implicit.** The header's tenant switcher names the
    session's tenant, unlabelled, while the page shows another tenant.
    Opening a bucket from `/buckets` lands under `/storage-backends`, and the
    sidebar's highlight moves with the URL prefix. The sidebar's *Agents*
    group links into the session tenant's workspace under names that read as
    platform pages, and the cross-tenant collections index is not in it.
  - **Navigation repeats and breaks.** Backend and bucket pages carry a second
    breadcrumb row and a "back" button beside the trail; the trail's
    `…/buckets` crumb is a 404; the last crumb title-cases an id the heading
    spells as it is.
  - **The lists omit what ADR-0032 made central.** Bucket lists have no owner
    and no grantees, and show columns that are empty on every row. The
    tenants list showed a default bucket for the caller's own tenant only:
    the binding sits under RLS and was joined in the caller's scope.
  - **One thing has several names**: a collection is also an "object key" and
    a "folder"; the default binding is a "Default Route"; labels are shown
    upper-cased although they are case-sensitive.

- **Decision.**
  1. **Two spaces.** The *platform* holds the infrastructure and the
     catalogues: storage backends, buckets, tenants, users, policies, audit.
     A *tenant workspace* (`/tenants/{t}/…`) holds what one tenant owns:
     collections and objects, its access (policies, capabilities, machine
     tokens), its activity (events, audit) and its limits (quotas, budget).
     A bucket is platform infrastructure even when one tenant owns it.
  2. **One URL per entity**, built from its resource name:
     - backend — `/storage-backends/{b}`;
     - bucket — `/storage-backends/{b}/buckets/{id}`, with its settings as
       tabs of that page (`/lifecycle`, `/policy`, `/replication`,
       `/versioning`, `/object-lock`, `/collections`);
     - tenant — `/tenants/{t}`; collection — `/tenants/{t}/collections/{c}`.

     The tenant-scoped bucket route redirects to the bucket's URL, so links
     already shared keep working. Every crumb of every trail is a page.
  3. **A tenant sees its storage, not a copy of the bucket page.** The
     workspace's *Storage* tab lists the tenant's default bucket and the
     buckets it may bind to, marked *owned* or *granted*, each linking to the
     bucket's one page.
  4. **The scope is always on screen.** Every page header carries a scope
     badge: *Platform*, or *Tenant · {name}*. The header's switcher is
     labelled as what it is — the tenant the session is signed in to. A
     setting of a bucket granted to other tenants says, at the control, how
     many tenants it applies to.
  5. **One navigation system.** One breadcrumb trail from the URL, no second
     row and no "back" buttons. The sidebar highlights the section that owns
     the page — a bucket highlights *Buckets* — decided by one mapping, not by
     URL prefix. The sidebar has a *Platform* part — backends, buckets,
     collections, tenants and the rest of the catalogues — and a part headed
     by the session tenant's name for the shortcuts into its workspace. The
     workspace's tabs are grouped: Overview, Collections,
     Storage, Access, Activity, Limits; a group with several pages shows them
     as a second row.
  6. **Lists show what the decisions rest on.** Bucket lists — the platform's,
     a backend's and a tenant's — are one component with the columns bucket,
     owner, shared with and state (a public bucket marked in it); the backend column appears only when
     there is more than one backend, and a column empty on every row is not
     shown. The tenants list shows each tenant's default bucket. To serve
     this, `Bucket` carries `granted_tenant_ids`, and tenant reads act as the
     tenant read, so its default binding is no longer dropped by RLS.
  7. **One vocabulary**, held by a test over the console's strings:

     | Was | Is |
     |---|---|
     | object keys, folder | Collection |
     | Default Route | Default bucket |
     | S3 Buckets | Buckets |
     | `APP=NAME` | `app=name`, as stored |
     | owner as a UUID | owner by name, the UUID on hover and copy |

- **Consequences.**
  - The console's routes change: the tenant-scoped bucket pages go, and the
    end-to-end suite moves with them. Old links redirect.
  - A bucket's settings are edited in one place, under the platform, where
    the reach of a change over every grantee is stated.
  - The trail, the highlight and the scope badge are derived from the URL by
    pure functions, which unit tests pin, so a new page cannot drift from
    them unnoticed.
