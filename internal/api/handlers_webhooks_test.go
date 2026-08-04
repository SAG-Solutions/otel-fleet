package api

import (
	"testing"

	"github.com/sag-solutions/otel-fleet/internal/api/apigen"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

func TestWebhookTypeAcceptsKnownAndRejectsUnknown(t *testing.T) {
	for _, ok := range []string{store.WebhookTypeGeneric, store.WebhookTypeSlack, store.WebhookTypePagerDuty, store.WebhookTypeOpsgenie} {
		wt := apigen.WebhookType(ok)
		got, err := webhookType(&wt)
		if err != nil || got != ok {
			t.Errorf("webhookType(%q) = %q, %v; want %q, nil", ok, got, err, ok)
		}
	}
	if got, err := webhookType(nil); err != nil || got != store.WebhookTypeGeneric {
		t.Errorf("nil type = %q,%v; want generic default", got, err)
	}
	bad := apigen.WebhookType("telegram")
	if _, err := webhookType(&bad); err == nil {
		t.Error("unknown channel type accepted")
	}
}

func TestValidateChannelURLSecret(t *testing.T) {
	cases := []struct {
		name, chType, url, secret string
		wantErr                   bool
	}{
		{"generic needs valid url", store.WebhookTypeGeneric, "https://hooks.example.com", "", false},
		{"generic rejects bad url", store.WebhookTypeGeneric, "ftp://nope", "", true},
		{"generic rejects empty url", store.WebhookTypeGeneric, "", "", true},
		{"slack needs valid url", store.WebhookTypeSlack, "https://hooks.slack.com/services/x", "", false},
		{"pagerduty needs a secret", store.WebhookTypePagerDuty, "", "", true},
		{"pagerduty ok with secret, default url", store.WebhookTypePagerDuty, "", "routing-key", false},
		{"pagerduty validates a provided url", store.WebhookTypePagerDuty, "ftp://nope", "routing-key", true},
		{"opsgenie needs a secret", store.WebhookTypeOpsgenie, "", "", true},
		{"opsgenie ok with secret", store.WebhookTypeOpsgenie, "", "genie-key", false},
		{"opsgenie EU region url ok", store.WebhookTypeOpsgenie, "https://api.eu.opsgenie.com/v2/alerts", "genie-key", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := validateChannelURLSecret(c.chType, c.url, c.secret)
			if (msg != "") != c.wantErr {
				t.Errorf("validateChannelURLSecret(%q,%q,secret=%t) = %q; wantErr=%t", c.chType, c.url, c.secret != "", msg, c.wantErr)
			}
		})
	}
}
