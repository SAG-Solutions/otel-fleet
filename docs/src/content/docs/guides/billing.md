---
title: "Metered billing"
description: "The Billing page (admin only) turns per-customer ingest usage into a priced monthly statement, on top of the same ClickHouse usage aggregates as the Costs\u2026"
---

The **Billing** page (admin only) turns per-customer ingest usage into a priced
monthly statement, on top of the same ClickHouse usage aggregates as the
[Costs](/) page.

## Pricing

Set a global price list under **Billing → Pricing**:

- **Price per GiB** — charged on ingested volume (the estimated in-memory row
  size, `byteSize`; not compressed-at-rest).
- **Price per million items** — charged on record count (log records, spans,
  metric data points).
- **Currency** — a 3-letter code shown on statements (display only; no FX).

Prices are stored as integer **micro-units** (1,000,000 micro = 1 unit of
currency) so statement math is exact — no floating-point rounding. The UI shows
and accepts plain decimals.

### Per-customer overrides

Under **Billing → Per-customer overrides** you can give a specific customer a
custom rate that supersedes the global price list. Set the price for one or both
dimensions; leave a field blank to **inherit** the global rate for that
dimension. A customer with no override bills entirely at the global rate.
Overridden customers are flagged with an `override` badge on the statement (and
an `Overridden` column in the CSV export).

Overrides are single-currency — they reuse the global currency; only the rates
differ.

## Monthly statement

Pick a calendar month; the statement lists every customer with:

| Column | Meaning |
|---|---|
| Items | total records ingested in the month |
| Volume | estimated ingested bytes |
| Volume cost | `bytes / GiB × price per GiB` |
| Items cost | `items / 1e6 × price per million items` |
| Total | volume cost + items cost |

with a grand total across customers. Rows are sorted by amount. **Export CSV**
downloads the statement for import into a billing system or spreadsheet.

## API

- `GET /api/v1/settings/billing` · `PUT /api/v1/settings/billing` — the price
  list (admin).
- `GET /api/v1/settings/billing/overrides` — list per-customer overrides
  (admin). `PUT` · `DELETE /api/v1/settings/billing/overrides/{customerId}` —
  set or remove one customer's override (admin).
- `GET /api/v1/billing/statement?month=YYYY-MM` — the priced statement for a
  calendar month (admin). Amounts are returned in micro-units; each line carries
  an `overridden` flag.

:::note
Billing reads the same 90-day ClickHouse usage aggregates as Costs, so
statements are available for roughly the trailing quarter. Invoice numbering
and persisted invoices are not implemented — this is usage metering with
per-customer pricing, not a full accounts-receivable system.
:::
