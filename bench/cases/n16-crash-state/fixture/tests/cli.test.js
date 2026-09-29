import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { run } from '../src/cli.js';

function sink() {
  let buf = '';
  return { write: (s) => { buf += s; }, text: () => buf };
}

function setup() {
  const file = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'js-cli-')), 'state.json');
  return { env: { JOB_STATE_FILE: file }, out: sink(), err: sink(), file };
}

test('mark then list prints jobs sorted by id', () => {
  const ctx = setup();
  assert.equal(run(['mark', 'b', 'done'], ctx), 0);
  assert.equal(run(['mark', 'a', 'running'], ctx), 0);
  assert.equal(run(['list'], ctx), 0);
  assert.equal(ctx.out.text(), 'a running\nb done\n');
});

test('list on a fresh directory prints nothing and succeeds', () => {
  const ctx = setup();
  assert.equal(run(['list'], ctx), 0);
  assert.equal(ctx.out.text(), '');
});

test('an unknown status is a usage error', () => {
  const ctx = setup();
  assert.equal(run(['mark', 'a', 'sleeping'], ctx), 2);
  assert.match(ctx.err.text(), /usage/);
});

test('a missing id is a usage error', () => {
  const ctx = setup();
  assert.equal(run(['mark'], ctx), 2);
});

test('an unknown command is a usage error', () => {
  const ctx = setup();
  assert.equal(run(['frobnicate'], ctx), 2);
});
