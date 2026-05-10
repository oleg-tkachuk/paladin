package cedar

import (
	"strings"
	"testing"
)

// TestPolicyTemplatesParse pins every Cedar template shipped in the
// frontend `/policies` playground so the parser catches regressions
// at backend test-time instead of at "operator clicked Load".
//
// Convention: when a template is added / changed in
//
//	frontend/src/app/policies/page.tsx (POLICY_TEMPLATES)
//
// mirror the change here. Duplication is intentional — a Go test
// that reads the TS file by regex would be brittle, and the
// templates change rarely (~once per quarter). The drift cost of
// keeping the two in sync is ~30s; the cost of shipping a broken
// template is the user-visible parser error we just fixed.
//
// History:
//
//	commit 0cf4093 — initial four templates, including a
//	  `deny-after-hours` that used `context.now.getHours()` (JS
//	  Date method, not a Cedar function). The parser rejected it
//	  on every Load click. Fixed in commit aacc650 by replacing
//	  with the `forbid-destructive-non-admin` template below.
//	  This test exists to make the same class of regression
//	  surface in CI rather than at runtime.
func TestPolicyTemplatesParse(t *testing.T) {
	templates := []struct {
		id    string
		cedar string
	}{
		{
			id: "read-only-auditor",
			cedar: `permit (
  principal in Role::"tenant.auditor",
  action in [Action::"GetObject", Action::"ListObjects", Action::"GetTenant", Action::"ListBuckets"],
  resource
);
forbid (
  principal,
  action in [Action::"PutObject", Action::"DeleteObject", Action::"SetObjectTags", Action::"SetQuota"],
  resource
);`,
		},
		{
			id: "tenant-uploader",
			// Bucket::"tenants/{tenant_id}/buckets/{bucket}" is a
			// template-time placeholder — operators search-and-replace
			// the {tenant_id} / {bucket} tokens before saving. Cedar
			// doesn't parse `{...}` as a sentinel, but the EUID literal
			// `Bucket::"tenants/{tenant_id}/buckets/{bucket}"` IS a
			// valid quoted identifier (Cedar accepts arbitrary chars
			// inside the quoted form), so it parses cleanly even before
			// substitution.
			cedar: `permit (
  principal in Role::"tenant.uploader",
  action in [Action::"UploadObject", Action::"CompleteObject", Action::"PresignDownload"],
  resource in Bucket::"tenants/{tenant_id}/buckets/{bucket}"
);`,
		},
		{
			id: "agent-with-budget",
			cedar: `permit (
  principal in Role::"agent",
  action in [Action::"GetObject", Action::"PresignDownload", Action::"ListObjects"],
  resource in ObjectKey::"tenants/{tenant_id}/objectKeys/{object_key}"
);`,
		},
		{
			id: "forbid-destructive-non-admin",
			cedar: `// Forbid destructive ops unless the caller is platform.admin.
// Cedar evaluates forbid before permit, so this acts as a hard
// ceiling — pair with permits for the rest of the action surface.
forbid (
  principal,
  action in [Action::"DeleteObject", Action::"DeleteBucket", Action::"PurgeObject"],
  resource
)
unless {
  principal in Role::"platform.admin"
};`,
		},
	}

	for _, tt := range templates {
		t.Run(tt.id, func(t *testing.T) {
			if err := Validate(tt.cedar); err != nil {
				t.Errorf("template %q failed Cedar parse: %v\n\nCedar:\n%s",
					tt.id, err, tt.cedar)
			}
		})
	}
}

// TestPolicyTemplatesParse_KnownBrokenExamples documents Cedar
// constructs that LOOK reasonable but don't compile, so the next
// person isn't surprised. Each negative case carries a comment
// explaining what the operator wanted vs why Cedar rejects it.
//
// These templates do NOT ship; they exist as a learn-from-our-
// mistakes register. The test asserts each is in fact a parse
// error — if Cedar later adds support for the construct, the test
// fails and we can promote the template to the production set.
func TestPolicyTemplatesParse_KnownBrokenExamples(t *testing.T) {
	broken := []struct {
		id     string
		reason string
		cedar  string
	}{
		{
			id: "time-of-day-getHours",
			reason: "context.now.getHours() — getHours is a JS Date method, " +
				"not a Cedar function. Cedar's datetime extension exposes day() / " +
				"time() in 4.x but neither maps to wall-clock hour-of-day, and " +
				"both require the extension to be explicitly enabled.",
			cedar: `forbid (
  principal,
  action in [Action::"DeleteObject"],
  resource
)
when {
  context.now.getHours() < 6
};`,
		},
	}

	for _, tt := range broken {
		t.Run(tt.id, func(t *testing.T) {
			err := Validate(tt.cedar)
			if err == nil {
				t.Errorf("template %q parsed unexpectedly — Cedar may have added "+
					"support for the construct; promote to production set?\n\nCedar:\n%s\n\nReason it was previously rejected:\n%s",
					tt.id, tt.cedar, tt.reason)
				return
			}
			// Sanity-check the error message references something near
			// the bad token. Loose match — Cedar's error format may
			// drift, we just want a non-empty error tied to the source.
			if !strings.Contains(err.Error(), "parse") &&
				!strings.Contains(err.Error(), "syntax") &&
				!strings.Contains(err.Error(), "method") &&
				!strings.Contains(err.Error(), "function") {
				t.Logf("template %q parser error didn't match the usual "+
					"shape — worth re-reviewing the failure category. "+
					"Error: %v", tt.id, err)
			}
		})
	}
}
