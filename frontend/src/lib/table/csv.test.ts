import { describe, expect, it } from "vitest";

import {
  csvExport,
  csvField,
  csvFileName,
  neutraliseFormula,
  scopedListName,
  toCsv,
  type CsvColumn,
} from "./csv";

describe("neutraliseFormula", () => {
  it.each([
    ["=SUM(A1:A2)", "'=SUM(A1:A2)"],
    ["+1", "'+1"],
    ["-1", "'-1"],
    ["@cmd", "'@cmd"],
    ["\tx", "'\tx"],
    ["\rx", "'\rx"],
    ["\nx", "'\nx"],
    ["\vx", "'\vx"],
    ["\fx", "'\fx"],
    ["|cmd", "'|cmd"],
    ["%x", "'%x"],
    // A spreadsheet strips what leads a value before it evaluates it, so
    // the lead is looked for past blanks, controls, NBSP and a BOM; the
    // guard still goes before the text as it was.
    [" =1+1", "' =1+1"],
    ["   +1", "'   +1"],
    ["\u00A0=1", "'\u00A0=1"],
    ["\uFEFF=1", "'\uFEFF=1"],
    ["\u0000@x", "'\u0000@x"],
    ["\u001F-1", "'\u001F-1"],
    [" \t\u00A0\uFEFF%x", "' \t\u00A0\uFEFF%x"],
  ])("guards %j", (text, want) => {
    expect(neutraliseFormula(text)).toBe(want);
  });

  it.each(["plain", "", "a=b", "1-2", " x=1", "\u00A0x", "'already"])(
    "leaves %j as it is",
    (text) => {
      expect(neutraliseFormula(text)).toBe(text);
    },
  );
});

describe("csvField", () => {
  it.each([
    ["plain", '"plain"'],
    ["", '""'],
    ["a,b", '"a,b"'],
    // Quoted whatever it holds, so a reader splitting on ; or a tab still
    // reads one cell.
    ["a;b", '"a;b"'],
    ["a\tb", '"a\tb"'],
    ['say "hi"', '"say ""hi"""'],
    ["two\nlines", '"two\nlines"'],
    ["cr\rlf", '"cr\rlf"'],
    ["ünïcödé", '"ünïcödé"'],
  ])("writes %j as %j", (text, want) => {
    expect(csvField(text)).toBe(want);
  });

  // The guard goes inside the quotes, so the field still parses as one.
  it("neutralises before it quotes", () => {
    expect(csvField("=1,2")).toBe(`"'=1,2"`);
    expect(csvField('-"x"')).toBe(`"'-""x"""`);
    expect(csvField(" =1;2")).toBe(`"' =1;2"`);
  });
});

type Row = { name: string; size: number };
const COLUMNS: CsvColumn<Row>[] = [
  { header: "Name", text: (r) => r.name },
  { header: "Size, bytes", text: (r) => String(r.size) },
];

describe("toCsv", () => {
  it("writes the header, then each row in the order given, CRLF-ended", () => {
    const rows = [
      { name: "b", size: 2 },
      { name: "a", size: 1 },
    ];
    expect(toCsv(COLUMNS, rows)).toBe(
      '"Name","Size, bytes"\r\n"b","2"\r\n"a","1"\r\n',
    );
  });

  it("writes the header alone for no rows", () => {
    expect(toCsv(COLUMNS, [])).toBe('"Name","Size, bytes"\r\n');
  });

  it("guards a negative number as it guards any leading minus", () => {
    expect(toCsv(COLUMNS, [{ name: "x", size: -3 }])).toContain(
      `"x","'-3"\r\n`,
    );
  });
});

describe("csvFileName", () => {
  const at = new Date(2026, 10, 4, 9, 5, 7);

  it("names the list and the local time, to the second", () => {
    expect(csvFileName("users", at)).toBe("users-20261104-090507.csv");
  });

  it("keeps only letters, digits and dashes", () => {
    expect(csvFileName("M2M tokens / acme", at)).toBe(
      "m2m-tokens-acme-20261104-090507.csv",
    );
  });

  it("names a list within a resource after both", () => {
    expect(csvFileName(scopedListName("acme", "buckets"), at)).toBe(
      "acme-buckets-20261104-090507.csv",
    );
  });

  it("falls back when nothing of the name is left", () => {
    expect(csvFileName("///", at)).toBe("export-20261104-090507.csv");
  });
});

describe("csvExport", () => {
  it("counts the rows and builds the text only when asked", () => {
    let reads = 0;
    const counted: CsvColumn<Row>[] = [
      {
        header: "Name",
        text: (r) => {
          reads += 1;
          return r.name;
        },
      },
    ];
    const ex = csvExport("users", counted, [{ name: "a", size: 1 }]);
    expect(ex).toMatchObject({ name: "users", rowCount: 1 });
    expect(reads).toBe(0);
    expect(ex.build()).toBe('"Name"\r\n"a"\r\n');
    expect(reads).toBe(1);
  });
});
