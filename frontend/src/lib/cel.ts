/**
 * Building CEL filter expressions to send to the API.
 *
 * The server compiles what arrives here, so a query typed by an operator is
 * untrusted input being spliced into an expression. Everything below exists to
 * make that safe and to keep the console's idea of a match identical to the
 * server's.
 */

/**
 * Encode a JS string as a CEL string literal, quotes included.
 *
 * The escaping that existed before this — `q.replace(/'/g, "\\'")` on the
 * objects page — handled the quote and not the backslash, so a query ending in
 * `\` produced `key.contains('foo\')`, where the backslash escapes the closing
 * quote and the expression no longer parses. A query is text an operator typed;
 * it should never be able to change the shape of the expression around it.
 */
export function celString(value: string): string {
  let out = '"';
  for (const ch of value) {
    switch (ch) {
      case '"':
        out += '\\"';
        break;
      case "\\":
        out += "\\\\";
        break;
      case "\n":
        out += "\\n";
        break;
      case "\r":
        out += "\\r";
        break;
      case "\t":
        out += "\\t";
        break;
      default:
        out += ch;
    }
  }
  return out + '"';
}

/**
 * Fold A–Z and leave every other character alone — deliberately NOT
 * `toLowerCase()`.
 *
 * This is the third spelling of one definition. The other two are
 * `cel.SearchText` in Go, which builds the `search` field the server filters
 * on, and `lower(col COLLATE "C")` in SQL, which pushes the filter down. All
 * three fold ASCII only, because Go and Postgres are two Unicode
 * implementations that are not obliged to agree — and a disagreement there
 * drops rows silently.
 *
 * `toLowerCase()` here would break the agreement from this end instead: the
 * server's search text for "Über Cache" is "Über cache" (the Ü untouched), so
 * a query lowered to "über" would match nothing while "Über" matches. Searching
 * non-ASCII text is therefore case-sensitive, on purpose and in all three
 * places.
 */
export function asciiLower(value: string): string {
  let out = "";
  for (const ch of value) {
    const c = ch.charCodeAt(0);
    out += c >= 65 && c <= 90 ? String.fromCharCode(c + 32) : ch;
  }
  return out;
}

/**
 * The filter for a console search box: one conjunct over the derived `search`
 * field, never a disjunction over two columns.
 *
 * `bucket_id.contains(q) || display_name.contains(q)` is the obvious shape and
 * the wrong one. The server's pushdown walks the top-level `&&` chain only, so
 * a disjunction pushes nothing into SQL; the server then reads one page,
 * filters it in memory, and answers with whatever that page held. The operator
 * is told the bucket does not exist because its name sorted past row 500.
 *
 * An empty or whitespace-only query returns "" — no filter, not a filter that
 * matches everything, so the request stays identical to an unfiltered one.
 */
export function searchFilter(query: string): string {
  const q = query.trim();
  if (!q) return "";
  return `search.contains(${celString(asciiLower(q))})`;
}
