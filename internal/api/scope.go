package api

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/auth"
)

// errNoCustomerAccess is wrapped in a forbiddenError (→403) when a tenant-scoped
// user touches a customer outside their grant set.
var errNoCustomerAccess = errors.New("you do not have access to this customer")

// requireCustomerAccess returns a forbiddenError when the request principal is
// tenant-scoped and not granted the given customer. A nil customerID (a
// resource with no owning customer, e.g. a gateway agent) requires unscoped
// access. Handlers call this before touching customer-scoped data.
func requireCustomerAccess(ctx context.Context, customerID *uuid.UUID) error {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok || p.AllCustomers {
		// No principal means Guard already authenticated and did not scope
		// (unreachable on guarded routes); unscoped principals pass.
		return nil
	}
	if !p.CanAccessCustomer(customerID) {
		return forbiddenError{errNoCustomerAccess}
	}
	return nil
}

// requireUnscoped returns a forbiddenError when the principal is tenant-scoped.
// Used for fleet-wide admin-ish mutations (e.g. creating a customer) that a
// scoped user must not perform.
func requireUnscoped(ctx context.Context) error {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok || p.AllCustomers {
		return nil
	}
	return forbiddenError{errors.New("this action requires access to all customers")}
}

// customerScope reports the principal's scope: allowed=nil means unscoped (all
// customers). Used to filter list/aggregate endpoints.
func customerScope(ctx context.Context) (allowed map[uuid.UUID]bool, scoped bool) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok || p.AllCustomers {
		return nil, false
	}
	return p.AllowedCustomers, true
}

// requirePipelineAccess resolves a pipeline's owning customer and enforces the
// caller's scope. A missing pipeline returns nil so the handler's own load
// produces the 404 (avoids leaking existence differently to scoped users).
func (s *Server) requirePipelineAccess(ctx context.Context, pipelineID uuid.UUID) error {
	if _, scoped := customerScope(ctx); !scoped {
		return nil
	}
	pipe, err := s.store.GetPipeline(ctx, pipelineID)
	if err != nil {
		return nil // ErrNotFound (or transient): let the handler surface it
	}
	return requireCustomerAccess(ctx, &pipe.CustomerID)
}
