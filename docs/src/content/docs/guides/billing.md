---
title: "Metered billing"
description: "The Billing page (admin only) turns per-customer ingest usage into a priced monthly statement, on top of the same ClickHouse usage aggregates as the Costs\u2026"
---

The **Billing** page (admin only) turns per-customer ingest usage into a priced
monthly statement, on top of the same ClickHouse usage aggregates as the
[Costs](/otel-fleet/) page.

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
- `GET /api/v1/billing/statement?month=YYYY-MM` — the priced statement for a
  calendar month (admin). Amounts are returned in micro-units.

:::note
Billing reads the same 90-day ClickHouse usage aggregates as Costs, so
statements are available for roughly the trailing quarter. Per-customer
price overrides and invoice numbering are not implemented — this is usage
metering, not a full billing system.
:::
