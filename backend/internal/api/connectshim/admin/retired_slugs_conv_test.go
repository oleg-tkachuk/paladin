package admin

import (
	"testing"
	"time"

	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
)

// release_time carries the handler's ReleaseAt, and is absent when no
// reservation period is set: the slug is kept until it is released.
func TestRetiredSlugReleaseTimeOnTheWire(t *testing.T) {
	retired := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	release := retired.Add(90 * 24 * time.Hour)
	resp := retiredSlugPageToProto(tenanth.RetiredSlugPage{Slugs: []tenanth.RetiredSlug{
		{Slug: "kept", FormerTenantID: uuid.New(), RetiredAt: retired},
		{Slug: "timed", FormerTenantID: uuid.New(), RetiredAt: retired, ReleaseAt: &release},
	}})
	kept, timed := resp.GetRetiredSlugs()[0], resp.GetRetiredSlugs()[1]
	if kept.GetReleaseTime() != nil {
		t.Errorf("kept release_time = %v, want none", kept.GetReleaseTime())
	}
	if got := timed.GetReleaseTime(); got == nil || !got.AsTime().Equal(release) {
		t.Errorf("timed release_time = %v, want %v", got, release)
	}
	if !timed.GetRetireTime().AsTime().Equal(retired) {
		t.Errorf("retire_time = %v, want %v", timed.GetRetireTime(), retired)
	}
}
