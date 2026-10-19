import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { create } from "@bufbuild/protobuf";

import { render, screen } from "@/test/utils";
import { PresignedUrlSchema } from "@/gen/paladin/common/v1/resource_pb";
import { ObjectSchema, ObjectState } from "@/gen/paladin/data/v1/types_pb";
import { TEXT_PREVIEW_MAX_BYTES } from "@/lib/objectPreview";

const h = vi.hoisted(() => ({ presignDownload: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  presignClient: { presignDownload: h.presignDownload },
}));

import { ObjectPreview, PREVIEW_UNAVAILABLE } from "./ObjectPreview";

const DOWNLOAD = create(PresignedUrlSchema, {
  url: "https://s3.test/b/key?sig=download",
});

const object = (contentType: string, size = 10) =>
  create(ObjectSchema, {
    name: "tenants/t/collections/c/objects/o",
    key: "docs/note.txt",
    contentType,
    sizeBytes: BigInt(size),
    state: ObjectState.AVAILABLE,
    resourceVersion: "1",
  });

const fetchMock = vi.fn();
beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
  fetchMock.mockReset();
  h.presignDownload.mockReset();
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe("ObjectPreview", () => {
  it("shows an image from the download URL", () => {
    render(
      <ObjectPreview object={object("image/png")} downloadUrl={DOWNLOAD} />,
    );
    expect(
      screen.getByRole("img", { name: /Preview of note.txt/ }),
    ).toHaveAttribute("src", DOWNLOAD.url);
  });

  // Markup is shown as its source: the text never becomes part of the page.
  it("reads a small text object and shows it as text", async () => {
    fetchMock.mockResolvedValue(new Response("<b>bold</b>"));
    render(
      <ObjectPreview object={object("text/html")} downloadUrl={DOWNLOAD} />,
    );
    expect(
      await screen.findByLabelText(/Contents of note.txt/),
    ).toHaveTextContent("<b>bold</b>");
    expect(fetchMock).toHaveBeenCalledWith(DOWNLOAD.url, expect.anything());
    expect(document.querySelector("b")).toBeNull();
  });

  it("does not read a text object past the limit", () => {
    render(
      <ObjectPreview
        object={object("text/csv", TEXT_PREVIEW_MAX_BYTES + 1)}
        downloadUrl={DOWNLOAD}
      />,
    );
    expect(fetchMock).not.toHaveBeenCalled();
    expect(screen.getByText("text/csv")).toBeInTheDocument();
  });

  it("says so when the store refuses the read", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 403 }));
    render(
      <ObjectPreview object={object("text/plain")} downloadUrl={DOWNLOAD} />,
    );
    expect(await screen.findByText(PREVIEW_UNAVAILABLE)).toBeInTheDocument();
  });

  // The download URL is signed as an attachment; a frame would save it.
  it("frames a PDF on a URL of its own, signed inline", async () => {
    const inline = "https://s3.test/b/key?sig=inline";
    h.presignDownload.mockResolvedValue({
      downloadUrl: create(PresignedUrlSchema, { url: inline }),
    });
    render(
      <ObjectPreview
        object={object("application/pdf")}
        downloadUrl={DOWNLOAD}
      />,
    );
    expect(await screen.findByTitle(/Preview of note.txt/)).toHaveAttribute(
      "src",
      inline,
    );
    expect(h.presignDownload).toHaveBeenCalledWith(
      expect.objectContaining({
        contentDisposition: expect.stringMatching(/^inline;/),
      }),
      expect.anything(),
    );
  });

  it("names the type of anything it cannot show", () => {
    render(
      <ObjectPreview
        object={object("application/octet-stream")}
        downloadUrl={DOWNLOAD}
      />,
    );
    expect(screen.getByText("application/octet-stream")).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(h.presignDownload).not.toHaveBeenCalled();
  });
});
