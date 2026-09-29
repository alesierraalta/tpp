import nodefs from 'node:fs';
import { dirname } from 'node:path';

export function loadState(path, fs = nodefs) {
  let text;
  try {
    text = fs.readFileSync(path, 'utf8');
  } catch (e) {
    if (e.code === 'ENOENT') return { jobs: {} };
    throw e;
  }
  const state = JSON.parse(text);
  if (!state || typeof state.jobs !== 'object') throw new Error('invalid state file');
  return state;
}

export function saveState(path, state, fs = nodefs) {
  const data = JSON.stringify(state, null, 2) + '\n';
  const tmp = `${path}.tmp-${process.pid}`;
  try {
    const fd = fs.openSync(tmp, 'w');
    try {
      fs.writeSync(fd, data);
      fs.fsyncSync(fd);
    } finally {
      fs.closeSync(fd);
    }
    fs.renameSync(tmp, path);
    const dirFd = fs.openSync(dirname(path), 'r');
    try {
      fs.fsyncSync(dirFd);
    } finally {
      fs.closeSync(dirFd);
    }
  } catch (e) {
    try { fs.unlinkSync(tmp); } catch { /* temp file may not exist */ }
    throw e;
  }
}

export function recordJob(path, id, status, fs = nodefs) {
  const state = loadState(path, fs);
  state.jobs[id] = status;
  saveState(path, state, fs);
  return state;
}
