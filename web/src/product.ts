// The product name lives here and nowhere else on the TS side (docs/PRINCIPLES.md).
// The Go side's copy is internal/product/product.go; keep the two in step by hand.
export const PRODUCT_NAME = 'Infercat';
export const INVITE_PREFIX = 'ic1';
// What the page says about itself to a search engine or a social card (index.html and the web app
// manifest are filled from this line by vite.config.ts). Same words as the connect screen.
export const DESCRIPTION =
  'Chat with a friend’s GPU. They send you one code; you paste it here. No account, no install.';
// Where the code lives: the About line links here. MIT (LICENSE at the repo root).
export const SOURCE_URL = 'https://github.com/infercat/infercat';
// Where a stranger with no code is sent: the README's host quickstart, which is the whole answer to
// "how do I get one of these". The header link and the card's no-code line are the same destination
// and so are one constant, until there is a docs site to point at instead (039).
export const HOST_URL = `${SOURCE_URL}#quickstart-host`;
// The build stamps VITE_APP_VERSION from internal/product/product.go (see the Makefile), so the
// app and the host binary report the same version and a bug report can name both halves. A bare
// `vite build` or `vite dev` has no stamp and says so by falling back to the source default.
export const VERSION: string = import.meta.env.VITE_APP_VERSION ?? '0.0.1-dev';
