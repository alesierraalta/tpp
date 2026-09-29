export function createLedgerServer(clock) {
  const charges = [];
  const byKey = new Map();
  const queue = [];
  let fallback = 'ok';
  const state = { calls: 0 };

  function apply(req) {
    if (req.key && byKey.has(req.key)) return byKey.get(req.key);
    const res = { status: 201, body: { id: `ch_${charges.length + 1}` } };
    charges.push({ amount: req.body.amount, key: req.key });
    if (req.key) byKey.set(req.key, res);
    return res;
  }

  function timeout(ms) {
    clock.advance(ms);
    const err = new Error('timeout');
    err.code = 'ETIMEDOUT';
    throw err;
  }

  async function transport(req, { timeoutMs }) {
    state.calls++;
    const behaviour = queue.length ? queue.shift() : fallback;
    if (behaviour === '503') return { status: 503, body: {} };
    if (behaviour === 'hang') timeout(timeoutMs);
    if (req.method === 'GET') return { status: 200, body: { count: charges.length } };
    const res = apply(req);
    if (behaviour === 'lost') timeout(timeoutMs);
    return res;
  }

  return {
    transport,
    script: (steps) => queue.push(...steps),
    always: (b) => {
      fallback = b;
    },
    charges,
    get calls() {
      return state.calls;
    },
  };
}
