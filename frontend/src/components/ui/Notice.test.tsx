import { beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";

import { render, screen } from "@/test/utils";
import { noticeKey, type Notice } from "@/lib/notice";

const h = vi.hoisted(() => ({
  stored: [] as string[],
  update: vi.fn(),
}));
vi.mock("@/hooks/usePreference", () => ({
  usePreference: () => [h.stored, vi.fn(), h.update],
}));

import { NoticeBanner, NoticeIcon } from "./Notice";
import { TooltipProvider } from "./Tooltip";

const NOTICE: Notice = {
  content: "Maintenance on Sunday from 02:00 UTC",
  severity: "warning",
  url: "https://status.example.com",
  permanent: false,
};

beforeEach(() => {
  h.stored = [];
  h.update.mockReset();
  h.update.mockResolvedValue(undefined);
});

describe("NoticeBanner", () => {
  it("says the notice, how loud it is, and where to read more", () => {
    render(<NoticeBanner notice={NOTICE} />);
    const banner = screen.getByRole("status");
    expect(banner).toHaveTextContent(
      "Warning: Maintenance on Sunday from 02:00 UTC",
    );
    expect(banner).toHaveAttribute("data-severity", "warning");
    const link = screen.getByRole("link", { name: /read more/i });
    expect(link).toHaveAttribute("href", "https://status.example.com");
    expect(link).toHaveAttribute("target", "_blank");
  });

  it("links a page of the console in place", () => {
    render(<NoticeBanner notice={{ ...NOTICE, url: "/trash" }} />);
    const link = screen.getByRole("link", { name: /read more/i });
    expect(link).toHaveAttribute("href", "/trash");
    expect(link).not.toHaveAttribute("target");
  });

  it("goes when dismissed, and remembers it under what it says", async () => {
    render(<NoticeBanner notice={NOTICE} />);
    await userEvent.click(
      screen.getByRole("button", { name: "Dismiss notice" }),
    );
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(h.update).toHaveBeenCalledTimes(1);
    const change = h.update.mock.calls[0][0] as (raw: unknown) => unknown;
    expect(change(["older"])).toEqual(["older", noticeKey(NOTICE)]);
  });

  // The save failing costs the dismissal on the next visit, not this one.
  it("stays dismissed for the visit when the settings cannot be saved", async () => {
    h.update.mockRejectedValue(new Error("settings unavailable"));
    render(<NoticeBanner notice={NOTICE} />);
    await userEvent.click(
      screen.getByRole("button", { name: "Dismiss notice" }),
    );
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("stays away once dismissed, until what it says changes", () => {
    h.stored = [noticeKey(NOTICE)];
    const { rerender } = render(<NoticeBanner notice={NOTICE} />);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    rerender(
      <NoticeBanner notice={{ ...NOTICE, content: "Moved to Monday" }} />,
    );
    expect(screen.getByRole("status")).toHaveTextContent("Moved to Monday");
  });

  it("cannot be dismissed when permanent, nor hidden by an old dismissal", () => {
    const permanent = { ...NOTICE, permanent: true };
    h.stored = [noticeKey(permanent)];
    render(<NoticeBanner notice={permanent} />);
    expect(screen.getByRole("status")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Dismiss notice" }),
    ).not.toBeInTheDocument();
  });
});

describe("NoticeIcon", () => {
  it("reads the notice to a screen reader and takes focus for its tooltip", () => {
    render(
      <TooltipProvider>
        <NoticeIcon notice={{ ...NOTICE, severity: "critical" }} />
      </TooltipProvider>,
    );
    const icon = screen.getByRole("img", {
      name: "Critical: Maintenance on Sunday from 02:00 UTC",
    });
    expect(icon).toHaveAttribute("tabindex", "0");
  });
});
