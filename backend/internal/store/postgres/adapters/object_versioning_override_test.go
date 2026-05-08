package adapters

import "testing"

func TestReadBoolOverride(t *testing.T) {
	cases := map[string]struct {
		raw     string
		key     string
		want    bool
		present bool
	}{
		"enabled true":  {raw: `{"versioning_enabled":true}`, key: "versioning_enabled", want: true, present: true},
		"enabled false": {raw: `{"versioning_enabled":false}`, key: "versioning_enabled", want: false, present: true},
		"missing":       {raw: `{"other":1}`, key: "versioning_enabled", present: false},
		"wrong type":    {raw: `{"versioning_enabled":"yes"}`, key: "versioning_enabled", present: false},
		"empty":         {raw: ``, key: "versioning_enabled", present: false},
		"malformed":     {raw: `{not json`, key: "versioning_enabled", present: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			gotV, gotPresent := readBoolOverride([]byte(tc.raw), tc.key)
			if gotPresent != tc.present {
				t.Fatalf("present: got %v want %v", gotPresent, tc.present)
			}
			if gotPresent && gotV != tc.want {
				t.Errorf("value: got %v want %v", gotV, tc.want)
			}
		})
	}
}
