// Copyright The otel-fleet Authors
// SPDX-License-Identifier: Apache-2.0

package tenantauth // import "github.com/sag-solutions/otel-fleet/collector/extension/tenantauth"

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
)

var componentType = component.MustNewType("tenantauth")

const (
	defaultTTL          = 30 * time.Second
	defaultNegativeTTL  = 5 * time.Second
	defaultMaxEntries   = 50000
	defaultStaleIfError = 15 * time.Minute
)

// NewFactory creates the factory for the tenantauth server authenticator.
func NewFactory() extension.Factory {
	return extension.NewFactory(
		componentType,
		createDefaultConfig,
		create,
		component.StabilityLevelBeta,
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		Cache: CacheConfig{
			TTL:          defaultTTL,
			NegativeTTL:  defaultNegativeTTL,
			MaxEntries:   defaultMaxEntries,
			StaleIfError: defaultStaleIfError,
		},
	}
}

func create(_ context.Context, set extension.Settings, cfg component.Config) (extension.Extension, error) {
	return newAuthenticator(cfg.(*Config), set.TelemetrySettings)
}
