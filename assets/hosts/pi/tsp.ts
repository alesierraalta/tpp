// tsp for Pi: the shared tsp CLI as a native /tsp command, loadable without an installer:
//   pi --extension /path/to/assets/hosts/pi/tsp.ts
//
// Detection reads this session's registered tools (pi.getAllTools()), never PATH: exact
// Gentle-owned tool names or whole gentle-pi/gentle-ai source path segments. When Gentle is
// observed, the spawned `tsp doctor` child gets the process-scoped observation through
// child_process.execFile's env — no process.env mutation, no file. That observation is session
// UX evidence for mode selection, not authentication or security authority. Gentle review
// authority is never called; UI writes are guarded by ctx.hasUI.

import { execFile } from "node:child_process";
import type { ExtensionAPI, ExtensionCommandContext } from "@earendil-works/pi-coding-agent";

/** Protocol with internal/mode (Go); mode.VerifiedGentleSignal accepts only this exact pair. */
export const GENTLE_OBSERVATION_ENV = "TPP_GENTLE_OBSERVATION";
export const GENTLE_OBSERVATION_VALUE = "pi-session-gentle-active";

export interface RegisteredTool {
  name: string;
  sourceInfo?: { path?: string };
}

export type TppDispatch =
  | { kind: "check"; rest: string[] } | { kind: "feedback-summary" }
  | { kind: "doctor"; mode: "auto" | "gentle" } | { kind: "help"; reason: string };

// The Gentle-owned tools registered by gentle-pi, by exact name — not any gentle_* prefix.
const GENTLE_TOOLS = new Set(["gentle_odd_phase", "gentle_review", "gentle_review_capture", "gentle_review_capture_group", "gentle_review_scope"]);
// Gentle-owned directories, matched as whole path segments only — never as substrings.
const GENTLE_DIRS = new Set(["gentle-pi", "gentle-ai"]);

/** True when Gentle is actually registered in this session — no PATH lookup, no version probe. */
export function observesGentleSession(tools: readonly RegisteredTool[]): boolean {
  return tools.some((t) => GENTLE_TOOLS.has(t.name) || (t.sourceInfo?.path ?? "").split(/[\\/]+/).some((s) => GENTLE_DIRS.has(s)));
}

const HELP = "Usage: /tsp check [--path <plan.md>] · /tsp feedback --summary · /tsp doctor [--mode auto|gentle]";

/** Maps the raw arguments after /tsp or its /tpp compatibility alias. */
export function parseTppCommand(rawArgs: string): TppDispatch {
  const [head, ...rest] = rawArgs.trim().split(/\s+/).filter(Boolean);
  switch (head) {
    case undefined: return { kind: "help", reason: "no subcommand" };
    case "check": return { kind: "check", rest };
    case "feedback":
      return rest.length === 1 && rest[0] === "--summary" ? { kind: "feedback-summary" } : { kind: "help", reason: "feedback supports only --summary" };
    case "doctor": {
      if (rest.length === 0) return { kind: "doctor", mode: "auto" };
      const mode = rest[1];
      return rest.length === 2 && rest[0] === "--mode" && (mode === "auto" || mode === "gentle")
        ? { kind: "doctor", mode } : { kind: "help", reason: "doctor supports only [--mode auto|gentle]" };
    }
    default: return { kind: "help", reason: `unknown subcommand "${head}"` };
  }
}

/** The shared CLI's argv for one dispatch. */
export function buildArgv(d: TppDispatch, cwd: string): string[] {
  switch (d.kind) {
    case "check": return ["check", "--cwd", cwd, ...d.rest];
    case "feedback-summary": return ["feedback", "--summary"];
    case "doctor": return ["doctor", "--mode", d.mode];
    case "help": return [];
  }
}

/** Child env for one tpp process: the observation rides only an observed doctor child; base is copied, never mutated. */
export function childEnv(base: Readonly<Record<string, string | undefined>>, d: TppDispatch, gentleObserved: boolean): Record<string, string | undefined> {
  const env: Record<string, string | undefined> = { ...base };
  if (gentleObserved && d.kind === "doctor") env[GENTLE_OBSERVATION_ENV] = GENTLE_OBSERVATION_VALUE;
  return env;
}

type ExecResult = { stdout: string; stderr: string; code: number };
type ExecOpts = { cwd: string; env: Record<string, string | undefined> };
export type ExecTpp = (argv: readonly string[], opts: ExecOpts) => Promise<ExecResult>;

/** One tpp child without a shell; a non-zero exit is data — only a binary that never started rejects. */
export function execTpp(argv: readonly string[], opts: ExecOpts): Promise<ExecResult> {
  return new Promise((resolve, reject) => {
    execFile("tsp", [...argv], { cwd: opts.cwd, env: opts.env as NodeJS.ProcessEnv, encoding: "utf8" }, (error, stdout, stderr) => {
      if (error && typeof (error as { code?: unknown }).code !== "number") return reject(error);
      resolve({ stdout: String(stdout), stderr: String(stderr), code: error ? Number((error as { code?: unknown }).code) : 0 });
    });
  });
}

export interface TppDeps {
  tools: readonly RegisteredTool[];
  cwd: string;
  env: Readonly<Record<string, string | undefined>>;
  hasUI: boolean;
  exec: ExecTpp;
  notify: (message: string, type?: "info" | "warning" | "error") => void;
  setStatus?: (text: string | undefined) => void; // status-bar writer; called only when hasUI
}

const MAX_NOTICE = 1200; // a notification is a toast, not a terminal
const notice = (text: string): string => (text.trim().length <= MAX_NOTICE ? text.trim() : text.trim().slice(0, MAX_NOTICE) + "…");

/** Runs one invocation; every render is guarded by hasUI so headless sessions still get the child process. */
export async function runTppCommand(rawArgs: string, deps: TppDeps): Promise<void> {
  const d = parseTppCommand(rawArgs);
  if (d.kind === "help") {
    if (deps.hasUI) deps.notify(`${d.reason}. ${HELP}`, "warning");
    return;
  }
  const observed = observesGentleSession(deps.tools);
  const label = d.kind === "feedback-summary" ? "feedback" : d.kind;
  let outcome: ExecResult;
  try {
    outcome = await deps.exec(buildArgv(d, deps.cwd), { cwd: deps.cwd, env: childEnv(deps.env, d, observed) });
  } catch (error) {
    if (deps.hasUI) {
      deps.notify(notice(`tsp: ${error instanceof Error ? error.message : String(error)}`), "error");
      deps.setStatus?.("tsp: failed");
    }
    return;
  }
  if (deps.hasUI) {
    const output = outcome.stdout.trim() || outcome.stderr.trim();
    const ok = outcome.code === 0;
    deps.notify(notice(output || `tsp ${label}: ${ok ? "ok" : `exited ${outcome.code}`}`), ok ? "info" : "error");
    const suffix = observed && d.kind === "doctor" ? " · gentle session observed" : "";
    deps.setStatus?.(`tsp ${label}: ${ok ? "ok" : `exit ${outcome.code}`}${suffix}`);
  }
}

export default function (pi: ExtensionAPI) {
  const register = (name: "tsp" | "tpp") => pi.registerCommand(name, {
    description: `${name} check · feedback --summary · doctor [--mode auto|gentle] in this session`,
    handler: (args: string, ctx: ExtensionCommandContext) =>
      runTppCommand(args, {
        tools: pi.getAllTools(), cwd: ctx.cwd, env: process.env, hasUI: ctx.hasUI, exec: execTpp,
        notify: (message, type) => ctx.ui.notify(message, type),
        setStatus: (text) => ctx.ui.setStatus("tsp", text),
      }),
  });
  register("tsp");
  register("tpp");
}
