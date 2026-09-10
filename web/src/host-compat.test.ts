import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { hostAudio, hostImages, logsPrompts, modelVision, type Me } from './api';
import old from './fixtures/me/0.1.0.json';
import current from './fixtures/me/current.json';

const source = ts.createSourceFile('contract.ts', readFileSync(new URL('../../packages/client/src/contract.ts', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true);
const me = source.statements.find((s): s is ts.InterfaceDeclaration => ts.isInterfaceDeclaration(s) && s.name.text === 'Me')!;
const host = (me.members.find((m) => m.name?.getText(source) === 'host') as ts.PropertySignature).type as ts.TypeLiteralNode;
// Explicit extension inventory (082's size-1 alternative to a generated Go schema).
const optional = ['audio', 'images', 'log_prompts', 'vision'];

describe('shipped host capabilities', () => {
  it('keeps additive fields optional and requires an explicit inventory update', () => {
    expect(host.members.filter((m) => (m as ts.PropertySignature).questionToken).map((m) => m.name?.getText(source)).sort()).toEqual(optional);
    for (const m of host.members) {
      if (!(m.name!.getText(source) in old.host)) expect((m as ts.PropertySignature).questionToken).toBeDefined();
    }
    expect(Object.keys(current.host).sort()).toEqual(['audio', 'images', 'log_prompts', 'models', 'name', 'relay', 'upstream', 'vision']);
  });
  it('requires current fixture coverage for Go wire field names', () => {
    const proxy = readFileSync(new URL('../../internal/gateway/proxy.go', import.meta.url), 'utf8');
    const wire = proxy.split('type meResponse struct {')[1]!.split('\nfunc ')[0]!;
    const names = [...wire.matchAll(/json:"([^",]+)[^"]*"/g)].map((m) => m[1]);
    const fields = new Set<string>();
    function collect(value: unknown): void {
      if (value && typeof value === 'object' && !Array.isArray(value)) {
        for (const [key, child] of Object.entries(value)) { fields.add(key); collect(child); }
      }
    }
    collect(current);
    expect(current.usage.today_images).toBeGreaterThan(0); // omitempty cannot capture an explicit zero
    for (const name of names) expect(fields.has(name!), `capture current /me field ${name}`).toBe(true);
  });
  it('defaults absent capabilities honestly, retaining explicit false and unknown', () => {
    expect(modelVision(old as Me, 'compat-model')).toBeNull();
    expect(hostAudio(old as Me, 'transcriptions')).toBeNull();
    expect(hostAudio(old as Me, 'speech')).toBeNull();
    expect(hostImages(old as Me)).toBeNull();
    expect(hostImages(current as Me)?.model).toBe('FLUX.2-klein-4B-Q8_0');
    expect(modelVision(current as Me, 'compat-model')).toBe(true);
    expect(modelVision(current as Me, 'missing')).toBeNull();
    const me = { ...current, host: { ...current.host, vision: { text: false, unknown: null }, audio: { transcriptions: 'asr-model', speech: null } } } as Me;
    expect(modelVision(me, 'text')).toBe(false);
    expect(modelVision(me, 'unknown')).toBeNull();
    expect(hostAudio(me, 'transcriptions')).toBe('asr-model');
    expect(hostAudio(me, 'speech')).toBeNull();
    expect(logsPrompts({ ...me, host: { ...me.host, log_prompts: undefined } })).toBe(false);
  });
  // Building the full TypeScript program and resolving types is compiler work, not a unit lookup.
  it('keeps capability property reads inside the API accessor boundary', () => {
    const configPath = new URL('../tsconfig.json', import.meta.url).pathname;
    const config = ts.readConfigFile(configPath, ts.sys.readFile);
    const parsed = ts.parseJsonConfigFileContent(config.config, ts.sys, new URL('../', import.meta.url).pathname);
    const program = ts.createProgram(parsed.fileNames, parsed.options);
    const checker = program.getTypeChecker();
    const isHost = (n: ts.Node): boolean => ['name', 'upstream', 'models', 'relay'].every((key) => checker.getTypeAtLocation(n).getProperty(key));
    for (const tree of program.getSourceFiles()) {
      if (!tree.fileName.includes('/web/src/') || tree.fileName.endsWith('/api.ts') || tree.fileName.endsWith('.test.ts')) continue;
      function visit(n: ts.Node): void {
        if (ts.isPropertyAccessExpression(n) && isHost(n.expression)) expect(optional.includes(n.name.text), `${tree.fileName}: use a capability accessor`).toBe(false);
        if (ts.isElementAccessExpression(n) && isHost(n.expression)) expect(ts.isStringLiteral(n.argumentExpression) && !optional.includes(n.argumentExpression.text), `${tree.fileName}: use a capability accessor`).toBe(true);
        if (ts.isObjectBindingPattern(n) && isHost(n)) for (const item of n.elements) expect(optional.includes((item.propertyName ?? item.name).getText(tree)), `${tree.fileName}: use a capability accessor`).toBe(false);
        ts.forEachChild(n, visit);
      }
      visit(tree);
    }
  }, 30_000);
});
