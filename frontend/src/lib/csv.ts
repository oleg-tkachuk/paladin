// Minimal CSV parsing for operator-pasted / file-picked spreadsheet data.
// Extracted from CsvImportDialog so it can be unit-tested in isolation (see
// csv.test.ts). Pure — no DOM, no React.

/** Tab wins over comma so spreadsheet copy-paste (tab-separated) just works. */
export function detectDelimiter(line: string): string {
  return line.includes("\t") ? "\t" : ",";
}

/**
 * Parse a CSV/TSV string into an array of header-keyed row records.
 *
 * Minimal RFC-4180-ish: respects double-quoted cells (which may contain the
 * delimiter) and unescapes doubled quotes (`""` → `"`). Does NOT claim full
 * compliance (no Unicode-aware splits, no BOM stripping for non-ASCII
 * headers, no embedded newlines inside quotes). The first non-empty row is
 * the header; trailing blank lines are dropped; missing trailing cells are
 * filled with empty strings. Values and header keys are trimmed.
 */
export function parseCSV(text: string): Record<string, string>[] {
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  while (lines.length && lines[lines.length - 1].trim() === "") lines.pop();
  if (lines.length === 0) return [];
  const delim = detectDelimiter(lines[0]);
  const splitRow = (line: string): string[] => {
    const out: string[] = [];
    let buf = "";
    let inQ = false;
    for (let i = 0; i < line.length; i++) {
      const c = line[i];
      if (inQ) {
        if (c === '"' && line[i + 1] === '"') {
          buf += '"';
          i++;
        } else if (c === '"') {
          inQ = false;
        } else {
          buf += c;
        }
      } else if (c === '"') {
        inQ = true;
      } else if (c === delim) {
        out.push(buf);
        buf = "";
      } else {
        buf += c;
      }
    }
    out.push(buf);
    return out;
  };
  const header = splitRow(lines[0]).map((h) => h.trim());
  const rows: Record<string, string>[] = [];
  for (let i = 1; i < lines.length; i++) {
    const cells = splitRow(lines[i]);
    const obj: Record<string, string> = {};
    for (let j = 0; j < header.length; j++) {
      obj[header[j]] = (cells[j] ?? "").trim();
    }
    rows.push(obj);
  }
  return rows;
}
