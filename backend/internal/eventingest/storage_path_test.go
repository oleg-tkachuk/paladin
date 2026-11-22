package eventingest

import "testing"

func TestSplitStoragePath(t *testing.T) {
	for _, tc := range []struct {
		path, tenant, collection, key string
		ok                            bool
	}{
		{"t/docs/a.txt", "t", "docs", "a.txt", true},
		{"t/docs/a/b/c.txt", "t", "docs", "a/b/c.txt", true},
		{"t/invoices.2026.q1/report.pdf", "t", "invoices/2026/q1", "report.pdf", true},
		{"t/invoices/2026/q1/report.pdf", "t", "invoices", "2026/q1/report.pdf", true},
		{"t/docs", "", "", "", false},
		{"t/docs/", "", "", "", false},
		{"/docs/a", "", "", "", false},
		{"t//a", "", "", "", false},
		{"t/Docs/a", "", "", "", false},
		{"t/a..b/c", "", "", "", false},
		{"t/.system/x", "", "", "", false},
	} {
		tenant, collection, key, ok := splitStoragePath(tc.path)
		if ok != tc.ok || tenant != tc.tenant || collection != tc.collection || key != tc.key {
			t.Errorf("splitStoragePath(%q) = (%q, %q, %q, %v), want (%q, %q, %q, %v)",
				tc.path, tenant, collection, key, ok, tc.tenant, tc.collection, tc.key, tc.ok)
		}
	}
}

// Every source adapter decodes a nested collection's segment.
func TestSourcesDecodeANestedCollection(t *testing.T) {
	const tenant = "0192f6a4-0000-7000-8000-000000000001"
	const path = tenant + "/invoices.2026.q1/report.pdf"
	check := func(name string, f SubjectFields, ok bool) {
		t.Helper()
		if !ok || f.TenantID != tenant || f.Collection != "invoices/2026/q1" || f.Key != "report.pdf" {
			t.Errorf("%s: %+v, %v", name, f, ok)
		}
	}
	f, ok := parseStoragePath(path)
	check("s3", f, ok)
	f, ok = parseSeaweedFSPath("/buckets/b/"+path, "b")
	check("seaweedfs", f, ok)
	f, ok = parseCloudEventsSubject(path)
	check("cloudevents", f, ok)
}
