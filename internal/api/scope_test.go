package api

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/jansagurna/otelfleet/internal/auth"
	"github.com/jansagurna/otelfleet/internal/store"
)

func ctxWithPrincipal(p auth.Principal) context.Context {
	return auth.WithPrincipal(context.Background(), p)
}

func TestRequireCustomerAccess(t *testing.T) {
	acme := uuid.New()
	globex := uuid.New()

	unscoped := ctxWithPrincipal(auth.Principal{AllCustomers: true})
	scoped := ctxWithPrincipal(auth.Principal{AllowedCustomers: map[uuid.UUID]bool{acme: true}})

	// Unscoped principals may access any customer, including nil-customer
	// resources (e.g. gateway agents).
	if err := requireCustomerAccess(unscoped, &globex); err != nil {
		t.Fatalf("unscoped access to any customer: got %v", err)
	}
	if err := requireCustomerAccess(unscoped, nil); err != nil {
		t.Fatalf("unscoped access to nil customer: got %v", err)
	}

	// Scoped principals may access granted customers only.
	if err := requireCustomerAccess(scoped, &acme); err != nil {
		t.Fatalf("scoped access to granted customer: got %v", err)
	}
	var forbidden forbiddenError
	if err := requireCustomerAccess(scoped, &globex); !errors.As(err, &forbidden) {
		t.Fatalf("scoped access to other customer: want forbiddenError, got %v", err)
	}
	// A scoped principal must not reach nil-customer (gateway) resources.
	if err := requireCustomerAccess(scoped, nil); !errors.As(err, &forbidden) {
		t.Fatalf("scoped access to nil customer: want forbiddenError, got %v", err)
	}
}

func TestRequireUnscoped(t *testing.T) {
	unscoped := ctxWithPrincipal(auth.Principal{AllCustomers: true})
	scoped := ctxWithPrincipal(auth.Principal{AllowedCustomers: map[uuid.UUID]bool{uuid.New(): true}})

	if err := requireUnscoped(unscoped); err != nil {
		t.Fatalf("unscoped: got %v", err)
	}
	var forbidden forbiddenError
	if err := requireUnscoped(scoped); !errors.As(err, &forbidden) {
		t.Fatalf("scoped: want forbiddenError, got %v", err)
	}
}

func TestCustomerScope(t *testing.T) {
	acme := uuid.New()
	if allowed, scoped := customerScope(ctxWithPrincipal(auth.Principal{AllCustomers: true})); scoped || allowed != nil {
		t.Fatalf("unscoped: want (nil,false), got (%v,%v)", allowed, scoped)
	}
	allowed, scoped := customerScope(ctxWithPrincipal(auth.Principal{AllowedCustomers: map[uuid.UUID]bool{acme: true}}))
	if !scoped || !allowed[acme] {
		t.Fatalf("scoped: want ({acme:true},true), got (%v,%v)", allowed, scoped)
	}
}

// pipeStore is a store.Store that only answers GetPipeline (embedding the
// interface satisfies the type; other methods would panic if called).
type pipeStore struct {
	store.Store
	pipe store.Pipeline
	err  error
}

func (f pipeStore) GetPipeline(context.Context, uuid.UUID) (store.Pipeline, error) {
	return f.pipe, f.err
}

// TestRequirePipelineAccess covers the store-resolving scope guard used by the
// pipeline handlers (a scoped portal user may only reach their own pipelines).
func TestRequirePipelineAccess(t *testing.T) {
	acme, globex := uuid.New(), uuid.New()
	pipeID := uuid.New()

	srv := &Server{store: pipeStore{pipe: store.Pipeline{ID: pipeID, CustomerID: acme}}}
	var forbidden forbiddenError

	// Scoped to the owner -> allowed.
	ctxAcme := ctxWithPrincipal(auth.Principal{AllowedCustomers: map[uuid.UUID]bool{acme: true}})
	if err := srv.requirePipelineAccess(ctxAcme, pipeID); err != nil {
		t.Fatalf("owner access: got %v", err)
	}
	// Scoped to a different customer -> forbidden.
	ctxGlobex := ctxWithPrincipal(auth.Principal{AllowedCustomers: map[uuid.UUID]bool{globex: true}})
	if err := srv.requirePipelineAccess(ctxGlobex, pipeID); !errors.As(err, &forbidden) {
		t.Fatalf("foreign pipeline: want forbiddenError, got %v", err)
	}
	// Unscoped principal -> allowed without touching the store.
	ctxAll := ctxWithPrincipal(auth.Principal{AllCustomers: true})
	if err := srv.requirePipelineAccess(ctxAll, pipeID); err != nil {
		t.Fatalf("unscoped access: got %v", err)
	}
	// Missing pipeline -> nil so the handler's own load produces the 404
	// (existence is not leaked differently to scoped users).
	missing := &Server{store: pipeStore{err: store.ErrNotFound}}
	if err := missing.requirePipelineAccess(ctxGlobex, pipeID); err != nil {
		t.Fatalf("missing pipeline: want nil, got %v", err)
	}
}
