//go:build !windows

package testutil

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/steveyegge/beads/internal/storage/doltutil"
)

// utcSkewTolerance is the largest NOW()-vs-UTC gap treated as clock jitter
// rather than a timezone difference. Timezone offsets are minutes at minimum;
// this only has to be larger than round-trip noise.
const utcSkewTolerance = 90 * time.Second

// assertUTCTestServer REFUSES to run the suite against an external Dolt server
// whose clock is not UTC (aegis-clw7w).
//
// WHY THIS IS A REFUSAL AND NOT A NOTE. The suite compares stored timestamps
// against `time.Now().UTC()`. A server started without a timezone inherits the
// HOST's zone, so rows are stamped in local time and a UTC comparison filters
// every one of them out. The result is not an error — it is tests failing hours
// later with "expected event ... not found", which reads as a broken query.
// Measured: TestGetAllEventsSince_UnionBothTables failed exactly that way on an
// EDT host and passed on the same server started with TZ=UTC, same code, same
// data, same dolt binary.
//
// The container path never surfaced this because containers default to UTC. So
// the dependency was real, invisible, and only reachable through the seam that
// invites you to bring your own server — which said "start a local dolt
// sql-server" and nothing about its clock. This turns that silence into a
// refusal that names the remedy.
func assertUTCTestServer(port int) error {
	dsn := doltutil.ServerDSN{Host: "127.0.0.1", Port: port, User: "root", Timeout: 10 * time.Second}.String()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("connecting to the external test server to check its clock: %w", err)
	}
	defer db.Close()

	// Ask for the difference on the SERVER rather than comparing formatted
	// strings here: NOW() and UTC_TIMESTAMP() carry different sub-second
	// precision, so a string comparison reports a difference between two
	// identical instants.
	var skewSeconds int64
	if err := db.QueryRow("SELECT TIMESTAMPDIFF(SECOND, NOW(), UTC_TIMESTAMP())").Scan(&skewSeconds); err != nil {
		return fmt.Errorf("reading the external test server's clock: %w", err)
	}
	if skewSeconds < 0 {
		skewSeconds = -skewSeconds
	}
	if time.Duration(skewSeconds)*time.Second <= utcSkewTolerance {
		return nil
	}

	return fmt.Errorf("the Dolt server on port %d is not running in UTC: its NOW() differs from "+
		"UTC_TIMESTAMP() by %ds.\n"+
		"      These tests compare stored timestamps against time.Now().UTC(), so rows stamped in\n"+
		"      local time are filtered out and tests fail with \"not found\" rather than a clock error.\n"+
		"      Restart the server in UTC:\n\n"+
		"          TZ=UTC dolt sql-server --host 127.0.0.1 --port %d --data-dir <dir>\n\n"+
		"      (Containers default to UTC, which is why this only bites a server you started.)",
		port, skewSeconds, port)
}
