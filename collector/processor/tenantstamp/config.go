// Copyright The otel-fleet Authors
// SPDX-License-Identifier: Apache-2.0

package tenantstamp // import "github.com/sag-solutions/otel-fleet/collector/processor/tenantstamp"

// Config for the tenantstamp processor. The processor has no options: it
// always stamps tenant.id/client.id/customer.id from the authenticated
// client.Info and drops data that arrives without authentication.
type Config struct{}

func (cfg *Config) Validate() error { return nil }
