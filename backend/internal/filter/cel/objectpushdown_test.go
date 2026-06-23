package cel

import "testing"

func TestExtractObjectPushdown(t *testing.T) {
	cases := []struct {
		expr          string
		state, prefix string
		contains      string
		recognised    int
	}{
		{`state == 'AVAILABLE'`, "AVAILABLE", "", "", 1},
		{`key.startsWith('logs/')`, "", "logs/", "", 1},
		{`key.contains('report')`, "", "", "report", 1},
		{`state == 'PENDING' && key.contains('q1')`, "PENDING", "", "q1", 2},
		{`state == 'X' || key.contains('y')`, "", "", "", 0}, // disjunction → nothing pushed
		{`size_bytes > 10`, "", "", "", 0},                   // unsupported field
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			pd, err := ExtractObjectPushdown(tc.expr)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			if pd.StateEq != tc.state || pd.KeyPrefix != tc.prefix || pd.KeyContains != tc.contains {
				t.Errorf("got state=%q prefix=%q contains=%q; want %q/%q/%q",
					pd.StateEq, pd.KeyPrefix, pd.KeyContains, tc.state, tc.prefix, tc.contains)
			}
			if pd.Recognised != tc.recognised {
				t.Errorf("recognised=%d, want %d", pd.Recognised, tc.recognised)
			}
		})
	}
}
