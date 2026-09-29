---
name: crash-and-process-testing
description: "Trigger: crash consistency, fsync, atomic write, torn write, disk full, ENOSPC, kill -9, SIGTERM, SIGPIPE, exit code, stale lock, CLI process behavior. Prove durable state and process behavior survive kills, full disks and closed pipes."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "0.1.0"
  requires_tpp: "0.4.1"
  scope: [cli, files, durability, processes]
  auto_invoke: "Code that writes files or durable state, CLIs, and daemons with signal handlers: crash and ENOSPC injection, atomic replace, exit codes, signals, EPIPE, TTY vs pipe, concurrent runs"
---

## Activation Contract

Load when the target writes files or durable state (temp files, `rename`, `fsync`, sqlite/bolt/leveldb, append logs, config or state writers), is a CLI, or is a daemon or service with signal handlers: anything that must survive a kill or a full disk.

NOT for pure in-memory logic. Input-side checklists (disk full, TTY, time as inputs) stay in `exploit-testing`; this skill owns the fault-injection technique. Network and dependency faults, load and latency stay in `runtime-reliability-testing`; database transaction and migration semantics stay in `database-persistence-testing`.

## Hard Rules

1. **A failure at the write boundary is the input.** A suite that only writes to a healthy disk and reads back proves nothing about durability. Inject the fault, then reopen the durable state with a fresh reader and assert it is the old state or the new state, never torn, truncated or empty.
2. **Kill at every write step.** Enumerate the steps of the save (open, write, fsync, rename, directory fsync) and fail or kill between each pair. The ALICE study (OSDI 2014) reports 60 crash vulnerabilities across 11 systems found this way; treat ordering assumptions as suspect until each step is exercised.
3. **Atomic replace has four parts.** Write a temp file in the same directory, fsync it, rename over the target, fsync the directory. Classic defects: writing in place (open with truncate), rename without fsync, temp file on another filesystem (rename fails or is not atomic), missing directory fsync, leftover temp files after a failure.
4. **Disk full and short writes must fail loudly and keep the old state.** Inject `ENOSPC` or a short write at the write boundary. The program must report failure (Rule 5) and the previous file must be byte-identical. A swallowed error is a defect even when the file survives.
5. **Exit codes carry the truth.** Non-zero on failure, including partial failure; usage errors distinct from runtime failures when the README says so. Assert the code and the stderr text for each failure you inject, not only the happy path.
6. **Signals and pipes.** SIGTERM and SIGINT must flush, release locks and leave no half-written output. Closing stdout early (`cmd | head -1`) must end the process quietly on `EPIPE`/`SIGPIPE`, not hang or print a stack trace. Run with stdin at EOF and with stdin closed.
7. **TTY and pipe differ.** Colors, progress bars and buffering may change when output is not a terminal; assert the piped output is clean and complete.
8. **Two runs at once.** Start two instances on the same state: expect a lock or a defined loss of no data. After a kill, a stale lock file must not block the next run forever.
9. **Prove with a deterministic injection point and a re-run.** Use a seam the project already has (an injectable fs or writer, a fault hook, an env var). Record the observed durable state after reopen, fix, and re-run green. A test that never fails on the defective code is not evidence.

## Decision Gates

| Signal | Probe | Failing evidence |
| --- | --- | --- |
| Rewrites a state, config or data file | Fail or kill between each write step, reopen | Reopen throws, file empty or truncated |
| Writes to disk and reports status | `ENOSPC` or short write at the write call | Exit 0 or success message, old file changed |
| CLI with several exit paths | Force each failure, assert code and stderr | Exit 0 after a failed step |
| Long-running process or service | SIGTERM, SIGINT, SIGKILL then restart | Lost buffer, held lock, torn output |
| Writes to stdout | `cmd \| head -1`, closed stdout | Hang, stack trace, non-quiet exit |
| Lock or pid file | Two concurrent runs, kill one, rerun | Lost update, permanent stale lock |
| Pure in-memory logic | Stop; record why the gate does not apply | none |

## Execution Steps

1. List every durable artifact and the code path that writes it; list every process exit path and signal handler.
2. Find or add the injection seam. Leads, mark as `verify` until run: `strace -f -e inject=write:error=ENOSPC:when=3` for a real syscall fault, `libfiu`, a size-capped `tmpfs`, and `dm-flakey` for block-level power-loss simulation.
3. Per artifact, run the kill-at-each-step matrix; per exit path, force the failure and read the code. Keep each failing case as a regression test.
4. Fix, then re-run the same injection and show reopen state and exit code now correct.
5. Record probes you could not run as `not run` with the reason.

## Output Contract

Return per artifact and exit path: the step or fault injected, the injection method, the durable state observed after reopen (or the exit code and stderr), the class of defect, and the re-run result after the fix. List gates that did not apply with the reason.

## References

- ALICE (crash vulnerabilities in application file-system use): https://www.usenix.org/conference/osdi14/technical-sessions/presentation/pillai
