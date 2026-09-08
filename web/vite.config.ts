import { readFileSync } from 'node:fs';
import type { Plugin } from 'vite';
import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

// The product name lives in src/product.ts and nowhere else (docs/PRINCIPLES.md). This plugin reads it
// (and the one-line description) out of that file and fills the <title>, the description and
// social-card metas in index.html, and the web app manifest, which it emits at build time and serves
// in dev — so a rename touches product.ts and the rendered pages follow. %WEB_URL% is the app's
// public address (Makefile: VITE_WEB_URL from product.go's WebURL); empty until hosting is decided,
// which leaves the social-card image root-relative.
const productSource = readFileSync(new URL('./src/product.ts', import.meta.url), 'utf8');
const productName = /PRODUCT_NAME = '([^']+)'/.exec(productSource)?.[1] ?? 'app';
const productDescription = /DESCRIPTION =\s*'([^']+)'/.exec(productSource)?.[1] ?? '';
const webURL = (process.env.VITE_WEB_URL ?? '').replace(/\/+$/, '');
const manifest = JSON.stringify(
  {
    name: productName,
    short_name: productName,
    description: productDescription,
    start_url: '/',
    scope: '/',
    display: 'standalone',
    // The page ground, from the light half of the token file (src/styles.css, 038).
    background_color: '#ffffff',
    theme_color: '#ffffff',
    icons: [
      { src: '/icon-192.png', sizes: '192x192', type: 'image/png' },
      { src: '/icon-512.png', sizes: '512x512', type: 'image/png' },
      { src: '/icon-maskable-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
    ],
  },
  null,
  2,
);
const MANIFEST = 'manifest.webmanifest';
const productNameHtml: Plugin = {
  name: 'product-name-html',
  transformIndexHtml: (html: string) =>
    html
      .replaceAll('%PRODUCT_NAME%', productName)
      .replaceAll('%PRODUCT_DESCRIPTION%', productDescription)
      .replaceAll('%WEB_URL%', webURL),
  generateBundle() {
    this.emitFile({ type: 'asset', fileName: MANIFEST, source: manifest });
  },
  configureServer(server) {
    server.middlewares.use((req, res, next) => {
      if (req.url !== `/${MANIFEST}`) return next();
      res.setHeader('content-type', 'application/manifest+json');
      res.end(manifest);
    });
  },
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
