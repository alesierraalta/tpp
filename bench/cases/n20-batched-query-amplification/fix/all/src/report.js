export class ReportService {
  constructor(repo) {
    this.repo = repo;
    this.countsCache = new Map();
  }

  // Orders for the report page, each with its items filled in.
  listOrders(filter) {
    const orders = this.repo.orders(filter);
    const items = this.repo.itemsForOrders(orders.map((order) => order.id));
    const byOrder = new Map();
    for (const item of items) {
      if (!byOrder.has(item.orderId)) byOrder.set(item.orderId, []);
      byOrder.get(item.orderId).push(item);
    }
    return orders.map((order) => ({
      ...order,
      items: byOrder.get(order.id) ?? [],
    }));
  }

  // How many orders sit in each status.
  statusCounts() {
    const key = "status-counts";
    if (this.countsCache.has(key)) return this.countsCache.get(key);
    const counts = {};
    for (const order of this.repo.orders({})) {
      counts[order.status] = (counts[order.status] ?? 0) + 1;
    }
    this.countsCache.set(key, counts);
    return counts;
  }
}
