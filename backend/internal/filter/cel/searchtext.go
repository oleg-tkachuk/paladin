package cel

// SearchText builds the value of the derived `search` filter field.
//
// It exists so one console search box can send ONE conjunct. The obvious
// alternative — `bucket_id.contains(q) || display_name.contains(q)` — is a trap
// in this codebase: ExtractPushdown descends `&&` only, so a disjunction pushes
// nothing, the server reads a full page and filters it in memory, and the
// caller is handed the matches that page happened to hold. An operator would
// be told a bucket does not exist because its name sorted past row 500.
//
// THE SQL MUST SPELL THIS IDENTICALLY. The pushdown contract is that a hint
// only narrows and never drops a row the authoritative CEL pass accepts, and
// that holds only while the two definitions agree:
//
//	lower(col_a COLLATE "C") || chr(10) || lower(coalesce(col_b,'') COLLATE "C")
//
// ASCII-ONLY FOLDING, and the COLLATE is the reason. Go's strings.ToLower and
// Postgres' lower() are two Unicode implementations, and they are not obliged
// to agree: strings.ToLower('İ') is "i̇", which contains an ASCII 'i', while
// lower('İ') under some collations leaves it alone. A search for "i" would then
// match in CEL and not in SQL — the row silently dropped, which is precisely
// the failure this whole mechanism is built to avoid. Folding only A–Z is the
// one rule both engines implement the same way, and `COLLATE "C"` is how
// Postgres is told to do exactly that.
//
// The cost is stated rather than hidden: searching non-ASCII text is
// case-sensitive. Substring matching still works, and every ASCII query
// behaves as it always did.
//
// The separator is a newline because a query can only match across a field
// boundary by containing the separator, and a newline cannot be typed into a
// search input. So this matches what it claims to: one field or the other.
func SearchText(parts ...string) string {
	out := make([]byte, 0, 64)
	for i, p := range parts {
		if i > 0 {
			out = append(out, '\n')
		}
		out = append(out, []byte(asciiLower(p))...)
	}
	return string(out)
}

// asciiLower folds A–Z and leaves every other byte alone — deliberately not
// strings.ToLower. See SearchText.
func asciiLower(s string) string {
	b := []byte(s)
	changed := false
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
			changed = true
		}
	}
	if !changed {
		return s
	}
	return string(b)
}
