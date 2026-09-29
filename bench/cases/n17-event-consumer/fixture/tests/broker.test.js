import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createBroker } from '../src/broker.js';

test('messages are received in publish order', () => {
  const b = createBroker();
  b.publish({ n: 1 });
  b.publish({ n: 2 });
  assert.equal(b.receive().body.n, 1);
  assert.equal(b.receive().body.n, 2);
  assert.equal(b.receive(), null);
});

test('an acknowledged message is not delivered again', () => {
  const b = createBroker();
  b.publish({ n: 1 });
  b.ack(b.receive().id);
  b.restart();
  assert.equal(b.size(), 0);
});

test('restart returns unacknowledged messages to the queue', () => {
  const b = createBroker();
  b.publish({ n: 1 });
  b.receive();
  b.restart();
  assert.equal(b.size(), 1);
  assert.equal(b.receive().body.n, 1);
});
