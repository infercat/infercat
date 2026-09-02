import { describe, expect, it } from 'vitest';
import { hostsComputer, privacyLine } from './product';

// 020 promise 9: "Max’s laptop’s computer" reads as a template that forgot to branch.
describe('whose computer', () => {
  it('uses the possessive for a person and "the computer named" for a machine', () => {
    expect(hostsComputer('Max')).toBe('Max’s computer');
    expect(hostsComputer("Max's laptop")).toBe("the computer named Max's laptop");
    expect(hostsComputer('Max’s workstation')).toBe('the computer named Max’s workstation');
    expect(hostsComputer('desk')).toBe('desk’s computer');
    expect(hostsComputer('gpu-box')).toBe('the computer named gpu-box');
    expect(hostsComputer('')).toBe('your host’s computer');
  });

  it('never says it twice in the privacy line', () => {
    expect(privacyLine("Max's laptop", false)).not.toContain('laptop’s computer');
    expect(privacyLine("Max's laptop", false)).toContain("the computer named Max's laptop");
  });
});
