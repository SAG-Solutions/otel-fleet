package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// reencryptColumns are the discrete encrypted-secret columns re-keyed by
// ReencryptSecrets. (Pipeline exporter credentials live inside versioned
// pipeline configs and migrate to the new key the next time a version is
// saved — see the key-rotation guide.)
var reencryptColumns = []struct{ table, col string }{
	{"auth_providers", "client_secret_enc"},
	{"webhooks", "secret_enc"},
}

// ReencryptSecrets rewrites every stored secret through migrate, which
// receives the current ciphertext and returns the new ciphertext plus whether
// it changed (migrate skips secrets already under the primary key). It runs in
// one transaction and returns the number of secrets re-encrypted. Table/column
// names are fixed constants (no injection surface).
func (s *PG) ReencryptSecrets(ctx context.Context, migrate func(enc []byte) (newEnc []byte, changed bool, err error)) (int, error) {
	migrated := 0
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		for _, c := range reencryptColumns {
			rows, err := tx.Query(ctx, `SELECT id, `+c.col+` FROM `+c.table+` WHERE `+c.col+` IS NOT NULL`)
			if err != nil {
				return err
			}
			type row struct {
				id  uuid.UUID
				enc []byte
			}
			var batch []row
			for rows.Next() {
				var r row
				if err := rows.Scan(&r.id, &r.enc); err != nil {
					rows.Close()
					return err
				}
				batch = append(batch, r)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			for _, r := range batch {
				newEnc, changed, err := migrate(r.enc)
				if err != nil {
					return err
				}
				if !changed {
					continue
				}
				if _, err := tx.Exec(ctx, `UPDATE `+c.table+` SET `+c.col+` = $1 WHERE id = $2`, newEnc, r.id); err != nil {
					return err
				}
				migrated++
			}
		}
		return nil
	})
	return migrated, err
}
