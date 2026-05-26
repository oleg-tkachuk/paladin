package middleware

import "testing"

// TestIsCreateMethod pins the Connect-procedure-name parser used by
// RequireOnCreate. The risky cases are:
//   - service-prefix containing "Create" (must NOT match — we scope to
//     the trailing method segment, otherwise a hypothetical
//     CreateOrderHistoryService would force every method on it through
//     the gate),
//   - empty / malformed input (defensive).
//
// The interceptor itself is a four-line branch around this helper:
// `if RequireOnCreate && isCreateMethod(method) { reject }`. Covering
// the helper covers the policy. Wire-level enforcement (handler not
// invoked when header is missing) is verified through real RPC
// traffic in the bootstrap integration test once the interceptor is
// wired into the server chain.
func TestIsCreateMethod(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"create rpc", "/paladin.admin.v1.TenantService/CreateTenant", true},
		{"create bucket", "/paladin.admin.v1.BucketService/CreateBucket", true},
		{"list is not create", "/paladin.admin.v1.TenantService/ListTenants", false},
		{"update is not create", "/paladin.admin.v1.TenantService/UpdateTenant", false},
		{"service prefix containing create", "/paladin.admin.v1.CreateOrderHistoryService/ListOrders", false},
		{"empty", "", false},
		{"no slash", "Create", false},
		{"trailing slash", "/svc/", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCreateMethod(tc.in); got != tc.want {
				t.Fatalf("isCreateMethod(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
