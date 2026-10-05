import { test } from "node:test";
import assert from "node:assert/strict";
import { RuleMatcher } from "../src/matcher.js";

const rules = [
  { region: "eu", channel: "web", route: "eu-web" },
  { region: "us", channel: "app", route: "us-app" },
  { region: "eu", channel: "app", route: "eu-app" },
];

test("matches the first rule with the same region and channel", () => {
  const matcher = new RuleMatcher(rules);
  assert.equal(matcher.match({ region: "eu", channel: "web" }).route, "eu-web");
  assert.equal(matcher.match({ region: "us", channel: "app" }).route, "us-app");
  assert.equal(matcher.match({ region: "eu", channel: "app" }).route, "eu-app");
});

test("an unrouted record answers null", () => {
  const matcher = new RuleMatcher(rules);
  assert.equal(matcher.match({ region: "ap", channel: "web" }), null);
  assert.equal(matcher.match({ region: "eu", channel: "sms" }), null);
});

test("the first rule with a repeated key wins", () => {
  const duplicated = [...rules, { region: "eu", channel: "web", route: "eu-web-2" }];
  const matcher = new RuleMatcher(duplicated);
  assert.equal(matcher.match({ region: "eu", channel: "web" }).route, "eu-web");
});

test("matchAll answers one result per record, in record order", () => {
  const matcher = new RuleMatcher(rules);
  const records = [
    { region: "us", channel: "app" },
    { region: "ap", channel: "web" },
    { region: "eu", channel: "web" },
  ];
  assert.deepEqual(
    matcher.matchAll(records).map((rule) => (rule === null ? null : rule.route)),
    ["us-app", null, "eu-web"],
  );
});

test("matching records its work on the injected counter", () => {
  const counter = { count: 0 };
  const matcher = new RuleMatcher(rules, counter);
  assert.equal(counter.count, 0);
  matcher.match({ region: "eu", channel: "web" });
  assert.ok(counter.count > 0, "the matcher reports the rules it examined");
});

test("a 100-record batch over a 100-rule set is routed correctly", () => {
  const manyRules = [];
  for (let i = 0; i < 99; i++) {
    manyRules.push({ region: `r${i}`, channel: "web", route: `route-${i}` });
  }
  manyRules.push({ region: "eu", channel: "web", route: "route-eu" });
  const records = [];
  for (let i = 0; i < 100; i++) records.push({ region: "eu", channel: "web" });
  const matched = new RuleMatcher(manyRules).matchAll(records);
  assert.equal(matched.length, 100);
  assert.ok(matched.every((rule) => rule.route === "route-eu"));
});
