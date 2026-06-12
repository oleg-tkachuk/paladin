package object

import (
	"testing"
	"time"
)

func TestObjectLockActive(t *testing.T) {
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)
	now := time.Now()

	cases := []struct {
		name   string
		lock   ObjectLock
		bypass bool
		want   bool
	}{
		{"unlocked", ObjectLock{}, false, false},
		{"legal hold absolute", ObjectLock{LegalHold: true}, true, true},
		{"compliance active", ObjectLock{Mode: "COMPLIANCE", RetainUntil: &future}, false, true},
		{"compliance not bypassable", ObjectLock{Mode: "COMPLIANCE", RetainUntil: &future}, true, true},
		{"compliance expired", ObjectLock{Mode: "COMPLIANCE", RetainUntil: &past}, false, false},
		{"governance active no bypass", ObjectLock{Mode: "GOVERNANCE", RetainUntil: &future}, false, true},
		{"governance bypassed", ObjectLock{Mode: "GOVERNANCE", RetainUntil: &future}, true, false},
		{"governance expired", ObjectLock{Mode: "GOVERNANCE", RetainUntil: &past}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.lock.Active(now, tc.bypass); got != tc.want {
				t.Errorf("Active() = %v, want %v", got, tc.want)
			}
		})
	}
}
