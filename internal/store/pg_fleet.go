package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sag-solutions/otel-fleet/internal/audit"
)

// --- bootstrap tokens ---

const bootstrapTokenCols = `id, customer_id, name, token_prefix, token_hash, max_uses, used_count, created_by, created_at, expires_at, revoked_at`

func scanBootstrapToken(row pgx.Row) (BootstrapToken, error) {
	var t BootstrapToken
	err := row.Scan(&t.ID, &t.CustomerID, &t.Name, &t.TokenPrefix, &t.TokenHash, &t.MaxUses,
		&t.UsedCount, &t.CreatedBy, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt)
	return t, err
}

// ListBootstrapTokens returns all tokens of a customer, newest first.
// Returns ErrNotFound when the customer is unknown or deleted.
func (s *PG) ListBootstrapTokens(ctx context.Context, customerID uuid.UUID) ([]BootstrapToken, error) {
	if _, err := s.GetCustomer(ctx, customerID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+bootstrapTokenCols+` FROM bootstrap_tokens
		WHERE customer_id = $1 ORDER BY created_at DESC, id`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BootstrapToken{}
	for rows.Next() {
		t, err := scanBootstrapToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *PG) CreateBootstrapToken(ctx context.Context, t NewBootstrapToken, entries []audit.Entry) (BootstrapToken, error) {
	if _, err := s.GetCustomer(ctx, t.CustomerID); err != nil {
		return BootstrapToken{}, err
	}
	var tok BootstrapToken
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		tok, err = scanBootstrapToken(tx.QueryRow(ctx, `
			INSERT INTO bootstrap_tokens (id, customer_id, name, token_prefix, token_hash, max_uses, created_by, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING `+bootstrapTokenCols,
			t.ID, t.CustomerID, t.Name, t.TokenPrefix, t.TokenHash, t.MaxUses, t.CreatedBy, t.ExpiresAt))
		if err != nil {
			return fmt.Errorf("insert bootstrap token: %w", err)
		}
		return audit.Write(ctx, tx, entries...)
	})
	if err != nil {
		return BootstrapToken{}, err
	}
	return tok, nil
}

// RevokeBootstrapToken is idempotent: revoking an already-revoked token succeeds.
func (s *PG) RevokeBootstrapToken(ctx context.Context, customerID, tokenID uuid.UUID, entries []audit.Entry) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var exists bool
		err := tx.QueryRow(ctx, `
			SELECT true FROM bootstrap_tokens WHERE id = $1 AND customer_id = $2`, tokenID, customerID).Scan(&exists)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE bootstrap_tokens SET revoked_at = now()
			WHERE id = $1 AND customer_id = $2 AND revoked_at IS NULL`, tokenID, customerID); err != nil {
			return fmt.Errorf("revoke bootstrap token: %w", err)
		}
		return audit.Write(ctx, tx, entries...)
	})
}

// ActiveBootstrapTokensByPrefix returns all non-revoked tokens with the given
// prefix joined with their customer. Expiry, use count and customer status are
// checked by the caller.
func (s *PG) ActiveBootstrapTokensByPrefix(ctx context.Context, prefix string) ([]EnrollToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.customer_id, t.token_hash, t.max_uses, t.used_count, t.expires_at, c.status
		FROM bootstrap_tokens t
		JOIN customers c ON c.id = t.customer_id
		WHERE t.token_prefix = $1 AND t.revoked_at IS NULL`, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EnrollToken
	for rows.Next() {
		var t EnrollToken
		if err := rows.Scan(&t.TokenID, &t.CustomerID, &t.TokenHash, &t.MaxUses, &t.UsedCount, &t.ExpiresAt, &t.CustomerStatus); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// --- agents ---

// agentCols selects an agent joined with its customer; requires aliases
// a (agents) and c (customers, LEFT JOIN).
const agentCols = `
	a.id, a.instance_uid, a.customer_id, c.name, a.class, a.name, a.display_name, a.labels, a.agent_version,
	a.description, a.capabilities, a.assigned_config_hash, a.reported_config_hash, a.acked_config_hash,
	a.reported_config_yaml, a.remote_config_status, a.remote_config_error,
	a.health, a.healthy, a.connected, a.last_seen_at, a.enrolled_via, a.created_at`

const agentFrom = `
	FROM agents a
	LEFT JOIN customers c ON c.id = a.customer_id`

func scanAgent(row pgx.Row) (Agent, error) {
	var a Agent
	err := row.Scan(&a.ID, &a.InstanceUID, &a.CustomerID, &a.CustomerName, &a.Class, &a.Name, &a.DisplayName, &a.Labels,
		&a.AgentVersion, &a.Description, &a.Capabilities, &a.AssignedConfigHash, &a.ReportedConfigHash, &a.AckedConfigHash,
		&a.ReportedConfigYAML, &a.RemoteConfigStatus, &a.RemoteConfigError,
		&a.Health, &a.Healthy, &a.Connected, &a.LastSeenAt, &a.EnrolledVia, &a.CreatedAt)
	return a, err
}

// EnrollAgent inserts a freshly enrolled agent, writes the 'enrolled' event
// and increments the bootstrap token's used_count, all in one transaction.
func (s *PG) EnrollAgent(ctx context.Context, a NewAgent) (Agent, error) {
	var out Agent
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO agents (id, instance_uid, customer_id, class, name, agent_version, description, capabilities, enrolled_via, connected, last_seen_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, false, now())`,
			a.ID, a.InstanceUID, a.CustomerID, a.Class, a.Name, a.AgentVersion, a.Description, a.Capabilities, a.EnrolledVia)
		if isUniqueViolation(err, "agents_instance_uid_key") {
			return ErrConflict
		}
		if err != nil {
			return fmt.Errorf("insert agent: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE bootstrap_tokens SET used_count = used_count + 1 WHERE id = $1`, a.EnrolledVia); err != nil {
			return fmt.Errorf("increment token used_count: %w", err)
		}
		if err := insertAgentEvent(ctx, tx, a.ID, AgentEventEnrolled, map[string]any{"token": a.EnrolledVia.String()}); err != nil {
			return err
		}
		out, err = scanAgent(tx.QueryRow(ctx, `SELECT`+agentCols+agentFrom+` WHERE a.id = $1`, a.ID))
		if err != nil {
			return fmt.Errorf("read back agent: %w", err)
		}
		return nil
	})
	if err != nil {
		return Agent{}, err
	}
	return out, nil
}

func (s *PG) GetAgent(ctx context.Context, id uuid.UUID) (Agent, error) {
	a, err := scanAgent(s.pool.QueryRow(ctx, `SELECT`+agentCols+agentFrom+` WHERE a.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	return a, err
}

func (s *PG) GetAgentByInstanceUID(ctx context.Context, instanceUID []byte) (Agent, error) {
	a, err := scanAgent(s.pool.QueryRow(ctx, `SELECT`+agentCols+agentFrom+` WHERE a.instance_uid = $1`, instanceUID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	return a, err
}

// AgentsByTokenPrefix returns the auth records of agents whose per-agent token
// carries the given prefix and whose customer is not deleted. The caller
// constant-time-compares the hash and checks the customer status.
func (s *PG) AgentsByTokenPrefix(ctx context.Context, prefix string) ([]AgentAuth, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.customer_id, a.instance_uid, a.agent_token_hash, c.status
		FROM agents a
		JOIN customers c ON c.id = a.customer_id
		WHERE a.agent_token_prefix = $1 AND c.status <> 'deleted'`, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentAuth{}
	for rows.Next() {
		var a AgentAuth
		if err := rows.Scan(&a.AgentID, &a.CustomerID, &a.InstanceUID, &a.TokenHash, &a.CustomerStatus); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetAgentToken stores (or rotates) an agent's per-agent token, replacing any
// previous one — the old token stops authenticating immediately.
func (s *PG) SetAgentToken(ctx context.Context, id uuid.UUID, prefix string, hash []byte, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE agents SET agent_token_prefix = $2, agent_token_hash = $3, agent_token_issued_at = $4
		WHERE id = $1`, id, prefix, hash, at)
	return err
}

func (s *PG) ListAgents(ctx context.Context, f AgentFilter) ([]Agent, error) {
	q := `SELECT` + agentCols + agentFrom + ` WHERE true`
	args := []any{}
	if f.Class != nil {
		args = append(args, *f.Class)
		q += fmt.Sprintf(` AND a.class = $%d`, len(args))
	}
	if f.CustomerID != nil {
		args = append(args, *f.CustomerID)
		q += fmt.Sprintf(` AND a.customer_id = $%d`, len(args))
	}
	if f.Connected != nil {
		args = append(args, *f.Connected)
		q += fmt.Sprintf(` AND a.connected = $%d`, len(args))
	}
	q += ` ORDER BY a.created_at, a.id`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Agent{}
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteAgent hard-deletes an agent; its events cascade.
func (s *PG) DeleteAgent(ctx context.Context, id uuid.UUID, entries []audit.Entry) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM agents WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("delete agent: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audit.Write(ctx, tx, entries...)
	})
}

// UpdateAgentDescription refreshes the identity fields reported via
// AgentDescription. capabilities is only written when non-nil (agents may
// omit unchanged capabilities).
func (s *PG) UpdateAgentDescription(ctx context.Context, id uuid.UUID, name, agentVersion *string, description []byte, capabilities *int64) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE agents
		SET name = $2, agent_version = $3, description = $4,
		    capabilities = COALESCE($5, capabilities)
		WHERE id = $1`, id, name, agentVersion, description, capabilities)
	if err != nil {
		return fmt.Errorf("update agent description: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateAgentMeta sets operator-managed metadata (friendly display name and
// labels), writes an audit entry, and returns the refreshed agent. Both fields
// are always written: pass the current value to leave one unchanged.
func (s *PG) UpdateAgentMeta(ctx context.Context, id uuid.UUID, displayName *string, labels []byte, entries []audit.Entry) (Agent, error) {
	var out Agent
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE agents SET display_name = $2, labels = COALESCE($3, labels) WHERE id = $1`,
			id, displayName, labels)
		if err != nil {
			return fmt.Errorf("update agent meta: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := audit.Write(ctx, tx, entries...); err != nil {
			return err
		}
		out, err = scanAgent(tx.QueryRow(ctx, `SELECT`+agentCols+agentFrom+` WHERE a.id = $1`, id))
		if err != nil {
			return fmt.Errorf("read back agent: %w", err)
		}
		return nil
	})
	if err != nil {
		return Agent{}, err
	}
	return out, nil
}

// SetAgentConnected flips the connected flag and records the transition event
// in the same transaction.
func (s *PG) SetAgentConnected(ctx context.Context, id uuid.UUID, connected bool, at time.Time) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE agents
			SET connected = $2, last_seen_at = GREATEST(COALESCE(last_seen_at, 'epoch'::timestamptz), $3)
			WHERE id = $1`, id, connected, at)
		if err != nil {
			return fmt.Errorf("set agent connected: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		event := AgentEventConnected
		if !connected {
			event = AgentEventDisconnected
		}
		return insertAgentEvent(ctx, tx, id, event, nil)
	})
}

func (s *PG) SetAgentAssignedConfig(ctx context.Context, id uuid.UUID, hash []byte) error {
	tag, err := s.pool.Exec(ctx, `UPDATE agents SET assigned_config_hash = $2 WHERE id = $1`, id, hash)
	if err != nil {
		return fmt.Errorf("set assigned config hash: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetAgentAckedConfig records the config hash the agent confirmed via OpAMP
// RemoteConfigStatus.last_remote_config_hash — the authoritative in-sync
// signal (nil clears it, e.g. when the agent reports having no remote config).
func (s *PG) SetAgentAckedConfig(ctx context.Context, id uuid.UUID, hash []byte) error {
	tag, err := s.pool.Exec(ctx, `UPDATE agents SET acked_config_hash = $2 WHERE id = $1`, id, hash)
	if err != nil {
		return fmt.Errorf("set acked config hash: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PG) SetAgentEffectiveConfig(ctx context.Context, id uuid.UUID, yaml string, hash []byte) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE agents SET reported_config_yaml = $2, reported_config_hash = $3 WHERE id = $1`, id, yaml, hash)
	if err != nil {
		return fmt.Errorf("set effective config: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetAgentRemoteConfigStatus updates the remote-config state and, when
// eventType is non-nil, records the transition event in the same transaction.
func (s *PG) SetAgentRemoteConfigStatus(ctx context.Context, id uuid.UUID, status string, errorMessage *string, eventType *string, detail any) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE agents SET remote_config_status = $2, remote_config_error = $3 WHERE id = $1`,
			id, status, errorMessage)
		if err != nil {
			return fmt.Errorf("set remote config status: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if eventType == nil {
			return nil
		}
		return insertAgentEvent(ctx, tx, id, *eventType, detail)
	})
}

// SetAgentHealth stores the reported health tree and, when flipEvent is
// non-nil, records the healthy/unhealthy transition in the same transaction.
func (s *PG) SetAgentHealth(ctx context.Context, id uuid.UUID, health []byte, healthy bool, flipEvent *string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE agents SET health = $2, healthy = $3 WHERE id = $1`, id, health, healthy)
		if err != nil {
			return fmt.Errorf("set agent health: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if flipEvent == nil {
			return nil
		}
		return insertAgentEvent(ctx, tx, id, *flipEvent, nil)
	})
}

// TouchAgents batch-updates last_seen_at (write-behind heartbeat flush),
// keeping the latest timestamp.
func (s *PG) TouchAgents(ctx context.Context, seen map[uuid.UUID]time.Time) error {
	if len(seen) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for id, ts := range seen {
		batch.Queue(`
			UPDATE agents
			SET last_seen_at = GREATEST(COALESCE(last_seen_at, 'epoch'::timestamptz), $2)
			WHERE id = $1`, id, ts)
	}
	return s.pool.SendBatch(ctx, batch).Close()
}

// ListAgentEvents returns the newest events of an agent (limit capped by the
// caller). Returns ErrNotFound for unknown agents.
func (s *PG) ListAgentEvents(ctx context.Context, agentID uuid.UUID, limit int) ([]AgentEvent, error) {
	if _, err := s.GetAgent(ctx, agentID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, agent_id, event_type, detail, created_at
		FROM agent_events WHERE agent_id = $1 ORDER BY id DESC LIMIT $2`, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentEvent{}
	for rows.Next() {
		var e AgentEvent
		if err := rows.Scan(&e.ID, &e.AgentID, &e.EventType, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func insertAgentEvent(ctx context.Context, tx pgx.Tx, agentID uuid.UUID, eventType string, detail any) error {
	var payload []byte
	if detail != nil {
		var err error
		if payload, err = json.Marshal(detail); err != nil {
			return fmt.Errorf("marshal event detail: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_events (agent_id, event_type, detail) VALUES ($1, $2, $3)`,
		agentID, eventType, payload); err != nil {
		return fmt.Errorf("insert agent event: %w", err)
	}
	return nil
}
