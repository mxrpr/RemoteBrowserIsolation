package policy

import (
	"database/sql"
	"testing"
)

// TestWriteRequestLog_InsertsRow verifies that WriteRequestLog inserts exactly one row.
func TestWriteRequestLog_InsertsRow(t *testing.T) {
	database := newTestDB(t)
	sqlDB := database.Unwrap()

	err := WriteRequestLog(sqlDB, "https://example.com", "example.com", "HtmlAllowInput", true, "127.0.0.1")
	if err != nil {
		t.Fatalf("WriteRequestLog error: %v", err)
	}

	// Verify the row exists.
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM RequestLogs`).Scan(&count); err != nil {
		t.Fatalf("count error: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}

// TestWriteRequestLog_FieldsStoredCorrectly verifies that all fields are stored correctly.
func TestWriteRequestLog_FieldsStoredCorrectly(t *testing.T) {
	database := newTestDB(t)
	sqlDB := database.Unwrap()

	rawURL := "https://example.com/path"
	host := "example.com"
	decision := "HtmlAllowInput"
	allowed := true
	clientIP := "192.168.1.1"

	err := WriteRequestLog(sqlDB, rawURL, host, decision, allowed, clientIP)
	if err != nil {
		t.Fatalf("WriteRequestLog error: %v", err)
	}

	// Fetch the row and verify fields.
	var storedURL, storedHost, storedDecision string
	var storedAllowed int
	var storedClientIP sql.NullString
	if err := sqlDB.QueryRow(
		`SELECT Url, Host, Decision, Allowed, ClientIp FROM RequestLogs WHERE Id = 1`,
	).Scan(&storedURL, &storedHost, &storedDecision, &storedAllowed, &storedClientIP); err != nil {
		t.Fatalf("query error: %v", err)
	}

	if storedURL != rawURL {
		t.Errorf("expected URL %q, got %q", rawURL, storedURL)
	}
	if storedHost != host {
		t.Errorf("expected Host %q, got %q", host, storedHost)
	}
	if storedDecision != decision {
		t.Errorf("expected Decision %q, got %q", decision, storedDecision)
	}
	if storedAllowed != 1 {
		t.Errorf("expected Allowed=1 for true, got %d", storedAllowed)
	}
	if !storedClientIP.Valid || storedClientIP.String != clientIP {
		t.Errorf("expected ClientIp %q, got Valid=%v %q", clientIP, storedClientIP.Valid, storedClientIP.String)
	}
}

// TestWriteRequestLog_EmptyClientIP_StoredAsNULL verifies that an empty ClientIP is stored as NULL.
func TestWriteRequestLog_EmptyClientIP_StoredAsNULL(t *testing.T) {
	database := newTestDB(t)
	sqlDB := database.Unwrap()

	err := WriteRequestLog(sqlDB, "https://example.com", "example.com", "deny", false, "")
	if err != nil {
		t.Fatalf("WriteRequestLog error: %v", err)
	}

	// Fetch the row and verify ClientIp is NULL.
	var clientIp sql.NullString
	if err := sqlDB.QueryRow(
		`SELECT ClientIp FROM RequestLogs WHERE Id = 1`,
	).Scan(&clientIp); err != nil {
		t.Fatalf("query error: %v", err)
	}

	if clientIp.Valid {
		t.Errorf("expected ClientIp to be NULL, but got Valid=%v with value %q", clientIp.Valid, clientIp.String)
	}
}

// TestWriteRequestLog_NonEmptyClientIP_StoredAsString verifies that a non-empty ClientIP is stored.
func TestWriteRequestLog_NonEmptyClientIP_StoredAsString(t *testing.T) {
	database := newTestDB(t)
	sqlDB := database.Unwrap()

	clientIP := "10.0.0.1"
	err := WriteRequestLog(sqlDB, "https://example.com", "example.com", "HtmlNoInput", true, clientIP)
	if err != nil {
		t.Fatalf("WriteRequestLog error: %v", err)
	}

	// Fetch the row and verify ClientIp is stored.
	var storedClientIP sql.NullString
	if err := sqlDB.QueryRow(
		`SELECT ClientIp FROM RequestLogs WHERE Id = 1`,
	).Scan(&storedClientIP); err != nil {
		t.Fatalf("query error: %v", err)
	}

	if !storedClientIP.Valid {
		t.Error("expected ClientIp to be NOT NULL")
	}
	if storedClientIP.String != clientIP {
		t.Errorf("expected ClientIp %q, got %q", clientIP, storedClientIP.String)
	}
}

// TestWriteRequestLog_AllowedFalse_StoredAs0 verifies that Allowed=false is stored as 0.
func TestWriteRequestLog_AllowedFalse_StoredAs0(t *testing.T) {
	database := newTestDB(t)
	sqlDB := database.Unwrap()

	err := WriteRequestLog(sqlDB, "https://example.com", "example.com", "deny", false, "127.0.0.1")
	if err != nil {
		t.Fatalf("WriteRequestLog error: %v", err)
	}

	// Fetch the row and verify Allowed is stored as 0.
	var allowedInt int
	if err := sqlDB.QueryRow(
		`SELECT Allowed FROM RequestLogs WHERE Id = 1`,
	).Scan(&allowedInt); err != nil {
		t.Fatalf("query error: %v", err)
	}

	if allowedInt != 0 {
		t.Errorf("expected Allowed=0 for false, got %d", allowedInt)
	}
}

// TestWriteRequestLog_MultipleRows_EachInserted verifies that multiple calls each
// insert a separate row.
func TestWriteRequestLog_MultipleRows_EachInserted(t *testing.T) {
	database := newTestDB(t)
	sqlDB := database.Unwrap()

	// Insert multiple rows.
	for i := 0; i < 5; i++ {
		err := WriteRequestLog(
			sqlDB,
			"https://example.com",
			"example.com",
			"HtmlAllowInput",
			true,
			"127.0.0.1",
		)
		if err != nil {
			t.Fatalf("WriteRequestLog iteration %d error: %v", i, err)
		}
	}

	// Verify all 5 rows were inserted.
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM RequestLogs`).Scan(&count); err != nil {
		t.Fatalf("count error: %v", err)
	}
	if count != 5 {
		t.Errorf("expected 5 rows, got %d", count)
	}
}
