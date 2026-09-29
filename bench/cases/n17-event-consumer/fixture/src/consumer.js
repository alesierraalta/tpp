export function createConsumer(broker) {
  const orders = new Map();
  const stats = { handled: 0, ignored: 0 };

  function order(id) {
    if (!orders.has(id)) orders.set(id, { paid: 0, status: 'new', version: 0 });
    return orders.get(id);
  }

  function apply(ev) {
    const o = order(ev.orderId);
    switch (ev.type) {
      case 'payment.captured':
        o.paid += ev.amount;
        stats.handled++;
        return;
      case 'order.status':
        o.status = ev.status;
        o.version = ev.version;
        stats.handled++;
        return;
      default:
        stats.ignored++;
    }
  }

  function pump() {
    const m = broker.receive();
    if (!m) return false;
    apply(m.body);
    broker.ack(m.id);
    return true;
  }

  function drain() {
    let n = 0;
    while (pump()) n++;
    return n;
  }

  function getOrder(id) {
    return { ...order(id) };
  }

  return { pump, drain, getOrder, stats };
}
