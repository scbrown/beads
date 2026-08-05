package schema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrUnpinnedConnection is returned when the handle given to MigrateUp does not
// preserve session state between statements.
var ErrUnpinnedConnection = errors.New("migration connection is not pinned")

// assertSessionPinned REFUSES to migrate through a handle that does not carry
// @user variables from one statement to the next (aegis-pakar).
//
// WHY THIS EXISTS. Many migrations in this tree guard their work with a session
// variable — `SET @needs = (...)` in one statement, `IF(@needs = 1, ...)` in the
// next. Go's database/sql POOLS connections, and MySQL/Dolt session variables
// live on ONE connection. Run through a *sql.DB the guard reads an unset
// variable on whatever connection serves it next, every guarded statement
// evaluates to its no-op branch, nothing errors, and the migration reports
// success having done NOTHING.
//
// A silent no-op and a successful migration are the same observable: exit 0.
// That is the failure this refusal converts into a loud one.
//
// The production paths already pin correctly — internal/storage/dolt and
// internal/storage/embeddeddolt both call db.Conn(ctx) and pass the *sql.Conn.
// But DBConn is an interface that *sql.DB also satisfies, so the pin is a
// convention the type system does not enforce, and several tests do pass a
// pooled handle. This makes the requirement checkable instead of remembered.
//
// It probes BEHAVIOR rather than the concrete type: a wrapper, a mock or a
// future handle is judged on whether session state actually survives, which is
// the property migrations depend on.
func assertSessionPinned(ctx context.Context, db DBConn) error {
	// DETERMINISTIC ARM. *sql.DB is the pooled handle by definition, and the
	// behavioral probe below cannot be relied on to catch it: a quiet
	// single-goroutine caller usually gets its idle connection back and the
	// probe passes. So reject the concrete type outright rather than hoping the
	// pool misbehaves while we are watching.
	if _, pooled := db.(*sql.DB); pooled {
		return fmt.Errorf("%w: MigrateUp was given a *sql.DB, which is a connection POOL.\n"+
			"      @user-variable guards in these migrations would read an unset variable on\n"+
			"      whichever connection served them, take their no-op branch, and report success\n"+
			"      having applied nothing. Pin the connection and pass it instead:\n\n"+
			"          conn, err := db.Conn(ctx)   // *sql.Conn\n"+
			"          defer conn.Close()\n"+
			"          schema.MigrateUp(ctx, conn)", ErrUnpinnedConnection)
	}

	// BEHAVIORAL ARM, kept as well as the type check rather than instead of
	// it. It judges a wrapper, mock or future handle on the property migrations
	// actually depend on. It is a true positive when it fires and NOT a proof
	// of pinning when it passes — session state can survive by luck on a pool
	// that happens to reuse its idle connection.
	const sentinel = 0x5ED9 // arbitrary, distinctive in an error message

	if _, err := db.ExecContext(ctx, fmt.Sprintf("SET @beads_pin_probe = %d", sentinel)); err != nil {
		return fmt.Errorf("setting the session-pin probe: %w", err)
	}

	var got sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT @beads_pin_probe").Scan(&got); err != nil {
		return fmt.Errorf("reading the session-pin probe: %w", err)
	}

	if got.Valid && got.Int64 == sentinel {
		_, _ = db.ExecContext(ctx, "SET @beads_pin_probe = NULL")
		return nil
	}

	return fmt.Errorf("%w: a session variable set by one statement was not visible to the next "+
		"(wrote %d, read back %v).\n"+
		"      Migrations in this tree guard their work with @user variables, which live on ONE\n"+
		"      connection. Through a pooled *sql.DB every guard reads an unset variable, takes its\n"+
		"      no-op branch, and the migration reports success having applied nothing.\n"+
		"      Pin the connection and pass it instead:\n\n"+
		"          conn, err := db.Conn(ctx)   // *sql.Conn\n"+
		"          defer conn.Close()\n"+
		"          schema.MigrateUp(ctx, conn)",
		ErrUnpinnedConnection, sentinel, got)
}
