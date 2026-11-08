import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    include: ["packages/**/src/**/*.test.{ts,tsx}", "plugins/**/src/**/*.test.ts"],
    exclude: ["**/node_modules/**", "**/dist/**", ...(process.env.PREPARE_DEV_PACKAGE_TESTS ? [] : ["packages/cli/src/distribution.test.ts"])],
    testTimeout: 60_000,
    hookTimeout: 60_000,
  },
});
