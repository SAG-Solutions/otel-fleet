import { createFileRoute, Link, Outlet, redirect, useNavigate } from '@tanstack/react-router'
import { useMutation } from '@tanstack/react-query'
import {
  Building2,
  ChartLine,
  ChevronsUpDown,
  Coins,
  LayoutDashboard,
  LogOut,
  Moon,
  Network,
  Receipt,
  ScrollText,
  Settings,
  Ship,
  Sun,
  Telescope,
} from 'lucide-react'
import { getMeOptions } from '@/api/generated/@tanstack/react-query.gen'
import { logoutMutation } from '@/api/generated/@tanstack/react-query.gen'
import { setCsrfToken } from '@/lib/api-client'
import { useTheme } from '@/lib/theme'
import { useMe, isAdmin, isPortalUser } from '@/hooks/use-me'
import { cn } from '@/lib/utils'
import { Wordmark } from '@/components/wordmark'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import type { ComponentType } from 'react'

export const Route = createFileRoute('/_auth')({
  beforeLoad: async ({ context, location }) => {
    try {
      const me = await context.queryClient.ensureQueryData(getMeOptions())
      setCsrfToken(me.csrfToken)
    } catch {
      throw redirect({ to: '/login', search: { redirect: location.href } })
    }
  },
  component: AuthLayout,
})

function AuthLayout() {
  return (
    <div className="flex min-h-screen bg-bg">
      <Sidebar />
      <main className="min-w-0 flex-1">
        <div className="mx-auto max-w-[1400px] px-6 py-6">
          <Outlet />
        </div>
      </main>
    </div>
  )
}

type NavItem = {
  to: string
  label: string
  icon: ComponentType<{ className?: string }>
  adminOnly?: boolean
}

const NAV_ITEMS: NavItem[] = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/customers', label: 'Customers', icon: Building2 },
  { to: '/pipelines', label: 'Pipelines', icon: Network },
  { to: '/fleet', label: 'Fleet', icon: Ship },
  { to: '/explore', label: 'Explore', icon: Telescope },
  { to: '/metrics', label: 'Metrics', icon: ChartLine },
  { to: '/costs', label: 'Costs', icon: Coins },
  { to: '/billing', label: 'Billing', icon: Receipt, adminOnly: true },
  { to: '/audit', label: 'Audit', icon: ScrollText, adminOnly: true },
  { to: '/settings', label: 'Settings', icon: Settings, adminOnly: true },
]

// Portal users self-manage a single tenant: no fleet-wide or global surfaces.
const PORTAL_NAV_ITEMS: NavItem[] = [
  { to: '/', label: 'Overview', icon: LayoutDashboard },
  { to: '/pipelines', label: 'Pipelines', icon: Network },
  { to: '/fleet', label: 'Agents', icon: Ship },
  { to: '/explore', label: 'Explore', icon: Telescope },
  { to: '/costs', label: 'Usage & cost', icon: Coins },
]

function Sidebar() {
  const me = useMe()
  const items = isPortalUser(me)
    ? PORTAL_NAV_ITEMS
    : NAV_ITEMS.filter((item) => !item.adminOnly || isAdmin(me))
  return (
    <aside className="sticky top-0 flex h-screen w-56 shrink-0 flex-col border-r border-line bg-surface">
      <div className="flex h-14 items-center border-b border-line px-4">
        <Link
          to="/"
          className="rounded outline-none focus-visible:ring-2 focus-visible:ring-accent/70"
        >
          <Wordmark />
        </Link>
      </div>
      <nav aria-label="Main" className="flex flex-1 flex-col gap-0.5 p-2">
        {items.map(({ to, label, icon: Icon }) => (
          <Link
            key={to}
            to={to}
            activeOptions={{ exact: to === '/' }}
            className="group flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-[13px] text-ink-2 transition-colors outline-none hover:bg-surface-2 hover:text-ink focus-visible:ring-2 focus-visible:ring-accent/70 data-[status=active]:bg-surface-2 data-[status=active]:font-medium data-[status=active]:text-ink"
          >
            {({ isActive }) => (
              <>
                <span
                  aria-hidden
                  className={cn(
                    'h-4 w-0.5 rounded-full transition-colors',
                    isActive ? 'bg-accent' : 'bg-transparent',
                  )}
                />
                <Icon className="size-4 shrink-0" />
                {label}
              </>
            )}
          </Link>
        ))}
      </nav>
      <UserMenu />
    </aside>
  )
}

function UserMenu() {
  const me = useMe()
  const { theme, toggleTheme } = useTheme()
  const navigate = useNavigate()

  const logout = useMutation({
    ...logoutMutation(),
    onSettled: () => {
      setCsrfToken(null)
      void navigate({ to: '/login' })
    },
  })

  return (
    <div className="border-t border-line p-2">
      <DropdownMenu>
        <DropdownMenuTrigger className="flex w-full cursor-pointer items-center gap-2 rounded-md px-2.5 py-2 text-left outline-none hover:bg-surface-2 focus-visible:ring-2 focus-visible:ring-accent/70">
          <span className="flex size-6 shrink-0 items-center justify-center rounded-full bg-accent/15 font-mono text-[11px] font-semibold text-accent uppercase">
            {me?.email.slice(0, 2) ?? '··'}
          </span>
          <span className="min-w-0 flex-1">
            <span className="block truncate text-[13px] text-ink">
              {me?.displayName ?? me?.email ?? '—'}
            </span>
            <span className="block truncate font-mono text-[11px] text-ink-3">
              {me?.role ?? ''}
            </span>
          </span>
          <ChevronsUpDown className="size-3.5 shrink-0 text-ink-3" />
        </DropdownMenuTrigger>
        <DropdownMenuContent side="top" align="start" className="w-52">
          <DropdownMenuLabel className="truncate font-mono">{me?.email}</DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onSelect={(e) => {
              e.preventDefault()
              toggleTheme()
            }}
          >
            {theme === 'dark' ? <Sun /> : <Moon />}
            {theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={() => logout.mutate({})}>
            <LogOut />
            Log out
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
