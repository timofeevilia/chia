// k6 load test: hammers the ledger with concurrent multi-currency expenses,
// retries every write with the same Idempotency-Key, and then proves that no
// money was created, lost or double-booked.
//
//   docker compose up --build -d      (with RATE_LIMIT_PER_MINUTE=0)
//   k6 run loadtest/ledger.js
//
// Tune with env vars: BASE_URL, VUS, DURATION, GROUPS.
import http from 'k6/http';
import { check, fail } from 'k6';
import { Counter } from 'k6/metrics';
import { uuidv4 } from 'https://jslib.k6.io/k6-utils/1.4.0/index.js';

const BASE = __ENV.BASE_URL || 'http://localhost:8080';
const GROUPS = Number(__ENV.GROUPS || 20);
const JSON_HEADERS = { 'Content-Type': 'application/json' };

const imbalance = new Counter('ledger_imbalanced_groups');
const duplicates = new Counter('ledger_duplicate_writes');

export const options = {
  scenarios: {
    writers: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '10s', target: Number(__ENV.VUS || 50) },
        { duration: __ENV.DURATION || '40s', target: Number(__ENV.VUS || 50) },
        { duration: '5s', target: 0 },
      ],
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.001'],
    'http_req_duration{op:expense}': ['p(95)<150', 'p(99)<400'],
    'http_req_duration{op:balances}': ['p(95)<100'],
    checks: ['rate>0.999'],
    // The correctness gates: any violation fails the run.
    ledger_imbalanced_groups: ['count==0'],
    ledger_duplicate_writes: ['count==0'],
  },
};

const CURRENCIES = ['SGD', 'VND', 'USD', 'THB', 'JPY'];

export function setup() {
  const groups = [];
  for (let i = 0; i < GROUPS; i++) {
    const res = http.post(`${BASE}/v1/groups`, JSON.stringify({
      name: `Load test ${i}`,
      base_currency: i % 2 ? 'VND' : 'SGD',
      members: ['An', 'Binh', 'Chi', 'Dung', 'Mei'],
    }), { headers: JSON_HEADERS });
    if (res.status !== 201) fail(`setup: create group returned ${res.status}: ${res.body}`);
    const g = res.json();
    groups.push({ id: g.id, members: g.members.map((m) => m.id) });
  }
  return { groups };
}

export default function (data) {
  const g = data.groups[Math.floor(Math.random() * data.groups.length)];
  const payer = g.members[Math.floor(Math.random() * g.members.length)];
  const currency = CURRENCIES[Math.floor(Math.random() * CURRENCIES.length)];
  const body = JSON.stringify({
    description: 'k6 expense',
    amount_minor: 100 + Math.floor(Math.random() * 500000),
    currency,
    paid_by: payer,
    shares: g.members.map((m, i) => ({ member_id: m, weight: 1 + (i % 3) })),
  });
  const params = {
    headers: Object.assign({ 'Idempotency-Key': uuidv4() }, JSON_HEADERS),
    tags: { op: 'expense' },
  };

  const first = http.post(`${BASE}/v1/groups/${g.id}/expenses`, body, params);
  const ok = check(first, { 'expense created': (r) => r.status === 201 });
  if (!ok) return;

  // Simulate a client that timed out and retried: must get the same transaction.
  const retry = http.post(`${BASE}/v1/groups/${g.id}/expenses`, body, params);
  const same = check(retry, {
    'retry replayed': (r) => r.status === 201 && r.json('id') === first.json('id'),
  });
  if (!same) duplicates.add(1);

  const bal = http.get(`${BASE}/v1/groups/${g.id}/balances`, { tags: { op: 'balances' } });
  check(bal, { 'balances ok': (r) => r.status === 200 });
}

export function teardown(data) {
  for (const g of data.groups) {
    const res = http.get(`${BASE}/v1/groups/${g.id}/balances`);
    const sum = res.json('balances').reduce((acc, b) => acc + b.amount_minor, 0);
    if (res.status !== 200 || sum !== 0) imbalance.add(1);
  }
}
