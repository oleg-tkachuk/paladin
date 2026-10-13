package postgres

import (
	"encoding/base64"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/limes"
)

// The cursors this store hands out. Each is a place in its list's order,
// opaque to the caller; limes leaves a cursor's format to its store.

// ceilingClass is where a tenant's kind of ceiling ranks in
// ListTenantBudgets, as the query's ceiling_class spells it: a ceiling of 0
// leads, a bounded one ranks by utilization, an unlimited one trails.
const (
	ceilingClassZero      int32 = 0
	ceilingClassBounded   int32 = 1
	ceilingClassUnlimited int32 = 2
)

// tenantBudgetCursor is a place in ListTenantBudgets' order: the last row's
// ceiling class, its rank key — the query's exact utilization key, 0 outside
// the bounded class — and its tenant id.
type tenantBudgetCursor struct {
	class int32
	// key is the rank key as the query wrote it: a whole number in decimal.
	key      string
	tenantID uuid.UUID
}

// cursorSep separates a cursor's fields; no class, decimal, integer or uuid
// contains it.
const cursorSep = "|"

// tenantBudgetCursorFields is how many fields a tenant budget cursor has.
const tenantBudgetCursorFields = 3

// encode is the cursor as ListTenantBudgets hands it out: URL-safe.
func (c tenantBudgetCursor) encode() string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join([]string{
		strconv.Itoa(int(c.class)), c.key, c.tenantID.String(),
	}, cursorSep)))
}

// keyNumeric is the cursor's rank key as the query compares it.
func (c tenantBudgetCursor) keyNumeric() (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(c.key); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("%w: capability/postgres: tenant budget cursor key %q", limes.ErrInvalidRequest, c.key)
	}
	return n, nil
}

// decodeTenantBudgetCursor reads a cursor encode wrote. Anything else is
// ErrInvalidRequest: a class the query does not rank by, a key that is not a
// whole number at least 0, or a key outside the bounded class.
func decodeTenantBudgetCursor(s string) (tenantBudgetCursor, error) {
	bad := fmt.Errorf("%w: capability/postgres: tenant budget cursor %q", limes.ErrInvalidRequest, s)
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return tenantBudgetCursor{}, bad
	}
	fields := strings.Split(string(raw), cursorSep)
	if len(fields) != tenantBudgetCursorFields {
		return tenantBudgetCursor{}, bad
	}
	class, err := strconv.ParseInt(fields[0], 10, 32)
	if err != nil || int32(class) < ceilingClassZero || int32(class) > ceilingClassUnlimited {
		return tenantBudgetCursor{}, bad
	}
	key, ok := new(big.Int).SetString(fields[1], 10)
	if !ok || key.Sign() < 0 || (int32(class) != ceilingClassBounded && key.Sign() != 0) {
		return tenantBudgetCursor{}, bad
	}
	tenant, err := uuid.Parse(fields[2])
	if err != nil {
		return tenantBudgetCursor{}, bad
	}
	return tenantBudgetCursor{class: int32(class), key: key.String(), tenantID: tenant}, nil
}

// reservationPlace is a reservation's place in ListReservations' order, and
// the cursor of it: its expiry and id.
type reservationPlace struct {
	expires time.Time
	id      uuid.UUID
}

// reservationCursorFields is how many fields a reservation cursor has: the
// expiry's Unix seconds, its nanoseconds within the second, and the id.
// Seconds and nanoseconds apart, because a single count of Unix nanoseconds
// overflows past year 2262, and a hold's TTL reaches further.
const reservationCursorFields = 3

func (p reservationPlace) String() string {
	return strings.Join([]string{
		strconv.FormatInt(p.expires.Unix(), 10),
		strconv.Itoa(p.expires.Nanosecond()),
		p.id.String(),
	}, cursorSep)
}

func parseReservationPlace(cursor string) (reservationPlace, error) {
	invalid := fmt.Errorf("%w: capability/postgres: reservation cursor %q", limes.ErrInvalidRequest, cursor)
	fields := strings.Split(cursor, cursorSep)
	if len(fields) != reservationCursorFields {
		return reservationPlace{}, invalid
	}
	sec, err1 := strconv.ParseInt(fields[0], 10, 64)
	nsec, err2 := strconv.ParseInt(fields[1], 10, 64)
	id, err3 := uuid.Parse(fields[2])
	if err1 != nil || err2 != nil || err3 != nil || nsec < 0 || nsec >= int64(time.Second) {
		return reservationPlace{}, invalid
	}
	return reservationPlace{time.Unix(sec, nsec).UTC(), id}, nil
}
