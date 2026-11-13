package web

import "strings"

// The segments of the paths that moved. Where they now lead comes from the
// contract (HealthPath, StorageBackendsPath), which the pages' own links
// (src/lib/navigation) are checked against.
const (
	opsSegment     = "ops"
	tenantsSegment = "tenants"
	bucketsSegment = "buckets"
	// A tenant's bucket path: tenants/<tenant>/buckets/<backend>/<bucket>[/tab...].
	tenantBucketMinSegments = 5
	// A backend's bucket list: storage-backends/<backend>/buckets.
	backendBucketsSegments = 3
)

// legacyTarget is where a path that moved now lives, or false. It takes and
// returns escaped paths, so a segment keeps its own encoding.
//
// These were pages that only redirected; a static export has no server to do
// it. The client router asks for a page's payload at its path plus ".txt", so
// a payload request is redirected to the new page's payload, which it follows.
func (s *server) legacyTarget(escapedPath string, rsc bool) (string, bool) {
	p := strings.TrimRight(escapedPath, pathSeparator)
	if rsc {
		if !strings.HasSuffix(p, rscExt) {
			return "", false
		}
		p = strings.TrimSuffix(p, rscExt)
	}
	target, ok := s.movedTo(strings.Split(strings.TrimPrefix(p, pathSeparator), pathSeparator))
	if ok && rsc {
		target += rscExt
	}
	return target, ok
}

func (s *server) movedTo(segs []string) (string, bool) {
	backends := s.cfg.Names.StorageBackendsPath
	backendsSegment := strings.TrimPrefix(backends, pathSeparator)
	switch {
	// The nav and older links point here; operations live on the health page.
	case len(segs) == 1 && segs[0] == opsSegment:
		return s.cfg.Names.HealthPath, true
	// A backend's buckets are listed on the backend's page (ADR-0033).
	case len(segs) == backendBucketsSegments && segs[0] == backendsSegment && segs[2] == bucketsSegment:
		return backends + pathSeparator + segs[1], true
	// A bucket has one page, under its backend (ADR-0033); links into the copy
	// under its owner's workspace land on the same tab there.
	case len(segs) >= tenantBucketMinSegments && segs[0] == tenantsSegment && segs[2] == bucketsSegment:
		return backends + pathSeparator + strings.Join(append([]string{segs[3], bucketsSegment}, segs[4:]...), pathSeparator), true
	}
	return "", false
}
