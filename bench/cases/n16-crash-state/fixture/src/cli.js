import nodefs from 'node:fs';
import { loadState, recordJob } from './store.js';

const STATUSES = ['pending', 'running', 'done', 'failed'];

export function run(argv, { fs = nodefs, out = process.stdout, err = process.stderr, env = process.env } = {}) {
  const path = env.JOB_STATE_FILE || './job-state.json';
  const [cmd, id, status] = argv;

  if (cmd === 'mark') {
    if (!id || !STATUSES.includes(status)) {
      err.write('usage: job-state mark <id> <pending|running|done|failed>\n');
      return 2;
    }
    try {
      recordJob(path, id, status, fs);
    } catch (e) {
      err.write(`warning: could not save state: ${e.message}\n`);
    }
    return 0;
  }

  if (cmd === 'list') {
    const { jobs } = loadState(path, fs);
    for (const key of Object.keys(jobs).sort()) out.write(`${key} ${jobs[key]}\n`);
    return 0;
  }

  err.write('usage: job-state <mark|list>\n');
  return 2;
}
