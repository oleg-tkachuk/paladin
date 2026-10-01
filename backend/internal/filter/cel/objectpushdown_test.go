package cel

import "testing"

func TestExtractObjectPushdown(t *testing.T) {
	cases := []struct {
		expr          string
		state, prefix string
		contains      string
	}{
		{`state == 'AVAILABLE'`, "AVAILABLE", "", ""},
		{`key.startsWith('logs/')`, "", "logs/", ""},
		{`key.contains('report')`, "", "", "report"},
		{`state == 'PENDING' && key.contains('q1')`, "PENDING", "", "q1"},
		{`state == 'X' || key.contains('y')`, "", "", ""}, // disjunction → nothing pushed
		{`size_bytes > 10`, "", "", ""},                   // unsupported field
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
		})
	}
}
