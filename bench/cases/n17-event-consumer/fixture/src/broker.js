export function createBroker() {
  const queue = [];
  const inflight = new Map();
  let seq = 0;
  return {
    publish(body) {
      queue.push({ id: ++seq, body });
    },
    receive() {
      const m = queue.shift();
      if (!m) return null;
      inflight.set(m.id, m);
      return m;
    },
    ack(id) {
      inflight.delete(id);
    },
    restart() {
      queue.unshift(...inflight.values());
      inflight.clear();
    },
    size() {
      return queue.length;
    },
  };
}
