import { useEffect } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@/test/utils";

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

// A card by its title: a role's name also labels its pool in the database card.
const roleCard = async (role: string) =>
  (
    await screen.findByText(
      (_, el) =>
        el?.getAttribute("data-slot") === "card-title" &&
        el.textContent === role,
    )
  ).closest('[data-slot="card"]')!;

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

  // Why a component is off is the expected state, not news: it was a line
  // under every disabled row. It now waits behind the row's chevron.
  it("folds why a disabled component is off behind its chevron", async () => {
    render(<HealthPage />);
    const reason = "not in use: no enabled subscription delivers to rabbitmq";
    const r = await row("rabbitmq");
    expect(r).toHaveTextContent("Disabled");
    expect(screen.queryByText(reason)).not.toBeInTheDocument();
    expect(r).toHaveAttribute("aria-expanded", "false");
    expect(r.querySelector("svg")).not.toBeNull();

    fireEvent.click(r);
    expect(r).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText(reason)).toBeInTheDocument();
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
                { name: "in use", value: "3/20" },
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
    expect(list).toHaveTextContent("in use3/20idle2");
  });

  it("offers nothing to expand for details alone", async () => {
    render(<HealthPage />);
    const row = (await screen.findByText("postgres")).closest("button")!;
    expect(row).not.toHaveAttribute("aria-expanded");
    expect(row.querySelector("svg")).toBeNull();
  });
});

// Every role reported the same schema version and replica state, each in its
// own card. They are gathered into one database card; a role card keeps one
// row saying whether that role reaches the database.
describe("HealthPage database card", () => {
  const db = (name: string, extra: object = {}) => ({
    name,
    status: "healthy",
    latency_ms: 1,
    category: "database",
    critical: true,
    ...extra,
  });
  beforeEach(() => {
    payload = {
      roles: ["api", "worker"].map((role, i) => ({
        role,
        status: "healthy",
        checked_at: "2026-10-08T18:00:00Z",
        components: [
          db("postgres", {
            details: [
              { name: "in use", value: `${i + 3}/20` },
              { name: "idle", value: `${i + 7}` },
            ],
          }),
          db("postgres-schema", {
            details: [{ name: "schema applied", value: "56" }],
          }),
          db("postgres-replica", {
            status: "disabled",
            critical: false,
            message:
              "off by configuration: datastores.postgres.replica.enabled",
          }),
          component("outbox", true),
        ],
      })),
    };
  });

  it("gathers the database components into one card", async () => {
    render(<HealthPage />);
    const card = await roleCard("database");
    for (const name of ["postgres", "postgres-schema", "postgres-replica"]) {
      expect(card).toHaveTextContent(name);
    }
    // Said once, being the same for every role.
    expect(card.textContent!.match(/schema applied/g)).toHaveLength(1);
    // Each role's own pool: a table, a role to a column, a fact to a row,
    // so the same fact lines up across roles.
    const table = card.querySelector("table")!;
    const rows = Array.from(table.querySelectorAll("tr"), (tr) =>
      Array.from(tr.children, (c) => c.textContent),
    );
    expect(rows).toEqual([
      ["", "api", "worker"],
      ["in use", "3/20", "4/20"],
      ["idle", "7", "8"],
    ]);
  });

  it("leaves a role card one database row, online or offline", async () => {
    render(<HealthPage />);
    const card = await roleCard("api");
    expect(card).not.toHaveTextContent("postgres-schema");
    expect(card).not.toHaveTextContent("postgres-replica");
    expect(card).not.toHaveTextContent("connections");
    const pg = Array.from(card.querySelectorAll("button")).find((b) =>
      b.textContent!.startsWith("postgres"),
    )!;
    expect(pg).toHaveTextContent("Online");
    expect(card).toHaveTextContent("outbox");
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
