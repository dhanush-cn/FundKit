/**
 * FundKit - production-like load and idempotency test for POST /orders.
 *
 * Engineered by Dhanush C N (github.com/dhanush-cn)
 *
 * What this exercises
 * -------------------
 * The write path that actually costs something: api-gateway (JWT verify, rate
 * limit, reverse proxy) -> order-service -> Redis idempotency claim -> Postgres
 * insert of the order and its outbox row in one transaction. The Kafka publish
 * is asynchronous behind the outbox relay, so it does not show up in the
 * response latency here; watch kafka_events_published_total and the outbox
 * backlog in Grafana instead (see perf/README.md).
 *
 * Two scenarios run concurrently:
 *
 *   orders_ramp        0 -> 500 VUs over 2m, hold 3m, drain 1m.
 *                      90% of iterations place a brand new order with a unique
 *                      UUID idempotency key. The other 10% deliberately reuse a
 *                      key drawn from a small shared pool, so several hundred
 *                      VUs collide on the same keys at the same time. This is
 *                      the double-submit / client-retry pattern.
 *
 *   idempotency_race   A low, steady arrival rate of strict races: one fresh
 *                      key fired as N simultaneous requests via http.batch.
 *                      Exactly one must be accepted; the rest must be refused.
 *                      This is the assertion that proves SETNX + the unique
 *                      index actually prevent double processing, rather than
 *                      just hoping the 10% traffic happens to collide.
 *
 * Everything is tunable from the environment; defaults match the compose stack.
 */

import http from 'k6/http';
import { check, sleep } from 'k6';
import exec from 'k6/execution';
import { Trend, Rate, Counter } from 'k6/metrics';

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

// gateway: authenticate and go through the edge (the realistic path).
// direct:  talk straight to order-service and forge the identity headers the
//          gateway would normally stamp. Use it to subtract gateway overhead,
//          and because only in this mode does a per-request random user_id
//          survive - through the gateway the verified JWT subject always wins.
const MODE = (__ENV.MODE || 'gateway').toLowerCase();

const USERNAME = __ENV.LOAD_USER || 'fundkit_loadtest';
const PASSWORD = __ENV.LOAD_PASSWORD || 'LoadTest#2026';

const TARGET_VUS = Number(__ENV.TARGET_VUS || 500);
const RAMP_UP = __ENV.RAMP_UP || '2m';
const HOLD = __ENV.HOLD || '3m';
const RAMP_DOWN = __ENV.RAMP_DOWN || '1m';

// Share of orders_ramp iterations that replay a key from the shared pool.
const REPLAY_SHARE = Number(__ENV.REPLAY_SHARE || 0.1);
// Size of that pool. Smaller pool = harder contention per key.
const REPLAY_POOL = Number(__ENV.REPLAY_POOL || 200);

// Strict race scenario: iterations per second, and requests per race.
const RACE_RATE = Number(__ENV.RACE_RATE || 5);
const RACE_FANOUT = Number(__ENV.RACE_FANOUT || 4);

const P95_BUDGET_MS = Number(__ENV.P95_BUDGET_MS || 200);
const ERROR_BUDGET = Number(__ENV.ERROR_BUDGET || 0.01);

// Stamped into every idempotency key so re-runs never collide with each other,
// and so teardown can tell this run's orders apart from everything already in
// the database.
const RUN_ID = __ENV.RUN_ID || `${Date.now().toString(36)}`;

// k6 writes handleSummary output relative to the working directory. Keep it
// overridable so a CI job can drop the JSON wherever it archives artefacts.
const SUMMARY_PATH = __ENV.SUMMARY_PATH || `perf/results/summary-${RUN_ID}.json`;

const FUNDS = [
  'FUND-NIFTY50-IDX',
  'FUND-BLUECHIP-EQ',
  'FUND-MIDCAP-OPP',
  'FUND-LIQUID-DEBT',
  'FUND-BALANCED-ADV',
];

// ---------------------------------------------------------------------------
// Custom metrics
//
// http_req_failed alone is not enough here: a 409 is a *correct* answer to a
// replayed key, and counting it as a failure would make a healthy system look
// broken. So the response callback below teaches k6 which statuses are
// expected, and order_errors carries the real error signal.
// ---------------------------------------------------------------------------

const orderLatency = new Trend('order_create_latency', true);
const orderErrors = new Rate('order_errors');
const ordersAccepted = new Counter('idem_accepted');
const poolAccepted = new Counter('idem_pool_accepted');
const idemConflicts = new Counter('idem_conflicts');
const rateLimited = new Counter('rate_limited_429');
const doubleProcessed = new Counter('idem_double_processed');
const raceWinners = new Trend('idem_race_winners');

http.setResponseCallback(http.expectedStatuses(200, 201, 409));

// ---------------------------------------------------------------------------
// Options
// ---------------------------------------------------------------------------

export const options = {
  discardResponseBodies: false,
  noConnectionReuse: false,
  scenarios: {
    orders_ramp: {
      executor: 'ramping-vus',
      exec: 'orderTraffic',
      startVUs: 0,
      gracefulRampDown: '30s',
      stages: [
        { duration: RAMP_UP, target: TARGET_VUS },
        { duration: HOLD, target: TARGET_VUS },
        { duration: RAMP_DOWN, target: 0 },
      ],
      tags: { scenario_kind: 'ramp' },
    },
    idempotency_race: {
      executor: 'constant-arrival-rate',
      exec: 'idempotencyRace',
      rate: RACE_RATE,
      timeUnit: '1s',
      duration: totalDuration(),
      preAllocatedVUs: Math.max(10, RACE_RATE * RACE_FANOUT),
      maxVUs: Math.max(50, RACE_RATE * RACE_FANOUT * 4),
      tags: { scenario_kind: 'race' },
    },
  },
  thresholds: {
    // Latency budget applies to the order write path only, so gateway health
    // probes and the login in setup() cannot flatter the number.
    'http_req_duration{endpoint:create_order}': [`p(95)<${P95_BUDGET_MS}`],
    'order_create_latency': [`p(95)<${P95_BUDGET_MS}`, `p(99)<${P95_BUDGET_MS * 3}`],

    // Real errors: 5xx, 429, transport failures, and anything that is neither
    // a fresh accept nor a legitimate conflict.
    'order_errors': [`rate<${ERROR_BUDGET}`],
    'http_req_failed': [`rate<${ERROR_BUDGET}`],

    // Correctness, not performance. A single duplicate accept means the Redis
    // claim and the unique index both let a double order through, which is a
    // money bug - so this one aborts the run rather than reporting at the end.
    'idem_double_processed': [
      { threshold: 'count==0', abortOnFail: true, delayAbortEval: '30s' },
    ],
    // At most one order may ever be created per pooled replay key.
    'idem_pool_accepted': [`count<=${REPLAY_POOL}`],

    'checks': ['rate>0.99'],
  },
};

function totalDuration() {
  return __ENV.TOTAL_DURATION || sumDurations([RAMP_UP, HOLD, RAMP_DOWN]);
}

// k6 wants a single duration string for the arrival-rate scenario; add the
// ramp stages up so the race runs for exactly as long as the ramp does.
function sumDurations(list) {
  let seconds = 0;
  for (const d of list) {
    const m = String(d).match(/^(\d+(?:\.\d+)?)(ms|s|m|h)$/);
    if (!m) continue;
    const value = Number(m[1]);
    seconds += m[2] === 'ms' ? value / 1000 : m[2] === 's' ? value : m[2] === 'm' ? value * 60 : value * 3600;
  }
  return `${Math.ceil(seconds)}s`;
}

// ---------------------------------------------------------------------------
// Payload helpers - dynamic data, no external jslib import so the script runs
// on an air-gapped runner.
// ---------------------------------------------------------------------------

function uuidv4() {
  let out = '';
  for (let i = 0; i < 36; i++) {
    if (i === 8 || i === 13 || i === 18 || i === 23) {
      out += '-';
    } else if (i === 14) {
      out += '4';
    } else {
      const r = (Math.random() * 16) | 0;
      out += (i === 19 ? (r & 0x3) | 0x8 : r).toString(16);
    }
  }
  return out;
}

function randomInt(min, max) {
  return Math.floor(Math.random() * (max - min + 1)) + min;
}

function randomUserID() {
  // Spread the write load across many user partitions rather than hammering a
  // single row's index pages.
  return `load-${RUN_ID}-u${randomInt(1, 5000)}`;
}

// SIPs are small and recurring, lump sums are large and occasional. Keeping
// that shape matters: a flat amount distribution would not reproduce the mix of
// row sizes and the fund-level skew the real write path sees.
function randomAmount(type) {
  const [min, max] = type === 'SIP' ? [500, 25000] : [10000, 2500000];
  return Math.round((Math.random() * (max - min) + min) * 100) / 100;
}

function newOrderPayload(idempotencyKey, userID) {
  const type = Math.random() < 0.6 ? 'SIP' : 'LUMPSUM';
  return {
    user_id: userID,
    fund_id: FUNDS[randomInt(0, FUNDS.length - 1)],
    amount: randomAmount(type),
    type: type,
    idempotency_key: idempotencyKey,
  };
}

function requestParams(token, userID, extraTags) {
  const headers = {
    'Content-Type': 'application/json',
    // Correlates this exact request with the structured logs of every service
    // it touches, since api-gateway and order-service both adopt an inbound
    // x-request-id instead of minting their own.
    'x-request-id': `k6-${RUN_ID}-${uuidv4()}`,
  };

  if (MODE === 'direct') {
    headers['x-fundkit-user-id'] = userID;
    headers['x-fundkit-username'] = USERNAME;
    headers['x-fundkit-user-email'] = `${userID}@loadtest.fundkit.local`;
  } else {
    headers['Authorization'] = `Bearer ${token}`;
  }

  return {
    headers,
    tags: Object.assign({ endpoint: 'create_order' }, extraTags || {}),
    timeout: '10s',
  };
}

// A pooled key is shared by every VU in the run, which is what produces the
// concurrent replay. Same key, many VUs, same instant.
function pooledReplayKey() {
  return `fk-${RUN_ID}-replay-${randomInt(0, REPLAY_POOL - 1)}`;
}

// ---------------------------------------------------------------------------
// Response classification
// ---------------------------------------------------------------------------

function classify(res, params) {
  orderLatency.add(res.timings.duration, params.tags);

  const status = res.status;
  const accepted = status === 201 || status === 200;
  const conflict = status === 409;

  if (status === 429) {
    rateLimited.add(1);
  }
  if (accepted) {
    ordersAccepted.add(1);
  }
  if (conflict) {
    idemConflicts.add(1);
  }

  // Anything outside {accepted, conflict} is a genuine failure of the write
  // path, including the 429s: a load test that is being shed is not a load
  // test, it is a rate limiter demo.
  orderErrors.add(!(accepted || conflict));
  return { accepted, conflict };
}

// ---------------------------------------------------------------------------
// setup: authenticate once, and warn loudly about the two things that silently
// invalidate a FundKit load run.
// ---------------------------------------------------------------------------

export function setup() {
  const health = http.get(`${BASE_URL}/healthz`, { tags: { endpoint: 'healthz' } });
  if (health.status !== 200) {
    exec.test.abort(`target ${BASE_URL} is not healthy (/healthz -> ${health.status}); start the stack first`);
  }

  // The gateway rate limiter is per client IP and defaults to 5 rps / burst 10.
  // From one load generator that ceiling is reached in the first second and the
  // rest of the run is 429s. Detect it here instead of at the summary.
  let throttled = 0;
  for (let i = 0; i < 15; i++) {
    if (http.get(`${BASE_URL}/healthz`, { tags: { endpoint: 'healthz' } }).status === 429) {
      throttled++;
    }
  }
  if (throttled > 0) {
    console.warn(
      `[setup] gateway returned ${throttled}/15 429s on a burst probe. ` +
        'Raise the edge limit for the run, e.g. FUNDKIT_RATE_LIMIT_RPS=100000 ' +
        'FUNDKIT_RATE_LIMIT_BURST=100000 docker compose up -d api-gateway, ' +
        'or run with MODE=direct BASE_URL=http://localhost:8081 to bypass the edge.'
    );
  }

  if (MODE === 'direct') {
    console.log(`[setup] direct mode against ${BASE_URL}; identity headers are forged, no JWT.`);
    return { token: '', runId: RUN_ID };
  }

  const credentials = {
    username: USERNAME,
    password: PASSWORD,
    email: `${USERNAME}@loadtest.fundkit.local`,
    phone: '9000000000',
    full_name: 'FundKit Load Test',
  };
  const jsonHeaders = { headers: { 'Content-Type': 'application/json' }, tags: { endpoint: 'auth' } };

  // Registration is best-effort: on a re-run the account already exists.
  http.post(`${BASE_URL}/auth/register`, JSON.stringify(credentials), jsonHeaders);

  const login = http.post(
    `${BASE_URL}/auth/login`,
    JSON.stringify({ username: USERNAME, password: PASSWORD }),
    jsonHeaders
  );
  if (login.status !== 200) {
    exec.test.abort(`login failed (${login.status}): ${String(login.body).slice(0, 200)}`);
  }

  const token = login.json('token');
  if (!token) {
    exec.test.abort('login succeeded but returned no token');
  }

  console.log(`[setup] run_id=${RUN_ID} mode=${MODE} target=${BASE_URL} vus=${TARGET_VUS}`);
  return { token, runId: RUN_ID };
}

// ---------------------------------------------------------------------------
// Scenario 1: the ramp. 90% unique orders, 10% concurrent replays.
// ---------------------------------------------------------------------------

export function orderTraffic(data) {
  const replay = Math.random() < REPLAY_SHARE;
  const userID = randomUserID();
  const key = replay ? pooledReplayKey() : uuidv4();

  const params = requestParams(data.token, userID, {
    call: replay ? 'replayed_key' : 'unique_key',
  });

  const res = http.post(`${BASE_URL}/orders`, JSON.stringify(newOrderPayload(key, userID)), params);
  const verdict = classify(res, params);

  if (replay) {
    // Whichever VU gets there first wins the key; every later attempt on that
    // same key must be refused. Counting the winners lets the
    // idem_pool_accepted threshold assert "at most one order per pooled key"
    // across the whole run.
    if (verdict.accepted) {
      poolAccepted.add(1);
    }
    check(res, {
      'replay: settled as accept-once or conflict': () => verdict.accepted || verdict.conflict,
      'replay: never a 5xx': (r) => r.status < 500,
    });
  } else {
    check(res, {
      'unique key: 201 Created': (r) => r.status === 201,
      'unique key: body carries an order id': (r) => {
        if (r.status !== 201) return false;
        const id = r.json('id');
        return typeof id === 'string' && id.length > 0;
      },
      'unique key: echoes the request id': (r) => !!r.headers['X-Request-Id'],
    });
  }

  // A real client is not a tight loop. Roughly one order every second or two
  // per user keeps arrival somewhere near Poisson instead of a lockstep wave.
  sleep(Math.random() * 1.5 + 0.5);
}

// ---------------------------------------------------------------------------
// Scenario 2: the strict race.
//
// One fresh key, RACE_FANOUT identical requests fired simultaneously from the
// same VU with http.batch, then one late replay after the dust settles.
// Passing this is the actual proof that a double-submit cannot create two
// orders; the 10% traffic above only makes it likely that races occur.
// ---------------------------------------------------------------------------

export function idempotencyRace(data) {
  const userID = randomUserID();
  // The iteration index alone would not be unique if this run were split
  // across several k6 instances sharing a RUN_ID, and a colliding key would
  // read as a lost race rather than the accounting artefact it is.
  const key = `fk-${RUN_ID}-race-${exec.scenario.iterationInTest}-${uuidv4().slice(0, 8)}`;
  const params = requestParams(data.token, userID, { call: 'race' });
  const body = JSON.stringify(newOrderPayload(key, userID));

  const requests = [];
  for (let i = 0; i < RACE_FANOUT; i++) {
    requests.push(['POST', `${BASE_URL}/orders`, body, params]);
  }

  const responses = http.batch(requests);

  let winners = 0;
  let conflicts = 0;
  for (const res of responses) {
    const verdict = classify(res, params);
    if (verdict.accepted) winners++;
    if (verdict.conflict) conflicts++;
  }
  raceWinners.add(winners);

  // The invariant. More than one accept means two orders exist for one key:
  // the customer was charged twice. This trips the abortOnFail threshold.
  if (winners > 1) {
    doubleProcessed.add(winners - 1);
    console.error(`[race] key=${key} accepted ${winners} times - double processing`);
  }

  check(null, {
    'race: exactly one request accepted': () => winners === 1,
    'race: the losers were refused, not errored': () => winners + conflicts === RACE_FANOUT,
  });

  // The settled path: once the winner is committed, a later replay of the same
  // key must still be refused - by the Redis reservation if it is still held,
  // and by the unique index on idempotency_key if it has expired.
  sleep(0.5);
  const late = http.post(`${BASE_URL}/orders`, body, requestParams(data.token, userID, { call: 'race_late_replay' }));
  const lateVerdict = classify(late, params);
  if (lateVerdict.accepted) {
    doubleProcessed.add(1);
    console.error(`[race] key=${key} was accepted again after settling - idempotency window lost`);
  }
  check(late, {
    'late replay: refused': () => lateVerdict.conflict,
  });
}

// ---------------------------------------------------------------------------
// teardown: an independent read-back check. Thresholds prove what k6 observed;
// this asks the database what it actually stored.
// ---------------------------------------------------------------------------

export function teardown(data) {
  const params = requestParams(data.token, `load-${RUN_ID}-teardown`, { endpoint: 'orders_list' });
  const res = http.get(`${BASE_URL}/orders?limit=500`, params);
  if (res.status !== 200) {
    console.warn(`[teardown] could not read orders back (${res.status}); skipping duplicate scan`);
    return;
  }

  let orders;
  try {
    orders = res.json();
  } catch (err) {
    console.warn(`[teardown] orders list was not JSON: ${err}`);
    return;
  }
  if (!Array.isArray(orders)) return;

  // Sample-based by design: /orders caps at 500 rows, so this inspects the most
  // recent page rather than the whole run. A duplicate anywhere in it is still
  // conclusive proof of a bug; an empty result is not proof of correctness.
  const seen = {};
  let duplicates = 0;
  let mine = 0;
  for (const order of orders) {
    const key = order.idempotency_key;
    if (!key || key.indexOf(`fk-${RUN_ID}-`) !== 0) continue;
    mine++;
    if (seen[key]) {
      duplicates++;
      console.error(`[teardown] duplicate order for idempotency_key=${key}`);
    }
    seen[key] = true;
  }
  console.log(`[teardown] scanned ${mine} of this run's orders in the latest page; duplicates=${duplicates}`);
}

// ---------------------------------------------------------------------------
// Summary. Written without the jslib text-summary import so the script has no
// network dependency of its own; k6's own stdout summary still prints.
// ---------------------------------------------------------------------------

export function handleSummary(summary) {
  const metrics = summary.metrics || {};
  const value = (name, field) => {
    const m = metrics[name];
    if (!m || !m.values) return 0;
    return m.values[field] || 0;
  };

  const lines = [
    '',
    '  FundKit POST /orders - load and idempotency summary',
    `  run_id ${RUN_ID}   mode ${MODE}   target ${BASE_URL}`,
    '  ' + '-'.repeat(60),
    `  p95 latency          ${value('order_create_latency', 'p(95)').toFixed(1)} ms  (budget ${P95_BUDGET_MS} ms)`,
    `  p99 latency          ${value('order_create_latency', 'p(99)').toFixed(1)} ms`,
    `  error rate           ${(value('order_errors', 'rate') * 100).toFixed(3)} %  (budget ${(ERROR_BUDGET * 100).toFixed(1)} %)`,
    `  orders accepted      ${value('idem_accepted', 'count')}`,
    `  idempotent conflicts ${value('idem_conflicts', 'count')}`,
    `  pooled keys accepted ${value('idem_pool_accepted', 'count')}  (must be <= ${REPLAY_POOL})`,
    `  race winners (avg)   ${value('idem_race_winners', 'avg').toFixed(3)}  (must be 1.000)`,
    `  double processed     ${value('idem_double_processed', 'count')}  (must be 0)`,
    `  rate limited (429)   ${value('rate_limited_429', 'count')}`,
    '  ' + '-'.repeat(60),
    '',
  ];

  return {
    stdout: lines.join('\n'),
    [SUMMARY_PATH]: JSON.stringify(summary, null, 2),
  };
}
