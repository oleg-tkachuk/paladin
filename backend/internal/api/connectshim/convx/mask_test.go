package convx

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
)

func TestCheckMask(t *testing.T) {
	supported := []string{"display_name", "labels"}
	cases := []struct {
		name    string
		paths   []string
		wantErr bool
	}{
		{"empty mask", nil, false},
		{"every path supported", []string{"display_name", "labels"}, false},
		{"a misspelled path", []string{"display_nam"}, true},
		{"one good and one unknown", []string{"labels", "cedar_policy"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckMask(tc.paths, supported)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			var ce *connect.Error
			if err != nil && (!errors.As(err, &ce) || ce.Code() != connect.CodeInvalidArgument) {
				t.Errorf("err = %v, want InvalidArgument", err)
			}
		})
	}
}
