import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { loadState, saveState, recordJob } from '../src/store.js';

function tmpFile() {
  return path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'js-')), 'state.json');
}

test('a missing file loads as empty state', () => {
  assert.deepEqual(loadState(tmpFile()), { jobs: {} });
});

test('save then load round-trips', () => {
  const p = tmpFile();
  saveState(p, { jobs: { a: 'done' } });
  assert.deepEqual(loadState(p), { jobs: { a: 'done' } });
});

test('recordJob adds and updates jobs', () => {
  const p = tmpFile();
  recordJob(p, 'a', 'running');
  recordJob(p, 'b', 'pending');
  recordJob(p, 'a', 'done');
  assert.deepEqual(loadState(p), { jobs: { a: 'done', b: 'pending' } });
});

test('a corrupt state file is rejected on load', () => {
  const p = tmpFile();
  fs.writeFileSync(p, '{"jobs": {');
  assert.throws(() => loadState(p));
});

test('a state file without jobs is rejected on load', () => {
  const p = tmpFile();
  fs.writeFileSync(p, '{}');
  assert.throws(() => loadState(p), /invalid state file/);
});
