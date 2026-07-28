import { createFileRoute, Link } from '@tanstack/react-router'
import { cn } from '@/lib/utils'
import { AdminGate } from '@/components/admin-gate'
import { ApiTokensTab } from '@/features/settings/api-tokens-tab'
import { SsoTab } from '@/features/settings/sso-tab'
import { UsersTab } from '@/features/settings/users-tab'
import { WebhooksTab } from '@/features/settings/webhooks-tab'
import { AlertRulesTab } from '@/features/settings/alert-rules-tab'

const TABS = ['sso', 'users', 'tokens', 'alerts', 'alert-rules'] as const
type Tab = (typeof TABS)[number]

interface SettingsSearch {
  tab?: Tab
}

export const Route = createFileRoute('/_auth/settings')({
  validateSearch: (search: Record<string, unknown>): SettingsSearch => ({
    tab: TABS.includes(search.tab as Tab) ? (search.tab as Tab) : undefined,
  }),
  component: SettingsPage,
})

function SettingsPage() {
  const { tab = 'sso' } = Route.useSearch()

  return (
    <AdminGate>
      <div className="flex flex-col gap-5">
        <div>
          <h1 className="text-lg font-semibold text-ink">Settings</h1>
          <p className="text-[13px] text-ink-2">
            Single sign-on providers and console user accounts.
          </p>
        </div>
        <TabBar active={tab} />
        {tab === 'sso' && <SsoTab />}
        {tab === 'users' && <UsersTab />}
        {tab === 'tokens' && <ApiTokensTab />}
        {tab === 'alerts' && <WebhooksTab />}
        {tab === 'alert-rules' && <AlertRulesTab />}
      </div>
    </AdminGate>
  )
}

function TabBar({ active }: { active: Tab }) {
  const labels: Record<Tab, string> = {
    sso: 'SSO providers',
    users: 'Users',
    tokens: 'API tokens',
    alerts: 'Alerts',
    'alert-rules': 'Alert rules',
  }
  return (
    <nav aria-label="Settings sections" className="flex gap-1 border-b border-line">
      {TABS.map((tab) => (
        <Link
          key={tab}
          to="/settings"
          search={{ tab }}
          aria-current={active === tab ? 'page' : undefined}
          className={cn(
            '-mb-px rounded-t px-3 py-2 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-accent/70',
            active === tab
              ? 'border-b-2 border-accent font-medium text-ink'
              : 'border-b-2 border-transparent text-ink-2 hover:text-ink',
          )}
        >
          {labels[tab]}
        </Link>
      ))}
    </nav>
  )
}
