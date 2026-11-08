import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    include: ["packages/**/src/**/*.test.ts", "plugins/**/src/**/*.test.ts"],
    exclude: ["**/node_modules/**", "**/dist/**", "packages/ui/**"],
    testTimeout: 60_000,
    hookTimeout: 60_000,
  },
});
