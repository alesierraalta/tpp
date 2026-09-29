import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createBroker } from '../src/broker.js';
import { createConsumer } from '../src/consumer.js';

function setup() {
  const broker = createBroker();
  return { broker, consumer: createConsumer(broker) };
}

test('a payment adds to the paid total', () => {
  const { broker, consumer } = setup();
  broker.publish({ type: 'payment.captured', eventId: 'e1', orderId: 'o1', amount: 40 });
  consumer.drain();
  assert.equal(consumer.getOrder('o1').paid, 40);
});

test('payments accumulate per order', () => {
  const { broker, consumer } = setup();
  broker.publish({ type: 'payment.captured', eventId: 'e1', orderId: 'o1', amount: 40 });
  broker.publish({ type: 'payment.captured', eventId: 'e2', orderId: 'o1', amount: 10 });
  broker.publish({ type: 'payment.captured', eventId: 'e3', orderId: 'o2', amount: 5 });
  consumer.drain();
  assert.equal(consumer.getOrder('o1').paid, 50);
  assert.equal(consumer.getOrder('o2').paid, 5);
});

test('status events in order end at the latest status', () => {
  const { broker, consumer } = setup();
  broker.publish({ type: 'order.status', eventId: 'e1', orderId: 'o1', status: 'paid', version: 1 });
  broker.publish({ type: 'order.status', eventId: 'e2', orderId: 'o1', status: 'shipped', version: 2 });
  consumer.drain();
  assert.deepEqual(consumer.getOrder('o1'), { paid: 0, status: 'shipped', version: 2 });
});

test('an unknown event type is acknowledged and counted as ignored', () => {
  const { broker, consumer } = setup();
  broker.publish({ type: 'order.archived', eventId: 'e1', orderId: 'o1' });
  assert.equal(consumer.drain(), 1);
  assert.equal(consumer.stats.ignored, 1);
  assert.equal(broker.size(), 0);
});

test('an unknown order reads as new', () => {
  const { consumer } = setup();
  assert.deepEqual(consumer.getOrder('nope'), { paid: 0, status: 'new', version: 0 });
});
