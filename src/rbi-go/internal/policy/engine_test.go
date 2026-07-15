package policy

import (
	"testing"
	"time"

	"rbi-go/internal/db"
)

// newTestDB creates an in-memory SQLite database for tests.
func newTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("newTestDB: open in-memory DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

// seedPolicy inserts a site policy directly into the database for testing.
func seedPolicy(t *testing.T, eng *Engine, hostPattern string, mode db.ViewMode) int64 {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := eng.SQLDB().Exec(
		`INSERT INTO SitePolicies (HostPattern, ViewMode, CreatedAt, UpdatedAt)
		 VALUES (?, ?, ?, ?)`,
		hostPattern, int(mode), now, now,
	)
	if err != nil {
		t.Fatalf("seedPolicy: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// === isHostMatch Tests ===

// TestIsHostMatch_ExactMatch verifies that exact hostname matches.
func TestIsHostMatch_ExactMatch(t *testing.T) {
	if !isHostMatch("example.com", "example.com") {
		t.Error("expected exact match for 'example.com' against 'example.com'")
	}
}

// TestIsHostMatch_Subdomain_Matches verifies that a subdomain matches the pattern.
func TestIsHostMatch_Subdomain_Matches(t *testing.T) {
	if !isHostMatch("www.example.com", "example.com") {
		t.Error("expected 'www.example.com' to match 'example.com'")
	}
}

// TestIsHostMatch_DeepSubdomain_Matches verifies that a deep subdomain matches the pattern.
func TestIsHostMatch_DeepSubdomain_Matches(t *testing.T) {
	if !isHostMatch("app.api.example.com", "example.com") {
		t.Error("expected 'app.api.example.com' to match 'example.com'")
	}
}

// TestIsHostMatch_DifferentHost_NoMatch verifies that a different host does not match.
func TestIsHostMatch_DifferentHost_NoMatch(t *testing.T) {
	if isHostMatch("other.com", "example.com") {
		t.Error("expected 'other.com' not to match 'example.com'")
	}
}

// TestIsHostMatch_SuffixWithoutDot_NoMatch verifies that a host that merely ends with
// the pattern but lacks a dot separator does not match.
func TestIsHostMatch_SuffixWithoutDot_NoMatch(t *testing.T) {
	if isHostMatch("notexample.com", "example.com") {
		t.Error("expected 'notexample.com' not to match 'example.com'")
	}
}

// TestIsHostMatch_EmptyHost_EmptyPattern_Match verifies that both empty strings match.
func TestIsHostMatch_EmptyHost_EmptyPattern_Match(t *testing.T) {
	if !isHostMatch("", "") {
		t.Error("expected empty host to match empty pattern")
	}
}

// TestIsHostMatch_CaseInsensitive verifies that matching is case-insensitive.
func TestIsHostMatch_CaseInsensitive(t *testing.T) {
	if !isHostMatch("WWW.EXAMPLE.COM", "example.com") {
		t.Error("expected case-insensitive match")
	}
	if !isHostMatch("example.com", "EXAMPLE.COM") {
		t.Error("expected case-insensitive match (pattern uppercase)")
	}
}

// === Resolve Basics Tests ===

// TestEngine_Resolve_ExactMatch_ReturnsMode verifies that resolving against an exact
// match returns the correct ViewMode.
func TestEngine_Resolve_ExactMatch_ReturnsMode(t *testing.T) {
	database := newTestDB(t)
	eng := NewEngine(database)
	seedPolicy(t, eng, "example.com", db.ViewModeHtmlAllowInput)

	mode, err := eng.Resolve("example.com")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if mode == nil {
		t.Fatal("expected mode to be non-nil")
	}
	if *mode != db.ViewModeHtmlAllowInput {
		t.Errorf("expected ViewModeHtmlAllowInput, got %d", *mode)
	}
}

// TestEngine_Resolve_SubdomainMatch_ReturnsMode verifies that resolving a subdomain
// against a broader pattern returns the mode.
func TestEngine_Resolve_SubdomainMatch_ReturnsMode(t *testing.T) {
	database := newTestDB(t)
	eng := NewEngine(database)
	seedPolicy(t, eng, "example.com", db.ViewModeVideoAllowInput)

	mode, err := eng.Resolve("www.example.com")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if mode == nil {
		t.Fatal("expected mode to be non-nil")
	}
	if *mode != db.ViewModeVideoAllowInput {
		t.Errorf("expected ViewModeVideoAllowInput, got %d", *mode)
	}
}

// TestEngine_Resolve_NoMatch_ReturnsNil verifies that resolving against a hostname
// with no matching policy returns nil (deny by default).
func TestEngine_Resolve_NoMatch_ReturnsNil(t *testing.T) {
	database := newTestDB(t)
	eng := NewEngine(database)
	seedPolicy(t, eng, "example.com", db.ViewModeHtmlAllowInput)

	mode, err := eng.Resolve("other.com")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if mode != nil {
		t.Errorf("expected nil for unmatched host, got %d", *mode)
	}
}

// TestEngine_Resolve_MixedCase_Host_Normalised verifies that mixed-case hostnames
// are normalized to lowercase before resolution.
func TestEngine_Resolve_MixedCase_Host_Normalised(t *testing.T) {
	database := newTestDB(t)
	eng := NewEngine(database)
	seedPolicy(t, eng, "example.com", db.ViewModeHtmlNoInput)

	mode, err := eng.Resolve("WWW.EXAMPLE.COM")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if mode == nil {
		t.Fatal("expected mode to be non-nil for normalized host")
	}
	if *mode != db.ViewModeHtmlNoInput {
		t.Errorf("expected ViewModeHtmlNoInput, got %d", *mode)
	}
}

// === Longest-Match Tests ===

// TestEngine_Resolve_LongestPattern_Wins verifies that when multiple patterns match,
// the longest (most specific) one is returned.
func TestEngine_Resolve_LongestPattern_Wins(t *testing.T) {
	database := newTestDB(t)
	eng := NewEngine(database)
	seedPolicy(t, eng, "example.com", db.ViewModeHtmlAllowInput)
	seedPolicy(t, eng, "app.example.com", db.ViewModeVideoAllowInput)

	mode, err := eng.Resolve("app.example.com")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if mode == nil {
		t.Fatal("expected mode to be non-nil")
	}
	// Should match the longer pattern "app.example.com", not the shorter "example.com".
	if *mode != db.ViewModeVideoAllowInput {
		t.Errorf("expected VideoAllowInput from longer pattern, got %d", *mode)
	}
}

// TestEngine_Resolve_ShortestPattern_LosesWhenLongerExists verifies that a shorter
// pattern does not win when a longer one matches the same host.
func TestEngine_Resolve_ShortestPattern_LosesWhenLongerExists(t *testing.T) {
	database := newTestDB(t)
	eng := NewEngine(database)
	seedPolicy(t, eng, "example.com", db.ViewModeHtmlNoInput)
	seedPolicy(t, eng, "www.example.com", db.ViewModeVideoNoInput)

	mode, err := eng.Resolve("www.example.com")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if mode == nil {
		t.Fatal("expected mode to be non-nil")
	}
	// Should match the longer pattern "www.example.com", not the shorter "example.com".
	if *mode != db.ViewModeVideoNoInput {
		t.Errorf("expected VideoNoInput from longer pattern, got %d", *mode)
	}
}

// === Port Behavior Tests ===

// TestEngine_Resolve_HostWithPort_NotStripped_ByEngine documents that the Engine
// does NOT strip ports from the hostname argument passed to Resolve. The caller
// (e.g. an HTTP handler extracting hostname from a URL) is responsible for stripping.
// This test verifies that if a port is included, it will not match a pattern without port.
func TestEngine_Resolve_HostWithPort_NotStripped_ByEngine(t *testing.T) {
	database := newTestDB(t)
	eng := NewEngine(database)
	seedPolicy(t, eng, "example.com", db.ViewModeHtmlAllowInput)

	// Resolve with port included — should NOT match because "example.com:8080" != "example.com"
	// and "example.com:8080" does not end with ".example.com".
	mode, err := eng.Resolve("example.com:8080")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if mode != nil {
		t.Errorf("expected nil for host:port input, but Engine matched (no auto-strip): got %d", *mode)
	}
}

// === Cache Invalidation Tests ===

// TestEngine_CacheInvalidation_AfterCreate verifies that the cache is invalidated
// and reloaded after a new policy is inserted.
func TestEngine_CacheInvalidation_AfterCreate(t *testing.T) {
	database := newTestDB(t)
	eng := NewEngine(database)

	// First resolve should hit the empty cache.
	mode1, err := eng.Resolve("example.com")
	if err != nil {
		t.Fatalf("first resolve error: %v", err)
	}
	if mode1 != nil {
		t.Error("expected nil for unmatched host (empty cache)")
	}

	// Insert a policy directly (bypassing the engine).
	seedPolicy(t, eng, "example.com", db.ViewModeHtmlAllowInput)

	// Without invalidation, the cache would still be empty. Verify the issue:
	mode2, err := eng.Resolve("example.com")
	if err != nil {
		t.Fatalf("resolve before invalidation error: %v", err)
	}
	if mode2 != nil {
		// Cache was not invalidated, so this should return nil.
		t.Error("expected stale nil from cache, but somehow got a mode (cache should have been stale)")
	}

	// Now invalidate and resolve again — should reload the cache and find the policy.
	eng.Invalidate()
	mode3, err := eng.Resolve("example.com")
	if err != nil {
		t.Fatalf("resolve after invalidation error: %v", err)
	}
	if mode3 == nil {
		t.Fatal("expected mode after invalidation, got nil")
	}
	if *mode3 != db.ViewModeHtmlAllowInput {
		t.Errorf("expected ViewModeHtmlAllowInput, got %d", *mode3)
	}
}

// TestEngine_CacheInvalidation_AfterUpdate verifies that the cache is invalidated
// after an UPDATE to an existing policy.
func TestEngine_CacheInvalidation_AfterUpdate(t *testing.T) {
	database := newTestDB(t)
	eng := NewEngine(database)

	// Insert and cache a policy.
	id := seedPolicy(t, eng, "example.com", db.ViewModeHtmlAllowInput)
	eng.Invalidate() // Clear so first resolve loads fresh
	mode1, err := eng.Resolve("example.com")
	if err != nil {
		t.Fatalf("first resolve error: %v", err)
	}
	if mode1 == nil || *mode1 != db.ViewModeHtmlAllowInput {
		t.Fatalf("expected ViewModeHtmlAllowInput, got %v", mode1)
	}

	// Update the policy directly (simulating a PUT endpoint).
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = eng.SQLDB().Exec(
		`UPDATE SitePolicies SET ViewMode = ?, UpdatedAt = ? WHERE Id = ?`,
		int(db.ViewModeVideoAllowInput), now, id,
	)
	if err != nil {
		t.Fatalf("update error: %v", err)
	}

	// Without invalidation, the cache still returns the old (stale) mode.
	mode2, err := eng.Resolve("example.com")
	if err != nil {
		t.Fatalf("resolve before invalidation error: %v", err)
	}
	if mode2 == nil || *mode2 != db.ViewModeHtmlAllowInput {
		t.Errorf("expected stale ViewModeHtmlAllowInput before invalidation, got %v", mode2)
	}

	// Invalidate and resolve again.
	eng.Invalidate()
	mode3, err := eng.Resolve("example.com")
	if err != nil {
		t.Fatalf("resolve after invalidation error: %v", err)
	}
	if mode3 == nil {
		t.Fatal("expected mode after invalidation, got nil")
	}
	if *mode3 != db.ViewModeVideoAllowInput {
		t.Errorf("expected updated ViewModeVideoAllowInput, got %d", *mode3)
	}
}

// TestEngine_CacheInvalidation_AfterDelete verifies that the cache is invalidated
// after a DELETE of a policy.
func TestEngine_CacheInvalidation_AfterDelete(t *testing.T) {
	database := newTestDB(t)
	eng := NewEngine(database)

	// Insert and cache a policy.
	id := seedPolicy(t, eng, "example.com", db.ViewModeHtmlAllowInput)
	eng.Invalidate()
	mode1, err := eng.Resolve("example.com")
	if err != nil {
		t.Fatalf("first resolve error: %v", err)
	}
	if mode1 == nil {
		t.Fatal("expected mode before delete")
	}

	// Delete the policy directly (simulating a DELETE endpoint).
	_, err = eng.SQLDB().Exec(`DELETE FROM SitePolicies WHERE Id = ?`, id)
	if err != nil {
		t.Fatalf("delete error: %v", err)
	}

	// Without invalidation, the cache still returns the deleted policy (stale hit).
	mode2, err := eng.Resolve("example.com")
	if err != nil {
		t.Fatalf("resolve before invalidation error: %v", err)
	}
	if mode2 == nil {
		t.Error("expected stale mode before invalidation, got nil")
	}

	// Invalidate and resolve again — should now return nil.
	eng.Invalidate()
	mode3, err := eng.Resolve("example.com")
	if err != nil {
		t.Fatalf("resolve after invalidation error: %v", err)
	}
	if mode3 != nil {
		t.Errorf("expected nil after delete + invalidation, got %d", *mode3)
	}
}

// TestEngine_NoInvalidate_StaleCache verifies that without calling Invalidate(),
// a stale cache is returned on subsequent resolves.
func TestEngine_NoInvalidate_StaleCache(t *testing.T) {
	database := newTestDB(t)
	eng := NewEngine(database)

	// Seed a policy and resolve once to populate the cache.
	seedPolicy(t, eng, "example.com", db.ViewModeHtmlAllowInput)
	mode1, err := eng.Resolve("example.com")
	if err != nil {
		t.Fatalf("first resolve error: %v", err)
	}
	if mode1 == nil || *mode1 != db.ViewModeHtmlAllowInput {
		t.Fatalf("expected ViewModeHtmlAllowInput, got %v", mode1)
	}

	// Insert a second policy while cache is hot.
	seedPolicy(t, eng, "other.com", db.ViewModeVideoAllowInput)

	// Resolve the new host WITHOUT invalidation — should still be in stale cache.
	mode2, err := eng.Resolve("other.com")
	if err != nil {
		t.Fatalf("resolve other.com error: %v", err)
	}
	// Since the cache was loaded before the second insert, "other.com" should not be found.
	if mode2 != nil {
		t.Errorf("expected nil from stale cache, got %d", *mode2)
	}

	// Invalidate and try again — should find it now.
	eng.Invalidate()
	mode3, err := eng.Resolve("other.com")
	if err != nil {
		t.Fatalf("resolve after invalidation error: %v", err)
	}
	if mode3 == nil {
		t.Fatal("expected mode after invalidation, got nil")
	}
	if *mode3 != db.ViewModeVideoAllowInput {
		t.Errorf("expected ViewModeVideoAllowInput, got %d", *mode3)
	}
}
