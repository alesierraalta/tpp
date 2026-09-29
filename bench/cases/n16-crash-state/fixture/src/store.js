import nodefs from 'node:fs';

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
  const fd = fs.openSync(path, 'w');
  try {
    fs.writeSync(fd, data);
  } finally {
    fs.closeSync(fd);
  }
}

export function recordJob(path, id, status, fs = nodefs) {
  const state = loadState(path, fs);
  state.jobs[id] = status;
  saveState(path, state, fs);
  return state;
}
