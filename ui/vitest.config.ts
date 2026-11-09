import { defineConfig } from "vitest/config";

// The UI tests render components to static markup; they need no browser.
export default defineConfig({
  test: { include: ["src/**/*.test.{ts,tsx}"] },
});
