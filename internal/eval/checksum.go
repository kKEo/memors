package eval

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
)

// RawChecksum hashes every raw row (sources, documents, chunks and, when
// includeFacts is set, facts) so a test can prove that a derived operation
// such as compaction touched none of them (research D-A). Two calls on an
// unchanged store return the same string.
func RawChecksum(ctx context.Context, db *sql.DB, includeFacts bool) (string, error) {
	h := sha256.New()
	queries := []string{
		`SELECT id, namespace, uri, content_hash, trust FROM sources ORDER BY id`,
		`SELECT id, source_id, revision, content, deleted_at, superseded_by FROM documents ORDER BY id`,
		`SELECT id, document_id, ord, text FROM chunks ORDER BY id`,
	}
	if includeFacts {
		queries = append(queries, `SELECT id, statement, trust, deleted_at, invalidated_at FROM facts ORDER BY id`)
	}
	for _, q := range queries {
		if err := hashRows(ctx, db, q, h); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashRows(ctx context.Context, db *sql.DB, q string, h interface{ Write([]byte) (int, error) }) error {
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		fmt.Fprintf(h, "%v\n", vals)
	}
	return rows.Err()
}
