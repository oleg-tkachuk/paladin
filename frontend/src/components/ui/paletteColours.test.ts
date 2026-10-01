import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, it } from "vitest";

// The shared components every page is built from use theme tokens, not raw
// palette colours: a hex or slate/rose/amber class stays the same colour in
// every theme. Measured before the fix: ConfirmModal (18 classes, a fixed
// #0A0C10 surface) and IdentifierCopy (15).

const UI = __dirname;
const PALETTE =
  "slate|gray|zinc|neutral|stone|indigo|rose|emerald|amber|red|green|blue|sky|violet|purple|cyan|teal|yellow|orange|pink|white|black";
const RAW_COLOUR = new RegExp(
  `\\b(?:bg|text|border|ring|from|to|via|fill|stroke|shadow|placeholder|divide|outline)-(?:\\[#[0-9A-Fa-f]{3,8}\\]|(?:${PALETTE})\\b)[^\\s"'\`]*`,
  "g",
);

// "<file>#<class>" → why a raw colour is right there.
const ALLOWED: Record<string, string> = {
  "alert-dialog.tsx#bg-black/10": "the overlay scrim darkens in both themes",
  "dialog.tsx#bg-black/10": "the overlay scrim darkens in both themes",
  "sheet.tsx#bg-black/10": "the overlay scrim darkens in both themes",
  "switch.tsx#bg-white": "the thumb is white on either track colour",
};

function rawColours(): string[] {
  return readdirSync(UI)
    .filter((f) => f.endsWith(".tsx") && !f.endsWith(".test.tsx"))
    .flatMap((f) =>
      [...readFileSync(join(UI, f), "utf8").matchAll(RAW_COLOUR)].map(
        (m) => `${f}#${m[0]}`,
      ),
    );
}

describe("shared UI colours", () => {
  const found = rawColours();

  it("are theme tokens", () => {
    expect(
      found.filter((k) => !(k in ALLOWED)),
      "use a token from globals.css",
    ).toEqual([]);
  });

  it("every allowed entry still names a raw colour", () => {
    expect(Object.keys(ALLOWED).filter((k) => !found.includes(k))).toEqual([]);
  });
});
