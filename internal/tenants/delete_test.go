package tenants

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/audit"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

// deleteFakeStore satisfies store.Store via the embedded interface (unused
// methods are nil and would panic if called); DeleteCustomer only touches
// GetCustomer + SoftDeleteCustomer.
type deleteFakeStore struct {
	store.Store
	cust      store.Customer
	getErr    error
	deleteErr error
	deleted   uuid.UUID
}

func (f *deleteFakeStore) GetCustomer(context.Context, uuid.UUID) (store.Customer, error) {
	return f.cust, f.getErr
}

func (f *deleteFakeStore) SoftDeleteCustomer(_ context.Context, id uuid.UUID, _ []audit.Entry) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = id
	return nil
}

func TestDeleteCustomerPurgesTelemetry(t *testing.T) {
	id := uuid.New()
	st := &deleteFakeStore{cust: store.Customer{ID: id, ClientID: "cust_abc", Region: "eu"}}

	var gotClient, gotRegion string
	calls := 0
	svc := NewService(st, func(_ context.Context, clientID, region string) {
		calls++
		gotClient, gotRegion = clientID, region
	})

	if err := svc.DeleteCustomer(context.Background(), nil, id); err != nil {
		t.Fatalf("DeleteCustomer: %v", err)
	}
	if st.deleted != id {
		t.Fatalf("customer not soft-deleted")
	}
	if calls != 1 || gotClient != "cust_abc" || gotRegion != "eu" {
		t.Fatalf("purge called %d times with (%q,%q), want 1 (cust_abc, eu)", calls, gotClient, gotRegion)
	}
}

func TestDeleteCustomerNoPurgeWhenNotFound(t *testing.T) {
	id := uuid.New()
	st := &deleteFakeStore{getErr: store.ErrNotFound}
	calls := 0
	svc := NewService(st, func(context.Context, string, string) { calls++ })

	if err := svc.DeleteCustomer(context.Background(), nil, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("purge must not run when the customer was not found")
	}
}

func TestDeleteCustomerNilPurgeIsSafe(t *testing.T) {
	id := uuid.New()
	st := &deleteFakeStore{cust: store.Customer{ID: id, ClientID: "cust_x", Region: "default"}}
	svc := NewService(st, nil) // purge disabled
	if err := svc.DeleteCustomer(context.Background(), nil, id); err != nil {
		t.Fatalf("DeleteCustomer with nil purge: %v", err)
	}
}
