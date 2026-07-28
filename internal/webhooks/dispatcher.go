// Package webhooks delivers HMAC-SHA256-signed fleet-event notifications to
// admin-configured webhook endpoints. The dispatcher consumes agent lifecycle
// events from the OpAMP module through a bounded in-process queue — the OpAMP
// path never blocks on webhook delivery.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jansagurna/otelfleet/internal/crypto"
	"github.com/jansagurna/otelfleet/internal/store"
)

// queueSize bounds the event queue; on overflow the oldest event is dropped
// with a warning.
const queueSize = 256

// deliveryTimeout caps one HTTP delivery attempt.
const deliveryTimeout = 10 * time.Second

// Store is the persistence subset the dispatcher needs.
type Store interface {
	ListWebhooks(ctx context.Context) ([]store.Webhook, error)
	GetAgent(ctx context.Context, id uuid.UUID) (store.Agent, error)
}

// Event is one fleet event to fan out.
type Event struct {
	Type       string // store.WebhookEventAgent* — matched against webhook subscriptions
	OccurredAt time.Time
	AgentID    uuid.UUID
	CustomerID uuid.UUID
	Detail     map[string]any
}

// AgentInfo identifies the agent behind an event in the delivery payload.
type AgentInfo struct {
	ID           uuid.UUID  `json:"id"`
	Name         *string    `json:"name"`
	Class        string     `json:"class"`
	CustomerID   *uuid.UUID `json:"customerId"`
	CustomerName *string    `json:"customerName"`
}

// Payload is the JSON body of one webhook delivery.
type Payload struct {
	Event      string         `json:"event"`
	OccurredAt time.Time      `json:"occurredAt"`
	Agent      *AgentInfo     `json:"agent,omitempty"`
	Detail     map[string]any `json:"detail,omitempty"`
}

// Dispatcher fans fleet events out to the subscribed webhooks.
type Dispatcher struct {
	store   Store
	cipher  *crypto.Cipher // nil: master key not configured (unsigned hooks still work)
	httpc   *http.Client
	log     *slog.Logger
	queue   chan Event
	backoff []time.Duration // sleep before retry attempt i+1
	wg      sync.WaitGroup  // in-flight deliveries
}

// New wires the dispatcher (worker started by Run).
func New(st Store, cipher *crypto.Cipher, log *slog.Logger) *Dispatcher {
	return &Dispatcher{
		store:   st,
		cipher:  cipher,
		httpc:   &http.Client{Timeout: deliveryTimeout},
		log:     log,
		queue:   make(chan Event, queueSize),
		backoff: []time.Duration{time.Second, 5 * time.Second, 25 * time.Second},
	}
}

// AgentEvent implements the OpAMP module's event sink; it never blocks.
func (d *Dispatcher) AgentEvent(eventType string, agentID, customerID uuid.UUID, detail map[string]any) {
	d.Publish(Event{
		Type:       eventType,
		OccurredAt: time.Now().UTC(),
		AgentID:    agentID,
		CustomerID: customerID,
		Detail:     detail,
	})
}

// Publish enqueues an event without blocking; when the queue is full the
// oldest queued event is dropped with a warning.
func (d *Dispatcher) Publish(evt Event) {
	for {
		select {
		case d.queue <- evt:
			return
		default:
			select {
			case dropped := <-d.queue:
				d.log.Warn("webhooks: queue overflow, dropping oldest event",
					"event", dropped.Type, "agent", dropped.AgentID)
			default: // a concurrent consumer already made room
			}
		}
	}
}

// Run consumes the queue until ctx is cancelled, then waits briefly for
// in-flight deliveries.
func (d *Dispatcher) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			done := make(chan struct{})
			go func() { d.wg.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				d.log.Warn("webhooks: shutdown with deliveries still in flight")
			}
			return
		case evt := <-d.queue:
			d.dispatch(ctx, evt)
		}
	}
}

// dispatch fans one event out to every enabled webhook subscribed to its
// type; deliveries run concurrently and retry independently.
func (d *Dispatcher) dispatch(ctx context.Context, evt Event) {
	hooks, err := d.store.ListWebhooks(ctx)
	if err != nil {
		d.log.Error("webhooks: list webhooks failed", "err", err)
		return
	}
	var matched []store.Webhook
	for _, wh := range hooks {
		if wh.Enabled && slices.Contains(wh.Events, evt.Type) {
			matched = append(matched, wh)
		}
	}
	if len(matched) == 0 {
		return
	}

	payload := d.buildPayload(ctx, evt)
	for _, wh := range matched {
		body, err := encodeBody(wh.Type, payload)
		if err != nil {
			d.log.Error("webhooks: encode payload failed", "webhook", wh.ID, "type", wh.Type, "err", err)
			continue
		}
		d.wg.Add(1)
		go func(wh store.Webhook, body []byte) {
			defer d.wg.Done()
			d.deliverWithRetry(ctx, wh, evt.Type, body)
		}(wh, body)
	}
}

// encodeBody renders the delivery body for a channel type: a Slack incoming-
// webhook message for slack channels, otherwise the generic JSON payload.
func encodeBody(whType string, p Payload) ([]byte, error) {
	if whType == store.WebhookTypeSlack {
		return json.Marshal(slackMessage(p))
	}
	return json.Marshal(p)
}

// slackMessage formats an event as a Slack incoming-webhook message (mrkdwn).
func slackMessage(p Payload) map[string]any {
	emoji, title := slackTitle(p.Event)
	lines := []string{fmt.Sprintf("%s *otelfleet* — %s", emoji, title)}
	if p.Agent != nil {
		name := "unknown"
		if p.Agent.Name != nil && *p.Agent.Name != "" {
			name = *p.Agent.Name
		}
		lines = append(lines, fmt.Sprintf("*Agent:* %s (%s)", name, p.Agent.Class))
		if p.Agent.CustomerName != nil && *p.Agent.CustomerName != "" {
			lines = append(lines, fmt.Sprintf("*Customer:* %s", *p.Agent.CustomerName))
		}
	}
	keys := make([]string, 0, len(p.Detail))
	for k := range p.Detail {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic ordering
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("*%s:* %v", k, p.Detail[k]))
	}
	lines = append(lines, fmt.Sprintf("_%s_", p.OccurredAt.Format(time.RFC3339)))
	return map[string]any{"text": strings.Join(lines, "\n")}
}

// slackTitle maps an event type to an emoji + human title.
func slackTitle(event string) (emoji, title string) {
	switch event {
	case store.WebhookEventAgentOffline:
		return ":red_circle:", "Agent offline"
	case store.WebhookEventAgentUnhealthy:
		return ":warning:", "Agent unhealthy"
	case store.WebhookEventAgentConfigFailed:
		return ":x:", "Config apply failed"
	case "test":
		return ":bell:", "Test notification"
	case AlertFiring:
		return ":rotating_light:", "Alert firing"
	case AlertResolved:
		return ":white_check_mark:", "Alert resolved"
	default:
		return ":information_source:", event
	}
}

// Alert event types (used as Payload.Event for metric-threshold alerts).
const (
	AlertFiring   = "alert.firing"
	AlertResolved = "alert.resolved"
)

// SendToChannels delivers a payload to an explicit set of channels (metric
// alerts route to a rule's channels rather than by event subscription). It
// formats per channel type and retries like a normal delivery.
func (d *Dispatcher) SendToChannels(ctx context.Context, channels []store.Webhook, p Payload) {
	for _, wh := range channels {
		if !wh.Enabled {
			continue
		}
		body, err := encodeBody(wh.Type, p)
		if err != nil {
			d.log.Error("webhooks: encode alert payload failed", "webhook", wh.ID, "err", err)
			continue
		}
		d.wg.Add(1)
		go func(wh store.Webhook, body []byte) {
			defer d.wg.Done()
			d.deliverWithRetry(ctx, wh, p.Event, body)
		}(wh, body)
	}
}

// buildPayload enriches the event with agent identity from the store; when
// the agent is gone (deleted) the payload carries the IDs it has.
func (d *Dispatcher) buildPayload(ctx context.Context, evt Event) Payload {
	info := &AgentInfo{ID: evt.AgentID, Class: store.AgentClassEdge}
	if evt.CustomerID != uuid.Nil {
		cid := evt.CustomerID
		info.CustomerID = &cid
	}
	if agent, err := d.store.GetAgent(ctx, evt.AgentID); err == nil {
		info.Name = agent.Name
		info.Class = agent.Class
		info.CustomerID = agent.CustomerID
		info.CustomerName = agent.CustomerName
	}
	return Payload{
		Event:      evt.Type,
		OccurredAt: evt.OccurredAt,
		Agent:      info,
		Detail:     evt.Detail,
	}
}

// deliverWithRetry attempts one delivery with exponential backoff (1s/5s/25s)
// on network errors and 5xx responses.
func (d *Dispatcher) deliverWithRetry(ctx context.Context, wh store.Webhook, eventType string, body []byte) {
	for attempt := 0; ; attempt++ {
		status, err := d.Deliver(ctx, wh, eventType, body)
		switch {
		case err == nil && status >= 200 && status < 300:
			d.log.Info("webhooks: delivered", "webhook", wh.ID, "event", eventType, "status", status, "attempt", attempt+1)
			return
		case err == nil && status < 500:
			// Permanent receiver-side verdict (4xx): retrying will not help.
			d.log.Warn("webhooks: delivery rejected", "webhook", wh.ID, "event", eventType, "status", status)
			return
		}
		if attempt >= len(d.backoff) {
			d.log.Error("webhooks: delivery failed, giving up",
				"webhook", wh.ID, "event", eventType, "attempts", attempt+1, "status", status, "err", err)
			return
		}
		d.log.Warn("webhooks: delivery failed, retrying",
			"webhook", wh.ID, "event", eventType, "attempt", attempt+1, "status", status, "err", err)
		select {
		case <-time.After(d.backoff[attempt]):
		case <-ctx.Done():
			return
		}
	}
}

// Deliver performs a single synchronous delivery attempt and returns the
// receiver's HTTP status (also used by the testWebhook endpoint).
func (d *Dispatcher) Deliver(ctx context.Context, wh store.Webhook, eventType string, body []byte) (int, error) {
	reqCtx, cancel := context.WithTimeout(ctx, deliveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, wh.URL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Otelfleet-Event", eventType)
	req.Header.Set("X-Otelfleet-Delivery", uuid.NewString())
	// Slack incoming webhooks do not verify a signature; only sign generic
	// channels that carry a secret.
	if wh.Type != store.WebhookTypeSlack && len(wh.SecretEnc) > 0 {
		secret, err := d.cipher.Decrypt(wh.SecretEnc)
		if err != nil {
			return 0, fmt.Errorf("decrypt webhook secret: %w", err)
		}
		req.Header.Set("X-Otelfleet-Signature", Signature(secret, body))
	}
	resp, err := d.httpc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()                    //nolint:errcheck
	_, _ = io.Copy(io.Discard, resp.Body)      //nolint:errcheck // drain for connection reuse
	return resp.StatusCode, nil
}

// SendTest delivers a synchronous test event and reports the outcome for the
// testWebhook endpoint.
func (d *Dispatcher) SendTest(ctx context.Context, wh store.Webhook) (bool, string) {
	body, err := encodeBody(wh.Type, Payload{
		Event:      "test",
		OccurredAt: time.Now().UTC(),
		Detail:     map[string]any{"message": "otelfleet test delivery", "channel": wh.Name},
	})
	if err != nil {
		return false, err.Error()
	}
	status, err := d.Deliver(ctx, wh, "test", body)
	if err != nil {
		return false, fmt.Sprintf("delivery failed: %v", err)
	}
	if status >= 200 && status < 300 {
		return true, fmt.Sprintf("receiver returned HTTP %d", status)
	}
	return false, fmt.Sprintf("receiver returned HTTP %d", status)
}

// Signature computes the delivery signature header value:
// "sha256=" + hex(HMAC-SHA256(secret, body)).
func Signature(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
