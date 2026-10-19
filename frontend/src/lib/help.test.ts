import { existsSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

import { DOCS_BASE_URL, HELP, helpHref } from "./help";

const DOCS = join(__dirname, "../../../docs");

describe("HELP", () => {
  // A link to a document that moved is a dead end in the very place the
  // reader went for an answer.
  it.each(Object.entries(HELP))(
    "%s links to a document that exists",
    (_, e) => {
      expect(existsSync(join(DOCS, e.doc))).toBe(true);
    },
  );

  it("builds links under the docs base", () => {
    expect(helpHref("publicRead")).toBe(
      DOCS_BASE_URL + "adr/0027-public-collections.md",
    );
  });
});
