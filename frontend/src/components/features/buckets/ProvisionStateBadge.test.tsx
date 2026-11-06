import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { STATUS_BADGE_VARIANT } from "@/components/ui/StatusBadge";
import { PROVISION_STATES, provisionStatus } from "@/lib/bucketProvision";
import { ProvisionStateBadge } from "./ProvisionStateBadge";

describe("ProvisionStateBadge", () => {
  it.each(PROVISION_STATES)(
    "names %s as the status card and the filter do",
    (state) => {
      const { label, tone } = provisionStatus(state);
      render(<ProvisionStateBadge state={state} />);
      const badge = screen.getByText(label);
      expect(badge).toHaveAttribute("data-tone", tone);
      expect(badge).toHaveAttribute("data-variant", STATUS_BADGE_VARIANT[tone]);
    },
  );

  it("reads a bucket from before the state was kept as ready", () => {
    render(<ProvisionStateBadge state="" />);
    expect(screen.getByText("Ready")).toHaveAttribute("data-tone", "success");
  });
});
