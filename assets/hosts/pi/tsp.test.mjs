// Deterministic tests for the Pi extension's dispatch, in-session detection, observation, and UI.
// The registered-callback test mocks execFile; no real child process or ambient PATH is used.
// Run: node --test assets/hosts/pi/tsp.test.mjs
import assert from "node:assert/strict";
import { mock, test } from "node:test";
import childProcess from "node:child_process";
import { syncBuiltinESMExports } from "node:module";
import tppExtension, {
  GENTLE_OBSERVATION_ENV,
  GENTLE_OBSERVATION_VALUE,
  observesGentleSession,
  parseTppCommand,
  buildArgv,
  childEnv,
  runTppCommand,
} from "./tsp.ts";

// The literals internal/mode pins on the Go side: the shared protocol, spelled out on both shores.
const protocolEnv = "TPP_GENTLE_OBSERVATION";
const protocolValue = "pi-session-gentle-active";

test("observation protocol matches Go literals; registered commands invoke the shared CLI", async (t) => {
  assert.equal(GENTLE_OBSERVATION_ENV, protocolEnv);
  assert.equal(GENTLE_OBSERVATION_VALUE, protocolValue);
  const calls = [];
  const pi = {
    registerCommand: (name, options) => calls.push({ name, options }),
    getAllTools: () => [],
  };
  tppExtension(pi);
  assert.deepEqual(calls.map(({ name }) => name), ["tsp", "tpp"]);
  assert.match(calls[0].options.description, /tsp/);
  const executions = [];
  const execFileMock = mock.method(childProcess, "execFile", (file, args, options, callback) => {
    executions.push({ file, args, cwd: options.cwd });
    callback(null, "ok\\n", "");
    return {};
  });
  syncBuiltinESMExports();
  t.after(() => {
    execFileMock.mock.restore();
    syncBuiltinESMExports();
  });
  const invocations = [];
  for (const { name, options } of calls) {
    const rec = harness();
    const ctx = {
      cwd: "/w", hasUI: true,
      ui: { notify: (...args) => rec.deps.notify(...args), setStatus: (_key, text) => rec.deps.setStatus(text) },
    };
    assert.equal(typeof options.handler, "function");
    await options.handler("check", ctx);
    invocations.push({ name, notifications: rec.rec.notify, status: rec.rec.status });
  }
  assert.deepEqual(invocations.map(({ name }) => name), ["tsp", "tpp"]);
  assert.deepEqual(executions, [
    { file: "tsp", args: ["check", "--cwd", "/w"], cwd: "/w" },
    { file: "tsp", args: ["check", "--cwd", "/w"], cwd: "/w" },
  ]);
  for (const { notifications, status } of invocations) {
    assert.deepEqual(notifications, [{ message: "ok\\n", type: "info" }]);
    assert.deepEqual(status, ["tsp check: ok"]);
  }
});

test("dispatch and argv expose the shared CLI's check, feedback --summary, and doctor", () => {
  assert.deepEqual(parseTppCommand("check"), { kind: "check", rest: [] });
  assert.deepEqual(parseTppCommand("check --path plan/tpp.md"), { kind: "check", rest: ["--path", "plan/tpp.md"] });
  assert.deepEqual(parseTppCommand("feedback --summary"), { kind: "feedback-summary" });
  assert.deepEqual(parseTppCommand("doctor"), { kind: "doctor", mode: "auto" });
  assert.deepEqual(parseTppCommand("doctor --mode gentle"), { kind: "doctor", mode: "gentle" });
  assert.deepEqual(parseTppCommand("doctor --mode auto"), { kind: "doctor", mode: "auto" });
  assert.deepEqual(buildArgv(parseTppCommand("check --path plan/tpp.md"), "/w"), ["check", "--cwd", "/w", "--path", "plan/tpp.md"]);
  assert.deepEqual(buildArgv(parseTppCommand("feedback --summary"), "/w"), ["feedback", "--summary"]);
  assert.deepEqual(buildArgv(parseTppCommand("doctor"), "/w"), ["doctor", "--mode", "auto"]);
  assert.deepEqual(buildArgv(parseTppCommand("doctor --mode gentle"), "/w"), ["doctor", "--mode", "gentle"]);
});

test("dispatch refuses anything outside that surface", () => {
  for (const raw of ["", "   ", "review", "feedback", "feedback --file x", "doctor --mode turbo", "doctor --json", "sync"]) {
    const d = parseTppCommand(raw);
    assert.equal(d.kind, "help", `${JSON.stringify(raw)} must route to help, got ${JSON.stringify(d)}`);
    assert.ok(d.reason.length > 0, "help names the reason");
  }
});

test("gentle detection reads registered tools, not PATH", () => {
  const cases = [
    [{ name: "gentle_review" }, true],
    [{ name: "gentle_review_scope" }, true],
    [{ name: "gentle_custom_thing" }, false, "exact Gentle-owned tool names, not a generic gentle_ prefix"],
    [{ name: "review", sourceInfo: { path: "/x/gentle-pi/extensions/gentle-ai.ts" } }, true],
    [{ name: "review", sourceInfo: { path: "/home/u/gentle-pi-fork/index.ts" } }, false, "whole path segments, never substrings"],
    [{ name: "review", sourceInfo: { path: "/x/some-ext/index.ts" } }, false],
    [{ name: "bash" }, false],
  ];
  for (const [tool, want, why] of cases) assert.equal(observesGentleSession([tool]), want, why ?? JSON.stringify(tool));
  assert.equal(observesGentleSession([]), false);
});

test("the observation rides only the doctor child, only when observed, and mutates nothing", () => {
  const base = Object.freeze({ PATH: "/usr/bin", HOME: "/home/u" });
  // The parent shell may legitimately export the protocol var; capture it so the
  // mutation check below compares before/after instead of assuming it is absent.
  const ambientBefore = process.env[protocolEnv];
  assert.equal(childEnv(base, parseTppCommand("doctor"), true)[protocolEnv], protocolValue);
  assert.equal(childEnv(base, parseTppCommand("doctor"), true).PATH, "/usr/bin", "the child keeps its normal environment");
  assert.equal(childEnv(base, parseTppCommand("check"), true)[protocolEnv], undefined, "check never needs a mode observation, whatever the shell exports");
  assert.equal(childEnv(base, parseTppCommand("doctor"), false)[protocolEnv], undefined, "detection failing never forges the observation");
  assert.deepEqual({ ...base }, { PATH: "/usr/bin", HOME: "/home/u" }, "the base env object is untouched");
  assert.equal(process.env[protocolEnv], ambientBefore, "childEnv never mutates this process's ambient environment");
  // The child's env comes from the caller's base, never from ambient process.env:
  // a base-borne value rides verbatim, even for check, where the extension adds nothing.
  const marker = "from-caller-base";
  const carry = childEnv({ ...base, [protocolEnv]: marker }, parseTppCommand("check"), true);
  assert.equal(carry[protocolEnv], marker, "childEnv copies the caller's base verbatim");
});

// A harness recording what the extension would do, without spawning anything.
function harness({ tools = [], hasUI = true, outcome = { stdout: "ok\n", stderr: "", code: 0 } } = {}) {
  const rec = { exec: [], notify: [], status: [] };
  const deps = {
    tools, cwd: "/w", env: { PATH: "/usr/bin" }, hasUI,
    exec: async (argv, opts) => { rec.exec.push({ argv, opts }); return outcome; },
    notify: (message, type) => rec.notify.push({ message, type }),
    setStatus: (text) => rec.status.push(text),
  };
  return { deps, rec };
}

test("/tpp check and feedback run the shared CLI in the session cwd with guarded UI", async () => {
  const check = harness();
  await runTppCommand("check", check.deps);
  assert.deepEqual(check.rec.exec[0].argv, ["check", "--cwd", "/w"]);
  assert.equal(check.rec.exec[0].opts.cwd, "/w");
  assert.equal(check.rec.notify[0].type, "info");
  assert.match(check.rec.notify[0].message, /ok/);
  assert.equal(check.rec.status.length, 1);
  const feedback = harness();
  await runTppCommand("feedback --summary", feedback.deps);
  assert.deepEqual(feedback.rec.exec[0].argv, ["feedback", "--summary"]);
});

test("/tpp doctor passes the observation only under a live gentle session", async () => {
  const gentle = harness({ tools: [{ name: "gentle_review" }] });
  await runTppCommand("doctor", gentle.deps);
  assert.equal(gentle.rec.exec[0].opts.env[protocolEnv], protocolValue);
  const plain = harness({ tools: [{ name: "bash" }] });
  await runTppCommand("doctor", plain.deps);
  assert.equal(plain.rec.exec[0].opts.env[protocolEnv], undefined);
  assert.deepEqual(plain.rec.exec[0].argv, ["doctor", "--mode", "auto"]);
});

test("failures surface through notify and status instead of throwing at Pi", async () => {
  const failed = harness({ outcome: { stdout: "", stderr: "tpp: pending\n", code: 1 } });
  await runTppCommand("check", failed.deps);
  assert.equal(failed.rec.notify[0].type, "error");
  assert.match(failed.rec.notify[0].message, /pending/);
  assert.match(failed.rec.status[0], /exit 1/);
  const missing = harness();
  missing.deps.exec = async () => { const err = new Error("spawn tpp ENOENT"); err.code = "ENOENT"; throw err; };
  await runTppCommand("check", missing.deps);
  assert.equal(missing.rec.notify[0].type, "error");
  assert.match(missing.rec.notify[0].message, /tpp/);
});

test("without UI nothing renders — and help never spawns a process", async () => {
  const quiet = harness({ hasUI: false });
  await runTppCommand("check", quiet.deps);
  assert.equal(quiet.rec.exec.length, 1, "the command itself still runs headless");
  assert.deepEqual(quiet.rec.notify, [], "no notify without UI");
  assert.deepEqual(quiet.rec.status, [], "setStatus is guarded by hasUI");
  const help = harness();
  await runTppCommand("review", help.deps);
  assert.deepEqual(help.rec.exec, [], "an unknown subcommand never reaches a child process");
  assert.equal(help.rec.notify.length, 1);
  assert.match(help.rec.notify[0].message, /Usage/);
});
