// The product name lives here and nowhere else on the TS side (pm/BELIEFS.md).
// The Go side's copy is internal/product/product.go; keep the two in step by hand.
export const PRODUCT_NAME = 'Bunny Network';
export const INVITE_PREFIX = 'bn1';
// The build stamps VITE_APP_VERSION from internal/product/product.go (see the Makefile), so the
// app and the host binary report the same version and a bug report can name both halves. A bare
// `vite build` or `vite dev` has no stamp and says so by falling back to the source default.
export const VERSION: string = import.meta.env.VITE_APP_VERSION ?? '0.0.1-dev';
