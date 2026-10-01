import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// What a keyboard or a screen reader cannot use, decided from the source.
//
// 1. A click handler on an element that is not a control (a div, a span) runs
//    for a mouse only: it takes no focus and no key press. Measured before the
//    fix: copying an identifier and applying a saved view were mouse-only.
// 2. A button whose only content is an icon, with no aria-label or title,
//    announces as "button". Measured before the fix: purging an object from
//    the trash and deleting a saved view. Links and Radix triggers and close
//    buttons are held to the same rule, and so is a button whose content is
//    an expression that can only produce icons (`{busy ? <Spin/> : <XIcon/>}`),
//    which used to count as a name because it was an expression.

const SRC = join(__dirname, "..");
const GENERATED = "gen";

// Elements that take focus and key presses themselves.
const CONTROLS = new Set([
  "a",
  "button",
  "input",
  "select",
  "textarea",
  "label",
  "summary",
]);
// Controls a screen reader announces by their content: buttons, links, and
// Radix triggers and close buttons. An `asChild` one hands its role to the
// child, which is checked itself.
const NAMED_BY_CONTENT = /^(button|Button|a|Link|[A-Z]\w*(Trigger|Close))$/;
// A component that renders an icon and nothing else.
const ICON_TAG = /(Icon|^svg)$/;
const NAMING_ATTRS = new Set(["aria-label", "aria-labelledby", "title"]);

// "<file under src>#<onClick source>" → the keyboard path that exists anyway.
// An entry that no longer matches fails, so the list only shrinks.
const ALLOWED: Record<string, string> = {
  "components/features/ObjectInspector.tsx#onClose":
    "backdrop; the panel's Close button is the keyboard path",
  "components/features/objects/SaveViewModal.tsx#onClose":
    "backdrop; the modal's Cancel button is the keyboard path",
  "components/layout/CommandPalette.tsx#() => setIsOpen(false)":
    "backdrop; Escape closes the palette",
  "app/upload/page.tsx#() => !isLocked && fileInputRef.current?.click()":
    "drop zone; the Browse files button inside it is the keyboard path",
  "components/features/objects/ObjectTableRow.tsx#(e) => onToggleSelect(obj.objectId, e, obj.key)":
    "row click; the row's checkbox is the keyboard path",
  "components/features/objects/ObjectTableRow.tsx#(e) => { e.stopPropagation(); onStartInlineEdit(obj); }":
    "tag cell; the row menu's Edit tags is the keyboard path",
  "components/ui/ChipInput.tsx#() => inputRef.current?.focus()":
    "focuses the input inside, which is itself reachable",
};

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return e.name === GENERATED ? [] : sources(p);
    return e.name.endsWith(".tsx") && !e.name.endsWith(".test.tsx") ? [p] : [];
  });
}

const attrName = (a: ts.JsxAttributeLike, sf: ts.SourceFile) =>
  ts.isJsxAttribute(a) ? a.name.getText(sf) : "...";

/** A handler whose whole body is e.stopPropagation() does nothing itself. */
function onlyStopsPropagation(handler: string): boolean {
  return /^\(?\w+\)? => (\{ )?\w+\.stopPropagation\(\);?( \})?$/.test(handler);
}

/** Whether an expression inside a control can render a name. */
function exprNames(e: ts.Expression, sf: ts.SourceFile): boolean {
  if (ts.isParenthesizedExpression(e)) return exprNames(e.expression, sf);
  if (ts.isConditionalExpression(e))
    return exprNames(e.whenTrue, sf) || exprNames(e.whenFalse, sf);
  if (
    ts.isBinaryExpression(e) &&
    e.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken
  )
    return exprNames(e.right, sf);
  if (ts.isJsxElement(e) || ts.isJsxSelfClosingElement(e))
    return elementNames(e, sf);
  if (
    e.kind === ts.SyntaxKind.NullKeyword ||
    e.kind === ts.SyntaxKind.FalseKeyword ||
    (ts.isIdentifier(e) && e.text === "undefined")
  )
    return false;
  // A string, a variable, a call: it may well be text.
  return true;
}

/** Whether a JSX element inside a control can render a name. */
function elementNames(
  el: ts.JsxElement | ts.JsxSelfClosingElement,
  sf: ts.SourceFile,
): boolean {
  const opening = ts.isJsxElement(el) ? el.openingElement : el;
  if (ICON_TAG.test(opening.tagName.getText(sf))) return false;
  const attrs = opening.attributes.properties;
  if (attrs.some((a) => NAMING_ATTRS.has(attrName(a, sf)))) return true;
  // Any other component may render text of its own.
  if (ts.isJsxSelfClosingElement(el)) return true;
  return hasContent(el, sf);
}

/**
 * Whether anything under a control can give it a name: text, an expression
 * that can render text, or props spread from the caller.
 */
function hasContent(node: ts.JsxElement, sf: ts.SourceFile): boolean {
  if (node.openingElement.attributes.properties.some(ts.isJsxSpreadAttribute))
    return true;
  return node.children.some((c) => {
    if (ts.isJsxText(c)) return c.text.trim() !== "";
    if (ts.isJsxExpression(c))
      return !!c.expression && exprNames(c.expression, sf);
    if (ts.isJsxElement(c) || ts.isJsxSelfClosingElement(c))
      return elementNames(c, sf);
    return ts.isJsxFragment(c);
  });
}

function findings() {
  const clickOnly: string[] = [];
  const unnamed: string[] = [];
  for (const file of sources(SRC)) {
    const sf = ts.createSourceFile(
      file,
      readFileSync(file, "utf8"),
      ts.ScriptTarget.Latest,
      true,
      ts.ScriptKind.TSX,
    );
    const rel = relative(SRC, file);
    const at = (n: ts.Node) =>
      `${rel}:${sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1}`;
    const visit = (node: ts.Node) => {
      const opening = ts.isJsxElement(node)
        ? node.openingElement
        : ts.isJsxSelfClosingElement(node)
          ? node
          : null;
      if (opening) {
        const tag = opening.tagName.getText(sf);
        const attrs = opening.attributes.properties;
        const names = attrs.map((a) => attrName(a, sf));
        const onClick = attrs.find(
          (a): a is ts.JsxAttribute =>
            ts.isJsxAttribute(a) && a.name.getText(sf) === "onClick",
        );
        if (
          onClick?.initializer &&
          /^[a-z]/.test(tag) &&
          !CONTROLS.has(tag) &&
          !names.includes("role") &&
          !names.includes("onKeyDown")
        ) {
          const handler = onClick.initializer
            .getText(sf)
            .replace(/^\{|\}$/g, "")
            .replace(/\s+/g, " ")
            .trim();
          const key = `${rel}#${handler}`;
          if (!onlyStopsPropagation(handler)) clickOnly.push(key);
        }
        if (
          ts.isJsxElement(node) &&
          NAMED_BY_CONTENT.test(tag) &&
          !names.includes("asChild")
        ) {
          const named =
            names.some((n) => NAMING_ATTRS.has(n)) || hasContent(node, sf);
          if (!named) unnamed.push(at(node));
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return { clickOnly, unnamed };
}

describe("keyboard and screen-reader reach", () => {
  const { clickOnly, unnamed } = findings();

  it("no click handler sits on an element the keyboard cannot reach", () => {
    expect(
      clickOnly.filter((k) => !(k in ALLOWED)),
      "use a button, or add role and onKeyDown",
    ).toEqual([]);
  });

  it("every allowed entry still names a handler", () => {
    expect(Object.keys(ALLOWED).filter((k) => !clickOnly.includes(k))).toEqual(
      [],
    );
  });

  it("no icon-only button is left without a name", () => {
    expect(unnamed, "add an aria-label saying what it does").toEqual([]);
  });
});
