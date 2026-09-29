export function encodeOrder(order) {
  return JSON.stringify({
    v: 2,
    id: order.id,
    totalCents: order.totalCents,
    currency: order.currency,
  });
}

export function decodeOrder(json) {
  const msg = JSON.parse(json);
  return {
    id: msg.id,
    totalCents: msg.v === 2 ? msg.totalCents : Math.round(msg.total * 100),
    currency: msg.currency,
  };
}
