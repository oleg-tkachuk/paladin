package paladin

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// Resource-name segments, as the server spells them.
const (
	tenantsSegment     = "tenants"
	collectionsSegment = "collections"
	keysSegment        = "keys"
	tenantsPrefix      = tenantsSegment + "/"
	collectionsSep     = "/" + collectionsSegment + "/"
	objectsSep         = "/objects/"
	versionsSep        = "/versions/"
	keysSep            = "/" + keysSegment + "/"
	// URIScheme is the scheme of an object URI: paladin://tenants/…
	URIScheme = "paladin"
	uriPrefix = URIScheme + "://"
)

// ErrInvalidName is a resource name or object URI the server would refuse.
var ErrInvalidName = errors.New("paladin: invalid resource name")

func invalid(kind, s, why string) error {
	return fmt.Errorf("%w: %s %q: %s", ErrInvalidName, kind, s, why)
}

// canonicalUUID parses a UUID as the server does and returns its canonical,
// lower-case form.
func canonicalUUID(s string) (string, bool) {
	id, err := uuid.Parse(s)
	if err != nil {
		return "", false
	}
	return id.String(), true
}

// TenantName is tenants/{tenant}: a tenant's id or its slug.
type TenantName struct {
	Tenant string
}

func (n TenantName) String() string { return tenantsPrefix + n.Tenant }

// ParseTenantName parses tenants/{tenant}.
func ParseTenantName(s string) (TenantName, error) {
	rest, ok := strings.CutPrefix(s, tenantsPrefix)
	if !ok || rest == "" || strings.Contains(rest, "/") {
		return TenantName{}, invalid("tenant", s, "want tenants/{id or slug}")
	}
	return TenantName{Tenant: rest}, nil
}

// CollectionName is tenants/{tenant}/collections/{collection}. Under a
// tenant the server takes the tenant's id, a UUID, not its slug; the
// collection may contain '/'.
type CollectionName struct {
	Tenant     string
	Collection string
}

func (n CollectionName) String() string {
	return tenantsPrefix + n.Tenant + collectionsSep + n.Collection
}

// ParseCollectionName parses tenants/{tenant-id}/collections/{collection}.
func ParseCollectionName(s string) (CollectionName, error) {
	rest, ok := strings.CutPrefix(s, tenantsPrefix)
	if !ok {
		return CollectionName{}, invalid("collection", s, "want tenants/{tenant-id}/collections/{collection}")
	}
	tenant, collection, found := strings.Cut(rest, collectionsSep)
	if !found || collection == "" {
		return CollectionName{}, invalid("collection", s, "want tenants/{tenant-id}/collections/{collection}")
	}
	id, ok := canonicalUUID(tenant)
	if !ok {
		return CollectionName{}, invalid("collection", s, "the tenant must be its id, a UUID")
	}
	return CollectionName{Tenant: id, Collection: collection}, nil
}

// ObjectName is tenants/{tenant}/collections/{collection}/objects/{object},
// the object being its id, a UUID.
type ObjectName struct {
	CollectionName
	Object string
}

func (n ObjectName) String() string { return n.CollectionName.String() + objectsSep + n.Object }

// ParseObjectName parses an object name. The object id is the segment after
// the last /objects/, so a collection containing /objects/ still parses.
func ParseObjectName(s string) (ObjectName, error) {
	i := strings.LastIndex(s, objectsSep)
	if i < 0 {
		return ObjectName{}, invalid("object", s, "want …/collections/{collection}/objects/{object-id}")
	}
	collection, err := ParseCollectionName(s[:i])
	if err != nil {
		return ObjectName{}, invalid("object", s, "its collection: "+err.Error())
	}
	id, ok := canonicalUUID(s[i+len(objectsSep):])
	if !ok {
		return ObjectName{}, invalid("object", s, "the object must be its id, a UUID")
	}
	return ObjectName{CollectionName: collection, Object: id}, nil
}

// ObjectVersionName is an object name followed by /versions/{version}, a UUID.
type ObjectVersionName struct {
	ObjectName
	Version string
}

func (n ObjectVersionName) String() string { return n.ObjectName.String() + versionsSep + n.Version }

// ParseObjectVersionName parses an object version name.
func ParseObjectVersionName(s string) (ObjectVersionName, error) {
	i := strings.LastIndex(s, versionsSep)
	if i < 0 {
		return ObjectVersionName{}, invalid("object version", s, "want …/objects/{object-id}/versions/{version-id}")
	}
	object, err := ParseObjectName(s[:i])
	if err != nil {
		return ObjectVersionName{}, invalid("object version", s, "its object: "+err.Error())
	}
	id, ok := canonicalUUID(s[i+len(versionsSep):])
	if !ok {
		return ObjectVersionName{}, invalid("object version", s, "the version must be its id, a UUID")
	}
	return ObjectVersionName{ObjectName: object, Version: id}, nil
}

// ObjectURI addresses an object by its key, where a resource name addresses
// it by id: paladin://tenants/{tenant-id}/collections/{collection}/keys/{key}.
// The collection and the key are each one path segment, escaped, so either
// may contain '/'. It is the resource-name space of the paladin:// resources
// the MCP bridge serves, extended to objects.
type ObjectURI struct {
	Collection CollectionName
	Key        string
}

// Parent is the collection's name, the parent LookupObject takes.
func (u ObjectURI) Parent() string { return u.Collection.String() }

func (u ObjectURI) String() string {
	return uriPrefix + tenantsPrefix + u.Collection.Tenant + collectionsSep +
		url.PathEscape(u.Collection.Collection) + keysSep + url.PathEscape(u.Key)
}

// ParseObjectURI parses a paladin:// object URI.
func ParseObjectURI(s string) (ObjectURI, error) {
	const want = "want paladin://tenants/{tenant-id}/collections/{collection}/keys/{key}"
	rest, ok := strings.CutPrefix(s, uriPrefix+tenantsPrefix)
	if !ok {
		return ObjectURI{}, invalid("object URI", s, want)
	}
	segments := strings.Split(rest, "/")
	// {tenant} collections {collection} keys {key}
	const tenantAt, collectionsAt, collectionAt, keysAt, keyAt, count = 0, 1, 2, 3, 4, 5
	if len(segments) != count || segments[collectionsAt] != collectionsSegment || segments[keysAt] != keysSegment {
		return ObjectURI{}, invalid("object URI", s, want)
	}
	tenant, ok := canonicalUUID(segments[tenantAt])
	if !ok {
		return ObjectURI{}, invalid("object URI", s, "the tenant must be its id, a UUID")
	}
	collection, cerr := url.PathUnescape(segments[collectionAt])
	key, kerr := url.PathUnescape(segments[keyAt])
	if cerr != nil || kerr != nil || collection == "" || key == "" {
		return ObjectURI{}, invalid("object URI", s, want)
	}
	return ObjectURI{Collection: CollectionName{Tenant: tenant, Collection: collection}, Key: key}, nil
}

// Admin-plane name segments: a bucket lives under its storage backend.
const (
	storageBackendsPrefix = "storageBackends/"
	bucketsSep            = "/buckets/"
)

// BucketName is storageBackends/{backend}/buckets/{bucket}, the name the admin
// plane gives a physical bucket and a bucket quota's parent.
type BucketName struct {
	Backend string
	Bucket  string
}

func (n BucketName) String() string { return storageBackendsPrefix + n.Backend + bucketsSep + n.Bucket }

// ParseBucketName parses storageBackends/{backend}/buckets/{bucket}.
func ParseBucketName(s string) (BucketName, error) {
	const want = "want storageBackends/{backend}/buckets/{bucket}"
	rest, ok := strings.CutPrefix(s, storageBackendsPrefix)
	if !ok {
		return BucketName{}, invalid("bucket", s, want)
	}
	backend, bucket, found := strings.Cut(rest, bucketsSep)
	if !found || backend == "" || bucket == "" || strings.Contains(backend, "/") || strings.Contains(bucket, "/") {
		return BucketName{}, invalid("bucket", s, want)
	}
	return BucketName{Backend: backend, Bucket: bucket}, nil
}

// APITokenPrefix starts every Paladin API token, paladin_pat_…: the server
// tells an API token from a JWT by it, and secret scanners find one by it.
const APITokenPrefix = "paladin_pat_" // #nosec G101 -- a public prefix, not a credential

// ObjectResourceScheme is the scheme of the resource a capability grants on
// objects, object://{tenant-id}/{collection}/{key}.
const ObjectResourceScheme = "object://"

// ObjectResource is the resource a capability names for an object, or for
// every object under a key prefix when key is one: the server checks a
// capability's resource against object://{tenant-id}/{collection}/{key}. The
// tenant is the id, a UUID; a prefix ends where the caller wants it to, so
// give "reports/" to cover the keys under reports/.
func ObjectResource(tenant, collection, key string) string {
	return ObjectResourceScheme + tenant + "/" + collection + "/" + key
}
