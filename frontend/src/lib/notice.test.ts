import { describe, expect, it } from "vitest";

import {
  isNoticeLabel,
  noticeFromLabels,
  noticeKey,
  noticeUrl,
  readDismissed,
  readNotice,
  withDismissed,
  withNoticeLabels,
} from "./notice";

// Spelled out rather than imported: these are the keys operators write, and a
// change to them is a change to every resource that carries one.
const NOTICE_LABELS = {
  content: "notice.paladin.io/content",
  severity: "notice.paladin.io/severity",
  url: "notice.paladin.io/url",
  permanent: "notice.paladin.io/permanent",
};

describe("readNotice", () => {
  it("is null when there is nothing to say", () => {
    expect(readNotice({})).toBeNull();
    expect(readNotice({ content: "   ", severity: "critical" })).toBeNull();
  });

  it("reads every field", () => {
    expect(
      readNotice({
        content: " Read-only until Friday ",
        severity: "Critical",
        url: "https://status.example.com/incident/7",
        permanent: "TRUE",
      }),
    ).toEqual({
      content: "Read-only until Friday",
      severity: "critical",
      url: "https://status.example.com/incident/7",
      permanent: true,
    });
  });

  // A typo in a severity should still show the notice, at its quietest.
  it("takes an unknown severity as info", () => {
    expect(readNotice({ content: "x", severity: "urgent" })?.severity).toBe(
      "info",
    );
  });

  it("is dismissible unless the flag says true", () => {
    expect(readNotice({ content: "x", permanent: "yes" })?.permanent).toBe(
      false,
    );
    expect(readNotice({ content: "x", permanent: true })?.permanent).toBe(true);
  });

  // The text comes from a label any tenant editor can write and is shown to
  // every reader, so the link cannot be a script or leave by a side door.
  it.each([
    ["javascript:alert(1)"],
    ["//evil.example.com"],
    ["/\\evil.example.com"],
    ["ftp://files.example.com"],
    ["not a url"],
  ])("drops the link %s", (url) => {
    expect(readNotice({ content: "x", url })).not.toHaveProperty("url");
  });

  it("keeps a link to a page of the console", () => {
    expect(readNotice({ content: "x", url: "/trash" })?.url).toBe("/trash");
  });

  it("cuts content that would fill a page", () => {
    expect(readNotice({ content: "a".repeat(2000) })?.content).toHaveLength(
      500,
    );
  });
});

describe("noticeUrl", () => {
  it("keeps what a notice may link to, trimmed", () => {
    expect(noticeUrl(" https://status.example.com ")).toBe(
      "https://status.example.com",
    );
    expect(noticeUrl("/tenants/acme")).toBe("/tenants/acme");
    expect(noticeUrl("javascript:void(0)")).toBeUndefined();
    expect(noticeUrl("")).toBeUndefined();
  });
});

describe("notice labels", () => {
  it("knows its own keys", () => {
    for (const key of Object.values(NOTICE_LABELS)) {
      expect(isNoticeLabel(key)).toBe(true);
    }
    expect(isNoticeLabel("team")).toBe(false);
    expect(isNoticeLabel("notice.paladin.io/other")).toBe(false);
  });

  it("reads a resource's notice from its labels", () => {
    expect(
      noticeFromLabels({
        team: "storage",
        [NOTICE_LABELS.content]: "Migrating to the new backend",
        [NOTICE_LABELS.severity]: "warning",
      }),
    ).toEqual({
      content: "Migrating to the new backend",
      severity: "warning",
      permanent: false,
    });
    expect(noticeFromLabels({ team: "storage" })).toBeNull();
    expect(noticeFromLabels(undefined)).toBeNull();
  });

  it("writes a notice and keeps every other label", () => {
    const labels = withNoticeLabels(
      { team: "storage", [NOTICE_LABELS.url]: "https://old.example.com" },
      { content: "Frozen for audit", severity: "info", permanent: true },
    );
    // The default severity is left out, and so is the link the new notice
    // does not have.
    expect(labels).toEqual({
      team: "storage",
      [NOTICE_LABELS.content]: "Frozen for audit",
      [NOTICE_LABELS.permanent]: "true",
    });
    expect(noticeFromLabels(labels)).toEqual({
      content: "Frozen for audit",
      severity: "info",
      permanent: true,
    });
  });

  it("takes the notice out", () => {
    expect(
      withNoticeLabels(
        {
          team: "storage",
          [NOTICE_LABELS.content]: "x",
          [NOTICE_LABELS.severity]: "critical",
        },
        null,
      ),
    ).toEqual({ team: "storage" });
  });
});

describe("noticeKey", () => {
  const notice = {
    content: "Down at 02:00",
    severity: "info",
    permanent: false,
  } as const;

  it("names the same notice the same way", () => {
    expect(noticeKey(notice)).toBe(noticeKey({ ...notice }));
    expect(noticeKey(notice)).toMatch(/^[0-9a-f]{8}$/);
  });

  // A dismissed notice shows again once what it says changes.
  it("names a changed notice differently", () => {
    const key = noticeKey(notice);
    expect(noticeKey({ ...notice, content: "Down at 03:00" })).not.toBe(key);
    expect(noticeKey({ ...notice, severity: "critical" })).not.toBe(key);
    expect(
      noticeKey({ ...notice, url: "https://status.example.com" }),
    ).not.toBe(key);
  });
});

describe("dismissed notices", () => {
  it("reads only keys out of what the settings hold", () => {
    expect(readDismissed(["a", 1, null, "b"])).toEqual(["a", "b"]);
    expect(readDismissed({ a: 1 })).toEqual([]);
    expect(readDismissed(undefined)).toEqual([]);
  });

  it("adds a key once, newest last, and forgets the oldest", () => {
    expect(withDismissed(["a", "b"], "a")).toEqual(["b", "a"]);
    const many = Array.from({ length: 50 }, (_, i) => `k${i}`);
    const next = withDismissed(many, "new");
    expect(next).toHaveLength(50);
    expect(next[0]).toBe("k1");
    expect(next.at(-1)).toBe("new");
  });
});
