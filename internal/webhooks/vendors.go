package webhooks

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/sag-solutions/otel-fleet/internal/store"
)

// Default vendor endpoints. Users may override wh.URL (e.g. the Opsgenie EU
// region api.eu.opsgenie.com, or a PagerDuty proxy).
const (
	pagerDutyEndpoint = "https://events.pagerduty.com/v2/enqueue"
	opsgenieEndpoint  = "https://api.opsgenie.com/v2/alerts"
)

// alertDedupKey builds a stable key correlating a firing event with its later
// resolve — from the alert rule + customer + metric, falling back to the agent
// + event type for non-alert notifications. PagerDuty (dedup_key) and Opsgenie
// (alias) both use it so a resolve closes the alert opened by the fire.
func alertDedupKey(p Payload) string {
	if rule, ok := p.Detail["rule"]; ok {
		cust := "all"
		if c, ok := p.Detail["customer"]; ok && fmt.Sprint(c) != "" {
			cust = fmt.Sprint(c)
		}
		metric := ""
		if m, ok := p.Detail["metric"]; ok {
			metric = fmt.Sprint(m)
		}
		return fmt.Sprintf("otel-fleet:%v:%s:%s", rule, cust, metric)
	}
	if p.Agent != nil {
		return fmt.Sprintf("otel-fleet:%s:%s", p.Event, p.Agent.ID)
	}
	return "otel-fleet:" + p.Event
}

// eventSummary is a one-line human title shared by the PagerDuty summary and
// the Opsgenie message.
func eventSummary(p Payload) string {
	_, title := slackTitle(p.Event)
	s := "otel-fleet — " + title
	if rule, ok := p.Detail["rule"]; ok {
		s += fmt.Sprintf(": %v", rule)
	} else if p.Agent != nil && p.Agent.Name != nil && *p.Agent.Name != "" {
		s += ": " + *p.Agent.Name
	}
	return s
}

// eventSource identifies who the event is about (PagerDuty source field).
func eventSource(p Payload) string {
	if p.Agent != nil {
		if p.Agent.CustomerName != nil && *p.Agent.CustomerName != "" {
			return *p.Agent.CustomerName
		}
		if p.Agent.Name != nil && *p.Agent.Name != "" {
			return *p.Agent.Name
		}
	}
	if c, ok := p.Detail["customer"]; ok && fmt.Sprint(c) != "" {
		return fmt.Sprint(c)
	}
	return "otel-fleet"
}

// pagerDutySeverity maps an alert severity (or, absent one, the agent event
// type) to a PagerDuty Events v2 severity: critical | error | warning | info.
func pagerDutySeverity(p Payload) string {
	if sev, ok := p.Detail["severity"].(string); ok {
		switch sev {
		case store.AlertSeverityCritical:
			return "critical"
		case store.AlertSeverityWarning:
			return "warning"
		case store.AlertSeverityInfo:
			return "info"
		}
	}
	switch p.Event {
	case store.WebhookEventAgentOffline:
		return "critical"
	case store.WebhookEventAgentConfigFailed:
		return "error"
	case store.WebhookEventAgentUnhealthy:
		return "warning"
	default:
		return "info"
	}
}

// opsgeniePriority maps an alert severity (or the agent event type) to an
// Opsgenie priority P1 (highest) .. P5.
func opsgeniePriority(p Payload) string {
	if sev, ok := p.Detail["severity"].(string); ok {
		switch sev {
		case store.AlertSeverityCritical:
			return "P1"
		case store.AlertSeverityWarning:
			return "P3"
		case store.AlertSeverityInfo:
			return "P5"
		}
	}
	switch p.Event {
	case store.WebhookEventAgentOffline:
		return "P1"
	case store.WebhookEventAgentConfigFailed:
		return "P2"
	case store.WebhookEventAgentUnhealthy:
		return "P3"
	default:
		return "P4"
	}
}

// detailStrings renders the payload detail as string values (Opsgenie details
// require string→string).
func detailStrings(p Payload) map[string]string {
	out := make(map[string]string, len(p.Detail))
	for k, v := range p.Detail {
		out[k] = fmt.Sprint(v)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// pagerDutyBody builds a PagerDuty Events API v2 event. A resolve event carries
// only the routing key, action and dedup key (PagerDuty ignores the payload on
// resolve); a trigger carries the full alert payload.
func pagerDutyBody(p Payload, routingKey string) ([]byte, error) {
	action := "trigger"
	if p.Event == AlertResolved {
		action = "resolve"
	}
	body := map[string]any{
		"routing_key":  routingKey,
		"event_action": action,
		"dedup_key":    alertDedupKey(p),
	}
	if action == "trigger" {
		body["payload"] = map[string]any{
			"summary":        truncate(eventSummary(p), 1024),
			"source":         eventSource(p),
			"severity":       pagerDutySeverity(p),
			"timestamp":      p.OccurredAt.Format(time.RFC3339),
			"custom_details": p.Detail,
		}
	}
	return json.Marshal(body)
}

// opsgenieCreateBody builds the Opsgenie create-alert body (a fire event); a
// resolve is a separate close request against the alias endpoint (see render).
func opsgenieCreateBody(p Payload) ([]byte, error) {
	return json.Marshal(map[string]any{
		"message":  truncate(eventSummary(p), 130), // Opsgenie message cap
		"alias":    truncate(alertDedupKey(p), 512),
		"priority": opsgeniePriority(p),
		"source":   "otel-fleet",
		"details":  detailStrings(p),
	})
}

// opsgenieCloseBody is the body for closing an alert by alias.
func opsgenieCloseBody() ([]byte, error) {
	return json.Marshal(map[string]any{"source": "otel-fleet", "note": "resolved by otel-fleet"})
}
