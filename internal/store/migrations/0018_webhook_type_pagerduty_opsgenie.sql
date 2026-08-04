-- +goose Up

-- Add PagerDuty (Events API v2) and Opsgenie (Alert API) as first-class
-- notification-channel types. Both carry a required secret (PagerDuty routing
-- key / Opsgenie API key) and post to a fixed vendor endpoint. The inline
-- CHECK from 0010 is auto-named webhooks_type_check.
ALTER TABLE webhooks DROP CONSTRAINT webhooks_type_check;
ALTER TABLE webhooks ADD CONSTRAINT webhooks_type_check
    CHECK (type IN ('webhook', 'slack', 'pagerduty', 'opsgenie'));

-- +goose Down
ALTER TABLE webhooks DROP CONSTRAINT webhooks_type_check;
ALTER TABLE webhooks ADD CONSTRAINT webhooks_type_check
    CHECK (type IN ('webhook', 'slack'));
