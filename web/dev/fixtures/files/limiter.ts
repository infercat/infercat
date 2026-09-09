export const BURST_MULTIPLIER = 2;
export function canSpend(tokens: number, limit: number) {
  return tokens <= limit * BURST_MULTIPLIER;
}
