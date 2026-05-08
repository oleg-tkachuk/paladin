package config

import (
	"strings"
	"testing"
)

func TestValidateBackendAuthModes(t *testing.T) {
	cases := []struct {
		name    string
		b       StorageBackend
		wantErr string
	}{
		{
			name:    "missing mode rejected",
			b:       StorageBackend{},
			wantErr: "auth.mode is required",
		},
		{
			name: "static_keys: missing secret_key rejected",
			b: StorageBackend{
				Auth: StorageBackendAuth{Mode: AuthModeStaticKeys, AccessKey: "AKIA"},
			},
			wantErr: "static_keys",
		},
		{
			name: "static_keys: full pair OK",
			b: StorageBackend{
				Auth: StorageBackendAuth{Mode: AuthModeStaticKeys, AccessKey: "AKIA", SecretKey: "secret"},
			},
		},
		{
			name: "default_chain: any static key rejected",
			b: StorageBackend{
				Auth: StorageBackendAuth{Mode: AuthModeDefaultChain, AccessKey: "AKIA"},
			},
			wantErr: "default_chain",
		},
		{
			name: "default_chain: clean OK",
			b: StorageBackend{
				Auth: StorageBackendAuth{Mode: AuthModeDefaultChain},
			},
		},
		{
			name: "assume_role: role_arn required",
			b: StorageBackend{
				Auth: StorageBackendAuth{Mode: AuthModeAssumeRole},
			},
			wantErr: "role_arn is required",
		},
		{
			name: "assume_role: with role_arn OK",
			b: StorageBackend{
				Auth: StorageBackendAuth{Mode: AuthModeAssumeRole, RoleARN: "arn:aws:iam::123:role/x"},
			},
		},
		{
			name: "web_identity: with role_arn OK",
			b: StorageBackend{
				Auth: StorageBackendAuth{Mode: AuthModeWebIdentity, RoleARN: "arn:aws:iam::123:role/x"},
			},
		},
		{
			name: "unknown mode rejected",
			b: StorageBackend{
				Auth: StorageBackendAuth{Mode: "magic"},
			},
			wantErr: "unknown",
		},
		{
			name: "static_keys with role_arn rejected",
			b: StorageBackend{
				Auth: StorageBackendAuth{Mode: AuthModeStaticKeys, AccessKey: "AKIA", SecretKey: "secret", RoleARN: "arn:aws:iam::1:role/x"},
			},
			wantErr: "role_arn",
		},
		{
			name: "negative duration rejected",
			b: StorageBackend{
				Auth: StorageBackendAuth{Mode: AuthModeAssumeRole, RoleARN: "arn:aws:iam::1:role/x", DurationSeconds: -1},
			},
			wantErr: "duration_seconds",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateBackendAuth("primary", tc.b)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
