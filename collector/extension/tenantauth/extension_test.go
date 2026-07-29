// Copyright The otel-fleet Authors
// SPDX-License-Identifier: Apache-2.0

package tenantauth

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/component/componenttest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sag-solutions/otel-fleet/collector/extension/tenantauth/internal/authv1"
)

// fakeAuthServer is an in-process AuthService implementation.
type fakeAuthServer struct {
	authv1.UnimplementedAuthServiceServer

	mu    sync.Mutex
	calls int
	down  bool
	keys  map[string]*authv1.ValidateAPIKeyResponse
}

func (f *fakeAuthServer) ValidateAPIKey(_ context.Context, req *authv1.ValidateAPIKeyRequest) (*authv1.ValidateAPIKeyResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.down {
		return nil, status.Error(codes.Unavailable, "control plane down")
	}
	if resp, ok := f.keys[req.GetApiKey()]; ok {
		return resp, nil
	}
	return &authv1.ValidateAPIKeyResponse{Valid: false}, nil
}

func (f *fakeAuthServer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeAuthServer) setDown(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}

func startFakeServer(t *testing.T, f *fakeAuthServer) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	authv1.RegisterAuthServiceServer(srv, f)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func newTestAuthenticator(t *testing.T, endpoint string, mutate func(*Config)) (*authenticator, *time.Time) {
	t.Helper()
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = endpoint
	cfg.Insecure = true
	if mutate != nil {
		mutate(cfg)
	}
	require.NoError(t, cfg.Validate())

	a, err := newAuthenticator(cfg, componenttest.NewNopTelemetrySettings())
	require.NoError(t, err)

	now := time.Now()
	a.now = func() time.Time { return now }

	require.NoError(t, a.Start(context.Background(), componenttest.NewNopHost()))
	t.Cleanup(func() { require.NoError(t, a.Shutdown(context.Background())) })
	return a, &now
}

func validKeyResponse(tenant, customer string) *authv1.ValidateAPIKeyResponse {
	return &authv1.ValidateAPIKeyResponse{
		Valid:      true,
		ClientId:   tenant,
		CustomerId: customer,
	}
}

func TestExtractKey(t *testing.T) {
	tests := []struct {
		name    string
		sources map[string][]string
		want    string
	}{
		{"nil sources", nil, ""},
		{"empty sources", map[string][]string{}, ""},
		{"bearer lowercase header", map[string][]string{"authorization": {"Bearer otm_abc"}}, "otm_abc"},
		{"bearer canonical header", map[string][]string{"Authorization": {"Bearer otm_abc"}}, "otm_abc"},
		{"bearer weird header case", map[string][]string{"AUTHORIZATION": {"bearer otm_abc"}}, "otm_abc"},
		{"bearer scheme uppercase", map[string][]string{"authorization": {"BEARER otm_abc"}}, "otm_abc"},
		{"non-bearer scheme ignored", map[string][]string{"authorization": {"Basic dXNlcjpwYXNz"}}, ""},
		{"bearer empty key", map[string][]string{"authorization": {"Bearer   "}}, ""},
		{"x-api-key lowercase", map[string][]string{"x-api-key": {"otm_xyz"}}, "otm_xyz"},
		{"x-api-key canonical", map[string][]string{"X-Api-Key": {"otm_xyz"}}, "otm_xyz"},
		{"x-api-key uppercase", map[string][]string{"X-API-KEY": {"otm_xyz"}}, "otm_xyz"},
		{"bearer preferred over x-api-key", map[string][]string{
			"authorization": {"Bearer otm_bearer"},
			"x-api-key":     {"otm_xkey"},
		}, "otm_bearer"},
		{"falls back to x-api-key on bad scheme", map[string][]string{
			"authorization": {"Basic zzz"},
			"x-api-key":     {"otm_xkey"},
		}, "otm_xkey"},
		{"first non-empty value wins", map[string][]string{"x-api-key": {"", "otm_second"}}, "otm_second"},
		{"unrelated headers ignored", map[string][]string{"user-agent": {"curl"}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, extractKey(tt.sources))
		})
	}
}

func TestAuthenticateNoKey(t *testing.T) {
	fake := &fakeAuthServer{}
	a, _ := newTestAuthenticator(t, startFakeServer(t, fake), nil)

	_, err := a.Authenticate(context.Background(), map[string][]string{"user-agent": {"curl"}})
	require.ErrorIs(t, err, errNoKey)
	assert.Equal(t, 0, fake.callCount())
}

func TestAuthenticateValidKeySetsAuthData(t *testing.T) {
	fake := &fakeAuthServer{keys: map[string]*authv1.ValidateAPIKeyResponse{
		"otm_good": validKeyResponse("client-1", "customer-1"),
	}}
	a, _ := newTestAuthenticator(t, startFakeServer(t, fake), nil)

	ctx, err := a.Authenticate(context.Background(), map[string][]string{"authorization": {"Bearer otm_good"}})
	require.NoError(t, err)

	info := client.FromContext(ctx)
	require.NotNil(t, info.Auth)
	assert.Equal(t, "client-1", info.Auth.GetAttribute("tenant.id"))
	assert.Equal(t, "client-1", info.Auth.GetAttribute("client.id"))
	assert.Equal(t, "customer-1", info.Auth.GetAttribute("customer.id"))
	assert.Equal(t, int64(0), info.Auth.GetAttribute("rate_limit_items_per_sec"), "absent limit must surface as int64 0 (unlimited)")
	assert.Nil(t, info.Auth.GetAttribute("unknown"))
	assert.ElementsMatch(t, []string{"tenant.id", "client.id", "customer.id", "rate_limit_items_per_sec"}, info.Auth.GetAttributeNames())
}

func TestAuthenticateRateLimitAttribute(t *testing.T) {
	resp := validKeyResponse("client-1", "customer-1")
	resp.RateLimitItemsPerSec = 50
	fake := &fakeAuthServer{keys: map[string]*authv1.ValidateAPIKeyResponse{"otm_limited": resp}}
	a, now := newTestAuthenticator(t, startFakeServer(t, fake), nil)
	headers := map[string][]string{"x-api-key": {"otm_limited"}}

	ctx, err := a.Authenticate(context.Background(), headers)
	require.NoError(t, err)
	assert.Equal(t, int64(50), client.FromContext(ctx).Auth.GetAttribute(AttrRateLimitItemsPerSec))

	// Served from cache: the limit must ride the cache entry.
	ctx, err = a.Authenticate(context.Background(), headers)
	require.NoError(t, err)
	assert.Equal(t, int64(50), client.FromContext(ctx).Auth.GetAttribute(AttrRateLimitItemsPerSec))
	assert.Equal(t, 1, fake.callCount())

	// Limit changes propagate once the fresh TTL expires (<=30s default).
	updated := validKeyResponse("client-1", "customer-1")
	updated.RateLimitItemsPerSec = 200
	fake.mu.Lock()
	fake.keys["otm_limited"] = updated
	fake.mu.Unlock()
	*now = now.Add(defaultTTL + time.Second)
	ctx, err = a.Authenticate(context.Background(), headers)
	require.NoError(t, err)
	assert.Equal(t, int64(200), client.FromContext(ctx).Auth.GetAttribute(AttrRateLimitItemsPerSec))
	assert.Equal(t, 2, fake.callCount())
}

func TestAuthenticateCacheHit(t *testing.T) {
	fake := &fakeAuthServer{keys: map[string]*authv1.ValidateAPIKeyResponse{
		"otm_good": validKeyResponse("client-1", "customer-1"),
	}}
	a, now := newTestAuthenticator(t, startFakeServer(t, fake), nil)
	headers := map[string][]string{"x-api-key": {"otm_good"}}

	for range 5 {
		_, err := a.Authenticate(context.Background(), headers)
		require.NoError(t, err)
	}
	assert.Equal(t, 1, fake.callCount(), "subsequent calls within ttl must be served from cache")

	// Past the TTL the key must be revalidated.
	*now = now.Add(defaultTTL + time.Second)
	_, err := a.Authenticate(context.Background(), headers)
	require.NoError(t, err)
	assert.Equal(t, 2, fake.callCount())
}

func TestAuthenticateServerTTLCapsCache(t *testing.T) {
	resp := validKeyResponse("client-1", "customer-1")
	resp.CacheTtlSeconds = 2 // shorter than the configured 30s
	fake := &fakeAuthServer{keys: map[string]*authv1.ValidateAPIKeyResponse{"otm_good": resp}}
	a, now := newTestAuthenticator(t, startFakeServer(t, fake), nil)
	headers := map[string][]string{"x-api-key": {"otm_good"}}

	_, err := a.Authenticate(context.Background(), headers)
	require.NoError(t, err)

	*now = now.Add(3 * time.Second)
	_, err = a.Authenticate(context.Background(), headers)
	require.NoError(t, err)
	assert.Equal(t, 2, fake.callCount(), "server cache_ttl_seconds must cap the configured ttl")
}

func TestAuthenticateNegativeCache(t *testing.T) {
	fake := &fakeAuthServer{}
	a, now := newTestAuthenticator(t, startFakeServer(t, fake), nil)
	headers := map[string][]string{"x-api-key": {"otm_bad"}}

	for range 3 {
		_, err := a.Authenticate(context.Background(), headers)
		require.ErrorIs(t, err, errInvalidKey)
	}
	assert.Equal(t, 1, fake.callCount(), "rejections within negative_ttl must be served from cache")

	*now = now.Add(defaultNegativeTTL + time.Second)
	_, err := a.Authenticate(context.Background(), headers)
	require.ErrorIs(t, err, errInvalidKey)
	assert.Equal(t, 2, fake.callCount())
}

func TestAuthenticateStaleIfError(t *testing.T) {
	fake := &fakeAuthServer{keys: map[string]*authv1.ValidateAPIKeyResponse{
		"otm_good": validKeyResponse("client-1", "customer-1"),
	}}
	a, now := newTestAuthenticator(t, startFakeServer(t, fake), nil)
	goodHeaders := map[string][]string{"x-api-key": {"otm_good"}}

	_, err := a.Authenticate(context.Background(), goodHeaders)
	require.NoError(t, err)

	// Control plane goes down; entry expires but is within stale_if_error.
	fake.setDown(true)
	*now = now.Add(defaultTTL + time.Minute)

	ctx, err := a.Authenticate(context.Background(), goodHeaders)
	require.NoError(t, err, "known-good key must be served stale while the control plane is down")
	assert.Equal(t, "client-1", client.FromContext(ctx).Auth.GetAttribute("tenant.id"))

	// Unknown keys fail closed while the control plane is down.
	_, err = a.Authenticate(context.Background(), map[string][]string{"x-api-key": {"otm_unknown"}})
	require.Error(t, err)
	require.NotErrorIs(t, err, errInvalidKey)

	// Past stale_if_error even known keys are rejected.
	*now = now.Add(defaultStaleIfError)
	_, err = a.Authenticate(context.Background(), goodHeaders)
	require.Error(t, err)

	// Control plane recovers: key authenticates again.
	fake.setDown(false)
	_, err = a.Authenticate(context.Background(), goodHeaders)
	require.NoError(t, err)
}

func TestAuthenticateConcurrency(t *testing.T) {
	keys := map[string]*authv1.ValidateAPIKeyResponse{}
	for i := range 10 {
		keys[fmt.Sprintf("otm_key_%d", i)] = validKeyResponse(fmt.Sprintf("client-%d", i), "customer-1")
	}
	fake := &fakeAuthServer{keys: keys}
	a, _ := newTestAuthenticator(t, startFakeServer(t, fake), nil)

	var wg sync.WaitGroup
	for g := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 20 {
				key := fmt.Sprintf("otm_key_%d", (g+i)%10)
				ctx, err := a.Authenticate(context.Background(), map[string][]string{"x-api-key": {key}})
				assert.NoError(t, err)
				assert.NotNil(t, client.FromContext(ctx).Auth)
			}
		}()
	}
	wg.Wait()
}

func TestCacheLRUEviction(t *testing.T) {
	c := newKeyCache(2)
	now := time.Now()
	e := cacheEntry{valid: true, data: &authData{}, freshUntil: now.Add(time.Hour)}
	c.put("a", e)
	c.put("b", e)
	_, _ = c.get("a") // "a" is now most recently used
	c.put("c", e)     // evicts "b"

	assert.Equal(t, 2, c.len())
	_, ok := c.get("a")
	assert.True(t, ok)
	_, ok = c.get("b")
	assert.False(t, ok)
	_, ok = c.get("c")
	assert.True(t, ok)
}

func TestConfigValidate(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	require.Error(t, cfg.Validate(), "endpoint is required")
	cfg.Endpoint = "localhost:9443"
	require.NoError(t, cfg.Validate())
	cfg.Cache.MaxEntries = 0
	require.Error(t, cfg.Validate())
}
