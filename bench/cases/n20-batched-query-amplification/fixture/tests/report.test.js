import { test } from "node:test";
import assert from "node:assert/strict";
import { ReportService } from "../src/report.js";

const orders = [
  { id: "o1", status: "paid", total: 30 },
  { id: "o2", status: "pending", total: 12 },
  { id: "o3", status: "paid", total: 7 },
  { id: "o4", status: "pending", total: 5 },
];

const items = [
  { orderId: "o1", sku: "a" },
  { orderId: "o2", sku: "b" },
  { orderId: "o1", sku: "c" },
  { orderId: "o4", sku: "e" },
];

function countingRepo() {
  return {
    calls: 0,
    orders(filter) {
      this.calls++;
      return orders.filter((o) => !filter.status || o.status === filter.status);
    },
    itemsFor(id) {
      this.calls++;
      return items.filter((i) => i.orderId === id);
    },
    itemsForOrders(ids) {
      this.calls++;
      return items.filter((i) => ids.includes(i.orderId));
    },
  };
}

test("lists every order with its items", () => {
  const svc = new ReportService(countingRepo());
  assert.deepEqual(svc.listOrders({}), [
    { id: "o1", status: "paid", total: 30, items: [{ orderId: "o1", sku: "a" }, { orderId: "o1", sku: "c" }] },
    { id: "o2", status: "pending", total: 12, items: [{ orderId: "o2", sku: "b" }] },
    { id: "o3", status: "paid", total: 7, items: [] },
    { id: "o4", status: "pending", total: 5, items: [{ orderId: "o4", sku: "e" }] },
  ]);
});

test("applies the filter before filling items", () => {
  const svc = new ReportService(countingRepo());
  const rows = svc.listOrders({ status: "paid" });
  assert.deepEqual(rows.map((r) => r.id), ["o1", "o3"]);
  assert.deepEqual(rows[0].items, [{ orderId: "o1", sku: "a" }, { orderId: "o1", sku: "c" }]);
});

test("a filter that matches nothing comes back empty", () => {
  const svc = new ReportService(countingRepo());
  assert.deepEqual(svc.listOrders({ status: "cancelled" }), []);
});

test("statusCounts tallies the orders by status", () => {
  const svc = new ReportService(countingRepo());
  assert.deepEqual(svc.statusCounts(), { paid: 2, pending: 2 });
});

test("repeated statusCounts answers are equal", () => {
  const svc = new ReportService(countingRepo());
  assert.deepEqual(svc.statusCounts(), svc.statusCounts());
});

test("repeated statusCounts reads the repository only once", () => {
  const repo = countingRepo();
  const svc = new ReportService(repo);
  svc.statusCounts();
  svc.statusCounts();
  assert.equal(repo.calls, 1);
});
