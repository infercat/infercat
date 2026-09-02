import { readFileSync } from 'node:fs';
import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

// The product name lives in src/product.ts and nowhere else (pm/BELIEFS.md), including the
// <title> in index.html, which this plugin fills in.
const productName = /PRODUCT_NAME = '([^']+)'/.exec(
  readFileSync(new URL('./src/product.ts', import.meta.url), 'utf8'),
)?.[1];
const productNameHtml = {
  name: 'product-name-html',
  transformIndexHtml: (html: string) => html.replaceAll('%PRODUCT_NAME%', productName ?? 'app'),
};

// Dev ports live in 49000-49999 and are overridable (WEB_PORT / FAKE_GATEWAY_PORT).
const webPort = Number(process.env.WEB_PORT ?? 49173);
const gatewayPort = Number(process.env.FAKE_GATEWAY_PORT ?? 49090);

export default defineConfig({
  plugins: [react(), productNameHtml],
  server: { host: '127.0.0.1', port: webPort, strictPort: true },
  preview: { host: '127.0.0.1', port: webPort + 1, strictPort: true },
  define: {
    // Default target for Direct mode in dev: the fake gateway. Overridden by VITE_DIRECT_URL.
    __DEFAULT_DIRECT_URL__: JSON.stringify(
      process.env.VITE_DIRECT_URL ?? `http://127.0.0.1:${gatewayPort}`,
    ),
  },
  build: { target: 'es2022', chunkSizeWarningLimit: 900 },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
    reporters: ['default'],
  },
});
