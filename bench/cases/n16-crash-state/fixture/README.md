# job-state

A small command line tool that records the status of batch jobs in one JSON state file, so a
scheduler can ask which jobs finished.

- `job-state mark <id> <status>` records a job. `status` is `pending`, `running`, `done` or
  `failed`. The state file is `$JOB_STATE_FILE`, default `./job-state.json`.
- `job-state list` prints one `<id> <status>` line per job, sorted by id.
- Exit codes: `0` on success, `2` for a usage error, `1` when the command could not do what was
  asked, including when the state could not be saved.
- The state file is the only record of finished jobs. A save that fails or is interrupted (crash,
  kill, full disk) must leave the previous state file exactly as it was: the next run reads either
  the old state or the new state, never a partial one.

`saveState(path, state, fs)` and `loadState(path, fs)` take the file system module as an optional
last argument (default `node:fs`) so tests can substitute it. `run(argv, { fs, out, err, env })`
is the CLI entry point and returns the exit code.
