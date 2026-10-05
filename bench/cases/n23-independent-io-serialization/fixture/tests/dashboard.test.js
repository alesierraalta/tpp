import { test } from "node:test";
import assert from "node:assert/strict";
import { DashboardLoader } from "../src/dashboard.js";

function fakeClient(log = []) {
  return {
    profile() {
      log.push("profile");
      return Promise.resolve({ name: "ada" });
    },
    orders() {
      log.push("orders");
      return Promise.resolve([{ id: "o1" }, { id: "o2" }]);
    },
    activity() {
      log.push("activity");
      return Promise.resolve([{ kind: "login" }]);
    },
  };
}

test("load resolves with the three panels", async () => {
  const dash = await new DashboardLoader(fakeClient()).load();
  assert.deepEqual(dash, {
    profile: { name: "ada" },
    orders: [{ id: "o1" }, { id: "o2" }],
    activity: [{ kind: "login" }],
  });
});

test("every source is asked exactly once, in panel order", async () => {
  const log = [];
  await new DashboardLoader(fakeClient(log)).load();
  assert.deepEqual(log, ["profile", "orders", "activity"]);
});

test("the dashboard object keeps its three panel keys", async () => {
  const dash = await new DashboardLoader(fakeClient()).load();
  assert.deepEqual(Object.keys(dash), ["profile", "orders", "activity"]);
});

test("the client's answers are used verbatim", async () => {
  const client = {
    profile: async () => "P",
    orders: async () => "O",
    activity: async () => "A",
  };
  const dash = await new DashboardLoader(client).load();
  assert.deepEqual(dash, { profile: "P", orders: "O", activity: "A" });
});
