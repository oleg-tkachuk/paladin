import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";

import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ICON_CHIP_TONE, IconChip, IconTone, alertTone } from "./IconChip";

const Glyph = (props: React.SVGProps<SVGSVGElement>) => (
  <svg data-testid="glyph" {...props} />
);

describe("IconChip", () => {
  it.each(Object.values(IconTone))(
    "paints the %s tone's surface and ink",
    (tone) => {
      const { container } = render(<IconChip icon={Glyph} tone={tone} />);
      const chip = container.querySelector('[data-slot="icon-chip"]');
      const { surface, ink } = ICON_CHIP_TONE[tone];
      expect(chip).toHaveClass(...surface.split(" "), ink);
      expect(chip).toHaveAttribute("data-tone", tone);
    },
  );

  it("is decoration: the title beside it names the card", () => {
    const { container } = render(<IconChip icon={Glyph} />);
    expect(container.firstElementChild).toHaveAttribute("aria-hidden", "true");
  });

  it("defaults to the accent", () => {
    const { container } = render(<IconChip icon={Glyph} />);
    expect(container.firstElementChild).toHaveAttribute(
      "data-tone",
      IconTone.Accent,
    );
  });
});

describe("alertTone", () => {
  it.each([
    [0, 0, IconTone.Success],
    [0, 2, IconTone.Warning],
    [1, 0, IconTone.Danger],
    [1, 5, IconTone.Danger],
  ])("%i at fault and %i near it is %s", (critical, warning, tone) => {
    expect(alertTone(critical, warning)).toBe(tone);
  });
});

// Every card that names its section does it with a CardTitle and an icon on
// a chip. A bare icon, a page's own square of colour, or a hand-written
// heading is how the console drifted into a dozen treatments of one thing.
describe("card titles", () => {
  const SRC = join(__dirname, "..", "..");
  const files = readdirSync(SRC, { recursive: true, encoding: "utf8" }).filter(
    (f) => f.endsWith(".tsx") && !f.endsWith(".test.tsx"),
  );

  // Cards whose heading is not a section's name, each with its reason.
  const EXEMPT: [file: string, heading: string, why: string][] = [
    [
      "app/billing/page.tsx",
      "No tenant to show",
      "a message in place of the page",
    ],
    [
      "components/features/billing/TenantBilling.tsx",
      "Billing is unavailable",
      "a message in place of the page",
    ],
    [
      "components/features/billing/TenantBilling.tsx",
      "No charges in this period",
      "an empty state",
    ],
    [
      "components/features/billing/KPITile.tsx",
      "{label}",
      "a figure's label on a number tile",
    ],
    [
      "app/storage-backends/[backendId]/buckets/[bucketId]/lifecycle/page.tsx",
      "No lifecycle rules",
      "an empty state",
    ],
    ["app/login/page.tsx", "Paladin", "the sign-in form's brand"],
    [
      "app/oauth/consent/page.tsx",
      "Authorize access",
      "the consent form's question",
    ],
  ];
  const exempt = (file: string, heading: string) =>
    EXEMPT.some(([f, h]) => file === f && heading.includes(h));

  // The <Card>…</Card> blocks of a file, nested ones counted.
  function cards(text: string): string[] {
    const tag = /<Card[\s>]|<\/Card>/g;
    const blocks: string[] = [];
    const open: number[] = [];
    for (const m of text.matchAll(tag)) {
      if (m[0].startsWith("</")) {
        const start = open.pop();
        if (start !== undefined)
          blocks.push(text.slice(start, m.index + m[0].length));
      } else {
        open.push(m.index);
      }
    }
    return blocks;
  }

  const HEADING = /<(CardTitle|h[1-4])\b[^>]*>([\s\S]*?)<\/\1>/;
  const BARE_ICON = /<[A-Z]\w*Icon\s+className=/;

  it("name their section with an icon on a chip", () => {
    const offenders = files.flatMap((file) =>
      cards(readFileSync(join(SRC, file), "utf8")).flatMap((block) => {
        const heading = HEADING.exec(block);
        if (heading === null) return [];
        const text = heading[2].replace(/\s+/g, " ").trim();
        if (exempt(file, text)) return [];
        const before = block.slice(0, heading.index);
        const chipped =
          (heading[1] === "CardTitle" && /\sicon=\{/.test(heading[0])) ||
          before.includes("<IconChip");
        const bare = BARE_ICON.test(heading[0]);
        return chipped && !bare ? [] : [`${file}: ${text.slice(0, 40)}`];
      }),
    );
    expect(offenders).toEqual([]);
  });

  // The header band, and the rule under it, come with CardHeader: a title
  // laid straight onto the card sits on the body's colour.
  it("hold their titles in a CardHeader", () => {
    const offenders = files.flatMap((file) =>
      cards(readFileSync(join(SRC, file), "utf8"))
        .filter(
          (block) =>
            block.includes("<CardTitle") && !block.includes("<CardHeader"),
        )
        .map((block) => `${file}: ${block.slice(0, 60)}`),
    );
    expect(offenders).toEqual([]);
  });

  it("exempt only cards that are still there", () => {
    const stale = EXEMPT.filter(([file, heading]) => {
      try {
        return !readFileSync(join(SRC, file), "utf8").includes(heading);
      } catch {
        return true;
      }
    });
    expect(stale).toEqual([]);
  });
});
