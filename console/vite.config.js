import { defineConfig } from 'vite';
import { readFileSync } from 'node:fs';

// Standalone builds and Makefile builds use the same product-owned version.
const product = readFileSync(new URL('../internal/product/product.go', import.meta.url), 'utf8');
const version = process.env.VITE_APP_VERSION || product.match(/Version\s*=\s*"([^"]+)"/)[1];
export default defineConfig({ define: { 'import.meta.env.VITE_APP_VERSION': JSON.stringify(version) } });
