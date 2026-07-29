// Copyright The otel-fleet Authors
// SPDX-License-Identifier: Apache-2.0

package tenantquota // import "github.com/sag-solutions/otel-fleet/collector/processor/tenantquota"

import "errors"

// Config for the tenantquota processor.
type Config struct {
	// BurstSeconds sizes each tenant's token bucket: capacity =
	// rate_limit_items_per_sec * burst_seconds. A tenant may therefore burst
	// up to burst_seconds worth of items at once while the sustained rate
	// stays at the limit. Default: 2.
	BurstSeconds float64 `mapstructure:"burst_seconds"`
}

func (cfg *Config) Validate() error {
	if cfg.BurstSeconds <= 0 {
		return errors.New("burst_seconds must be positive")
	}
	return nil
}
