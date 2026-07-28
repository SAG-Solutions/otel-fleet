import { render } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { vi } from 'vitest'
import { routeTree } from '@/routeTree.gen'
import type {
  Agent,
  AgentDetail,
  AgentEvent,
  AlertRule,
  ApiKey,
  ApiToken,
  AuditEntry,
  AuthProviderConfig,
  BillingSettings,
  BillingStatement,
  BootstrapToken,
  Customer,
  LogRecord,
  Me,
  Span,
  StatsOverview,
  TraceSummary,
  UserAccount,
  Webhook,
} from '@/api/generated'

export const testMe: Me = {
  id: '4f2c7a1e-0000-4000-8000-000000000001',
  email: 'ops@example.com',
  displayName: 'Ops Admin',
  role: 'admin',
  allCustomers: true,
  csrfToken: 'csrf-test-token',
}

export const testCustomer: Customer = {
  id: '4f2c7a1e-0000-4000-8000-000000000002',
  slug: 'acme',
  name: 'ACME Corp',
  clientId: 'cust_7f3a9b2c',
  status: 'active',
  createdAt: '2026-07-01T09:00:00Z',
}

export const testCustomer2: Customer = {
  id: '4f2c7a1e-0000-4000-8000-000000000005',
  slug: 'globex',
  name: 'Globex Inc',
  clientId: 'cust_2b1a9f3c',
  status: 'active',
  createdAt: '2026-07-03T09:00:00Z',
}

export const testApiKey: ApiKey = {
  id: '4f2c7a1e-0000-4000-8000-000000000003',
  customerId: testCustomer.id,
  name: 'prod-gateway',
  keyPrefix: 'otm_ab12cd34',
  createdAt: '2026-07-02T09:00:00Z',
  expiresAt: null,
  revokedAt: null,
  lastUsedAt: '2026-07-15T08:00:00Z',
}

export const testAgent: AgentDetail = {
  id: '4f2c7a1e-0000-4000-8000-000000000010',
  instanceUid: '019078abcdef40008000aabbccddeeff',
  class: 'edge',
  customerId: testCustomer.id,
  customerName: testCustomer.name,
  name: 'edge-agent-01',
  agentVersion: '0.104.0',
  connected: true,
  healthy: true,
  lastSeenAt: '2026-07-15T08:00:00Z',
  remoteConfigStatus: 'applied',
  remoteConfigError: null,
  assignedConfigHash: 'abc123def4567890',
  reportedConfigHash: 'abc123def4567890',
  configInSync: true,
  createdAt: '2026-07-10T09:00:00Z',
  description: { 'host.name': 'edge-host-01', 'service.name': 'otelcol' },
  health: { healthy: true, status: 'StatusOK' },
}

// A gateway with an operator-set display name + labels and no acknowledged
// config yet (configInSync=null → the neutral "—" chip).
export const testGatewayAgent: Agent = {
  id: '4f2c7a1e-0000-4000-8000-000000000012',
  instanceUid: '01907800000040008000ffeeddccbbaa',
  class: 'gateway',
  customerId: null,
  customerName: null,
  name: 'gateway-eu-01',
  displayName: 'EU Gateway',
  labels: { region: 'eu', role: 'ingest' },
  agentVersion: '0.104.0',
  connected: true,
  healthy: true,
  lastSeenAt: '2026-07-15T08:00:00Z',
  remoteConfigStatus: 'applied',
  remoteConfigError: null,
  assignedConfigHash: 'fff000aaa11122233',
  reportedConfigHash: null,
  configInSync: null,
  createdAt: '2026-07-05T09:00:00Z',
}

export const testAgentEvent: AgentEvent = {
  id: 1,
  eventType: 'connected',
  detail: { remoteAddr: '10.0.0.5' },
  createdAt: '2026-07-15T07:59:00Z',
}

export const testBootstrapToken: BootstrapToken = {
  id: '4f2c7a1e-0000-4000-8000-000000000011',
  customerId: testCustomer.id,
  name: 'factory-floor',
  tokenPrefix: 'obt_9x8y7z',
  maxUses: 0,
  usedCount: 3,
  createdAt: '2026-07-10T09:00:00Z',
  expiresAt: '2027-08-09T09:00:00Z',
  revokedAt: null,
}

export const testApiTokens: ApiToken[] = [
  {
    id: '4f2c7a1e-0000-4000-8000-000000000051',
    name: 'ci-deploy',
    tokenPrefix: 'otm_pat_7a3f',
    role: 'operator',
    createdBy: 'ops@example.com',
    createdAt: '2026-07-10T09:00:00Z',
    expiresAt: '2027-07-10T09:00:00Z',
    lastUsedAt: '2026-07-15T08:00:00Z',
    revokedAt: null,
  },
  {
    id: '4f2c7a1e-0000-4000-8000-000000000052',
    name: 'old-laptop',
    tokenPrefix: 'otm_pat_9c2b',
    role: 'viewer',
    createdBy: 'ops@example.com',
    createdAt: '2026-06-01T09:00:00Z',
    expiresAt: null,
    lastUsedAt: null,
    revokedAt: '2026-07-01T09:00:00Z',
  },
]

export const testViewerMe: Me = {
  id: '4f2c7a1e-0000-4000-8000-000000000021',
  email: 'viewer@example.com',
  displayName: 'Read Only',
  role: 'viewer',
  allCustomers: true,
  csrfToken: 'csrf-test-token',
}

// A tenant-scoped operator: non-admin, allCustomers=false, one grant. The SPA
// treats this session as a self-service portal user.
export const testPortalMe: Me = {
  id: '4f2c7a1e-0000-4000-8000-000000000024',
  email: 'portal@example.com',
  displayName: 'Portal Operator',
  role: 'operator',
  allCustomers: false,
  scopedCustomerIds: [testCustomer.id],
  csrfToken: 'csrf-test-token',
}

export const testUsers: UserAccount[] = [
  {
    id: testMe.id,
    email: testMe.email,
    displayName: testMe.displayName,
    role: 'admin',
    disabled: false,
    invited: false,
    identities: ['google'],
    lastLoginAt: '2026-07-15T08:00:00Z',
    createdAt: '2026-06-01T09:00:00Z',
  },
  {
    id: '4f2c7a1e-0000-4000-8000-000000000022',
    email: 'newbie@example.com',
    displayName: null,
    role: 'operator',
    disabled: false,
    invited: true,
    identities: [],
    lastLoginAt: null,
    createdAt: '2026-07-14T09:00:00Z',
  },
  {
    id: '4f2c7a1e-0000-4000-8000-000000000023',
    email: 'scoped@example.com',
    displayName: 'Scoped Operator',
    role: 'operator',
    disabled: false,
    invited: false,
    identities: ['google'],
    customerIds: [testCustomer.id],
    lastLoginAt: '2026-07-15T08:00:00Z',
    createdAt: '2026-07-12T09:00:00Z',
  },
]

export const testProviders: AuthProviderConfig[] = [
  {
    id: '4f2c7a1e-0000-4000-8000-000000000031',
    type: 'google',
    name: 'google',
    displayName: 'Google Workspace',
    clientId: '1234567890-abc.apps.googleusercontent.com',
    issuer: null,
    enabled: true,
    source: 'database',
    redirectUri: 'https://otelfleet.example.com/auth/google/callback',
    createdAt: '2026-07-01T09:00:00Z',
  },
  {
    id: '4f2c7a1e-0000-4000-8000-000000000032',
    type: 'oidc',
    name: 'corp',
    displayName: 'Corp Login',
    clientId: 'otelfleet-console',
    issuer: 'https://login.corp.example.com',
    enabled: true,
    source: 'environment',
    redirectUri: 'https://otelfleet.example.com/auth/corp/callback',
    createdAt: '2026-07-01T09:00:00Z',
  },
  {
    id: '4f2c7a1e-0000-4000-8000-000000000033',
    type: 'saml',
    name: 'okta-saml',
    displayName: 'Okta SAML',
    clientId: '',
    issuer: null,
    enabled: true,
    source: 'database',
    redirectUri: '',
    idpEntityId: 'https://idp.okta.example.com/entity',
    idpSsoUrl: 'https://idp.okta.example.com/sso/saml',
    idpCertificate: null,
    acsUrl: 'https://otelfleet.example.com/auth/okta-saml/acs',
    spEntityId: 'https://otelfleet.example.com/auth/okta-saml/metadata',
    createdAt: '2026-07-02T09:00:00Z',
  },
]

export const testAuditEntries: AuditEntry[] = [
  {
    id: 42,
    actorType: 'user',
    actorUserId: testMe.id,
    actorEmail: testMe.email,
    action: 'create_pipeline',
    entityType: 'pipeline',
    entityId: '4f2c7a1e-0000-4000-8000-000000000041',
    customerId: testCustomer.id,
    customerName: testCustomer.name,
    payload: { name: 'edge-default' },
    createdAt: '2026-07-15T08:00:00Z',
  },
  {
    id: 41,
    actorType: 'system',
    actorUserId: null,
    actorEmail: null,
    action: 'delete_api_key',
    entityType: 'api_key',
    entityId: '4f2c7a1e-0000-4000-8000-000000000042',
    customerId: testCustomer.id,
    customerName: testCustomer.name,
    payload: null,
    createdAt: '2026-07-14T08:00:00Z',
  },
]

export const testWebhooks: Webhook[] = [
  {
    id: '4f2c7a1e-0000-4000-8000-000000000061',
    type: 'webhook',
    name: 'pagerduty-bridge',
    url: 'https://alerts.example.com/otelfleet',
    events: ['agent_offline', 'agent_unhealthy'],
    enabled: true,
    hasSecret: true,
    createdAt: '2026-07-10T09:00:00Z',
  },
  {
    id: '4f2c7a1e-0000-4000-8000-000000000062',
    type: 'slack',
    name: 'ops-slack',
    url: 'https://hooks.slack.com/services/T000/B000/xxxxxxxx',
    events: ['agent_config_failed'],
    enabled: true,
    hasSecret: false,
    createdAt: '2026-07-11T09:00:00Z',
  },
]

export const testAlertRules: AlertRule[] = [
  {
    id: '4f2c7a1e-0000-4000-8000-000000000071',
    name: 'ingest-drop',
    metric: 'ingest_items',
    comparison: 'below',
    threshold: 999999,
    windowSeconds: 300,
    customerId: null,
    channelIds: ['4f2c7a1e-0000-4000-8000-000000000061'],
    enabled: true,
    createdAt: '2026-07-12T09:00:00Z',
  },
  {
    id: '4f2c7a1e-0000-4000-8000-000000000072',
    name: 'error-spike',
    metric: 'error_logs',
    comparison: 'above',
    threshold: 50,
    windowSeconds: 300,
    customerId: testCustomer.id,
    channelIds: [],
    enabled: false,
    createdAt: '2026-07-13T09:00:00Z',
  },
]

export const testBillingSettings: BillingSettings = {
  pricePerGibMicro: 500_000,
  pricePerMillionItemsMicro: 2_000_000,
  currency: 'EUR',
  updatedAt: '2026-07-01T09:00:00Z',
}

export const testBillingStatement: BillingStatement = {
  month: '2026-07',
  currency: 'EUR',
  pricePerGibMicro: 500_000,
  pricePerMillionItemsMicro: 2_000_000,
  lines: [
    {
      customerId: testCustomer.id,
      name: testCustomer.name,
      items: 3_000_000,
      bytes: 2_147_483_648,
      bytesCostMicro: 1_000_000,
      itemsCostMicro: 6_000_000,
      totalMicro: 7_000_000,
    },
    {
      customerId: testCustomer2.id,
      name: testCustomer2.name,
      items: 1_500_000,
      bytes: 1_073_741_824,
      bytesCostMicro: 500_000,
      itemsCostMicro: 3_000_000,
      totalMicro: 3_500_000,
    },
  ],
  totalMicro: 10_500_000,
}

const testThroughput = {
  series: (['logs', 'traces', 'metrics'] as const).map((signal) => ({
    signal,
    points: [
      { ts: '2026-07-15T07:00:00Z', value: 10 },
      { ts: '2026-07-15T07:15:00Z', value: 12 },
    ],
  })),
}

export const testOverview: StatsOverview = {
  activeCustomers: 3,
  totals: { logs: 864_000, traces: 432_000, metrics: 216_000 },
  refusedRequests: 12,
  topCustomers: [{ customerId: testCustomer.id, name: testCustomer.name, items: 900_000 }],
}

export const testLogs: LogRecord[] = [
  {
    timestamp: '2026-07-20T08:59:00Z',
    severityText: 'ERROR',
    severityNumber: 17,
    serviceName: 'checkout',
    body: 'payment gateway timeout after 3 retries',
    traceId: 'abc123def456abc123def456abc12300',
    spanId: '00ff00ff00ff00ff',
    attributes: { 'http.status_code': '504', 'net.peer.name': 'payments.internal' },
  },
  {
    timestamp: '2026-07-20T08:58:30Z',
    severityText: 'INFO',
    severityNumber: 9,
    serviceName: 'frontend',
    body: 'served / in 12ms',
    traceId: null,
    spanId: null,
    attributes: {},
  },
]

export const testTrace: TraceSummary = {
  traceId: 'abc123def456abc123def456abc12300',
  rootName: 'GET /checkout',
  rootService: 'frontend',
  startTime: '2026-07-20T08:59:00Z',
  durationMs: 1840,
  spanCount: 3,
  errorCount: 1,
}

export const testSpans: Span[] = [
  {
    spanId: 'root000000000001',
    parentSpanId: null,
    name: 'GET /checkout',
    service: 'frontend',
    kind: 'SPAN_KIND_SERVER',
    startTime: '2026-07-20T08:59:00.000Z',
    durationMs: 1840,
    statusCode: 'STATUS_CODE_UNSET',
    statusMessage: null,
    attributes: { 'http.route': '/checkout' },
  },
  {
    spanId: 'child00000000002',
    parentSpanId: 'root000000000001',
    name: 'charge card',
    service: 'checkout',
    kind: 'SPAN_KIND_CLIENT',
    startTime: '2026-07-20T08:59:00.100Z',
    durationMs: 1700,
    statusCode: 'STATUS_CODE_ERROR',
    statusMessage: 'gateway timeout',
    attributes: { 'http.status_code': '504' },
  },
]

function json(data: unknown): Response {
  return new Response(JSON.stringify(data), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

/** Route-matching fetch stub for the endpoints the pages use. */
export function stubApi(overrides: { me?: Me; customers?: Customer[] } = {}): void {
  const me = overrides.me ?? testMe
  const customers = overrides.customers ?? [testCustomer, testCustomer2]
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const request = input instanceof Request ? input : new Request(input)
      const path = new URL(request.url, 'http://localhost').pathname
      switch (path) {
        case '/api/v1/me':
          return json(me)
        case '/api/v1/auth/providers':
          return json({
            providers: [{ name: 'google', displayName: 'Google', loginUrl: '/auth/google/start' }],
            devLoginEnabled: true,
          })
        case '/api/v1/stats/overview':
          return json(testOverview)
        case '/api/v1/customers':
          return json({ customers })
        case `/api/v1/customers/${testCustomer.id}`:
          return json(testCustomer)
        case `/api/v1/customers/${testCustomer.id}/api-keys`:
          return json({ apiKeys: [testApiKey] })
        case '/api/v1/agents':
          return json({ agents: [testAgent, testGatewayAgent] })
        case `/api/v1/agents/${testAgent.id}`:
          return json(testAgent)
        case `/api/v1/agents/${testAgent.id}/config`:
          return json({ assignedYaml: 'receivers: {}\n', reportedYaml: 'receivers: {}\n' })
        case `/api/v1/agents/${testAgent.id}/events`:
          return json({ events: [testAgentEvent] })
        case `/api/v1/customers/${testCustomer.id}/bootstrap-tokens`:
          return json({ tokens: [testBootstrapToken] })
        case `/api/v1/customers/${testCustomer.id}/stats/throughput`:
          return json(testThroughput)
        case `/api/v1/customers/${testCustomer.id}/logs`:
          return json({ logs: testLogs, nextBefore: null })
        case `/api/v1/customers/${testCustomer.id}/traces`:
          return json({ traces: [testTrace], nextBefore: null })
        case `/api/v1/customers/${testCustomer.id}/traces/${testTrace.traceId}`:
          return json({ spans: testSpans })
        case '/api/v1/users':
          return json({ users: testUsers })
        case '/api/v1/settings/auth-providers':
          return json({ providers: testProviders })
        case '/api/v1/settings/api-tokens':
          return json({ tokens: testApiTokens })
        case '/api/v1/audit':
          return json({ entries: testAuditEntries, nextBeforeId: null })
        case '/api/v1/settings/webhooks':
          return json({ webhooks: testWebhooks })
        case '/api/v1/settings/alert-rules':
          return json({ rules: testAlertRules })
        case '/api/v1/settings/billing':
          return json(testBillingSettings)
        case '/api/v1/billing/statement':
          return json(testBillingStatement)
        default:
          return new Response(JSON.stringify({ code: 'not_found', message: 'not found' }), {
            status: 404,
            headers: { 'Content-Type': 'application/json' },
          })
      }
    }),
  )
}

export function renderApp(path: string) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  const view = render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { router, queryClient, view }
}
