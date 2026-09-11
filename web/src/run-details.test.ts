import { expect, it } from 'vitest';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import fixture from '../dev/testdata/run-details.json';
import type { RunRecord } from './api';
import { runItem, applyRunStep } from './runs';
import { runThroughput, runView } from './ui/RunItem';
import RunRow from './ui/Run';
import { diffPreview, isDiffOutput } from './ui/RunDiff';
const record = () => structuredClone(fixture) as RunRecord;
const diff = '--- a/notes.md\n+++ b/notes.md\n@@ -1,3 +1,3 @@\n same\n-old\n+new\n tail\n';
it('recognises only the explicit diff descriptor, not MIME or result text alone', () => {
  expect(isDiffOutput({ id:'x',name:'x',size:1,mime:'text/x-diff' })).toBe(false);
  expect(isDiffOutput({ id:'x',name:'x',size:1,kind:'diff',mime:'text/plain' })).toBe(false);
  expect(isDiffOutput({ ...record().outputs![0]!,mime:'application/json' })).toBe(false);
  expect(isDiffOutput(record().outputs![0]!)).toBe(true);
  expect(runView({ ...record(),outputs:[] }).diffOutputs).toEqual([]);
});
it('counts hunk additions/removals without treating file headers or header-like contents as metadata', () => {
  const parsed=diffPreview(diff.replace(/\n/g,'\r\n'));
  expect(parsed).toMatchObject({ added:1,removed:1,total:7 });
  expect(parsed.preview.map(l=>l.kind)).toEqual(['context','context','context','context','remove','add','context']);
  expect(diffPreview('--- a\n+++ b\n@@ -1 +1 @@\n--- old\n+++ new\n')).toMatchObject({added:1,removed:1});
  expect(diffPreview('--- a\n+++ b\n@@ -0,0 +1,2 @@\n+a\n+b\n')).toMatchObject({added:2,removed:0});
});
it('bounds rendered rows to twelve while keeping full counts and preserving raw characters', () => {
  const text='--- a\n+++ b\n@@ -0,0 +1,1000 @@\n'+Array.from({length:1000},(_,i)=>`+<script>${i}</script>`).join('\n');
  const parsed=diffPreview(text);expect(parsed.preview).toHaveLength(12);expect(parsed.total).toBe(1003);expect(parsed.added).toBe(1000);
  expect(parsed.preview[3]!.text).toBe('+<script>0</script>');
});
it('weights host generation intervals rather than averaging rates or using run age', () => {
  expect(runThroughput(record())).toBe(25);
  expect(runThroughput({...record(),updated:'2026-09-19T00:00:00Z'})).toBe(25);
  expect(runThroughput(runItem(record()).run!)).toBe(25);
  expect(runItem(record()).run!.outputs![0]).toMatchObject({kind:'diff',mime:'text/x-diff'});
});
it('omits missing, invalid, uncertain and unsettled measurements, including legacy persisted rows', () => {
  for(const patch of [{total_ms:undefined},{ttft_ms:undefined},{ttft_ms:0},{ttft_ms:3000},{total_ms:NaN},{completion_tokens:Infinity},{completion_tokens:-1}]) {
    const r=record();Object.assign(r.attempts[0]!.usage,patch);expect(runThroughput(r)).toBeUndefined();
  }
  for(const patch of [{settled:false},{accounting_uncertain:true}]){const r=record();Object.assign(r.attempts[0]!,patch);expect(runThroughput(r)).toBeUndefined();}
  expect(runThroughput({...record(),attempts:[]})).toBeUndefined();expect(runThroughput({...record(),state:'running'})).toBeUndefined();
});
it('settled meta displays measured throughput, including measured zero, without an unknown label', () => {
  const html=(r:RunRecord)=>renderToStaticMarkup(createElement(RunRow,{run:runView(r),host:'desk',connected:true,disabled:false,pending:false,onCancel(){},onAnswer(){},onRetry(){}}));
  expect(html(record())).toContain('25 tok/s');expect(html({...record(),attempts:[]})).not.toContain('tok/s');
  const zero=record();zero.attempts.forEach(a=>a.usage.completion_tokens=0);expect(html(zero)).toContain('0.0 tok/s');
});
it('full replacement steps and persisted descriptors retain the diff association', () => {
  const r=record(), step={...r.steps![0]!,result:'updated'};
  const next=applyRunStep(r,{cursor:'e:2',run_id:r.id,time:r.updated,state:'running',type:'step',step});
  expect(next.steps).toHaveLength(1);expect(runView(runItem(next).run!).steps[0]!.output_id).toBe('diff-1');
});
