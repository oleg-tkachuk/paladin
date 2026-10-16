import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import {
  BucketTenancyBadge,
  GRANTED_BUCKET_LABEL,
  OWNED_BUCKET_LABEL,
} from "./BucketTenancyBadge";

// ADR-0032: a bucket on a tenant's list is either its own or granted to it;
// no bucket is open to every tenant.
describe("BucketTenancyBadge", () => {
  it.each([
    { owned: true, label: OWNED_BUCKET_LABEL, not: GRANTED_BUCKET_LABEL },
    { owned: false, label: GRANTED_BUCKET_LABEL, not: OWNED_BUCKET_LABEL },
  ])("owned=$owned reads $label", ({ owned, label, not }) => {
    render(<BucketTenancyBadge owned={owned} />);
    expect(screen.getByText(label)).toBeInTheDocument();
    expect(screen.queryByText(not)).not.toBeInTheDocument();
  });

  it("does not call a granted bucket shared with everyone", () => {
    render(<BucketTenancyBadge owned={false} />);
    expect(screen.getByText(GRANTED_BUCKET_LABEL)).toHaveAttribute(
      "title",
      expect.stringMatching(/granted/),
    );
  });
});
