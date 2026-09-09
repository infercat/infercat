import { defineConfig } from "vitest/config";
import { fileURLToPath } from "node:url";
export default defineConfig({
  test: {
    alias: {
      "cloudflare:workers": fileURLToPath(
        new URL("./test/cloudflare.ts", import.meta.url),
      ),
    },
  },
});
