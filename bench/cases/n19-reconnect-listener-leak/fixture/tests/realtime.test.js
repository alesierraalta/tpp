import { test } from "node:test";
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { RealtimeClient } from "../src/realtime.js";

function client() {
  const bus = new EventEmitter();
  return { bus, rt: new RealtimeClient(bus) };
}

test("connect opens the session listener and disconnect takes it away", () => {
  const { bus, rt } = client();
  rt.connect();
  assert.equal(bus.listenerCount("message"), 1);
  rt.disconnect();
  assert.equal(bus.listenerCount("message"), 0);
});

test("messages are recorded while the session is open", () => {
  const { bus, rt } = client();
  rt.connect();
  bus.emit("message", "ping");
  assert.equal(rt.last, "ping");
});

test("nothing is recorded once the session is closed", () => {
  const { bus, rt } = client();
  rt.connect();
  rt.disconnect();
  bus.emit("message", "late");
  assert.equal(rt.last, null);
});

test("a subscriber receives bus messages", () => {
  const { bus, rt } = client();
  const seen = [];
  rt.subscribe((m) => seen.push(m));
  bus.emit("message", "hi");
  assert.deepEqual(seen, ["hi"]);
});

test("a reconnecting session keeps recording messages", () => {
  const { bus, rt } = client();
  rt.connect();
  rt.reconnect();
  bus.emit("message", "after reconnect");
  assert.equal(rt.last, "after reconnect");
});

test("an unsubscribed handler is detached and receives nothing more", () => {
  const { bus, rt } = client();
  const seen = [];
  const handler = (m) => seen.push(m);
  rt.subscribe(handler);
  assert.equal(bus.listenerCount("message"), 1);
  rt.unsubscribe(handler);
  assert.equal(bus.listenerCount("message"), 0);
  bus.emit("message", "hi");
  assert.deepEqual(seen, []);
});

test("subscribing the same handler twice still leaves nothing after one unsubscribe", () => {
  const { bus, rt } = client();
  const seen = [];
  const handler = (m) => seen.push(m);
  rt.subscribe(handler);
  rt.subscribe(handler);
  assert.equal(bus.listenerCount("message"), 1);
  rt.unsubscribe(handler);
  assert.equal(bus.listenerCount("message"), 0);
  bus.emit("message", "hi");
  assert.deepEqual(seen, []);
});
