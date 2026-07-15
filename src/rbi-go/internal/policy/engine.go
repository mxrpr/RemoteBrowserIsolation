// Package policy provides the site-policy resolver and request-log writer for
// the rbi-go backend.  It mirrors the C# PolicyEngine and RequestLogService: an
// in-memory cache of SitePolicy rows loaded from the database and matched using
// longest-host-match semantics (deny-by-default for unmatched hosts).
package policy

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"rbi-go/internal/db"
)

// Engine holds an in-memory snapshot of the SitePolicies table and resolves
// hostnames against it using longest-match semantics. It is thread-safe via a
// read-write mutex.  The cache is loaded lazily on the first Resolve call and
// must be invalidated (via Invalidate) after any CRUD mutation so that the next
// call sees fresh data.
type Engine struct {
	// sqlDB is the underlying SQLite connection used to reload the cache.
	sqlDB *sql.DB

	// mu protects cache and hasLoad.
	mu sync.RWMutex

	// cache is the in-memory copy of all SitePolicy rows, sorted by HostPattern.
	cache []db.SitePolicy

	// hasLoad is true once the cache has been populated at least once since the
	// last Invalidate call.
	hasLoad bool
}

// NewEngine creates a new Engine backed by the given database. The cache is
// populated lazily on the first Resolve call.
func NewEngine(database *db.DB) *Engine {
	return &Engine{sqlDB: database.Unwrap()}
}

// Invalidate clears the in-memory cache so that the next Resolve call will
// reload from the database. Must be called after every CRUD mutation to the
// SitePolicies table.
func (e *Engine) Invalidate() {
	e.mu.Lock()
	e.hasLoad = false
	e.mu.Unlock()
}

// SQLDB returns the underlying *sql.DB so that handler code can perform CRUD
// queries without needing a separate DB reference passed through the call chain.
func (e *Engine) SQLDB() *sql.DB {
	return e.sqlDB
}

// Resolve returns the ViewMode for the given bare hostname (no scheme or path),
// or nil if no matching policy exists (deny-by-default). Longest HostPattern
// wins when multiple patterns match, so a specific rule for "app.example.com"
// overrides a broader "example.com" rule — exactly mirroring C# PolicyEngine.
func (e *Engine) Resolve(host string) (*db.ViewMode, error) {
	host = strings.ToLower(strings.TrimSpace(host))

	policies, err := e.getPolicies()
	if err != nil {
		return nil, err
	}

	var best *db.SitePolicy
	for i := range policies {
		p := &policies[i]
		if !isHostMatch(host, p.HostPattern) {
			continue
		}
		// Longer pattern is more specific — pick it.
		if best == nil || len(p.HostPattern) > len(best.HostPattern) {
			best = p
		}
	}

	if best == nil {
		return nil, nil
	}
	mode := best.ViewMode
	return &mode, nil
}

// getPolicies returns the cached policy slice, loading from the database first
// if the cache is stale or has not yet been populated.
func (e *Engine) getPolicies() ([]db.SitePolicy, error) {
	// Fast path: cache is hot — return under read lock.
	e.mu.RLock()
	if e.hasLoad {
		policies := e.cache
		e.mu.RUnlock()
		return policies, nil
	}
	e.mu.RUnlock()

	// Slow path: load under write lock with double-check.
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.hasLoad {
		return e.cache, nil
	}
	if err := e.load(); err != nil {
		return nil, err
	}
	return e.cache, nil
}

// load reads all SitePolicy rows from the database and replaces e.cache.
// Must be called with e.mu write lock held.
func (e *Engine) load() error {
	rows, err := e.sqlDB.Query(
		`SELECT Id, HostPattern, ViewMode, CreatedAt, UpdatedAt
		   FROM SitePolicies
		  ORDER BY HostPattern`,
	)
	if err != nil {
		return fmt.Errorf("policy: load: %w", err)
	}
	defer rows.Close()

	var policies []db.SitePolicy
	for rows.Next() {
		var p db.SitePolicy
		var createdAt, updatedAt string
		if err := rows.Scan(&p.Id, &p.HostPattern, &p.ViewMode, &createdAt, &updatedAt); err != nil {
			return fmt.Errorf("policy: scan: %w", err)
		}
		p.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		p.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		policies = append(policies, p)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("policy: iterate: %w", err)
	}

	e.cache = policies
	e.hasLoad = true
	return nil
}

// isHostMatch returns true if host equals pattern (case-insensitive) or is a
// subdomain of it.  Mirrors C# PolicyEngine.IsHostMatch exactly: both "www.example.com"
// and "app.example.com" match pattern "example.com".
func isHostMatch(host, pattern string) bool {
	pattern = strings.ToLower(pattern)
	host = strings.ToLower(host)
	return host == pattern || strings.HasSuffix(host, "."+pattern)
}
