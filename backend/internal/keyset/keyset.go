// Package keyset is the page token of a list sorted by one field: the sort
// value and id of the last row a page fetched, and the order it was minted
// for. A list resumes after that (value, id) pair, so equal values page
// correctly, and a token presented for another order is refused rather than
// resuming at a row that order never put there.
package keyset

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"uuid"
)

// The ways a page token is refused. Both are the caller's error.
var (
	ErrMalformed    = errors.New("keyset: malformed page token")
	ErrOrderChanged = errors.New("keyset: page token was issued for another order; start from the first page")
)

// Order is a list's sort: the field, as order_by names it, and its direction.
type Order struct {
	Field string
	Desc  bool
}

// Cursor is where the next page starts: after the row holding Value and ID.
// Value is the sort field's value as text, in the form the store casts back.
type Cursor struct {
	Order Order
	Value string
	ID    uuid.UUID
}

type wire struct {
	Field string    `json:"f"`
	Desc  bool      `json:"d,omitempty"`
	Value string    `json:"v,omitempty"`
	ID    uuid.UUID `json:"i"`
}

// Encode returns c as an opaque page token.
func Encode(c Cursor) string {
	b, _ := json.Marshal(wire{Field: c.Order.Field, Desc: c.Order.Desc, Value: c.Value, ID: c.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}

// Decode reads token, which must have been minted for want.
func Decode(token string, want Order) (Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	var w wire
	if err := json.Unmarshal(b, &w); err != nil {
		return Cursor{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if w.ID == uuid.Nil() {
		return Cursor{}, fmt.Errorf("%w: no row id", ErrMalformed)
	}
	got := Order{Field: w.Field, Desc: w.Desc}
	if got != want {
		return Cursor{}, ErrOrderChanged
	}
	return Cursor{Order: got, Value: w.Value, ID: w.ID}, nil
}
