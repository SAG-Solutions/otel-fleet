package opamp

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/open-telemetry/opamp-go/server/types"
)

// agentConn is the in-memory state of one OpAMP-connected agent. Heartbeats
// only touch this struct; lastSeen is flushed to PostgreSQL write-behind.
type agentConn struct {
	conn        types.Connection
	instanceUID []byte
	agentID     uuid.UUID
	customerID  uuid.UUID

	capabilities       uint64
	lastAckedHash      []byte // RemoteConfigStatus.last_remote_config_hash
	remoteConfigStatus string // last mapped status (store.RemoteConfig*)
	healthy            *bool

	lastSeen  time.Time
	dirtySeen bool // lastSeen not yet flushed to the row

	tokenOffered bool // a per-agent token was offered on this connection already
}

// registry tracks live OpAMP connections, keyed by instance UID.
type registry struct {
	mu     sync.Mutex
	byUID  map[string]*agentConn
	byConn map[types.Connection]string
}

func newRegistry() *registry {
	return &registry{byUID: map[string]*agentConn{}, byConn: map[types.Connection]string{}}
}

// count returns the number of currently connected agents (for the connected
// gauge). O(1) under the lock.
func (r *registry) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byUID)
}

// get returns a snapshot copy of the state for uid (nil when unknown).
func (r *registry) get(uid []byte) *agentConn {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.byUID[string(uid)]
	if !ok {
		return nil
	}
	cp := *st
	return &cp
}

// put stores the state for uid, replacing any previous connection binding.
func (r *registry) put(st *agentConn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := string(st.instanceUID)
	if prev, ok := r.byUID[key]; ok && prev.conn != st.conn {
		delete(r.byConn, prev.conn)
	}
	r.byUID[key] = st
	if st.conn != nil {
		r.byConn[st.conn] = key
	}
}

// touch updates the in-memory heartbeat state of uid.
func (r *registry) touch(uid []byte, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st, ok := r.byUID[string(uid)]; ok {
		if at.After(st.lastSeen) {
			st.lastSeen = at
		}
		st.dirtySeen = true
	}
}

// update applies fn to the live state of uid (no-op when unknown).
func (r *registry) update(uid []byte, fn func(*agentConn)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st, ok := r.byUID[string(uid)]; ok {
		fn(st)
	}
}

// removeConn drops the binding of a closed connection and returns a snapshot
// of its state (nil when the connection never registered an agent).
func (r *registry) removeConn(conn types.Connection) *agentConn {
	r.mu.Lock()
	defer r.mu.Unlock()
	key, ok := r.byConn[conn]
	if !ok {
		return nil
	}
	delete(r.byConn, conn)
	st := r.byUID[key]
	// Only forget the agent when the closing connection is still its current
	// one (a reconnect may have replaced it already).
	if st != nil && st.conn == conn {
		delete(r.byUID, key)
		cp := *st
		return &cp
	}
	return nil
}

// isConnected reports whether an agent with the given instance UID has a live
// connection.
func (r *registry) isConnected(uid []byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.byUID[string(uid)]
	return ok
}

// connsForCustomer snapshots the live connections of one customer.
func (r *registry) connsForCustomer(customerID uuid.UUID) []agentConn {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []agentConn
	for _, st := range r.byUID {
		if st.customerID == customerID && st.agentID != uuid.Nil {
			out = append(out, *st)
		}
	}
	return out
}

// drainDirtySeen collects and clears all unflushed lastSeen timestamps.
func (r *registry) drainDirtySeen() map[uuid.UUID]time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[uuid.UUID]time.Time{}
	for _, st := range r.byUID {
		if st.dirtySeen && st.agentID != uuid.Nil {
			out[st.agentID] = st.lastSeen
			st.dirtySeen = false
		}
	}
	return out
}
