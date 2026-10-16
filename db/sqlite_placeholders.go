package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"

	"github.com/mattn/go-sqlite3"
)

// sqliteDriverName is the SQLite driver NewDBWithConfig opens. The generated
// queries use Postgres-style $N placeholders. SQLite reads $N as a named
// parameter and numbers parameters in order of first appearance, so a query
// such as "SET status = $2 WHERE id = $1" binds its arguments to the wrong
// columns, and an UPDATE silently matches no row. This driver rewrites $N to
// SQLite's numbered ?N form, which binds argument N exactly.
const sqliteDriverName = "sqlite3_numbered"

func init() {
	sql.Register(sqliteDriverName, &numberedSQLiteDriver{})
}

type numberedSQLiteDriver struct {
	sqlite3.SQLiteDriver
}

func (d *numberedSQLiteDriver) Open(dsn string) (driver.Conn, error) {
	conn, err := d.SQLiteDriver.Open(dsn)
	if err != nil {
		return nil, err
	}
	sc, ok := conn.(*sqlite3.SQLiteConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("unexpected sqlite connection type %T", conn)
	}
	return &numberedSQLiteConn{sc}, nil
}

// numberedSQLiteConn rewrites placeholders on every path that takes a query
// string; all other methods come from the embedded connection.
type numberedSQLiteConn struct {
	*sqlite3.SQLiteConn
}

func (c *numberedSQLiteConn) Prepare(query string) (driver.Stmt, error) {
	return c.SQLiteConn.Prepare(numberPlaceholders(query))
}

func (c *numberedSQLiteConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return c.SQLiteConn.PrepareContext(ctx, numberPlaceholders(query))
}

func (c *numberedSQLiteConn) Exec(query string, args []driver.Value) (driver.Result, error) {
	return c.SQLiteConn.Exec(numberPlaceholders(query), args)
}

func (c *numberedSQLiteConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.SQLiteConn.ExecContext(ctx, numberPlaceholders(query), args)
}

func (c *numberedSQLiteConn) Query(query string, args []driver.Value) (driver.Rows, error) {
	return c.SQLiteConn.Query(numberPlaceholders(query), args)
}

func (c *numberedSQLiteConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.SQLiteConn.QueryContext(ctx, numberPlaceholders(query), args)
}

// numberPlaceholders rewrites each $N placeholder to ?N, leaving string
// literals, quoted identifiers and comments untouched.
func numberPlaceholders(query string) string {
	if !strings.Contains(query, "$") {
		return query
	}
	var b strings.Builder
	b.Grow(len(query))
	for i := 0; i < len(query); {
		c := query[i]
		switch {
		case c == '\'' || c == '"':
			// Quoted literal or identifier; a doubled quote is an escape and
			// is consumed as two consecutive quoted runs.
			end := strings.IndexByte(query[i+1:], c)
			if end < 0 {
				b.WriteString(query[i:])
				return b.String()
			}
			b.WriteString(query[i : i+end+2])
			i += end + 2
		case c == '-' && i+1 < len(query) && query[i+1] == '-':
			end := strings.IndexByte(query[i:], '\n')
			if end < 0 {
				b.WriteString(query[i:])
				return b.String()
			}
			b.WriteString(query[i : i+end])
			i += end
		case c == '/' && i+1 < len(query) && query[i+1] == '*':
			end := strings.Index(query[i+2:], "*/")
			if end < 0 {
				b.WriteString(query[i:])
				return b.String()
			}
			b.WriteString(query[i : i+end+4])
			i += end + 4
		case c == '$' && i+1 < len(query) && query[i+1] >= '0' && query[i+1] <= '9':
			j := i + 1
			for j < len(query) && query[j] >= '0' && query[j] <= '9' {
				j++
			}
			b.WriteByte('?')
			b.WriteString(query[i+1 : j])
			i = j
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}
