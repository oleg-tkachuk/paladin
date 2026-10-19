import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

import { describe, expect, it } from "vitest";

// The console's notifications in one voice (docs/console-voice.md). Every
// literal title and message a showNotification call passes is held to the
// rules a reader can check without the guide open.

const SRC = join(__dirname, "..");

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const path = join(dir, e.name);
    if (e.isDirectory()) return e.name === "gen" ? [] : sources(path);
    return /\.tsx?$/.test(e.name) && !/\.test\./.test(e.name) ? [path] : [];
  });
}

/** Proper names and initialisms that keep their capitals mid-sentence. */
const PROPER = new Set([
  "Cedar",
  "Paladin",
  "Trash",
  "ID",
  "URL",
  "CEL",
  "JSON",
  "API",
  "MCP",
  "OAuth",
  "S3",
]);

type Text = { file: string; field: "title" | "message"; text: string };

const texts: Text[] = sources(SRC).flatMap((file) => {
  const src = readFileSync(file, "utf8");
  return [...src.matchAll(/showNotification\(\s*\{([\s\S]*?)\}\s*\)/g)].flatMap(
    ([, body]) =>
      [...body.matchAll(/\b(title|message):\s*"([^"]*)"/g)].map(
        ([, field, text]) => ({
          file: relative(SRC, file),
          field: field as Text["field"],
          text,
        }),
      ),
  );
});

/** What a text breaks, by the guide's rules. */
function voiceIssues({ field, text }: Pick<Text, "field" | "text">) {
  const issues: string[] = [];
  if (/\bplease\b/i.test(text)) issues.push("asks please");

  if (/!/.test(text)) issues.push("exclaims");
  if (/\bsuccess(ful(ly)?)?\b/i.test(text)) issues.push("says success");
  if (field === "title") {
    if (/^[a-z]/.test(text)) issues.push("starts lowercase");
    if (/\.$/.test(text)) issues.push("ends with a period");
    const capitalised = text
      .split(/\s+/)
      .slice(1)
      .filter(
        (w) => /^[A-Z][a-z]/.test(w) && !PROPER.has(w.replace(/\W+$/, "")),
      );
    if (capitalised.length) issues.push(`title case: ${capitalised.join(" ")}`);
  }
  return issues;
}

describe("notification voice", () => {
  it("finds the console's notifications", () => {
    expect(texts.length).toBeGreaterThan(0);
  });

  it.each([
    [
      { field: "title", text: "Update Successful" },
      ["says success", "title case: Successful"],
    ],
    [{ field: "title", text: "Stale, please retry" }, ["asks please"]],
    [{ field: "title", text: "refused" }, ["starts lowercase"]],
    [{ field: "title", text: "Saved." }, ["ends with a period"]],
    [{ field: "message", text: "Done!" }, ["exclaims"]],
    [{ field: "title", text: "Policy saved in Cedar" }, []],
    [{ field: "message", text: "It is back from the trash." }, []],
  ] as const)("reads %j as breaking %j", (text, want) => {
    expect(voiceIssues(text)).toEqual(want);
  });

  it("holds every notification to the guide", () => {
    const breaking = texts
      .map((t) => ({ ...t, issues: voiceIssues(t) }))
      .filter((t) => t.issues.length > 0)
      .map((t) => `${t.file} ${t.field} "${t.text}": ${t.issues.join(", ")}`);
    expect(breaking).toEqual([]);
  });
});
