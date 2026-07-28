import { useQuery } from '@tanstack/react-query'
import { getMeOptions } from '@/api/generated/@tanstack/react-query.gen'
import type { Me } from '@/api/generated'

/**
 * Current session user. The /_auth guard has already primed this query,
 * so inside the authenticated layout data is available from cache.
 */
export function useMe(): Me | undefined {
  const { data } = useQuery(getMeOptions())
  return data
}

/** Viewers see a read-only console; operators and admins can mutate. */
export function canMutate(me: Me | undefined): boolean {
  return me !== undefined && me.role !== 'viewer'
}

/** Settings and the audit log are admin-only surfaces. */
export function isAdmin(me: Me | undefined): boolean {
  return me !== undefined && me.role === 'admin'
}

/**
 * True when the user reaches every customer — admins always, and non-admins
 * with no tenant-scope grants. A scoped user only sees `scopedCustomerIds`.
 */
export function hasAllCustomers(me: Me | undefined): boolean {
  return me === undefined || me.allCustomers
}

/** How many customers a scoped (non-all-access) user is limited to. */
export function scopedCustomerCount(me: Me | undefined): number {
  return me?.scopedCustomerIds?.length ?? 0
}

/**
 * A portal user is a non-admin with per-customer grants (not all-access).
 * They get the purpose-built tenant self-service portal instead of the
 * fleet-wide admin console.
 */
export function isPortalUser(me: Me | undefined): boolean {
  return !isAdmin(me) && me !== undefined && !me.allCustomers && scopedCustomerCount(me) >= 1
}
