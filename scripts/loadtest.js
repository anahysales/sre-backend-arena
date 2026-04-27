import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  stages: [
    { duration: '30s', target: 20 },
    { duration: '1m', target: 50 },
    { duration: '30s', target: 0 },
  ],
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<500'],
  },
};

export default function () {
  const id = Math.floor(Math.random() * 1000);

  const res = http.get(`http://localhost:30007/deathstar-analysis/${id}`);

  check(res, {
    'status 200': (r) => r.status === 200,
  });

  sleep(1);
}