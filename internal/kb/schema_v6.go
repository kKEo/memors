package kb

import (
	"context"
	"database/sql"
	"fmt"
)

// schemaV6 is migration 6: the outline of the book, and page dependencies.
//
// An outline is a namespace's ordered list of chapters, written by a person
// or by the calling agent; nothing detects topics. A member is an entity or a
// source, and belongs to one chapter, so the book is a tree. ref_id has no
// foreign key because it points at two tables; ReadOutline reports a member
// whose record is gone.
//
// page_deps records the pages a topic or overview page was written from,
// with the revision each one had then, so a parent page knows how far its
// children have moved on since.
const schemaV6 = `
CREATE TABLE outline_chapters (
    id         TEXT PRIMARY KEY,
    namespace  TEXT NOT NULL REFERENCES namespaces(name),
    position   INTEGER NOT NULL,
    title      TEXT NOT NULL,
    scope      TEXT NOT NULL DEFAULT '',
    trust      TEXT NOT NULL DEFAULT 'agent',
    actor      TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX idx_outline_chapters_ns ON outline_chapters(namespace, position);

CREATE TABLE outline_members (
    chapter_id TEXT NOT NULL REFERENCES outline_chapters(id) ON DELETE CASCADE,
    position   INTEGER NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('entity','source')),
    ref_id     TEXT NOT NULL,
    PRIMARY KEY (kind, ref_id)
);
CREATE INDEX idx_outline_members_chapter ON outline_members(chapter_id, position);

CREATE TABLE page_deps (
    page_id     TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    dep_page_id TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    dep_rev     INTEGER NOT NULL,
    PRIMARY KEY (page_id, dep_page_id)
);
CREATE INDEX idx_page_deps_dep ON page_deps(dep_page_id);
`

func migrateV6(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, schemaV6); err != nil {
		return fmt.Errorf("create outline: %w", err)
	}
	return nil
}
