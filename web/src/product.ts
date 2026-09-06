// The product name lives here and nowhere else on the TS side (pm/BELIEFS.md).
// The Go side's copy is internal/product/product.go; keep the two in step by hand.
export const PRODUCT_NAME = 'Infercat';
export const INVITE_PREFIX = 'ic1';
// What the page says about itself to a search engine or a social card (index.html and the web app
// manifest are filled from this line by vite.config.ts). Same words as the connect screen.
export const DESCRIPTION =
  'Chat with a friend’s GPU. They send you one code; you paste it here. No account, no install.';
// Where the code lives: the About line links here. MIT (LICENSE at the repo root).
export const SOURCE_URL = 'https://github.com/2185Lab/infercat';
// The build stamps VITE_APP_VERSION from internal/product/product.go (see the Makefile), so the
// app and the host binary report the same version and a bug report can name both halves. A bare
// `vite build` or `vite dev` has no stamp and says so by falling back to the source default.
export const VERSION: string = import.meta.env.VITE_APP_VERSION ?? '0.0.1-dev';

/**
 * The privacy promise, in one sentence, in one place (014 promise 7). The connect screen, the empty
 * state and Settings each said something true and different, which reads as three promises rather
 * than one. `logging` is the `--log-prompts` variant: same three surfaces, opposite fact, said once.
 */
export function privacyLine(hostName: string, logging: boolean): string {
  const whose = hostsComputer(hostName);
  if (logging) {
    return `Encrypted end-to-end from your device to ${whose} — but this host has prompt logging on, so everything you send and everything the model answers is written to a log on their machine.`;
  }
  return `Encrypted end-to-end from your device to ${whose} — the relay in between can’t read it. ${PRODUCT_NAME} records counts, never text. The model runs on their machine.`;
}

/**
 * "Max’s computer" when the host is a person, "the computer named Max’s laptop" when the host has
 * already named a machine — "Max’s laptop’s computer" reads as a template that forgot to branch
 * (020 promise 9). A nameless host is "your host’s computer".
 */
export function hostsComputer(hostName: string): string {
  const name = hostName.trim();
  if (name === '') return 'your host’s computer';
  return /(’s|'s|s’|s')$|\b(laptop|desktop|computer|machine|workstation|server|pc|mac|box|rig)$/i.test(name)
    ? `the computer named ${name}`
    : `${name}’s computer`;
}
