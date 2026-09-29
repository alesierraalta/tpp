import http from 'k6/http';
import { check } from 'k6';
import { Trend, Rate, Counter } from 'k6/metrics';

const customLatency = new Trend('target_custom_latency', true);
const errorRate = new Rate('target_error_rate');
const timeouts = new Counter('target_timeouts');
const required = (name) => {
  if (!__ENV[name]) throw new Error(`${name} is required; set an explicit target-owned value`);
  return __ENV[name];
};
const number = (name, example) => Number(__ENV[name] || example); // shape parameters only
const requiredNumber = (name) => { // SLO bounds fail closed: no default, no run
  const v = Number(required(name));
  if (!Number.isFinite(v)) throw new Error(`${name} must be a number`);
  return v;
};
const stages = __ENV.TARGET_STAGES
  ? JSON.parse(__ENV.TARGET_STAGES)
  : [{ duration: '1m', target: 100 }, { duration: '3m', target: 100 }, { duration: '30s', target: 0 }]; // EXAMPLE only
const TARGET_URL = required('TARGET_URL');
const REQUEST_TIMEOUT = __ENV.TARGET_TIMEOUT || '3s'; // EXAMPLE only

export const options = {
  scenarios: { open_model_workload: {
    executor: 'ramping-arrival-rate',
    startRate: number('TARGET_START_RATE', 20), // EXAMPLE only
    timeUnit: '1s',
    preAllocatedVUs: number('TARGET_PREALLOCATED_VUS', 50), // EXAMPLE only
    maxVUs: number('TARGET_MAX_VUS', 500), // EXAMPLE only
    stages,
  } },
  thresholds: {
    // SLO thresholds come only from target-owned env vars; a missing one aborts before the run.
    // Timed-out requests report status 0; k6 does not guarantee their http_req_duration value.
    // They are counted in target_timeouts and reported beside the percentiles: percentiles over
    // successful responses only are optimistic, so read them as lower bounds when timeouts > 0.
    http_req_duration: [`p(95)<${requiredNumber('TARGET_P95_MS')}`, `p(99)<${requiredNumber('TARGET_P99_MS')}`],
    http_req_failed: [`rate<${requiredNumber('TARGET_HTTP_ERROR_RATE')}`],
    target_error_rate: [`rate<${requiredNumber('TARGET_ERROR_RATE')}`],
    // Dropped iterations mean the generator could not offer the declared rate: the run is
    // INCONCLUSIVE, so stop it instead of letting it finish with a flattering tail.
    dropped_iterations: [{ threshold: 'count==0', abortOnFail: true }],
  },
};

// EXAMPLE only: target owners may configure the marker or replace this hook with schema validation.
const RESPONSE_MARKER = __ENV.TARGET_RESPONSE_MARKER || 'ok'; // EXAMPLE only
export function validateResponse(res) {
  const body = typeof res.body === 'string' ? res.body : '';
  return (res.status === 200 || res.status === 201) && body.includes(RESPONSE_MARKER);
}

export default function () {
  const res = http.post(`${TARGET_URL}/api/v1/workload`, JSON.stringify({ timestamp: Date.now(), action: 'probe' }), {
    headers: { 'Content-Type': 'application/json' }, timeout: REQUEST_TIMEOUT,
  });
  if (res.status === 0) timeouts.add(1); // timeouts are counted separately, not trusted as latency
  else customLatency.add(res.timings.duration); // successful and non-timeout responses only
  const ok = check(res, {
    'status and semantic response are valid': (r) => (r.status === 200 || r.status === 201) && validateResponse(r),
  });
  errorRate.add(!ok);
}

// Offered load and completed throughput are different numbers: report both.
// attempted = completed iterations + dropped_iterations (every arrival the constant-arrival-rate
// executor tried to schedule); completed = iterations that finished.
export function handleSummary(data) {
  const secs = (data.state.testRunDurationMs || 0) / 1000;
  const completed = data.metrics.iterations?.values?.count || 0;
  const dropped = data.metrics.dropped_iterations?.values?.count || 0;
  const load = {
    duration_s: secs,
    attempted_iterations: completed + dropped,
    attempted_rate_per_s: secs ? (completed + dropped) / secs : null,
    completed_iterations: completed,
    completed_throughput_per_s: secs ? completed / secs : null,
    timeouts: data.metrics.target_timeouts?.values?.count || 0,
    inconclusive: dropped > 0,
  };
  if (load.inconclusive) console.warn('INCONCLUSIVE: dropped_iterations > 0; maxVUs was exhausted');
  const m = (name, stat) => data.metrics[name]?.values?.[stat];
  const fmt = (v) => (typeof v === 'number' ? v.toFixed(2) : 'n/a');
  const text = [
    `http_req_duration ms: p95=${fmt(m('http_req_duration', 'p(95)'))} p99=${fmt(m('http_req_duration', 'p(99)'))}`,
    `http_req_failed rate: ${fmt(m('http_req_failed', 'rate'))}`,
    `timeouts (status 0): ${load.timeouts} (percentiles exclude them: read as lower bounds when > 0)`,
    `load: ${JSON.stringify(load)}`,
  ].join('\n') + '\n';
  return {
    stdout: text,
    'summary.json': JSON.stringify({ load, metrics: data.metrics }, null, 2),
  };
}
