import { useEffect } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";

vi.mock("next/navigation", () => ({
  usePathname: () => "/health",
  useSearchParams: () => new URLSearchParams(),
  useRouter: () => ({ push: vi.fn() }),
}));
vi.mock("@/context/StatsContext", () => ({
  useStats: () => ({ stats: null }),
}));
// The page polls; one call on mount is all a render test needs.
vi.mock("@/hooks/useVisiblePolling", () => ({
  useVisiblePolling: (fn: () => void) => {
    useEffect(() => {
      fn();
    }, [fn]);
  },
}));

import HealthPage from "./page";

const component = (name: string, critical = false) => ({
  name,
  status: "healthy",
  latency_ms: 0,
  critical,
});
let payload: unknown;
const fivePayload = {
  roles: ["api", "admin", "worker", "dispatcher", "mcp"].map((role) => ({
    role,
    status: "healthy",
    components: [component("postgres", true), component("iam_listener", true)],
  })),
};

beforeEach(() => {
  payload = fivePayload;
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify(payload))),
  );
});
afterEach(() => vi.unstubAllGlobals());

const roleCard = async (role: string) =>
  (await screen.findByText(role)).closest('[data-slot="card"]')!;

describe("HealthPage role cards", () => {
  // A fixed column count squeezed five cards into four columns and cut
  // component names short ("capabili…", "iam_list…"). Columns now come from
  // the cards' minimum width, so a card is never narrower than its widest
  // row, and they fold to one on a narrow page.
  it("lays the cards out by their minimum width, not a column count", async () => {
    render(<HealthPage />);
    const grid = (await roleCard("dispatcher")).parentElement!;
    expect(grid.className).toContain(
      "grid-cols-[repeat(auto-fit,minmax(min(100%,23rem),1fr))]",
    );
    expect(grid.className).not.toMatch(/(^|\s)(md|lg|xl):grid-cols-/);
    expect(grid.children).toHaveLength(fivePayload.roles.length);
  });

  // The card's default gap and padding stacked on the header's own left a
  // blank band between a role's name and its components.
  it("adds no card gap between the header and the rows", async () => {
    render(<HealthPage />);
    const card = await roleCard("api");
    expect(card.classList).toContain("gap-0");
    expect(card.classList).toContain("py-0");
  });
});

// A component off by configuration was a green "Healthy 0ms" row with a
// "disabled" note under it and a chevron that revealed that note again.
describe("HealthPage disabled component", () => {
  beforeEach(() => {
    payload = {
      roles: [
        {
          role: "api",
          status: "healthy",
          components: [
            component("postgres", true),
            {
              name: "postgres-replica",
              status: "disabled",
              latency_ms: 0,
              critical: false,
            },
          ],
        },
      ],
    };
  });

  it("shows it as disabled, with no latency and nothing to expand", async () => {
    render(<HealthPage />);
    const row = (await screen.findByText("postgres-replica")).closest(
      "button",
    )!;
    expect(row).toHaveTextContent("Disabled");
    expect(row).not.toHaveTextContent("Healthy");
    expect(row).not.toHaveTextContent("ms");
    expect(row).not.toHaveAttribute("aria-expanded");
    expect(row.querySelector("svg")).toBeNull(); // no chevron
  });

  it("leaves it out of the component count", async () => {
    render(<HealthPage />);
    const card = await roleCard("api");
    expect(card).toHaveTextContent("1/1");
  });
});

// Each row says what switches it, and a disabled one says why it is off.
describe("HealthPage component switch", () => {
  beforeEach(() => {
    payload = {
      roles: [
        {
          role: "dispatcher",
          status: "healthy",
          components: [
            { ...component("postgres", true), control: "always_on" },
            {
              name: "rabbitmq",
              status: "disabled",
              message:
                "not in use: no enabled subscription delivers to rabbitmq",
              latency_ms: 0,
              critical: false,
              control: "database",
            },
            {
              name: "postgres-replica",
              status: "disabled",
              message:
                "off by configuration: datastores.postgres.replica.enabled",
              latency_ms: 0,
              critical: false,
              control: "config",
            },
          ],
        },
      ],
    };
  });

  const row = async (name: string) =>
    (await screen.findByText(name)).closest("button")!;

  it("badges a component switched by the database or by config", async () => {
    render(<HealthPage />);
    expect(await row("rabbitmq")).toHaveTextContent("db");
    expect(await row("postgres-replica")).toHaveTextContent("config");
  });

  it("badges nothing on a component with no switch", async () => {
    render(<HealthPage />);
    const postgres = await row("postgres");
    expect(postgres).not.toHaveTextContent("db");
    expect(postgres).not.toHaveTextContent("config");
  });

  it("shows why a disabled component is off, with nothing to expand", async () => {
    render(<HealthPage />);
    const r = await row("rabbitmq");
    expect(
      screen.getByText(
        "not in use: no enabled subscription delivers to rabbitmq",
      ),
    ).toBeInTheDocument();
    expect(r).toHaveTextContent("Disabled");
    expect(r).not.toHaveAttribute("aria-expanded");
    expect(r.querySelector("svg")).toBeNull();
  });
});

// The pool's use and the schema version are details: their own line under
// the row, not a message to expand.
describe("HealthPage component details", () => {
  beforeEach(() => {
    payload = {
      roles: [
        {
          role: "api",
          status: "healthy",
          components: [
            {
              ...component("postgres", true),
              details: [
                { name: "connections", value: "3 of 20 in use" },
                { name: "idle", value: "2" },
              ],
            },
          ],
        },
      ],
    };
  });

  it("lists each detail as name and value, in order", async () => {
    render(<HealthPage />);
    const row = (await screen.findByText("postgres")).closest("button")!;
    const list = row.parentElement!.querySelector("dl")!;
    expect(list).toHaveTextContent("connections:3 of 20 in useidle:2");
  });

  it("offers nothing to expand for details alone", async () => {
    render(<HealthPage />);
    const row = (await screen.findByText("postgres")).closest("button")!;
    expect(row).not.toHaveAttribute("aria-expanded");
    expect(row.querySelector("svg")).toBeNull();
  });
});

describe("HealthPage component message", () => {
  const withMessage = (message: string) => ({
    roles: [
      {
        role: "api",
        status: "degraded",
        components: [
          {
            name: "cache",
            status: "unhealthy",
            message,
            latency_ms: 3,
            critical: false,
          },
        ],
      },
    ],
  });

  it("offers no expansion for a message that fits its line", async () => {
    payload = withMessage("connection refused");
    render(<HealthPage />);
    const row = (await screen.findByText("cache")).closest("button")!;
    expect(row).not.toHaveAttribute("aria-expanded");
    expect(row.querySelector("svg")).toBeNull(); // no chevron
  });

  it("offers to expand a message its line cannot show", async () => {
    payload = withMessage("dial tcp: timeout\n  at retry 1\n  at retry 2");
    render(<HealthPage />);
    const row = (await screen.findByText("cache")).closest("button")!;
    expect(row).toHaveAttribute("aria-expanded", "false");
    expect(row.querySelector("svg")).not.toBeNull(); // the chevron
  });
});
