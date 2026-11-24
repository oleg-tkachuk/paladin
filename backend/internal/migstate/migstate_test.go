package migstate

import "testing"

func TestCancelTo(t *testing.T) {
	cases := []struct {
		state string
		to    string
		ok    bool
	}{
		{Provisioning, Failed, true},
		{Moving, RollingBack, true},
		{Completed, RollingBack, true},
		{RollingBack, "", false},
		{Failed, "", false},
		{Cleaned, "", false},
	}
	for _, tc := range cases {
		to, ok := CancelTo(tc.state)
		if to != tc.to || ok != tc.ok {
			t.Errorf("CancelTo(%q) = %q, %v; want %q, %v", tc.state, to, ok, tc.to, tc.ok)
		}
	}
}
