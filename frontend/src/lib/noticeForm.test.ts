import { describe, expect, it } from "vitest";

import { noticeFromLabels } from "@/lib/notice";

import {
  hasNotice,
  labelsWithNoticeForm,
  noticeFormFor,
  noticeLinkError,
  sameLabels,
  type NoticeForm,
} from "./noticeForm";

const form = (over: Partial<NoticeForm> = {}): NoticeForm => ({
  notice: "",
  severity: "info",
  url: "",
  permanent: false,
  ...over,
});

describe("noticeForm", () => {
  it("round-trips a notice through the labels, keeping the others", () => {
    const written = form({
      notice: "Read-only",
      severity: "critical",
      url: "/trash",
      permanent: true,
    });
    const labels = labelsWithNoticeForm({ team: "storage" }, written);
    expect(labels.team).toBe("storage");
    expect(noticeFormFor(labels)).toEqual(written);
    expect(noticeFromLabels(labels)?.severity).toBe("critical");
  });

  it("removes the notice's labels when the form says nothing", () => {
    const carried = labelsWithNoticeForm({}, form({ notice: "x" }));
    expect(labelsWithNoticeForm(carried, form())).toEqual({});
    expect(noticeFormFor(undefined)).toEqual(form());
  });

  it("refuses a link the console would drop, only with a notice to say", () => {
    expect(
      noticeLinkError(form({ notice: "x", url: "javascript:alert(1)" })),
    ).not.toBeNull();
    expect(noticeLinkError(form({ notice: "x", url: "/trash" }))).toBeNull();
    expect(noticeLinkError(form({ url: "javascript:alert(1)" }))).toBeNull();
    expect(hasNotice(form({ notice: "  " }))).toBe(false);
  });

  it("compares labels in any order", () => {
    expect(sameLabels({ a: "1", b: "2" }, { b: "2", a: "1" })).toBe(true);
    expect(sameLabels({ a: "1" }, { a: "2" })).toBe(false);
    expect(sameLabels({ a: "1" }, { a: "1", b: "2" })).toBe(false);
  });
});
