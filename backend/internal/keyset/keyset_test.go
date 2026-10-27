package keyset

import (
	"errors"
	"testing"
	"uuid"
)

func TestRoundTrip(t *testing.T) {
	c := Cursor{Order: Order{Field: "key", Desc: true}, Value: "a/b c", ID: uuid.New()}
	got, err := Decode(Encode(c), c.Order)
	if err != nil {
		t.Fatal(err)
	}
	if got != c {
		t.Errorf("decoded %+v, want %+v", got, c)
	}
}

// A token pages only the order it was minted for: resumed under another, it
// would start after a row that order never put there.
func TestDecode_RefusesAnotherOrder(t *testing.T) {
	tok := Encode(Cursor{Order: Order{Field: "key"}, Value: "x", ID: uuid.New()})
	for _, other := range []Order{{Field: "key", Desc: true}, {Field: "size_bytes"}, {}} {
		if _, err := Decode(tok, other); !errors.Is(err, ErrOrderChanged) {
			t.Errorf("decoded under %+v: err = %v, want ErrOrderChanged", other, err)
		}
	}
}

func TestDecode_RefusesMalformed(t *testing.T) {
	cases := map[string]string{
		"not base64":  "%%%",
		"not json":    "bm90LWpzb24",
		"a bare uuid": uuid.New().String(),
		"no row id":   Encode(Cursor{Order: Order{Field: "key"}, Value: "x"}),
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(tok, Order{Field: "key"}); !errors.Is(err, ErrMalformed) {
				t.Errorf("err = %v, want ErrMalformed", err)
			}
		})
	}
}
