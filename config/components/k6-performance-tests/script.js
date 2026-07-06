// Project Control Plane Overhead — k6 performance test
//
// Creates SCALE_PROJECTS projects concurrently and bootstraps each one by
// listing every resource type in its control plane. This triggers per-project
// goroutine and etcd watcher allocation, which the Grafana dashboard captures
// from Prometheus scraping the milo-apiserver and etcd.
//
// k6 tracks http_req_duration per group automatically; no manual timing needed.
//
// Environment variables:
//   API_SERVER_URL   Milo API server base URL   (default: https://localhost:6443)
//   PERF_TEST_TOKEN  Bearer token               (default: test-admin-token)
//   SCALE_PROJECTS   Number of projects         (default: 20)

import http from 'k6/http';
import { check, group } from 'k6';
import exec from 'k6/execution';

const BASE_URL = (__ENV.API_SERVER_URL || 'https://localhost:6443').replace(/\/$/, '');
const TOKEN = __ENV.PERF_TEST_TOKEN || 'test-admin-token';
const SCALE_PROJECTS = parseInt(__ENV.SCALE_PROJECTS || '20', 10);
const RM_API = '/apis/resourcemanager.miloapis.com/v1alpha1';

// Not deployed in the perf environment (backed by Zitadel).
const SKIP_GROUPS = new Set(['identity.miloapis.com']);

// ── Options ───────────────────────────────────────────────────────────────────

export const options = {
  scenarios: {
    bootstrap: {
      executor: 'shared-iterations',
      // Each iteration creates and bootstraps one project.
      iterations: SCALE_PROJECTS,
      vus: Math.min(20, SCALE_PROJECTS),
      maxDuration: '60m',
      exec: 'bootstrapProject',
    },
  },
  thresholds: {
    // Less than 1% of requests should fail.
    http_req_failed: ['rate<0.01'],
    // Project creation should complete within 5s at p95.
    'http_req_duration{group:::create_project}': ['p(95)<5000'],
  },
};

// ── HTTP params ───────────────────────────────────────────────────────────────

const baseParams = {
  headers: {
    Authorization: `Bearer ${TOKEN}`,
    'Content-Type': 'application/json',
    Accept: 'application/json',
  },
  timeout: '30s',
};

// ── Discovery ─────────────────────────────────────────────────────────────────

function discoverResources() {
  const resources = [];
  const res = http.get(`${BASE_URL}/apis`, baseParams);
  if (res.status !== 200) return resources;

  for (const g of JSON.parse(res.body).groups || []) {
    const gv = g.preferredVersion.groupVersion;
    if (SKIP_GROUPS.has(gv.split('/')[0])) continue;

    const resRes = http.get(`${BASE_URL}/apis/${gv}`, baseParams);
    if (resRes.status !== 200) continue;

    for (const r of JSON.parse(resRes.body).resources || []) {
      if (!r.name.includes('/') && Array.isArray(r.verbs) && r.verbs.includes('list')) {
        resources.push({ groupVersion: gv, resource: r.name });
      }
    }
  }
  return resources;
}

// ── Setup / Teardown ──────────────────────────────────────────────────────────

export function setup() {
  const resources = discoverResources();
  console.log(`discovered ${resources.length} listable resource types`);

  const orgName = `perf-${Date.now()}`;
  const res = http.post(
    `${BASE_URL}${RM_API}/organizations`,
    JSON.stringify({
      apiVersion: 'resourcemanager.miloapis.com/v1alpha1',
      kind: 'Organization',
      metadata: { name: orgName },
      spec: { type: 'Standard' },
    }),
    baseParams,
  );
  check(res, { 'org created': (r) => r.status === 201 });

  return { orgName, resources };
}

export function teardown(data) {
  if (!data) return;
  for (let i = 0; i < SCALE_PROJECTS; i++) {
    const name = `${data.orgName}-p-${String(i).padStart(3, '0')}`;
    http.del(`${BASE_URL}${RM_API}/projects/${name}`, null, baseParams);
  }
  http.del(`${BASE_URL}${RM_API}/organizations/${data.orgName}`, null, baseParams);
}

// ── VU function ───────────────────────────────────────────────────────────────

// Each VU iteration creates and bootstraps one project. The global iteration
// index gives each project a unique, deterministic name so teardown can delete
// them without any shared state.
export function bootstrapProject(data) {
  const idx = exec.scenario.iterationInTest;
  const projectId = `${data.orgName}-p-${String(idx).padStart(3, '0')}`;

  group('create_project', () => {
    const res = http.post(
      // Projects must be created via the org-scoped path so that
      // OrganizationContextHandler injects the required parent Extra fields.
      `${BASE_URL}${RM_API}/organizations/${data.orgName}/control-plane${RM_API}/projects`,
      JSON.stringify({
        apiVersion: 'resourcemanager.miloapis.com/v1alpha1',
        kind: 'Project',
        metadata: { name: projectId },
        spec: { ownerRef: { kind: 'Organization', name: data.orgName } },
      }),
      baseParams,
    );
    check(res, { 'project created': (r) => r.status === 201 });
  });

  group('bootstrap_resources', () => {
    const base = `${BASE_URL}${RM_API}/projects/${projectId}/control-plane`;
    for (const r of data.resources) {
      const res = http.get(`${base}/apis/${r.groupVersion}/${r.resource}`, baseParams);
      // 429 and 503 are transient (watch-cache reinit) — not counted as failures.
      check(res, {
        'resource listed': (r) => r.status === 200 || r.status === 429 || r.status === 503,
      });
    }
  });
}
