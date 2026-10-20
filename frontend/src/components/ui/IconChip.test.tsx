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

// A card title's icon goes on a chip. A bare icon there, or a page's own
// square of colour around one, is how the console drifted into a dozen
// treatments of the same thing.
describe("card titles", () => {
  const SRC = join(__dirname, "..", "..");
  const files = readdirSync(SRC, { recursive: true, encoding: "utf8" }).filter(
    (f) => f.endsWith(".tsx") && !f.endsWith(".test.tsx"),
  );
  const BARE_ICON = /<[A-Z]\w*Icon\s+className=/;

  it("put their icons on a chip", () => {
    const offenders = files.flatMap((f) => {
      const text = readFileSync(join(SRC, f), "utf8");
      return [...text.matchAll(/<CardTitle[\s\S]*?<\/CardTitle>/g)]
        .filter((m) => BARE_ICON.test(m[0]))
        .map(() => f);
    });
    expect(offenders).toEqual([]);
  });
});
