package postgres

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/limes"
)

// boundedRankKey is a utilization rank key as the query writes it: a whole
// number in decimal, wider than an int64 so the cursor must not narrow it.
const boundedRankKey = "123456789012345678901234567890"

func TestTenantBudgetCursorRoundTrip(t *testing.T) {
	tenant := uuid.New()
	for name, c := range map[string]tenantBudgetCursor{
		"zero ceiling": {class: ceilingClassZero, key: "0", tenantID: tenant},
		"bounded":      {class: ceilingClassBounded, key: boundedRankKey, tenantID: tenant},
		"unlimited":    {class: ceilingClassUnlimited, key: "0", tenantID: tenant},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := decodeTenantBudgetCursor(c.encode())
			if err != nil {
				t.Fatal(err)
			}
			if got != c {
				t.Errorf("decoded = %+v, want %+v", got, c)
			}
			n, err := got.keyNumeric()
			if err != nil {
				t.Fatalf("keyNumeric: %v", err)
			}
			v, err := n.Value()
			if err != nil {
				t.Fatal(err)
			}
			if v != c.key {
				t.Errorf("keyNumeric = %v, want %s", v, c.key)
			}
		})
	}
}

// encodeFields is a cursor carrying fields as given, in the cursor's
// encoding: what a caller who forged or truncated one would send.
func encodeFields(fields ...string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(fields, cursorSep)))
}

func TestTenantBudgetCursorRefusals(t *testing.T) {
	tenant := uuid.NewString()
	class := func(c int32) string { return strconv.Itoa(int(c)) }
	cases := map[string]string{
		"not base64":              "%%%",
		"too few fields":          encodeFields(class(ceilingClassBounded), boundedRankKey),
		"too many fields":         encodeFields(class(ceilingClassBounded), boundedRankKey, tenant, tenant),
		"class below the range":   encodeFields(class(ceilingClassZero-1), "0", tenant),
		"class above the range":   encodeFields(class(ceilingClassUnlimited+1), "0", tenant),
		"class not a number":      encodeFields("bounded", "0", tenant),
		"negative key":            encodeFields(class(ceilingClassBounded), "-1", tenant),
		"fractional key":          encodeFields(class(ceilingClassBounded), "1.5", tenant),
		"key outside the bounded": encodeFields(class(ceilingClassUnlimited), boundedRankKey, tenant),
		"key on a zero ceiling":   encodeFields(class(ceilingClassZero), boundedRankKey, tenant),
		"tenant not a uuid":       encodeFields(class(ceilingClassBounded), boundedRankKey, "tenant"),
		"empty":                   "",
	}
	for name, cursor := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeTenantBudgetCursor(cursor); !errors.Is(err, limes.ErrInvalidRequest) {
				t.Errorf("err = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

// A key the cursor holds that is no number is refused when the query would
// compare it, not passed through.
func TestTenantBudgetCursorKeyNumericRefusesGarbage(t *testing.T) {
	c := tenantBudgetCursor{class: ceilingClassBounded, key: "many", tenantID: uuid.New()}
	if _, err := c.keyNumeric(); !errors.Is(err, limes.ErrInvalidRequest) {
		t.Errorf("err = %v, want ErrInvalidRequest", err)
	}
}

// farExpiry is past year 2262, where a single count of Unix nanoseconds
// overflows: the reason the cursor keeps seconds and nanoseconds apart.
var farExpiry = time.Date(2300, time.January, 2, 3, 4, 5, 6, time.UTC)

func TestReservationPlaceRoundTrip(t *testing.T) {
	for name, at := range map[string]time.Time{
		"now":              time.Now().UTC(),
		"whole second":     time.Unix(time.Now().Unix(), 0).UTC(),
		"before the epoch": time.Date(1960, time.March, 1, 0, 0, 0, int(time.Second-1), time.UTC),
		"past year 2262":   farExpiry,
	} {
		t.Run(name, func(t *testing.T) {
			p := reservationPlace{expires: at, id: uuid.New()}
			got, err := parseReservationPlace(p.String())
			if err != nil {
				t.Fatal(err)
			}
			if !got.expires.Equal(p.expires) || got.id != p.id {
				t.Errorf("parsed = %v / %s, want %v / %s", got.expires, got.id, p.expires, p.id)
			}
		})
	}
}

func TestReservationPlaceRefusals(t *testing.T) {
	id := uuid.NewString()
	sec := strconv.FormatInt(farExpiry.Unix(), 10)
	join := func(fields ...string) string { return strings.Join(fields, cursorSep) }
	cases := map[string]string{
		"empty":                "",
		"too few fields":       join(sec, "0"),
		"too many fields":      join(sec, "0", id, id),
		"seconds not a number": join("soon", "0", id),
		"nanos not a number":   join(sec, "few", id),
		"negative nanos":       join(sec, "-1", id),
		"nanos a whole second": join(sec, strconv.FormatInt(int64(time.Second), 10), id),
		"id not a uuid":        join(sec, "0", "reservation"),
	}
	for name, cursor := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseReservationPlace(cursor); !errors.Is(err, limes.ErrInvalidRequest) {
				t.Errorf("err = %v, want ErrInvalidRequest", err)
			}
		})
	}
}
