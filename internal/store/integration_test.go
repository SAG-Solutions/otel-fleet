//go:build integration

// Package store integration tests run the REAL goose migrations against a real
// PostgreSQL and exercise the PG store end-to-end. They catch the class of bug
// unit tests with fakes cannot — SQL errors, column/scan mismatches (e.g. a
// hardcoded column list drifting from scanX), constraint violations — which
// otherwise only surface live.
//
// Run:  OTEL_FLEET_TEST_DATABASE_URL=postgres://otelfleet:otelfleet@localhost:5432/otel_fleet_test \
//         go test -tags=integration ./internal/store/...
// With the env unset the whole suite skips, so `go test ./...` stays green.
package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sag-solutions/otel-fleet/internal/audit"
)

var testPG *PG

func TestMain(m *testing.M) {
	dsn := os.Getenv("OTEL_FLEET_TEST_DATABASE_URL")
	if dsn == "" {
		fmt.Println("skipping store integration tests: OTEL_FLEET_TEST_DATABASE_URL unset")
		os.Exit(0)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
	if err := Migrate(ctx, pool); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	testPG = NewPG(pool)
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	return context.Background()
}

func uniq() string { return uuid.NewString()[:8] }

func auditEntry(action, entityType, entityID string) []audit.Entry {
	return []audit.Entry{{Action: action, EntityType: entityType, EntityID: entityID}}
}

// makeCustomer creates a customer + its initial API key and returns it.
func makeCustomer(t *testing.T) Customer {
	t.Helper()
	ctx := ctxT(t)
	id := uuid.New()
	slug := "it-" + uniq()
	cust, key, err := testPG.CreateCustomer(ctx,
		NewCustomer{ID: id, Slug: slug, Name: "IT " + slug, ClientID: "cust_" + uniq(), Region: "default"},
		NewAPIKey{ID: uuid.New(), CustomerID: id, Name: "default", KeyPrefix: "otm_" + uniq(), KeyHash: []byte("hash")},
		auditEntry("customer.create", "customer", id.String()))
	if err != nil {
		t.Fatalf("CreateCustomer: %v", err)
	}
	if cust.ID != id || cust.Slug != slug {
		t.Fatalf("customer round-trip mismatch: %+v", cust)
	}
	if key.CustomerID != id {
		t.Fatalf("initial key customer mismatch: %+v", key)
	}
	return cust
}

func TestIntegrationCustomers(t *testing.T) {
	ctx := ctxT(t)
	c := makeCustomer(t)

	got, err := testPG.GetCustomer(ctx, c.ID)
	if err != nil || got.ClientID != c.ClientID {
		t.Fatalf("GetCustomer: %+v err=%v", got, err)
	}
	if _, err := testPG.ListCustomers(ctx, nil); err != nil {
		t.Fatalf("ListCustomers: %v", err)
	}
	active := CustomerActive
	if _, err := testPG.ListCustomers(ctx, &active); err != nil {
		t.Fatalf("ListCustomers(active): %v", err)
	}
	if _, err := testPG.ListCustomerRefs(ctx); err != nil {
		t.Fatalf("ListCustomerRefs: %v", err)
	}
	if _, err := testPG.CountActiveCustomers(ctx); err != nil {
		t.Fatalf("CountActiveCustomers: %v", err)
	}

	rate := 500
	upd, err := testPG.UpdateCustomer(ctx, c.ID, CustomerUpdate{RateLimitItemsPerSec: OptionalInt{Set: true, Value: &rate}}, auditEntry("customer.update", "customer", c.ID.String()))
	if err != nil || upd.RateLimitItemsPerSec == nil || *upd.RateLimitItemsPerSec != 500 {
		t.Fatalf("UpdateCustomer quota: %+v err=%v", upd, err)
	}
	if _, err := testPG.ListAPIKeys(ctx, c.ID); err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
}

// TestIntegrationUsers guards the userCols/scanUser* column lists — the class
// of bug that once broke every login (7-vs-8 column mismatch).
func TestIntegrationUsers(t *testing.T) {
	ctx := ctxT(t)
	// Seed an enabled admin so the last-admin invariant holds for role changes.
	if _, err := testPG.CreateInvitedUser(ctx, uuid.New(), "admin-"+uniq()+"@example.com", "admin", auditEntry("user.invite", "user", "admin")); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	email := "it-" + uniq() + "@example.com"
	u, err := testPG.CreateInvitedUser(ctx, uuid.New(), email, "operator", auditEntry("user.invite", "user", email))
	if err != nil {
		t.Fatalf("CreateInvitedUser: %v", err)
	}
	if _, err := testPG.GetUser(ctx, u.ID); err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if _, err := testPG.GetUserWithIdentities(ctx, u.ID); err != nil {
		t.Fatalf("GetUserWithIdentities: %v", err)
	}
	byEmail, err := testPG.GetUserByEmail(ctx, email)
	if err != nil || byEmail.ID != u.ID {
		t.Fatalf("GetUserByEmail: %+v err=%v", byEmail, err)
	}
	if _, err := testPG.ListUsers(ctx); err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	role := "viewer"
	if _, err := testPG.UpdateUserAdmin(ctx, u.ID, UserUpdate{Role: &role}, auditEntry("user.update", "user", u.ID.String())); err != nil {
		t.Fatalf("UpdateUserAdmin: %v", err)
	}

	// SCIM provisioning + external id.
	dn, ext := "SCIM User", "idp-"+uniq()
	semail := "scim-" + uniq() + "@example.com"
	su, err := testPG.CreateSCIMUser(ctx, uuid.New(), semail, "viewer", &dn, &ext, auditEntry("scim.user.create", "user", semail))
	if err != nil || su.ExternalID == nil || *su.ExternalID != ext {
		t.Fatalf("CreateSCIMUser: %+v err=%v", su, err)
	}
	dn2 := "SCIM Renamed"
	if _, err := testPG.UpdateSCIMUser(ctx, su.ID, &dn2, &ext, auditEntry("scim.user.update", "user", su.ID.String())); err != nil {
		t.Fatalf("UpdateSCIMUser: %v", err)
	}

	// Tenant-scope grants round-trip.
	c := makeCustomer(t)
	if err := testPG.SetUserCustomerGrants(ctx, u.ID, []uuid.UUID{c.ID}, auditEntry("user.grants", "user", u.ID.String())); err != nil {
		t.Fatalf("SetUserCustomerGrants: %v", err)
	}
	ids, err := testPG.ListUserCustomerIDs(ctx, u.ID)
	if err != nil || len(ids) != 1 || ids[0] != c.ID {
		t.Fatalf("ListUserCustomerIDs: %v err=%v", ids, err)
	}
	// Clearing grants.
	if err := testPG.SetUserCustomerGrants(ctx, u.ID, nil, auditEntry("user.grants", "user", u.ID.String())); err != nil {
		t.Fatalf("clear grants: %v", err)
	}
	if ids, _ := testPG.ListUserCustomerIDs(ctx, u.ID); len(ids) != 0 {
		t.Fatalf("grants not cleared: %v", ids)
	}
}

// TestIntegrationAuthProviders guards authProviderCols incl. the saml_config
// column added in migration 0012.
func TestIntegrationAuthProviders(t *testing.T) {
	ctx := ctxT(t)
	issuer := "https://accounts.google.com"
	oidc, err := testPG.CreateAuthProvider(ctx, NewAuthProvider{
		ID: uuid.New(), Type: "oidc", Name: "oidc-" + uniq(), DisplayName: "OIDC",
		ClientID: "cid", ClientSecretEnc: []byte("enc"), Issuer: &issuer, Enabled: true,
	}, auditEntry("authprovider.create", "auth_provider", "oidc"))
	if err != nil {
		t.Fatalf("CreateAuthProvider oidc: %v", err)
	}
	saml, err := testPG.CreateAuthProvider(ctx, NewAuthProvider{
		ID: uuid.New(), Type: "saml", Name: "saml-" + uniq(), DisplayName: "SAML",
		SAMLConfig: []byte(`{"idpEntityId":"e","idpSsoUrl":"https://idp/sso","idpCertificate":"pem"}`),
		ClientSecretEnc: []byte{}, Enabled: true,
	}, auditEntry("authprovider.create", "auth_provider", "saml"))
	if err != nil {
		t.Fatalf("CreateAuthProvider saml: %v", err)
	}
	if len(saml.SAMLConfig) == 0 {
		t.Fatal("saml_config not persisted")
	}
	if _, err := testPG.GetAuthProvider(ctx, oidc.ID); err != nil {
		t.Fatalf("GetAuthProvider: %v", err)
	}
	if _, err := testPG.GetAuthProviderByName(ctx, saml.Name); err != nil {
		t.Fatalf("GetAuthProviderByName: %v", err)
	}
	if _, err := testPG.ListAuthProviders(ctx, false); err != nil {
		t.Fatalf("ListAuthProviders: %v", err)
	}
}

// TestIntegrationWebhooks guards webhookCols incl. the type column (0010) and
// that migration 0018 relaxed the CHECK to admit pagerduty/opsgenie.
func TestIntegrationWebhooks(t *testing.T) {
	ctx := ctxT(t)
	for _, typ := range []string{WebhookTypeGeneric, WebhookTypeSlack, WebhookTypePagerDuty, WebhookTypeOpsgenie} {
		w, err := testPG.CreateWebhook(ctx, NewWebhook{
			ID: uuid.New(), Type: typ, Name: typ + "-" + uniq(), URL: "https://hooks.example.com",
			Events: []string{WebhookEventAgentOffline}, Enabled: true,
		}, auditEntry("webhook.create", "webhook", typ))
		if err != nil || w.Type != typ {
			t.Fatalf("CreateWebhook %s: %+v err=%v", typ, w, err)
		}
		if _, err := testPG.GetWebhook(ctx, w.ID); err != nil {
			t.Fatalf("GetWebhook: %v", err)
		}
	}
	if _, err := testPG.ListWebhooks(ctx); err != nil {
		t.Fatalf("ListWebhooks: %v", err)
	}
}

// TestIntegrationAgents guards agentCols incl. acked_config_hash/display_name/
// labels (0008), enrollment, meta update and events.
func TestIntegrationAgents(t *testing.T) {
	ctx := ctxT(t)
	c := makeCustomer(t)
	bt, err := testPG.CreateBootstrapToken(ctx, NewBootstrapToken{
		ID: uuid.New(), CustomerID: c.ID, Name: "bt-" + uniq(), TokenPrefix: "otm_bt_" + uniq(),
		TokenHash: []byte("h"), ExpiresAt: time.Now().Add(time.Hour),
	}, auditEntry("bootstrap.create", "bootstrap_token", "bt"))
	if err != nil {
		t.Fatalf("CreateBootstrapToken: %v", err)
	}
	name := "edge-" + uniq()
	ag, err := testPG.EnrollAgent(ctx, NewAgent{
		ID: uuid.New(), InstanceUID: uuid.New().NodeID(), CustomerID: c.ID, Class: AgentClassEdge,
		Name: &name, Capabilities: 14599, EnrolledVia: bt.ID,
	})
	if err != nil {
		t.Fatalf("EnrollAgent: %v", err)
	}
	if _, err := testPG.GetAgent(ctx, ag.ID); err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if _, err := testPG.GetAgentByInstanceUID(ctx, ag.InstanceUID); err != nil {
		t.Fatalf("GetAgentByInstanceUID: %v", err)
	}
	edge := AgentClassEdge
	if _, err := testPG.ListAgents(ctx, AgentFilter{Class: &edge, CustomerID: &c.ID}); err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	dn := "Edge (Berlin)"
	updated, err := testPG.UpdateAgentMeta(ctx, ag.ID, &dn, []byte(`{"env":"prod"}`), auditEntry("agent.update", "agent", ag.ID.String()))
	if err != nil || updated.DisplayName == nil || *updated.DisplayName != dn {
		t.Fatalf("UpdateAgentMeta: %+v err=%v", updated, err)
	}
	if err := testPG.SetAgentAckedConfig(ctx, ag.ID, []byte("acked")); err != nil {
		t.Fatalf("SetAgentAckedConfig: %v", err)
	}
	if _, err := testPG.ListAgentEvents(ctx, ag.ID, 50); err != nil {
		t.Fatalf("ListAgentEvents: %v", err)
	}
}

func TestIntegrationAPITokens(t *testing.T) {
	ctx := ctxT(t)
	prefix := "otm_pat_" + uniq()
	tok, err := testPG.CreateAPIToken(ctx, NewAPIToken{
		ID: uuid.New(), Name: "ci-" + uniq(), TokenPrefix: prefix, TokenHash: []byte("h"), Role: "admin",
	}, auditEntry("apitoken.create", "api_token", prefix))
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	if _, err := testPG.ListAPITokens(ctx); err != nil {
		t.Fatalf("ListAPITokens: %v", err)
	}
	if _, err := testPG.ActiveAPITokensByPrefix(ctx, prefix); err != nil {
		t.Fatalf("ActiveAPITokensByPrefix: %v", err)
	}
	if err := testPG.RevokeAPIToken(ctx, tok.ID, auditEntry("apitoken.revoke", "api_token", tok.ID.String())); err != nil {
		t.Fatalf("RevokeAPIToken: %v", err)
	}
}

func TestIntegrationPipelines(t *testing.T) {
	ctx := ctxT(t)
	c := makeCustomer(t)
	pid, vid := uuid.New(), uuid.New()
	pipe, ver, err := testPG.CreatePipeline(ctx,
		NewPipeline{ID: pid, CustomerID: c.ID, Name: "pipe-" + uniq(), TargetClass: ClassForwarding},
		NewPipelineVersion{ID: vid, PipelineID: pid, Graph: []byte(`{"signals":["logs"]}`), RenderedYAML: "exporters: {}", ConfigHash: []byte("h"), ValidationStatus: ValidationValid},
		auditEntry("pipeline.create", "pipeline", pid.String()))
	if err != nil {
		t.Fatalf("CreatePipeline: %v", err)
	}
	if _, err := testPG.GetPipeline(ctx, pipe.ID); err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}
	if _, err := testPG.ListPipelines(ctx, &c.ID); err != nil {
		t.Fatalf("ListPipelines: %v", err)
	}
	if _, err := testPG.ListPipelineVersions(ctx, pipe.ID); err != nil {
		t.Fatalf("ListPipelineVersions: %v", err)
	}
	if _, _, err := testPG.ActivatePipelineVersion(ctx, pipe.ID, ver.Version, auditEntry("pipeline_version.activate", "pipeline", pipe.ID.String())); err != nil {
		t.Fatalf("ActivatePipelineVersion: %v", err)
	}
}

// TestIntegrationBillingSettings guards the singleton billing_settings (0013).
func TestIntegrationBillingSettings(t *testing.T) {
	ctx := ctxT(t)
	if _, err := testPG.GetBillingSettings(ctx); err != nil {
		t.Fatalf("GetBillingSettings (seeded row): %v", err)
	}
	gib, mil := int64(2_000_000), int64(500_000)
	cur := "EUR"
	got, err := testPG.UpdateBillingSettings(ctx, BillingSettingsUpdate{PricePerGiBMicro: &gib, PricePerMillionItemsMicro: &mil, Currency: &cur}, nil, auditEntry("billing.settings.update", "billing_settings", "singleton"))
	if err != nil || got.PricePerGiBMicro != gib || got.Currency != "EUR" {
		t.Fatalf("UpdateBillingSettings: %+v err=%v", got, err)
	}
}

// TestIntegrationBillingOverrides guards the per-customer price overrides
// (migration 0020): upsert (incl. a nil price = inherit), list, and delete
// with the not-found path.
func TestIntegrationBillingOverrides(t *testing.T) {
	ctx := ctxT(t)
	c := makeCustomer(t)

	gib, mil := int64(5_000_000), int64(750_000)
	got, err := testPG.SetBillingOverride(ctx, c.ID, &gib, &mil, nil, auditEntry("billing.override.set", "billing_price_override", c.ID.String()))
	if err != nil || got.PricePerGiBMicro == nil || *got.PricePerGiBMicro != gib || got.PricePerMillionItemsMicro == nil || *got.PricePerMillionItemsMicro != mil {
		t.Fatalf("SetBillingOverride: %+v err=%v", got, err)
	}

	// Upsert again with the items rate nil → inherit global for items only.
	got, err = testPG.SetBillingOverride(ctx, c.ID, &gib, nil, nil, auditEntry("billing.override.set", "billing_price_override", c.ID.String()))
	if err != nil || got.PricePerMillionItemsMicro != nil {
		t.Fatalf("SetBillingOverride (inherit items): %+v err=%v", got, err)
	}

	list, err := testPG.ListBillingOverrides(ctx)
	if err != nil {
		t.Fatalf("ListBillingOverrides: %v", err)
	}
	found := false
	for _, o := range list {
		if o.CustomerID == c.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("override for %s not listed", c.ID)
	}

	// Unknown customer → ErrNotFound (FK violation mapped).
	if _, err := testPG.SetBillingOverride(ctx, uuid.New(), &gib, nil, nil, auditEntry("billing.override.set", "billing_price_override", "x")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetBillingOverride unknown customer: want ErrNotFound, got %v", err)
	}

	if err := testPG.DeleteBillingOverride(ctx, c.ID, auditEntry("billing.override.delete", "billing_price_override", c.ID.String())); err != nil {
		t.Fatalf("DeleteBillingOverride: %v", err)
	}
	if err := testPG.DeleteBillingOverride(ctx, c.ID, auditEntry("billing.override.delete", "billing_price_override", c.ID.String())); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteBillingOverride (missing): want ErrNotFound, got %v", err)
	}
}

// TestIntegrationSCIMGroups guards SCIM group CRUD + the authoritative
// role/tenant recompute (migration 0021), incl. GetCustomerBySlug and the
// "managed but no group → default role, no grants" stickiness.
func TestIntegrationSCIMGroups(t *testing.T) {
	ctx := ctxT(t)
	cust := makeCustomer(t)
	mapping := SCIMMapping{RolePrefix: "role:", CustomerPrefix: "customer:", DefaultRole: "viewer"}

	user, err := testPG.CreateInvitedUser(ctx, uuid.New(), "scimgrp-"+uniq()+"@example.com", "viewer", auditEntry("user.invite", "user", "u"))
	if err != nil {
		t.Fatalf("CreateInvitedUser: %v", err)
	}

	// GetCustomerBySlug resolves the customer a `customer:<slug>` group names.
	bySlug, err := testPG.GetCustomerBySlug(ctx, cust.Slug)
	if err != nil || bySlug.ID != cust.ID {
		t.Fatalf("GetCustomerBySlug: %+v err=%v", bySlug, err)
	}
	if _, err := testPG.GetCustomerBySlug(ctx, "nope-"+uniq()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetCustomerBySlug unknown: want ErrNotFound, got %v", err)
	}

	roleGroup, err := testPG.CreateSCIMGroup(ctx, uuid.New(), "role:operator", nil, []uuid.UUID{user.ID}, auditEntry("scim.group.create", "scim_group", "r"))
	if err != nil {
		t.Fatalf("CreateSCIMGroup role: %v", err)
	}
	custGroup, err := testPG.CreateSCIMGroup(ctx, uuid.New(), "customer:"+cust.Slug, nil, []uuid.UUID{user.ID}, auditEntry("scim.group.create", "scim_group", "c"))
	if err != nil {
		t.Fatalf("CreateSCIMGroup customer: %v", err)
	}

	recompute := func() {
		if err := testPG.RecomputeSCIMUserAccess(ctx, user.ID, mapping, nil, auditEntry("scim.access.recompute", "user", user.ID.String())); err != nil {
			t.Fatalf("RecomputeSCIMUserAccess: %v", err)
		}
	}
	recompute()

	got, err := testPG.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Role != "operator" || !got.ScimManaged {
		t.Fatalf("after mapping: role=%q managed=%v", got.Role, got.ScimManaged)
	}
	grants, _ := testPG.ListUserCustomerIDs(ctx, user.ID)
	if len(grants) != 1 || grants[0] != cust.ID {
		t.Fatalf("grants = %v, want [%s]", grants, cust.ID)
	}

	// Remove from the customer group → still operator (role group), no access.
	if _, _, err := testPG.ModifySCIMGroupMembers(ctx, custGroup.ID, nil, []uuid.UUID{user.ID}, auditEntry("scim.group.patch", "scim_group", "c")); err != nil {
		t.Fatalf("ModifySCIMGroupMembers: %v", err)
	}
	recompute()
	if grants, _ := testPG.ListUserCustomerIDs(ctx, user.ID); len(grants) != 0 {
		t.Fatalf("grants after removal = %v, want empty (no access)", grants)
	}

	// Delete the role group → managed sticky: default role, still no grants.
	former, err := testPG.DeleteSCIMGroup(ctx, roleGroup.ID, auditEntry("scim.group.delete", "scim_group", "r"))
	if err != nil {
		t.Fatalf("DeleteSCIMGroup: %v", err)
	}
	if len(former) != 1 || former[0] != user.ID {
		t.Fatalf("delete former members = %v", former)
	}
	recompute()
	got, _ = testPG.GetUser(ctx, user.ID)
	if got.Role != "viewer" || !got.ScimManaged {
		t.Fatalf("after deleting role group: role=%q managed=%v (want viewer/managed)", got.Role, got.ScimManaged)
	}
}

// TestIntegrationAlertRules guards alert_rules CRUD incl. the channel_ids
// UUID[] scan (migration 0014).
func TestIntegrationAlertRules(t *testing.T) {
	ctx := ctxT(t)
	c := makeCustomer(t)
	ch := uuid.New()
	r, err := testPG.CreateAlertRule(ctx, NewAlertRule{
		ID: uuid.New(), Name: "ingest stopped " + uniq(), Metric: AlertMetricIngestItems,
		Comparison: AlertComparisonBelow, Threshold: 10, WindowSeconds: 300,
		CustomerID: &c.ID, ChannelIDs: []uuid.UUID{ch}, Enabled: true,
	}, auditEntry("alertrule.create", "alert_rule", "r"))
	if err != nil || len(r.ChannelIDs) != 1 || r.ChannelIDs[0] != ch {
		t.Fatalf("CreateAlertRule: %+v err=%v", r, err)
	}
	if _, err := testPG.GetAlertRule(ctx, r.ID); err != nil {
		t.Fatalf("GetAlertRule: %v", err)
	}
	// A cluster-wide PromQL rule (migration 0015: query column + CHECK).
	pq, err := testPG.CreateAlertRule(ctx, NewAlertRule{
		ID: uuid.New(), Name: "node cpu " + uniq(), Metric: AlertMetricPromQL,
		Query: `avg(node_cpu_usage)`, Comparison: AlertComparisonAbove, Threshold: 0.8,
		WindowSeconds: 60, Severity: AlertSeverityCritical, ChannelIDs: []uuid.UUID{ch}, Enabled: true,
	}, auditEntry("alertrule.create", "alert_rule", "pq"))
	if err != nil || pq.Query != `avg(node_cpu_usage)` || pq.CustomerID != nil || pq.Severity != AlertSeverityCritical {
		t.Fatalf("CreatePromQLAlertRule: %+v err=%v", pq, err)
	}
	// The first rule omitted severity → store defaults it to 'warning'.
	if r.Severity != AlertSeverityWarning {
		t.Fatalf("default severity = %q, want warning", r.Severity)
	}
	if _, err := testPG.ListAlertRules(ctx); err != nil {
		t.Fatalf("ListAlertRules: %v", err)
	}
	en, err := testPG.ListEnabledAlertRules(ctx)
	if err != nil {
		t.Fatalf("ListEnabledAlertRules: %v", err)
	}
	found := false
	for _, x := range en {
		if x.ID == r.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("enabled rule not listed")
	}
	th := 99.0
	disabled := false
	upd, err := testPG.UpdateAlertRule(ctx, r.ID, AlertRuleUpdate{Threshold: &th, Enabled: &disabled}, auditEntry("alertrule.update", "alert_rule", r.ID.String()))
	if err != nil || upd.Threshold != 99 || upd.Enabled {
		t.Fatalf("UpdateAlertRule: %+v err=%v", upd, err)
	}
	if err := testPG.DeleteAlertRule(ctx, r.ID, auditEntry("alertrule.delete", "alert_rule", r.ID.String())); err != nil {
		t.Fatalf("DeleteAlertRule: %v", err)
	}
}

// TestIntegrationMaintenanceWindows guards the maintenance-window CRUD +
// active-window lookup (migration 0016).
func TestIntegrationMaintenanceWindows(t *testing.T) {
	ctx := ctxT(t)
	now := time.Now()
	w, err := testPG.CreateMaintenanceWindow(ctx, NewMaintenanceWindow{
		ID: uuid.New(), Name: "deploy " + uniq(), StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour),
	}, auditEntry("maintenance_window.create", "maintenance_window", "w"))
	if err != nil {
		t.Fatalf("CreateMaintenanceWindow: %v", err)
	}
	active, err := testPG.ListActiveMaintenanceWindows(ctx, now)
	if err != nil {
		t.Fatalf("ListActiveMaintenanceWindows: %v", err)
	}
	found := false
	for _, a := range active {
		if a.ID == w.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("window covering now not reported active")
	}
	// A time outside the window is not active.
	if out, err := testPG.ListActiveMaintenanceWindows(ctx, now.Add(2*time.Hour)); err != nil || containsWindow(out, w.ID) {
		t.Fatalf("window should be inactive 2h later: err=%v", err)
	}
	if err := testPG.DeleteMaintenanceWindow(ctx, w.ID, auditEntry("maintenance_window.delete", "maintenance_window", w.ID.String())); err != nil {
		t.Fatalf("DeleteMaintenanceWindow: %v", err)
	}
}

func containsWindow(ws []MaintenanceWindow, id uuid.UUID) bool {
	for _, w := range ws {
		if w.ID == id {
			return true
		}
	}
	return false
}

// TestIntegrationAuditLog exercises the audit read path.
// TestIntegrationReencryptSecrets guards the master-key rotation path: every
// stored discrete secret is rewritten through migrate, primary-keyed secrets
// are skipped, and only the enc column is touched.
func TestIntegrationReencryptSecrets(t *testing.T) {
	ctx := ctxT(t)
	issuer := "https://accounts.google.com"
	ap, err := testPG.CreateAuthProvider(ctx, NewAuthProvider{
		ID: uuid.New(), Type: "oidc", Name: "reenc-" + uniq(), DisplayName: "OIDC",
		ClientID: "cid", ClientSecretEnc: []byte("v1:secret"), Issuer: &issuer, Enabled: true,
	}, auditEntry("authprovider.create", "auth_provider", "oidc"))
	if err != nil {
		t.Fatalf("CreateAuthProvider: %v", err)
	}
	wh, err := testPG.CreateWebhook(ctx, NewWebhook{
		ID: uuid.New(), Type: WebhookTypeGeneric, Name: "reenc-" + uniq(),
		URL: "https://hooks.example.com", Events: []string{WebhookEventAgentOffline},
		SecretEnc: []byte("v2:already"), Enabled: true,
	}, auditEntry("webhook.create", "webhook", "generic"))
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}

	// migrate rewrites v1: secrets to v2: and reports v2: (primary) as unchanged.
	calls := 0
	migrated, err := testPG.ReencryptSecrets(ctx, func(enc []byte) ([]byte, bool, error) {
		calls++
		if len(enc) < 3 || string(enc[:3]) != "v1:" {
			return nil, false, nil // already primary, or foreign/empty — leave it
		}
		return append([]byte("v2:"), enc[3:]...), true, nil
	})
	if err != nil {
		t.Fatalf("ReencryptSecrets: %v", err)
	}
	if migrated < 1 {
		t.Fatalf("migrated = %d, want >= 1 (this run's v1 provider)", migrated)
	}
	if calls < 2 {
		t.Fatalf("migrate called %d times, want >= 2 (provider + webhook seen)", calls)
	}

	got, err := testPG.GetAuthProvider(ctx, ap.ID)
	if err != nil {
		t.Fatalf("GetAuthProvider: %v", err)
	}
	if string(got.ClientSecretEnc) != "v2:secret" {
		t.Errorf("provider secret = %q, want re-encrypted %q", got.ClientSecretEnc, "v2:secret")
	}
	// The already-primary webhook secret is untouched, and other columns survive.
	gotWH, err := testPG.GetWebhook(ctx, wh.ID)
	if err != nil {
		t.Fatalf("GetWebhook: %v", err)
	}
	if string(gotWH.SecretEnc) != "v2:already" || gotWH.URL != "https://hooks.example.com" {
		t.Errorf("webhook mutated unexpectedly: secret=%q url=%q", gotWH.SecretEnc, gotWH.URL)
	}
}

func TestIntegrationAuditLog(t *testing.T) {
	ctx := ctxT(t)
	makeCustomer(t) // generates an audit row
	rows, err := testPG.ListAuditLog(ctx, AuditFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one audit row")
	}
}
