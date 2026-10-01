import { readdirSync, readFileSync } from "node:fs";
import { join, relative, sep } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// Every absolute in-console link must land on a page that exists. Two did not
// when this was measured: the tenant audit log linked a capability to
// /capabilities, and an object's Collection linked to /collections/<name> —
// neither is a route, so both clicks ended on a 404.
//
// Matched on the AST: an `href` attribute, a `router.push/replace/prefetch`
// argument, or an `href`/`path`/`to` property whose value is a string or
// template starting with "/". A template expression after a "/" is read as a
// dynamic segment; one anywhere else (a "?next=" suffix) ends the path.
// Relative links — the tenant tabs build theirs from a slug — are not checked.

const SRC = join(__dirname, "..");
const APP = join(SRC, "app");
const SEGMENT = "X";

function walk(dir: string, keep: (name: string) => boolean): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    return e.isDirectory() ? walk(p, keep) : keep(e.name) ? [p] : [];
  });
}

/** Each page as a pattern: "[id]" matches one segment, "[...x]" the rest. */
function pagePatterns(): RegExp[] {
  return walk(APP, (n) => n === "page.tsx").map((file) => {
    const route = relative(APP, file).split(sep).slice(0, -1);
    const parts = route
      .filter((s) => !/^\(.*\)$/.test(s)) // route groups add no segment
      .map((s) =>
        /^\[\[?\.\.\./.test(s)
          ? ".*"
          : /^\[.*\]$/.test(s)
            ? "[^/]+"
            : s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"),
      );
    return new RegExp(`^/${parts.join("/")}/?$`);
  });
}

function pathOf(node: ts.Node): string | null {
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
    return node.text;
  }
  if (!ts.isTemplateExpression(node)) return null;
  let path = node.head.text;
  for (const span of node.templateSpans) {
    if (!path.endsWith("/")) break;
    path += SEGMENT + span.literal.text;
  }
  return path;
}

function internalLinks(): { at: string; path: string }[] {
  const links: { at: string; path: string }[] = [];
  const files = walk(
    SRC,
    (n) => /\.tsx?$/.test(n) && !/\.test\.tsx?$/.test(n),
  ).filter((f) => !relative(SRC, f).startsWith(join("app", "api")));
  for (const file of files) {
    const sf = ts.createSourceFile(
      file,
      readFileSync(file, "utf8"),
      ts.ScriptTarget.Latest,
      true,
    );
    const take = (node: ts.Node) => {
      const raw = pathOf(node);
      if (!raw || !raw.startsWith("/") || raw.startsWith("//")) return;
      if (raw.startsWith("/api/") || raw.startsWith("/_next/")) return;
      const line = sf.getLineAndCharacterOfPosition(node.getStart(sf)).line;
      links.push({
        at: `${relative(SRC, file)}:${line + 1}`,
        path: raw.split(/[?#]/)[0],
      });
    };
    const visit = (node: ts.Node) => {
      if (
        ts.isJsxAttribute(node) &&
        node.name.getText(sf) === "href" &&
        node.initializer
      ) {
        const init = node.initializer;
        if (ts.isStringLiteral(init)) take(init);
        else if (ts.isJsxExpression(init) && init.expression)
          take(init.expression);
      }
      if (
        ts.isCallExpression(node) &&
        ts.isPropertyAccessExpression(node.expression) &&
        /^(push|replace|prefetch)$/.test(node.expression.name.text) &&
        node.arguments[0]
      ) {
        take(node.arguments[0]);
      }
      if (
        ts.isPropertyAssignment(node) &&
        /^(href|path|to)$/.test(node.name.getText(sf))
      ) {
        take(node.initializer);
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return links;
}

describe("internal links", () => {
  const pages = pagePatterns();
  const links = internalLinks();

  it("finds the pages and links it is meant to check", () => {
    expect(pages.length).toBeGreaterThan(30);
    expect(links.length).toBeGreaterThan(30);
  });

  it("every absolute link lands on a page", () => {
    const dead = links
      .filter(({ path }) => !pages.some((p) => p.test(path)))
      .map(({ at, path }) => `${at} ${path}`);
    expect(dead, "point the link at a page under src/app").toEqual([]);
  });
});
