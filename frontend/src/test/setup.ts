// jsdom test setup — loaded by the "dom" vitest project (see vitest.config.ts).
// Registers @testing-library/jest-dom matchers (toBeInTheDocument, …) and
// unmounts React trees after each test so state doesn't leak between cases
// (vitest does not auto-cleanup with globals:false).
import "@testing-library/jest-dom/vitest";
import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

afterEach(() => {
  cleanup();
});
