package policy

import (
	"database/sql"
	"fmt"
	"time"
)

// WriteRequestLog inserts one row into the RequestLogs table, recording every
// browse decision (allowed or denied, any mode).  It mirrors C# RequestLogService.LogAsync.
// clientIP may be empty (stored as NULL).  Callers should log any returned error
// but treat it as non-fatal so a DB hiccup does not interrupt the request flow.
func WriteRequestLog(sqlDB *sql.DB, rawURL, host, decision string, allowed bool, clientIP string) error {
	ts := time.Now().UTC().Format(time.RFC3339Nano)

	var clientIPVal interface{}
	if clientIP != "" {
		clientIPVal = clientIP
	}

	_, err := sqlDB.Exec(
		`INSERT INTO RequestLogs (Timestamp, Url, Host, Decision, Allowed, ClientIp)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		ts, rawURL, host, decision, boolToInt(allowed), clientIPVal,
	)
	if err != nil {
		return fmt.Errorf("requestlog: insert: %w", err)
	}
	return nil
}

// boolToInt converts a boolean to SQLite's integer representation (0/1).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
