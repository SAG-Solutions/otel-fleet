import { useMemo, useState } from 'react'
import { createFileRoute, Link, redirect } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import {
  createColumnHelper,
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  useReactTable,
  type SortingState,
} from '@tanstack/react-table'
import { ArrowDown, ArrowUp, Plus, Search } from 'lucide-react'
import { getMeOptions, listCustomersOptions } from '@/api/generated/@tanstack/react-query.gen'
import { formatDate } from '@/lib/format'
import { useMe, canMutate, isPortalUser } from '@/hooks/use-me'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { CopyButton } from '@/components/copy-button'
import { StatusBadge } from '@/components/status-badge'
import { ErrorState } from '@/components/error-state'
import { NewCustomerDialog } from '@/components/new-customer-dialog'
import { SecretDialog } from '@/components/secret-dialog'
import type { ApiKeyCreated, Customer } from '@/api/generated'

export const Route = createFileRoute('/_auth/customers/')({
  beforeLoad: async ({ context }) => {
    const me = await context.queryClient.ensureQueryData(getMeOptions())
    if (isPortalUser(me)) throw redirect({ to: '/' })
  },
  component: CustomersPage,
})

const columnHelper = createColumnHelper<Customer>()

const columns = [
  columnHelper.accessor('name', {
    header: 'Name',
    cell: (info) => (
      <Link
        to="/customers/$customerId"
        params={{ customerId: info.row.original.id }}
        className="rounded font-medium text-ink outline-none hover:text-accent hover:underline focus-visible:ring-2 focus-visible:ring-accent/70"
      >
        {info.getValue()}
      </Link>
    ),
  }),
  columnHelper.accessor('slug', {
    header: 'Slug',
    cell: (info) => <span className="font-mono text-xs text-ink-2">{info.getValue()}</span>,
  }),
  columnHelper.accessor('tenantId', {
    header: 'Tenant ID',
    enableSorting: false,
    cell: (info) => (
      <span className="inline-flex items-center gap-1">
        <code className="font-mono text-xs text-ink-2">{info.getValue()}</code>
        <CopyButton value={info.getValue()} label="Copy tenant ID" />
      </span>
    ),
  }),
  columnHelper.accessor('status', {
    header: 'Status',
    cell: (info) => <StatusBadge status={info.getValue()} />,
  }),
  columnHelper.accessor('createdAt', {
    header: 'Created',
    cell: (info) => (
      <span className="text-xs text-ink-2 tabular-nums">{formatDate(info.getValue())}</span>
    ),
  }),
]

function CustomersPage() {
  const me = useMe()
  const customersQuery = useQuery(listCustomersOptions())
  const [search, setSearch] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [createdKey, setCreatedKey] = useState<ApiKeyCreated | null>(null)

  const customers = customersQuery.data?.customers

  const filtered = useMemo(() => {
    if (!customers) return []
    const q = search.trim().toLowerCase()
    if (q === '') return customers
    return customers.filter(
      (c) =>
        c.name.toLowerCase().includes(q) ||
        c.slug.toLowerCase().includes(q) ||
        c.tenantId.toLowerCase().includes(q),
    )
  }, [customers, search])

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold text-ink">Customers</h1>
          <p className="text-[13px] text-ink-2">
            Tenants of the ingest fleet — each with its own tenant ID and API keys.
          </p>
        </div>
        {canMutate(me) && (
          <Button variant="primary" onClick={() => setDialogOpen(true)}>
            <Plus aria-hidden />
            New customer
          </Button>
        )}
      </div>

      <div className="relative max-w-xs">
        <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-ink-3" />
        <Input
          type="search"
          placeholder="Search name, slug, or client ID"
          aria-label="Search customers"
          className="pl-8"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </div>

      {customersQuery.isPending && <CustomersSkeleton />}
      {customersQuery.isError && (
        <ErrorState
          title="Could not load customers"
          onRetry={() => void customersQuery.refetch()}
        />
      )}
      {customersQuery.isSuccess && (
        <CustomersTable
          customers={filtered}
          empty={customers?.length === 0}
          searching={search.trim() !== ''}
        />
      )}

      <NewCustomerDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        onCreated={(_customer, initialApiKey) => setCreatedKey(initialApiKey)}
      />
      <SecretDialog apiKey={createdKey} onClose={() => setCreatedKey(null)} />
    </div>
  )
}

function CustomersTable({
  customers,
  empty,
  searching,
}: {
  customers: Customer[]
  empty: boolean
  searching: boolean
}) {
  const [sorting, setSorting] = useState<SortingState>([])

  const table = useReactTable({
    data: customers,
    columns,
    state: { sorting },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
  })

  if (empty) {
    return (
      <div className="rounded-lg border border-dashed border-line bg-surface px-6 py-10 text-center">
        <div className="text-sm font-semibold text-ink">No customers yet</div>
        <p className="mx-auto mt-1 max-w-md text-[13px] text-ink-2">
          Create the first customer to mint a client ID and an API key for OTLP ingest.
        </p>
      </div>
    )
  }

  return (
    <section className="rounded-lg border border-line bg-surface">
      <Table>
        <TableHeader>
          {table.getHeaderGroups().map((headerGroup) => (
            <TableRow key={headerGroup.id} className="hover:bg-transparent">
              {headerGroup.headers.map((header) => {
                const sortable = header.column.getCanSort()
                const dir = header.column.getIsSorted()
                return (
                  <TableHead key={header.id}>
                    {sortable ? (
                      <button
                        type="button"
                        onClick={header.column.getToggleSortingHandler()}
                        className={cn(
                          'inline-flex cursor-pointer items-center gap-1 rounded uppercase outline-none hover:text-ink focus-visible:ring-2 focus-visible:ring-accent/70',
                          dir && 'text-ink',
                        )}
                      >
                        {flexRender(header.column.columnDef.header, header.getContext())}
                        {dir === 'asc' && <ArrowUp className="size-3" />}
                        {dir === 'desc' && <ArrowDown className="size-3" />}
                      </button>
                    ) : (
                      flexRender(header.column.columnDef.header, header.getContext())
                    )}
                  </TableHead>
                )
              })}
            </TableRow>
          ))}
        </TableHeader>
        <TableBody>
          {table.getRowModel().rows.length === 0 ? (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={columns.length} className="py-6 text-center text-ink-2">
                {searching ? 'No customers match the search.' : 'No customers.'}
              </TableCell>
            </TableRow>
          ) : (
            table.getRowModel().rows.map((row) => (
              <TableRow key={row.id}>
                {row.getVisibleCells().map((cell) => (
                  <TableCell key={cell.id}>
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </TableCell>
                ))}
              </TableRow>
            ))
          )}
        </TableBody>
      </Table>
    </section>
  )
}

function CustomersSkeleton() {
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-line bg-surface p-4">
      {Array.from({ length: 5 }, (_, i) => (
        <Skeleton key={i} className="h-9 w-full" />
      ))}
    </div>
  )
}
