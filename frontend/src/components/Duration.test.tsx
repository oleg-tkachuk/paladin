import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@/test/utils";

import { Duration, UNTIL_NOW } from "./Duration";

const at = (seconds: number) => ({ seconds: BigInt(seconds), nanos: 0 });
const NOW_S = 1_000_000;

describe("Duration", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW_S * 1000);
  });
  afterEach(() => vi.useRealTimers());

  it("reads the span between two timestamps", () => {
    const { container } = render(<Duration from={at(0)} to={at(3 * 86_400)} />);
    expect(container).toHaveTextContent("3 d");
  });

  it("measures up to now for a process still open", () => {
    const { container } = render(
      <Duration from={at(NOW_S - 90)} to={UNTIL_NOW} />,
    );
    expect(container).toHaveTextContent("1 min 30 s");
  });

  it("reads a dash when an end is missing", () => {
    const { container } = render(<Duration from={at(0)} to={undefined} />);
    expect(container).toHaveTextContent("—");
  });
});
