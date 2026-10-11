// SPDX-License-Identifier: GPL-3.0-or-later

package dump

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, no cgo
)

// Row is one series row: column name to value, only for non-null columns.
// Values are int64, float64 or string.
type Row map[string]any

// Reader reads series rows from a dump database. The database is opened
// read-only.
type Reader struct {
	db      *sql.DB
	columns []string
}

// Open opens the dump database at path read-only.
func Open(ctx context.Context, path string) (*Reader, error) {
	// immutable=1: the file never changes while we read it, so SQLite
	// skips locking and never writes a journal next to it.
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("dump: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	r := &Reader{db: db}
	if r.columns, err = r.tableColumns(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return r, nil
}

func (r *Reader) tableColumns(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT name FROM pragma_table_info('series')`)
	if err != nil {
		return nil, fmt.Errorf("dump: read schema: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("dump: read schema: %w", err)
		}
		cols = append(cols, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dump: read schema: %w", err)
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("dump: no series table in the database")
	}
	return cols, nil
}

// Columns lists the columns of the series table.
func (r *Reader) Columns() []string { return r.columns }

// Count returns the number of rows.
func (r *Reader) Count(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM series`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("dump: count: %w", err)
	}
	return n, nil
}

// Each calls fn for every row in id order, streaming them. It stops at the
// first error fn returns.
func (r *Reader) Each(ctx context.Context, fn func(Row) error) error {
	rows, err := r.db.QueryContext(ctx, `SELECT * FROM series ORDER BY id`)
	if err != nil {
		return fmt.Errorf("dump: read: %w", err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("dump: read: %w", err)
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return fmt.Errorf("dump: read: %w", err)
		}
		row := make(Row, 32)
		for i, v := range vals {
			switch x := v.(type) {
			case nil:
			case []byte:
				row[cols[i]] = string(x)
			case bool:
				if x {
					row[cols[i]] = int64(1)
				} else {
					row[cols[i]] = int64(0)
				}
			default:
				row[cols[i]] = x
			}
		}
		if err := fn(row); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("dump: read: %w", err)
	}
	return nil
}

// Close closes the database.
func (r *Reader) Close() error { return r.db.Close() }
