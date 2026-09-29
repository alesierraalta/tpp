export function createClient({
  transport,
  clock,
  sleep,
  maxAttempts = 3,
  attemptTimeoutMs = 1000,
  baseBackoffMs = 100,
  deadlineMs = 5000,
}) {
  let seq = 0;

  async function send(method, path, body, opts = {}) {
    const budget = opts.deadlineMs ?? deadlineMs;
    let last = new Error('no attempt made');
    for (let attempt = 1; attempt <= maxAttempts; attempt++) {
      const timeoutMs = Math.min(attemptTimeoutMs, budget);
      const key = method === 'POST' ? `req-${++seq}` : undefined;
      try {
        const res = await transport({ method, path, body, key }, { timeoutMs });
        if (res.status < 500) return { ok: res.status < 400, status: res.status, data: res.body };
        last = new Error(`upstream ${res.status}`);
      } catch (err) {
        if (err.code !== 'ETIMEDOUT') throw err;
        last = err;
      }
      if (attempt < maxAttempts) await sleep(baseBackoffMs * 2 ** (attempt - 1));
    }
    return { ok: false, error: last.message };
  }

  return { send };
}
