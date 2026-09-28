import type { Browser, Page } from 'playwright';
export const CLOUDFLARE_BEACON_ORIGIN: string;
export function landingOrigins(base: string): string[];
export const ANSWER_SELECTOR: string;
export function completedAnswer(page: Page, timeout?: number): Promise<string>;
export function safeDiagnostic(value: unknown): string;
export function liveChat(browser: Browser, base: string, report?: (line: string) => void): Promise<void>;
