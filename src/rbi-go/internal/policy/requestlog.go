package policy

import (
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// logEntry is one queued RequestLogs row plus a completion channel that
// WriteRequestLog blocks on, so the caller still observes a durably-committed
// row (or an error) before returning — callers and tests keep the same
// synchronous-write contract as before.
type logEntry struct {
	ts, rawURL, host, decision string
	allowed                    int
	clientIP                   interface{}
	done                       chan error
}

// logBatchMax bounds how many queued entries one transaction will absorb, so a
// sustained burst still commits in bounded chunks rather than one huge
// transaction.
const logBatchMax = 200

// writerRegistry holds one background writer goroutine per distinct *sql.DB
// (keyed by pointer, so each test's isolated DB gets its own writer/queue —
// there is exactly one production DB in practice, matching db.Connect's
// single-connection pool).
var (
	writerRegistryMu sync.Mutex
	writerRegistry   = map[*sql.DB]chan logEntry{}
)

// getOrStartWriter returns the queue channel for sqlDB, starting its
// background writer goroutine on first use.
func getOrStartWriter(sqlDB *sql.DB) chan logEntry {
	writerRegistryMu.Lock()
	defer writerRegistryMu.Unlock()

	if ch, ok := writerRegistry[sqlDB]; ok {
		return ch
	}
	ch := make(chan logEntry, logBatchMax)
	writerRegistry[sqlDB] = ch
	go runLogWriter(sqlDB, ch)
	return ch
}

// runLogWriter is the single writer goroutine for one *sql.DB. It group-commits:
// after blocking for the first queued entry, it drains whatever else has
// already queued up (without waiting) into the same transaction, so bursts of
// concurrent WriteRequestLog calls collapse into one BEGIN/COMMIT instead of
// each serializing its own transaction on db.Connect's single-connection pool.
func runLogWriter(sqlDB *sql.DB, queue chan logEntry) {
	for first := range queue {
		batch := []logEntry{first}

	drain:
		for len(batch) < logBatchMax {
			select {
			case e := <-queue:
				batch = append(batch, e)
			default:
				break drain
			}
		}

		err := insertBatch(sqlDB, batch)
		for _, e := range batch {
			e.done <- err
		}
	}
}

// insertBatch writes all entries in one transaction using a single prepared
// statement, so a burst of N queued rows costs one BEGIN/COMMIT instead of N.
func insertBatch(sqlDB *sql.DB, batch []logEntry) error {
	tx, err := sqlDB.Begin()
	if err != nil {
		return fmt.Errorf("requestlog: begin tx: %w", err)
	}

	stmt, err := tx.Prepare(
		`INSERT INTO RequestLogs (Timestamp, Url, Host, Decision, Allowed, ClientIp)
		 VALUES (?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("requestlog: prepare: %w", err)
	}
	defer stmt.Close()

	for _, e := range batch {
		if _, err := stmt.Exec(e.ts, e.rawURL, e.host, e.decision, e.allowed, e.clientIP); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("requestlog: insert: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("requestlog: commit: %w", err)
	}
	return nil
}

// WriteRequestLog inserts one row into the RequestLogs table, recording every
// browse decision (allowed or denied, any mode). It mirrors C#
// RequestLogService.LogAsync. clientIP may be empty (stored as NULL). Callers
// should log any returned error but treat it as non-fatal so a DB hiccup does
// not interrupt the request flow.
//
// The write is queued to a per-DB background writer that group-commits
// concurrent entries into shared transactions (see runLogWriter), but this
// call still blocks until its own row is durably committed (or the write
// fails) — callers observe the same synchronous contract as a direct
// sqlDB.Exec, just without each concurrent caller paying its own full
// BEGIN/COMMIT round trip on the single-connection pool.
func WriteRequestLog(sqlDB *sql.DB, rawURL, host, decision string, allowed bool, clientIP string) error {
	var clientIPVal interface{}
	if clientIP != "" {
		clientIPVal = clientIP
	}

	entry := logEntry{
		ts:       time.Now().UTC().Format(time.RFC3339Nano),
		rawURL:   rawURL,
		host:     host,
		decision: decision,
		allowed:  boolToInt(allowed),
		clientIP: clientIPVal,
		done:     make(chan error, 1),
	}

	getOrStartWriter(sqlDB) <- entry
	return <-entry.done
}

// boolToInt converts a boolean to SQLite's integer representation (0/1).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
