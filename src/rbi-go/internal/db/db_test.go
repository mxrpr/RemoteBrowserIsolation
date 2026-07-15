package db

import (
	"path/filepath"
	"testing"
	"time"
)

// openTestDB is a test helper that connects to a fresh temporary SQLite database.
// It calls t.Helper() to mark itself as a helper function, and defers db.Close()
// so the database is cleaned up when the test completes.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Connect(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("openTestDB: Connect failed: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

// === parsePath tests ===

// TestParsePath_BareFilePath tests that parsePath accepts a bare file path without
// Data Source= prefix.
func TestParsePath_BareFilePath(t *testing.T) {
	path := parsePath("/tmp/test.db")
	if path != "/tmp/test.db" {
		t.Errorf("expected /tmp/test.db, got %q", path)
	}
}

// TestParsePath_DataSourceFormat tests that parsePath recognizes the ADO.NET
// "Data Source=<path>" format.
func TestParsePath_DataSourceFormat(t *testing.T) {
	path := parsePath("Data Source=/tmp/test.db")
	if path != "/tmp/test.db" {
		t.Errorf("expected /tmp/test.db, got %q", path)
	}
}

// TestParsePath_DataSourceFormat_LowerCase tests that parsePath is case-insensitive
// for the "data source=" prefix when all lowercase.
func TestParsePath_DataSourceFormat_LowerCase(t *testing.T) {
	path := parsePath("data source=/tmp/test.db")
	if path != "/tmp/test.db" {
		t.Errorf("expected /tmp/test.db, got %q", path)
	}
}

// TestParsePath_DataSourceFormat_MixedCase tests that parsePath is case-insensitive
// for mixed-case "Data Source=" prefix.
func TestParsePath_DataSourceFormat_MixedCase(t *testing.T) {
	path := parsePath("DaTa SoUrCe=/tmp/test.db")
	if path != "/tmp/test.db" {
		t.Errorf("expected /tmp/test.db, got %q", path)
	}
}

// TestParsePath_DataSourceWithTrailingOptions tests that parsePath strips trailing
// semicolon-separated options (e.g., Mode=ReadWrite).
func TestParsePath_DataSourceWithTrailingOptions(t *testing.T) {
	path := parsePath("Data Source=/tmp/test.db;Mode=ReadWrite;Pooling=true")
	if path != "/tmp/test.db" {
		t.Errorf("expected /tmp/test.db, got %q", path)
	}
}

// TestParsePath_DataSourceWithLeadingWhitespace tests that parsePath trims leading
// and trailing whitespace around the connection string and the path itself.
func TestParsePath_DataSourceWithLeadingWhitespace(t *testing.T) {
	path := parsePath("  Data Source=  /tmp/test.db  ")
	if path != "/tmp/test.db" {
		t.Errorf("expected /tmp/test.db, got %q", path)
	}
}

// TestParsePath_EmptyString tests that parsePath returns an empty string for an
// empty connection string.
func TestParsePath_EmptyString(t *testing.T) {
	path := parsePath("")
	if path != "" {
		t.Errorf("expected empty string, got %q", path)
	}
}

// TestParsePath_WhitespaceOnly tests that parsePath returns an empty string for
// a connection string containing only whitespace.
func TestParsePath_WhitespaceOnly(t *testing.T) {
	path := parsePath("   ")
	if path != "" {
		t.Errorf("expected empty string, got %q", path)
	}
}

// === Connect success tests ===

// TestConnect_FreshDB_Succeeds tests that Connect can open a fresh database that
// doesn't yet exist on disk.
func TestConnect_FreshDB_Succeeds(t *testing.T) {
	db := openTestDB(t)
	if db == nil {
		t.Fatal("expected non-nil *DB, got nil")
	}
}

// TestConnect_ExistingDB_Succeeds tests that Connect can reopen an existing database
// by connecting, closing, and connecting again to the same path.
func TestConnect_ExistingDB_Succeeds(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "test.db")

	// First connection
	db1, err := Connect(path)
	if err != nil {
		t.Fatalf("first Connect failed: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}

	// Second connection to the same path
	db2, err := Connect(path)
	if err != nil {
		t.Fatalf("second Connect failed: %v", err)
	}
	defer db2.Close()
}

// TestConnect_DataSourceConnStr_Succeeds tests that Connect accepts a connection
// string in ADO.NET "Data Source=<path>" format.
func TestConnect_DataSourceConnStr_Succeeds(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "test.db")
	connStr := "Data Source=" + path

	db, err := Connect(connStr)
	if err != nil {
		t.Fatalf("Connect with Data Source connStr failed: %v", err)
	}
	defer db.Close()
}

// === Connect properties tests ===

// TestConnect_WALMode_IsSet tests that Connect enables WAL mode by querying
// PRAGMA journal_mode.
func TestConnect_WALMode_IsSet(t *testing.T) {
	db := openTestDB(t)

	var mode string
	row := db.Unwrap().QueryRow("PRAGMA journal_mode")
	if err := row.Scan(&mode); err != nil {
		t.Fatalf("QueryRow journal_mode failed: %v", err)
	}
	if mode != "wal" {
		t.Errorf("expected journal_mode=wal, got %q", mode)
	}
}

// TestConnect_MaxOpenConns_IsOne tests that Connect sets MaxOpenConnections to 1
// to serialize SQLite writes and prevent connection-scoped PRAGMA issues.
func TestConnect_MaxOpenConns_IsOne(t *testing.T) {
	db := openTestDB(t)

	stats := db.Unwrap().Stats()
	if stats.MaxOpenConnections != 1 {
		t.Errorf("expected MaxOpenConnections=1, got %d", stats.MaxOpenConnections)
	}
}

// === Connect errors tests ===

// TestConnect_EmptyConnStr_ReturnsError tests that Connect rejects an empty
// connection string.
func TestConnect_EmptyConnStr_ReturnsError(t *testing.T) {
	_, err := Connect("")
	if err == nil {
		t.Fatal("expected error for empty connection string, got nil")
	}
}

// TestConnect_NonExistentDirectory_ReturnsError tests that Connect fails when
// the directory to create the database file in does not exist. The test does not
// assert which specific step fails, only that an error is returned.
func TestConnect_NonExistentDirectory_ReturnsError(t *testing.T) {
	path := "/nonexistent/directory/test.db"
	_, err := Connect(path)
	if err == nil {
		t.Fatal("expected error for non-existent directory, got nil")
	}
}

// === CreateSchema tests ===

// TestCreateSchema_AllSixTablesExist tests that CreateSchema creates all six required
// tables by querying sqlite_master.
func TestCreateSchema_AllSixTablesExist(t *testing.T) {
	db := openTestDB(t)

	expectedTables := map[string]bool{
		"AdminUsers":                  false,
		"SitePolicies":                false,
		"RequestLogs":                 false,
		"RootCertificateAuthorities":  false,
		"VideoEncoderSettings":        false,
		"LogLevelSettings":            false,
	}

	rows, err := db.Unwrap().Query("SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		t.Fatalf("query sqlite_master failed: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name failed: %v", err)
		}
		if _, ok := expectedTables[name]; ok {
			expectedTables[name] = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}

	for table, found := range expectedTables {
		if !found {
			t.Errorf("table %q was not created", table)
		}
	}
}

// TestCreateSchema_UniqueIndex_AdminUsersEmail_Exists tests that the unique index
// on AdminUsers.Email is created.
func TestCreateSchema_UniqueIndex_AdminUsersEmail_Exists(t *testing.T) {
	db := openTestDB(t)

	var name string
	err := db.Unwrap().QueryRow(
		"SELECT name FROM sqlite_master WHERE type='index' AND name='IX_AdminUsers_Email'",
	).Scan(&name)
	if err != nil {
		t.Errorf("unique index IX_AdminUsers_Email not found: %v", err)
	}
}

// TestCreateSchema_UniqueIndex_SitePoliciesHostPattern_Exists tests that the unique
// index on SitePolicies.HostPattern is created.
func TestCreateSchema_UniqueIndex_SitePoliciesHostPattern_Exists(t *testing.T) {
	db := openTestDB(t)

	var name string
	err := db.Unwrap().QueryRow(
		"SELECT name FROM sqlite_master WHERE type='index' AND name='IX_SitePolicies_HostPattern'",
	).Scan(&name)
	if err != nil {
		t.Errorf("unique index IX_SitePolicies_HostPattern not found: %v", err)
	}
}

// TestCreateSchema_CalledTwice_NoError tests that CreateSchema is idempotent —
// calling it twice on an existing database does not fail.
func TestCreateSchema_CalledTwice_NoError(t *testing.T) {
	db := openTestDB(t)

	// CreateSchema is already called by openTestDB -> Connect, so calling it
	// again should succeed without error.
	err := CreateSchema(db)
	if err != nil {
		t.Errorf("second CreateSchema call failed: %v", err)
	}
}

// === Unique constraints tests ===

// TestAdminUsers_DuplicateEmail_Fails tests that inserting two AdminUsers rows
// with the same Email violates the unique index and fails.
func TestAdminUsers_DuplicateEmail_Fails(t *testing.T) {
	db := openTestDB(t)

	// Insert the first user
	_, err := db.Unwrap().Exec(
		"INSERT INTO AdminUsers (Email, PasswordHash, CreatedAt) VALUES (?, ?, ?)",
		"admin@example.com", "hash1", time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("first insert failed: %v", err)
	}

	// Attempt to insert a second user with the same email
	_, err = db.Unwrap().Exec(
		"INSERT INTO AdminUsers (Email, PasswordHash, CreatedAt) VALUES (?, ?, ?)",
		"admin@example.com", "hash2", time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err == nil {
		t.Fatal("expected error for duplicate email, got nil")
	}
}

// TestAdminUsers_DifferentEmails_BothSucceed tests that inserting two AdminUsers
// rows with different emails succeeds.
func TestAdminUsers_DifferentEmails_BothSucceed(t *testing.T) {
	db := openTestDB(t)

	// Insert two users with different emails
	_, err1 := db.Unwrap().Exec(
		"INSERT INTO AdminUsers (Email, PasswordHash, CreatedAt) VALUES (?, ?, ?)",
		"user1@example.com", "hash1", time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err1 != nil {
		t.Fatalf("first insert failed: %v", err1)
	}

	_, err2 := db.Unwrap().Exec(
		"INSERT INTO AdminUsers (Email, PasswordHash, CreatedAt) VALUES (?, ?, ?)",
		"user2@example.com", "hash2", time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err2 != nil {
		t.Fatalf("second insert failed: %v", err2)
	}
}

// TestSitePolicies_DuplicateHostPattern_Fails tests that inserting two SitePolicies
// rows with the same HostPattern violates the unique index and fails.
func TestSitePolicies_DuplicateHostPattern_Fails(t *testing.T) {
	db := openTestDB(t)

	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Insert the first policy
	_, err := db.Unwrap().Exec(
		"INSERT INTO SitePolicies (HostPattern, ViewMode, CreatedAt, UpdatedAt) VALUES (?, ?, ?, ?)",
		"example.com", 0, now, now,
	)
	if err != nil {
		t.Fatalf("first insert failed: %v", err)
	}

	// Attempt to insert a second policy with the same host pattern
	_, err = db.Unwrap().Exec(
		"INSERT INTO SitePolicies (HostPattern, ViewMode, CreatedAt, UpdatedAt) VALUES (?, ?, ?, ?)",
		"example.com", 1, now, now,
	)
	if err == nil {
		t.Fatal("expected error for duplicate HostPattern, got nil")
	}
}

// TestSitePolicies_DifferentHostPatterns_BothSucceed tests that inserting two
// SitePolicies rows with different host patterns succeeds.
func TestSitePolicies_DifferentHostPatterns_BothSucceed(t *testing.T) {
	db := openTestDB(t)

	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Insert two policies with different host patterns
	_, err1 := db.Unwrap().Exec(
		"INSERT INTO SitePolicies (HostPattern, ViewMode, CreatedAt, UpdatedAt) VALUES (?, ?, ?, ?)",
		"example.com", 0, now, now,
	)
	if err1 != nil {
		t.Fatalf("first insert failed: %v", err1)
	}

	_, err2 := db.Unwrap().Exec(
		"INSERT INTO SitePolicies (HostPattern, ViewMode, CreatedAt, UpdatedAt) VALUES (?, ?, ?, ?)",
		"example.org", 1, now, now,
	)
	if err2 != nil {
		t.Fatalf("second insert failed: %v", err2)
	}
}

// === CHECK(Id=1) tests ===

// TestVideoEncoderSettings_InsertId1_Succeeds tests that inserting a row into
// VideoEncoderSettings with Id=1 succeeds.
func TestVideoEncoderSettings_InsertId1_Succeeds(t *testing.T) {
	db := openTestDB(t)

	_, err := db.Unwrap().Exec(
		"INSERT INTO VideoEncoderSettings (Id, Mode, UpdatedAt) VALUES (?, ?, ?)",
		1, 0, time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Errorf("insert with Id=1 failed: %v", err)
	}
}

// TestVideoEncoderSettings_InsertIdNot1_Fails tests that the CHECK(Id=1) constraint
// prevents inserting a row with Id != 1.
func TestVideoEncoderSettings_InsertIdNot1_Fails(t *testing.T) {
	db := openTestDB(t)

	_, err := db.Unwrap().Exec(
		"INSERT INTO VideoEncoderSettings (Id, Mode, UpdatedAt) VALUES (?, ?, ?)",
		2, 0, time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err == nil {
		t.Fatal("expected error for Id != 1, got nil")
	}
}

// TestVideoEncoderSettings_UpsertId1_Succeeds tests that INSERT OR REPLACE with Id=1
// succeeds for VideoEncoderSettings (upsert pattern for single-row table).
func TestVideoEncoderSettings_UpsertId1_Succeeds(t *testing.T) {
	db := openTestDB(t)

	now := time.Now().UTC().Format(time.RFC3339Nano)

	// First upsert (inserts)
	_, err1 := db.Unwrap().Exec(
		"INSERT OR REPLACE INTO VideoEncoderSettings (Id, Mode, UpdatedAt) VALUES (?, ?, ?)",
		1, 0, now,
	)
	if err1 != nil {
		t.Fatalf("first upsert failed: %v", err1)
	}

	// Second upsert (replaces)
	_, err2 := db.Unwrap().Exec(
		"INSERT OR REPLACE INTO VideoEncoderSettings (Id, Mode, UpdatedAt) VALUES (?, ?, ?)",
		1, 1, now,
	)
	if err2 != nil {
		t.Fatalf("second upsert failed: %v", err2)
	}
}

// TestLogLevelSettings_InsertId1_Succeeds tests that inserting a row into
// LogLevelSettings with Id=1 succeeds.
func TestLogLevelSettings_InsertId1_Succeeds(t *testing.T) {
	db := openTestDB(t)

	_, err := db.Unwrap().Exec(
		"INSERT INTO LogLevelSettings (Id, Level, UpdatedAt) VALUES (?, ?, ?)",
		1, "Information", time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Errorf("insert with Id=1 failed: %v", err)
	}
}

// TestLogLevelSettings_InsertIdNot1_Fails tests that the CHECK(Id=1) constraint
// prevents inserting a row with Id != 1.
func TestLogLevelSettings_InsertIdNot1_Fails(t *testing.T) {
	db := openTestDB(t)

	_, err := db.Unwrap().Exec(
		"INSERT INTO LogLevelSettings (Id, Level, UpdatedAt) VALUES (?, ?, ?)",
		2, "Debug", time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err == nil {
		t.Fatal("expected error for Id != 1, got nil")
	}
}

// TestLogLevelSettings_UpsertId1_Succeeds tests that INSERT OR REPLACE with Id=1
// succeeds for LogLevelSettings (upsert pattern for single-row table).
func TestLogLevelSettings_UpsertId1_Succeeds(t *testing.T) {
	db := openTestDB(t)

	now := time.Now().UTC().Format(time.RFC3339Nano)

	// First upsert (inserts)
	_, err1 := db.Unwrap().Exec(
		"INSERT OR REPLACE INTO LogLevelSettings (Id, Level, UpdatedAt) VALUES (?, ?, ?)",
		1, "Information", now,
	)
	if err1 != nil {
		t.Fatalf("first upsert failed: %v", err1)
	}

	// Second upsert (replaces)
	_, err2 := db.Unwrap().Exec(
		"INSERT OR REPLACE INTO LogLevelSettings (Id, Level, UpdatedAt) VALUES (?, ?, ?)",
		1, "Debug", now,
	)
	if err2 != nil {
		t.Fatalf("second upsert failed: %v", err2)
	}
}

// === Smoke insert/read tests ===

// TestAdminUsers_InsertRead tests that a row inserted into AdminUsers can be
// read back with matching values.
func TestAdminUsers_InsertRead(t *testing.T) {
	db := openTestDB(t)

	email := "admin@example.com"
	passwordHash := "bcrypt_hash_here"
	createdAt := time.Now().UTC()

	// Insert
	result, err := db.Unwrap().Exec(
		"INSERT INTO AdminUsers (Email, PasswordHash, CreatedAt) VALUES (?, ?, ?)",
		email, passwordHash, createdAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId failed: %v", err)
	}

	// Read
	var readEmail, readPasswordHash string
	var readCreatedAtStr string
	err = db.Unwrap().QueryRow(
		"SELECT Email, PasswordHash, CreatedAt FROM AdminUsers WHERE Id = ?",
		id,
	).Scan(&readEmail, &readPasswordHash, &readCreatedAtStr)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if readEmail != email {
		t.Errorf("Email: expected %q, got %q", email, readEmail)
	}
	if readPasswordHash != passwordHash {
		t.Errorf("PasswordHash: expected %q, got %q", passwordHash, readPasswordHash)
	}
	if readCreatedAtStr != createdAt.Format(time.RFC3339Nano) {
		t.Errorf("CreatedAt: expected %q, got %q", createdAt.Format(time.RFC3339Nano), readCreatedAtStr)
	}
}

// TestSitePolicies_InsertRead tests that a row inserted into SitePolicies can be
// read back with matching values.
func TestSitePolicies_InsertRead(t *testing.T) {
	db := openTestDB(t)

	hostPattern := "example.com"
	viewMode := ViewModeHtmlAllowInput
	createdAt := time.Now().UTC()
	updatedAt := time.Now().UTC()

	// Insert
	result, err := db.Unwrap().Exec(
		"INSERT INTO SitePolicies (HostPattern, ViewMode, CreatedAt, UpdatedAt) VALUES (?, ?, ?, ?)",
		hostPattern, viewMode, createdAt.Format(time.RFC3339Nano), updatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId failed: %v", err)
	}

	// Read
	var readHostPattern string
	var readViewMode int
	var readCreatedAtStr, readUpdatedAtStr string
	err = db.Unwrap().QueryRow(
		"SELECT HostPattern, ViewMode, CreatedAt, UpdatedAt FROM SitePolicies WHERE Id = ?",
		id,
	).Scan(&readHostPattern, &readViewMode, &readCreatedAtStr, &readUpdatedAtStr)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if readHostPattern != hostPattern {
		t.Errorf("HostPattern: expected %q, got %q", hostPattern, readHostPattern)
	}
	if readViewMode != int(viewMode) {
		t.Errorf("ViewMode: expected %d, got %d", viewMode, readViewMode)
	}
	if readCreatedAtStr != createdAt.Format(time.RFC3339Nano) {
		t.Errorf("CreatedAt mismatch")
	}
	if readUpdatedAtStr != updatedAt.Format(time.RFC3339Nano) {
		t.Errorf("UpdatedAt mismatch")
	}
}

// TestRequestLogs_InsertRead_WithClientIp tests that a RequestLog row with a
// non-null ClientIp is inserted and read back correctly.
func TestRequestLogs_InsertRead_WithClientIp(t *testing.T) {
	db := openTestDB(t)

	timestamp := time.Now().UTC()
	url := "https://example.com/path"
	host := "example.com"
	decision := "HtmlAllowInput"
	allowed := true
	clientIp := "192.168.1.1"

	// Insert
	result, err := db.Unwrap().Exec(
		"INSERT INTO RequestLogs (Timestamp, Url, Host, Decision, Allowed, ClientIp) VALUES (?, ?, ?, ?, ?, ?)",
		timestamp.Format(time.RFC3339Nano), url, host, decision, boolToInt(allowed), clientIp,
	)
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId failed: %v", err)
	}

	// Read
	var readTimestampStr, readUrl, readHost, readDecision string
	var readAllowed int
	var readClientIp string
	err = db.Unwrap().QueryRow(
		"SELECT Timestamp, Url, Host, Decision, Allowed, ClientIp FROM RequestLogs WHERE Id = ?",
		id,
	).Scan(&readTimestampStr, &readUrl, &readHost, &readDecision, &readAllowed, &readClientIp)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if readUrl != url {
		t.Errorf("Url: expected %q, got %q", url, readUrl)
	}
	if readHost != host {
		t.Errorf("Host: expected %q, got %q", host, readHost)
	}
	if readDecision != decision {
		t.Errorf("Decision: expected %q, got %q", decision, readDecision)
	}
	if readAllowed != boolToInt(allowed) {
		t.Errorf("Allowed: expected %d, got %d", boolToInt(allowed), readAllowed)
	}
	if readClientIp != clientIp {
		t.Errorf("ClientIp: expected %q, got %q", clientIp, readClientIp)
	}
}

// TestRequestLogs_InsertRead_NullClientIp tests that a RequestLog row with a
// NULL ClientIp is inserted and read back correctly.
func TestRequestLogs_InsertRead_NullClientIp(t *testing.T) {
	db := openTestDB(t)

	timestamp := time.Now().UTC()
	url := "https://example.com/path"
	host := "example.com"
	decision := "deny"
	allowed := false

	// Insert with NULL ClientIp (no value provided, relying on NULL default)
	result, err := db.Unwrap().Exec(
		"INSERT INTO RequestLogs (Timestamp, Url, Host, Decision, Allowed, ClientIp) VALUES (?, ?, ?, ?, ?, NULL)",
		timestamp.Format(time.RFC3339Nano), url, host, decision, boolToInt(allowed),
	)
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId failed: %v", err)
	}

	// Read
	var readUrl, readHost, readDecision string
	var readAllowed int
	var readClientIp *string // Use pointer to detect NULL
	err = db.Unwrap().QueryRow(
		"SELECT Url, Host, Decision, Allowed, ClientIp FROM RequestLogs WHERE Id = ?",
		id,
	).Scan(&readUrl, &readHost, &readDecision, &readAllowed, &readClientIp)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if readClientIp != nil {
		t.Errorf("ClientIp: expected NULL, got %q", *readClientIp)
	}
}

// TestRootCertificateAuthorities_InsertRead tests that a row inserted into
// RootCertificateAuthorities can be read back with matching values.
func TestRootCertificateAuthorities_InsertRead(t *testing.T) {
	db := openTestDB(t)

	subject := "CN=Test CA"
	notBefore := time.Now().UTC()
	notAfter := time.Now().UTC().Add(365 * 24 * time.Hour)
	thumbprint := "abcdef1234567890"
	uploadedAt := time.Now().UTC()
	pfxBytes := []byte{0x30, 0x82, 0x01, 0x00} // dummy PKCS#12 data
	pfxPassword := "secret"

	// Insert
	result, err := db.Unwrap().Exec(
		"INSERT INTO RootCertificateAuthorities (Subject, NotBefore, NotAfter, Thumbprint, UploadedAt, PfxBytes, PfxPassword) VALUES (?, ?, ?, ?, ?, ?, ?)",
		subject,
		notBefore.Format(time.RFC3339Nano),
		notAfter.Format(time.RFC3339Nano),
		thumbprint,
		uploadedAt.Format(time.RFC3339Nano),
		pfxBytes,
		pfxPassword,
	)
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId failed: %v", err)
	}

	// Read
	var readSubject, readThumbprint, readPfxPassword string
	var readNotBeforeStr, readNotAfterStr, readUploadedAtStr string
	var readPfxBytes []byte
	err = db.Unwrap().QueryRow(
		"SELECT Subject, NotBefore, NotAfter, Thumbprint, UploadedAt, PfxBytes, PfxPassword FROM RootCertificateAuthorities WHERE Id = ?",
		id,
	).Scan(&readSubject, &readNotBeforeStr, &readNotAfterStr, &readThumbprint, &readUploadedAtStr, &readPfxBytes, &readPfxPassword)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if readSubject != subject {
		t.Errorf("Subject: expected %q, got %q", subject, readSubject)
	}
	if readThumbprint != thumbprint {
		t.Errorf("Thumbprint: expected %q, got %q", thumbprint, readThumbprint)
	}
	if readPfxPassword != pfxPassword {
		t.Errorf("PfxPassword: expected %q, got %q", pfxPassword, readPfxPassword)
	}
	if len(readPfxBytes) != len(pfxBytes) {
		t.Errorf("PfxBytes length: expected %d, got %d", len(pfxBytes), len(readPfxBytes))
	}
}

// TestVideoEncoderSettings_InsertRead tests that a row inserted into
// VideoEncoderSettings can be read back with matching values.
func TestVideoEncoderSettings_InsertRead(t *testing.T) {
	db := openTestDB(t)

	mode := VideoEncoderModeCpu
	updatedAt := time.Now().UTC()

	// Insert with Id=1
	_, err := db.Unwrap().Exec(
		"INSERT INTO VideoEncoderSettings (Id, Mode, UpdatedAt) VALUES (?, ?, ?)",
		1, int(mode), updatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// Read
	var readId int64
	var readMode int
	var readUpdatedAtStr string
	err = db.Unwrap().QueryRow(
		"SELECT Id, Mode, UpdatedAt FROM VideoEncoderSettings WHERE Id = 1",
	).Scan(&readId, &readMode, &readUpdatedAtStr)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if readId != 1 {
		t.Errorf("Id: expected 1, got %d", readId)
	}
	if readMode != int(mode) {
		t.Errorf("Mode: expected %d, got %d", mode, readMode)
	}
	if readUpdatedAtStr != updatedAt.Format(time.RFC3339Nano) {
		t.Errorf("UpdatedAt mismatch")
	}
}

// TestLogLevelSettings_InsertRead tests that a row inserted into LogLevelSettings
// can be read back with matching values.
func TestLogLevelSettings_InsertRead(t *testing.T) {
	db := openTestDB(t)

	level := "Debug"
	updatedAt := time.Now().UTC()

	// Insert with Id=1
	_, err := db.Unwrap().Exec(
		"INSERT INTO LogLevelSettings (Id, Level, UpdatedAt) VALUES (?, ?, ?)",
		1, level, updatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// Read
	var readId int64
	var readLevel string
	var readUpdatedAtStr string
	err = db.Unwrap().QueryRow(
		"SELECT Id, Level, UpdatedAt FROM LogLevelSettings WHERE Id = 1",
	).Scan(&readId, &readLevel, &readUpdatedAtStr)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if readId != 1 {
		t.Errorf("Id: expected 1, got %d", readId)
	}
	if readLevel != level {
		t.Errorf("Level: expected %q, got %q", level, readLevel)
	}
	if readUpdatedAtStr != updatedAt.Format(time.RFC3339Nano) {
		t.Errorf("UpdatedAt mismatch")
	}
}

// boolToInt converts a boolean to 0 or 1 for SQLite storage.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
