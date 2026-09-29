import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createClock } from '../src/clock.js';
import { createLedgerServer } from '../src/ledgerServer.js';
import { createClient } from '../src/client.js';

function setup(opts = {}) {
  const clock = createClock();
  const server = createLedgerServer(clock);
  const sleep = async (ms) => clock.advance(ms);
  const client = createClient({ transport: server.transport, clock, sleep, ...opts });
  return { clock, server, client };
}

test('a GET returns the response body', async () => {
  const { client } = setup();
  const r = await client.send('GET', '/charges');
  assert.deepEqual(r, { ok: true, status: 200, data: { count: 0 } });
});

test('a POST creates one charge', async () => {
  const { client, server } = setup();
  const r = await client.send('POST', '/charges', { amount: 25 });
  assert.equal(r.ok, true);
  assert.equal(server.charges.length, 1);
});

test('a 503 is retried and the call then succeeds with one charge', async () => {
  const { client, server } = setup();
  server.script(['503']);
  const r = await client.send('POST', '/charges', { amount: 25 });
  assert.equal(r.ok, true);
  assert.equal(server.calls, 2);
  assert.equal(server.charges.length, 1);
});

test('attempts are capped at maxAttempts', async () => {
  const { client, server } = setup();
  server.always('503');
  const r = await client.send('GET', '/charges');
  assert.equal(r.ok, false);
  assert.equal(server.calls, 3);
});

test('backoff doubles between attempts', async () => {
  const { client, server, clock } = setup();
  server.script(['503', '503']);
  await client.send('GET', '/charges');
  assert.equal(clock.now(), 300);
});

test('a POST carries an idempotency key', async () => {
  const { client, server } = setup();
  await client.send('POST', '/charges', { amount: 25 });
  assert.ok(server.charges[0].key);
});

test('a call whose upstream never answers reports failure', async () => {
  const { client, server } = setup();
  server.always('hang');
  const r = await client.send('GET', '/charges', undefined, { deadlineMs: 1500 });
  assert.equal(r.ok, false);
});
