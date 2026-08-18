-- +goose Up

-- Per-customer price overrides for metered billing. A row here supersedes the
-- global billing_settings price list for that one customer. Either price column
-- may be NULL, meaning "inherit the global price for this dimension" — so an
-- override can adjust just the GiB rate, just the items rate, or both. No row =
-- fully global pricing (the common case). Prices are integer micro-units of the
-- global currency (the statement is single-currency), matching billing_settings.
CREATE TABLE billing_price_overrides (
    customer_id                   UUID PRIMARY KEY REFERENCES customers(id) ON DELETE CASCADE,
    price_per_gib_micro           BIGINT,
    price_per_million_items_micro BIGINT,
    updated_at                    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by                    UUID REFERENCES users(id)
);

-- +goose Down
DROP TABLE billing_price_overrides;
