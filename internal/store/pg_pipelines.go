package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sag-solutions/otel-fleet/internal/audit"
)

// pipelineCols selects a pipeline joined with its customer and version
// numbers; requires aliases p (pipelines), c (customers), av (active version).
const pipelineCols = `
	p.id, p.customer_id, c.name, c.slug, c.client_id, p.name, p.target_class,
	p.active_version_id, av.version,
	(SELECT max(v.version) FROM pipeline_versions v WHERE v.pipeline_id = p.id),
	p.created_at`

const pipelineFrom = `
	FROM pipelines p
	JOIN customers c ON c.id = p.customer_id
	LEFT JOIN pipeline_versions av ON av.id = p.active_version_id`

func scanPipeline(row pgx.Row) (Pipeline, error) {
	var p Pipeline
	err := row.Scan(&p.ID, &p.CustomerID, &p.CustomerName, &p.CustomerSlug, &p.ClientID,
		&p.Name, &p.TargetClass, &p.ActiveVersionID, &p.ActiveVersion, &p.LatestVersion, &p.CreatedAt)
	return p, err
}

// pipelineVersionCols selects a version; requires aliases v (pipeline_versions),
// u (users, LEFT JOIN) and p (pipelines, for the active flag).
const pipelineVersionCols = `
	v.id, v.pipeline_id, v.version, v.graph, v.rendered_yaml, v.config_hash,
	v.validation_status, v.validation_output, v.created_by, u.email, v.created_at,
	(p.active_version_id = v.id) IS TRUE`

const pipelineVersionFrom = `
	FROM pipeline_versions v
	JOIN pipelines p ON p.id = v.pipeline_id
	LEFT JOIN users u ON u.id = v.created_by`

func scanPipelineVersion(row pgx.Row) (PipelineVersion, error) {
	var v PipelineVersion
	err := row.Scan(&v.ID, &v.PipelineID, &v.Version, &v.Graph, &v.RenderedYAML, &v.ConfigHash,
		&v.ValidationStatus, &v.ValidationOutput, &v.CreatedBy, &v.CreatedByEmail, &v.CreatedAt, &v.Active)
	return v, err
}

// CreatePipeline inserts a pipeline plus its version 1 and the audit entries
// in one transaction. Returns ErrNameExists when (customer_id, name) exists
// and ErrNotFound when the customer is unknown or deleted.
func (s *PG) CreatePipeline(ctx context.Context, p NewPipeline, v NewPipelineVersion, entries []audit.Entry) (Pipeline, PipelineVersion, error) {
	var pipe Pipeline
	var ver PipelineVersion
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var exists bool
		err := tx.QueryRow(ctx, `
			SELECT true FROM customers WHERE id = $1 AND status <> 'deleted'`, p.CustomerID).Scan(&exists)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		targetClass := p.TargetClass
		if targetClass == "" {
			targetClass = ClassForwarding
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO pipelines (id, customer_id, name, target_class) VALUES ($1, $2, $3, $4)`,
			p.ID, p.CustomerID, p.Name, targetClass)
		if isUniqueViolation(err, "pipelines_customer_id_name_key") {
			return ErrNameExists
		}
		if err != nil {
			return fmt.Errorf("insert pipeline: %w", err)
		}

		if err := insertPipelineVersion(ctx, tx, v, 1); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, entries...); err != nil {
			return err
		}

		pipe, err = scanPipeline(tx.QueryRow(ctx, `SELECT`+pipelineCols+pipelineFrom+` WHERE p.id = $1`, p.ID))
		if err != nil {
			return fmt.Errorf("read back pipeline: %w", err)
		}
		ver, err = scanPipelineVersion(tx.QueryRow(ctx, `SELECT`+pipelineVersionCols+pipelineVersionFrom+` WHERE v.id = $1`, v.ID))
		if err != nil {
			return fmt.Errorf("read back version: %w", err)
		}
		return nil
	})
	if err != nil {
		return Pipeline{}, PipelineVersion{}, err
	}
	return pipe, ver, nil
}

func (s *PG) GetPipeline(ctx context.Context, id uuid.UUID) (Pipeline, error) {
	p, err := scanPipeline(s.pool.QueryRow(ctx, `SELECT`+pipelineCols+pipelineFrom+` WHERE p.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Pipeline{}, ErrNotFound
	}
	return p, err
}

// ListPipelines returns pipelines of one customer (customerID != nil) or of
// all non-deleted customers.
func (s *PG) ListPipelines(ctx context.Context, customerID *uuid.UUID) ([]Pipeline, error) {
	q := `SELECT` + pipelineCols + pipelineFrom + ` WHERE c.status <> 'deleted'`
	args := []any{}
	if customerID != nil {
		q += ` AND p.customer_id = $1`
		args = append(args, *customerID)
	}
	q += ` ORDER BY c.slug, p.name, p.id`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Pipeline{}
	for rows.Next() {
		p, err := scanPipeline(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeletePipeline hard-deletes a pipeline; its versions cascade. The deferred
// active_version_id FK makes the ordering within the transaction irrelevant.
func (s *PG) DeletePipeline(ctx context.Context, id uuid.UUID, entries []audit.Entry) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM pipelines WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("delete pipeline: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audit.Write(ctx, tx, entries...)
	})
}

// ListPipelineVersions returns all versions of a pipeline, newest first.
func (s *PG) ListPipelineVersions(ctx context.Context, pipelineID uuid.UUID) ([]PipelineVersion, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT`+pipelineVersionCols+pipelineVersionFrom+`
		WHERE v.pipeline_id = $1 ORDER BY v.version DESC`, pipelineID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PipelineVersion{}
	for rows.Next() {
		v, err := scanPipelineVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *PG) GetPipelineVersion(ctx context.Context, pipelineID uuid.UUID, version int) (PipelineVersion, error) {
	v, err := scanPipelineVersion(s.pool.QueryRow(ctx, `
		SELECT`+pipelineVersionCols+pipelineVersionFrom+`
		WHERE v.pipeline_id = $1 AND v.version = $2`, pipelineID, version))
	if errors.Is(err, pgx.ErrNoRows) {
		return PipelineVersion{}, ErrNotFound
	}
	return v, err
}

// CreatePipelineVersion assigns the next version number (max+1, guarded by a
// row lock on the pipeline) and inserts the version plus audit entries.
func (s *PG) CreatePipelineVersion(ctx context.Context, v NewPipelineVersion, entries []audit.Entry) (PipelineVersion, error) {
	var ver PipelineVersion
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var exists bool
		err := tx.QueryRow(ctx, `SELECT true FROM pipelines WHERE id = $1 FOR UPDATE`, v.PipelineID).Scan(&exists)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var next int
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(max(version), 0) + 1 FROM pipeline_versions WHERE pipeline_id = $1`,
			v.PipelineID).Scan(&next); err != nil {
			return err
		}
		if err := insertPipelineVersion(ctx, tx, v, next); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, entries...); err != nil {
			return err
		}
		ver, err = scanPipelineVersion(tx.QueryRow(ctx, `SELECT`+pipelineVersionCols+pipelineVersionFrom+` WHERE v.id = $1`, v.ID))
		return err
	})
	if err != nil {
		return PipelineVersion{}, err
	}
	return ver, nil
}

func insertPipelineVersion(ctx context.Context, tx pgx.Tx, v NewPipelineVersion, version int) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO pipeline_versions (id, pipeline_id, version, graph, rendered_yaml, config_hash, validation_status, validation_output, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		v.ID, v.PipelineID, version, v.Graph, v.RenderedYAML, v.ConfigHash, v.ValidationStatus, v.ValidationOutput, v.CreatedBy)
	if err != nil {
		return fmt.Errorf("insert pipeline version: %w", err)
	}
	return nil
}

// ActivatePipelineVersion points active_version_id at the given version.
// Returns ErrConflict when the version's validation_status is not 'valid'.
func (s *PG) ActivatePipelineVersion(ctx context.Context, pipelineID uuid.UUID, version int, entries []audit.Entry) (Pipeline, PipelineVersion, error) {
	var pipe Pipeline
	var ver PipelineVersion
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		ver, err = scanPipelineVersion(tx.QueryRow(ctx, `
			SELECT`+pipelineVersionCols+pipelineVersionFrom+`
			WHERE v.pipeline_id = $1 AND v.version = $2`, pipelineID, version))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if ver.ValidationStatus != ValidationValid {
			return fmt.Errorf("version %d is invalid: %w", version, ErrConflict)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE pipelines SET active_version_id = $2 WHERE id = $1`, pipelineID, ver.ID); err != nil {
			return fmt.Errorf("activate version: %w", err)
		}
		if err := audit.Write(ctx, tx, entries...); err != nil {
			return err
		}
		pipe, err = scanPipeline(tx.QueryRow(ctx, `SELECT`+pipelineCols+pipelineFrom+` WHERE p.id = $1`, pipelineID))
		if err != nil {
			return err
		}
		ver.Active = true
		return nil
	})
	if err != nil {
		return Pipeline{}, PipelineVersion{}, err
	}
	return pipe, ver, nil
}

// ListActivePipelines returns the renderer inputs: every pipeline of the
// given target class with an active version, owned by an active customer
// (optionally one customer). Suspended customers are excluded — the gateway
// refuses their data already, and dropping them from the routing table keeps
// the forwarding tier consistent with ingest.
func (s *PG) ListActivePipelines(ctx context.Context, targetClass string, customerID *uuid.UUID) ([]ActivePipeline, error) {
	q := `
		SELECT p.id, p.name, p.customer_id, c.slug, c.client_id, v.graph
		FROM pipelines p
		JOIN customers c ON c.id = p.customer_id AND c.status = 'active'
		JOIN pipeline_versions v ON v.id = p.active_version_id
		WHERE p.target_class = $1`
	args := []any{targetClass}
	if customerID != nil {
		q += ` AND p.customer_id = $2`
		args = append(args, *customerID)
	}
	q += ` ORDER BY c.slug, p.name, p.id`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActivePipeline{}
	for rows.Next() {
		var p ActivePipeline
		if err := rows.Scan(&p.PipelineID, &p.PipelineName, &p.CustomerID, &p.CustomerSlug, &p.ClientID, &p.Graph); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
