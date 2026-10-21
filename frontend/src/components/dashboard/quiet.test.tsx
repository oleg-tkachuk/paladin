import { useState } from "react";
import { describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@/test/utils";

import { ALL_CLEAR, QuietCards, useQuiet, type QuietCard } from "./quiet";

const FAILED: QuietCard = {
  id: "failedOperations",
  label: "No failed operations",
  href: "/operations",
};
const QUOTAS: QuietCard = {
  id: "quotas",
  label: "No quota near its limit",
  href: "/stats",
};

function Card({ card, items }: { card: QuietCard; items: number }) {
  if (useQuiet(card, items === 0)) return null;
  return <section aria-label={card.id}>{items} to look at</section>;
}

function Failures() {
  const [n, setN] = useState(0);
  return (
    <>
      <button onClick={() => setN(3)}>fail</button>
      <Card card={FAILED} items={n} />
    </>
  );
}

describe("QuietCards", () => {
  it("names the cards with nothing to report on one line, in order, and renders the rest", () => {
    render(
      <QuietCards>
        <Card card={QUOTAS} items={0} />
        <Card card={FAILED} items={2} />
      </QuietCards>,
    );
    const line = screen.getByRole("status");
    expect(line).toHaveTextContent(ALL_CLEAR);
    expect(line).toHaveTextContent(QUOTAS.label);
    expect(screen.getByRole("link", { name: QUOTAS.label })).toHaveAttribute(
      "href",
      "/stats",
    );
    expect(
      screen.getByRole("region", { name: "failedOperations" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "quotas" })).toBeNull();
  });

  it("brings a card back the moment it has something, and drops the line when none is quiet", () => {
    render(
      <QuietCards>
        <Failures />
      </QuietCards>,
    );
    expect(screen.getByRole("status")).toHaveTextContent(FAILED.label);
    fireEvent.click(screen.getByRole("button", { name: "fail" }));
    expect(
      screen.getByRole("region", { name: "failedOperations" }),
    ).toHaveTextContent("3 to look at");
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("leaves a card as it was outside QuietCards", () => {
    render(<Card card={QUOTAS} items={0} />);
    expect(screen.getByRole("region", { name: "quotas" })).toHaveTextContent(
      "0 to look at",
    );
  });

  it("lists the quiet cards in the dashboard's order, whatever order they report in", () => {
    render(
      <QuietCards>
        <Card card={QUOTAS} items={0} />
        <Card card={FAILED} items={0} />
      </QuietCards>,
    );
    const labels = screen.getAllByRole("link").map((l) => l.textContent);
    expect(labels).toEqual([FAILED.label, QUOTAS.label]);
  });
});
