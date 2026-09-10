import test from 'node:test';
import assert from 'node:assert/strict';
import { Readable } from 'node:stream';
import { frames, MAX_FRAME, rewrite, wording } from './adapter.mjs';

const read = async chunks => Array.fromAsync(frames(Readable.from(chunks)));
test('private frames handle split input and multiple messages in one chunk', async () => {
  assert.deepEqual(await read(['{"type":', '"cancel"}\n{"id":1}\n']), [{type:'cancel'}, {id:1}]);
});
test('unterminated frames are bounded before JSON parsing', async () => {
  await assert.rejects(read([' '.repeat(MAX_FRAME / 2), ' '.repeat(MAX_FRAME / 2)]), /too large/);
});
test('incomplete and malformed frames end the generation', async () => {
  await assert.rejects(read(['{"id":1}']), /Incomplete/);
  await assert.rejects(read(['not-json\n']), SyntaxError);
});
test('129 wording changes descriptions only and leaves the native schema intact', () => {
  const input = {tools:[{name:'write',description:'Original',parameters:{type:'object',required:['file_path','content'],properties:{
    sandbox_permissions:{type:'string',enum:['workspace-write'],description:'old'},justification:{type:'string',description:'old'},
  }}}]};
  const saved = structuredClone(input), result = rewrite(input), write = result.tools[0];
  assert.deepEqual(input, saved);
  assert.equal(write.description, 'Original\n\n' + wording.write);
  for (const key of ['sandbox_permissions','justification']) assert.equal(write.parameters.properties[key].description, wording[key]);
  assert.deepEqual(write.parameters.required, ['file_path','content']);
  assert.deepEqual(write.parameters.properties.sandbox_permissions.enum, ['workspace-write']);
  assert.throws(() => rewrite({tools:[]}), /Pinned/);
});
