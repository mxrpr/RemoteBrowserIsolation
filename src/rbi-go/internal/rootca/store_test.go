package rootca

import (
	"crypto/x509"
	"testing"

	"rbi-go/internal/db"
)

// TestParsePKCS12_ValidCAPFX_ReturnsCertAndKey verifies that a valid CA PFX is parsed correctly.
func TestParsePKCS12_ValidCAPFX_ReturnsCertAndKey(t *testing.T) {
	pfxBytes, expectedCert := makeCAPFX(t, "testpass")

	cert, key, err := ParsePKCS12(pfxBytes, "testpass")

	if err != nil {
		t.Fatalf("ParsePKCS12 failed: %v", err)
	}
	if cert == nil {
		t.Error("expected cert, got nil")
	}
	if key == nil {
		t.Error("expected key, got nil")
	}
	if cert.Subject.CommonName != expectedCert.Subject.CommonName {
		t.Errorf("expected CN %q, got %q", expectedCert.Subject.CommonName, cert.Subject.CommonName)
	}
}

// TestParsePKCS12_ValidLeafPFX_ReturnsCertWithKey verifies that a valid leaf PFX is parsed correctly.
func TestParsePKCS12_ValidLeafPFX_ReturnsCertWithKey(t *testing.T) {
	pfxBytes, _ := makeLeafPFX(t, "testpass")

	cert, key, err := ParsePKCS12(pfxBytes, "testpass")

	if err != nil {
		t.Fatalf("ParsePKCS12 failed: %v", err)
	}
	if cert == nil {
		t.Error("expected cert, got nil")
	}
	if key == nil {
		t.Error("expected key, got nil")
	}
	if cert.IsCA {
		t.Error("expected leaf cert (IsCA=false), got CA cert")
	}
}

// TestParsePKCS12_WrongPassword_ReturnsError verifies that wrong password causes error.
func TestParsePKCS12_WrongPassword_ReturnsError(t *testing.T) {
	pfxBytes, _ := makeCAPFX(t, "correctpass")

	_, _, err := ParsePKCS12(pfxBytes, "wrongpass")

	if err == nil {
		t.Error("expected error for wrong password, got nil")
	}
}

// TestParsePKCS12_MalformedBytes_ReturnsError verifies that malformed bytes cause error.
func TestParsePKCS12_MalformedBytes_ReturnsError(t *testing.T) {
	malformed := []byte("not a valid pfx file")

	_, _, err := ParsePKCS12(malformed, "anypass")

	if err == nil {
		t.Error("expected error for malformed bytes, got nil")
	}
}

// TestParsePKCS12_EmptyBytes_ReturnsError verifies that empty bytes cause error.
func TestParsePKCS12_EmptyBytes_ReturnsError(t *testing.T) {
	_, _, err := ParsePKCS12([]byte{}, "anypass")

	if err == nil {
		t.Error("expected error for empty bytes, got nil")
	}
}

// TestComputeThumbprint_IsUppercaseHex40Chars verifies that thumbprint is 40 uppercase hex chars.
func TestComputeThumbprint_IsUppercaseHex40Chars(t *testing.T) {
	_, cert := makeCAPFX(t, "pass")

	thumbprint := computeThumbprint(cert)

	if len(thumbprint) != 40 {
		t.Errorf("expected 40 chars, got %d", len(thumbprint))
	}
	for _, ch := range thumbprint {
		if !((ch >= '0' && ch <= '9') || (ch >= 'A' && ch <= 'F')) {
			t.Errorf("expected uppercase hex, got %c", ch)
		}
	}
}

// TestComputeThumbprint_IsDeterministic verifies that same cert gives same thumbprint.
func TestComputeThumbprint_IsDeterministic(t *testing.T) {
	_, cert := makeCAPFX(t, "pass")

	thumb1 := computeThumbprint(cert)
	thumb2 := computeThumbprint(cert)

	if thumb1 != thumb2 {
		t.Errorf("expected deterministic thumbprint, got %s != %s", thumb1, thumb2)
	}
}

// TestComputeThumbprint_DiffersForDifferentCerts verifies that different certs have different thumbprints.
func TestComputeThumbprint_DiffersForDifferentCerts(t *testing.T) {
	_, cert1 := makeCAPFX(t, "pass1")
	_, cert2 := makeCAPFX(t, "pass2")

	thumb1 := computeThumbprint(cert1)
	thumb2 := computeThumbprint(cert2)

	if thumb1 == thumb2 {
		t.Error("expected different thumbprints for different certs, got same")
	}
}

// TestStore_GetActiveCA_EmptyDB_ReturnsNil verifies that empty DB returns nil CA.
func TestStore_GetActiveCA_EmptyDB_ReturnsNil(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())

	ca, err := store.GetActiveCA()

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if ca != nil {
		t.Error("expected nil CA for empty DB, got non-nil")
	}
}

// TestStore_GetActiveCA_AfterSave_ReturnsParsedCA verifies that after Save, GetActiveCA returns the CA.
func TestStore_GetActiveCA_AfterSave_ReturnsParsedCA(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, expectedCert := makeCAPFX(t, "testpass")

	// Save the CA
	_, err = store.Save(pfxBytes, "testpass", expectedCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Retrieve it
	ca, err := store.GetActiveCA()

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if ca == nil {
		t.Fatal("expected non-nil CA, got nil")
	}
	if ca.Cert == nil {
		t.Error("expected non-nil Cert, got nil")
	}
	if ca.Key == nil {
		t.Error("expected non-nil Key, got nil")
	}
	if ca.Cert.Subject.CommonName != expectedCert.Subject.CommonName {
		t.Errorf("expected CN %q, got %q", expectedCert.Subject.CommonName, ca.Cert.Subject.CommonName)
	}
}

// TestStore_GetActiveCA_CacheReuse_SkipsDB verifies that second GetActiveCA uses cache.
func TestStore_GetActiveCA_CacheReuse_SkipsDB(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, expectedCert := makeCAPFX(t, "testpass")

	_, err = store.Save(pfxBytes, "testpass", expectedCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// First call loads from DB
	ca1, err := store.GetActiveCA()
	if err != nil {
		t.Fatalf("first GetActiveCA failed: %v", err)
	}

	// Second call should use cache (same pointer)
	ca2, err := store.GetActiveCA()
	if err != nil {
		t.Fatalf("second GetActiveCA failed: %v", err)
	}

	if ca1 != ca2 {
		t.Error("expected cached CA to return same pointer, got different pointers")
	}
}

// TestStore_GetActiveCA_AfterInvalidate_ReloadsFromDB verifies that Invalidate clears cache.
func TestStore_GetActiveCA_AfterInvalidate_ReloadsFromDB(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, expectedCert := makeCAPFX(t, "testpass")

	_, err = store.Save(pfxBytes, "testpass", expectedCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load into cache
	ca1, err := store.GetActiveCA()
	if err != nil {
		t.Fatalf("first GetActiveCA failed: %v", err)
	}
	if ca1 == nil {
		t.Fatal("first CA should not be nil")
	}

	// Invalidate cache
	store.Invalidate()

	// Load again (should be different pointer even though same data)
	ca2, err := store.GetActiveCA()
	if err != nil {
		t.Fatalf("second GetActiveCA failed: %v", err)
	}

	// Both should be non-nil, but after Invalidate, it reloads from DB
	if ca2 == nil {
		t.Fatal("second CA should not be nil")
	}
	if ca1 == ca2 {
		t.Error("expected different pointer after Invalidate, got same")
	}
}

// TestStore_GetMetadata_EmptyDB_ReturnsNilNil verifies that empty DB returns nil metadata.
func TestStore_GetMetadata_EmptyDB_ReturnsNilNil(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())

	row, err := store.GetMetadata()

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if row != nil {
		t.Error("expected nil row for empty DB, got non-nil")
	}
}

// TestStore_GetMetadata_WithCA_ReturnsCorrectFields verifies that GetMetadata returns correct fields.
func TestStore_GetMetadata_WithCA_ReturnsCorrectFields(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, expectedCert := makeCAPFX(t, "testpass")

	savedRow, err := store.Save(pfxBytes, "testpass", expectedCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	row, err := store.GetMetadata()

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if row == nil {
		t.Fatal("expected non-nil row, got nil")
	}
	if row.Id != savedRow.Id {
		t.Errorf("expected Id %d, got %d", savedRow.Id, row.Id)
	}
	if row.Subject != savedRow.Subject {
		t.Errorf("expected Subject %q, got %q", savedRow.Subject, row.Subject)
	}
	if row.Thumbprint != savedRow.Thumbprint {
		t.Errorf("expected Thumbprint %q, got %q", savedRow.Thumbprint, row.Thumbprint)
	}
}

// TestStore_GetMetadata_ReturnsLatestRow verifies that GetMetadata returns latest row after multiple saves.
func TestStore_GetMetadata_ReturnsLatestRow(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes1, cert1 := makeCAPFX(t, "pass1")
	pfxBytes2, cert2 := makeCAPFX(t, "pass2")

	// Save first CA
	row1, err := store.Save(pfxBytes1, "pass1", cert1)
	if err != nil {
		t.Fatalf("first Save failed: %v", err)
	}

	// Save second CA (should replace first)
	row2, err := store.Save(pfxBytes2, "pass2", cert2)
	if err != nil {
		t.Fatalf("second Save failed: %v", err)
	}

	// GetMetadata should return second
	row, err := store.GetMetadata()

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if row == nil {
		t.Fatal("expected non-nil row, got nil")
	}
	if row.Id != row2.Id {
		t.Errorf("expected latest row Id %d, got %d", row2.Id, row.Id)
	}
	if row.Subject != row2.Subject {
		t.Errorf("expected latest Subject %q, got %q", row2.Subject, row.Subject)
	}
	// First row should be gone
	if row.Thumbprint == row1.Thumbprint {
		t.Error("expected new CA, got old one")
	}
}

// TestStore_GetCertDER_EmptyDB_ReturnsNilNil verifies that empty DB returns nil cert DER.
func TestStore_GetCertDER_EmptyDB_ReturnsNilNil(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())

	derBytes, err := store.GetCertDER()

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if derBytes != nil {
		t.Error("expected nil DER for empty DB, got non-nil")
	}
}

// TestStore_GetCertDER_WithCA_ReturnsParsableBytes verifies that GetCertDER returns parseable DER.
func TestStore_GetCertDER_WithCA_ReturnsParsableBytes(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, expectedCert := makeCAPFX(t, "testpass")

	_, err = store.Save(pfxBytes, "testpass", expectedCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	derBytes, err := store.GetCertDER()

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if derBytes == nil {
		t.Fatal("expected non-nil DER, got nil")
	}

	// Verify DER is parseable
	parsedCert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		t.Fatalf("DER not parseable: %v", err)
	}
	if parsedCert.Subject.CommonName != expectedCert.Subject.CommonName {
		t.Errorf("expected CN %q, got %q", expectedCert.Subject.CommonName, parsedCert.Subject.CommonName)
	}
}

// TestStore_Save_HappyPath_Returns_CorrectRow verifies that Save returns correct row.
func TestStore_Save_HappyPath_Returns_CorrectRow(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, cert := makeCAPFX(t, "testpass")

	row, err := store.Save(pfxBytes, "testpass", cert)

	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if row == nil {
		t.Fatal("expected non-nil row, got nil")
	}
	if row.Id == 0 {
		t.Error("expected non-zero Id")
	}
	if row.Subject != cert.Subject.String() {
		t.Errorf("expected Subject %q, got %q", cert.Subject.String(), row.Subject)
	}
	if row.Thumbprint == "" {
		t.Error("expected non-empty Thumbprint")
	}
}

// TestStore_Save_ReplaceSemantics_OldRowDeleted verifies that Save replaces old row.
func TestStore_Save_ReplaceSemantics_OldRowDeleted(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes1, cert1 := makeCAPFX(t, "pass1")
	pfxBytes2, cert2 := makeCAPFX(t, "pass2")

	// Save first CA
	row1, err := store.Save(pfxBytes1, "pass1", cert1)
	if err != nil {
		t.Fatalf("first Save failed: %v", err)
	}

	// Save second CA
	row2, err := store.Save(pfxBytes2, "pass2", cert2)
	if err != nil {
		t.Fatalf("second Save failed: %v", err)
	}

	// Only second row should exist
	meta, err := store.GetMetadata()
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta == nil {
		t.Fatal("expected non-nil metadata, got nil")
	}
	if meta.Id != row2.Id {
		t.Errorf("expected only second row, got Id %d (first was %d)", meta.Id, row1.Id)
	}
}

// TestStore_Save_InvalidatesCache verifies that Save invalidates cache.
func TestStore_Save_InvalidatesCache(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes1, cert1 := makeCAPFX(t, "pass1")
	pfxBytes2, cert2 := makeCAPFX(t, "pass2")

	// Save and load first CA
	_, err = store.Save(pfxBytes1, "pass1", cert1)
	if err != nil {
		t.Fatalf("first Save failed: %v", err)
	}
	ca1, err := store.GetActiveCA()
	if err != nil {
		t.Fatalf("first GetActiveCA failed: %v", err)
	}
	if ca1 == nil {
		t.Fatal("ca1 should not be nil")
	}

	// Save second CA (should invalidate cache)
	_, err = store.Save(pfxBytes2, "pass2", cert2)
	if err != nil {
		t.Fatalf("second Save failed: %v", err)
	}

	// GetActiveCA should load new CA (different pointer)
	ca2, err := store.GetActiveCA()
	if err != nil {
		t.Fatalf("second GetActiveCA failed: %v", err)
	}
	if ca2 == nil {
		t.Fatal("ca2 should not be nil")
	}

	if ca1 == ca2 {
		t.Error("expected different CA after Save, got same pointer")
	}
}

// TestStore_Delete_EmptyDB_Succeeds verifies that Delete on empty DB succeeds.
func TestStore_Delete_EmptyDB_Succeeds(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())

	err = store.Delete()

	if err != nil {
		t.Errorf("expected no error on empty DB, got %v", err)
	}
}

// TestStore_Delete_WithCA_RemovesRow verifies that Delete removes CA row.
func TestStore_Delete_WithCA_RemovesRow(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, cert := makeCAPFX(t, "testpass")

	// Save CA
	_, err = store.Save(pfxBytes, "testpass", cert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify it exists
	meta, err := store.GetMetadata()
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta == nil {
		t.Fatal("expected CA to exist before delete")
	}

	// Delete it
	err = store.Delete()
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify it's gone
	meta, err = store.GetMetadata()
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta != nil {
		t.Error("expected CA to be deleted, but it still exists")
	}
}

// TestStore_Delete_InvalidatesCache verifies that Delete invalidates cache.
func TestStore_Delete_InvalidatesCache(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, cert := makeCAPFX(t, "testpass")

	// Save and load CA
	_, err = store.Save(pfxBytes, "testpass", cert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	ca1, err := store.GetActiveCA()
	if err != nil {
		t.Fatalf("first GetActiveCA failed: %v", err)
	}
	if ca1 == nil {
		t.Fatal("ca1 should not be nil")
	}

	// Delete CA
	err = store.Delete()
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// GetActiveCA should return nil (cache invalidated)
	ca2, err := store.GetActiveCA()
	if err != nil {
		t.Fatalf("second GetActiveCA failed: %v", err)
	}
	if ca2 != nil {
		t.Error("expected nil CA after delete, got non-nil")
	}
}

// TestStore_Invalidate_ForcesReload verifies that Invalidate clears cache.
func TestStore_Invalidate_ForcesReload(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, cert := makeCAPFX(t, "testpass")

	// Save and load CA
	_, err = store.Save(pfxBytes, "testpass", cert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	ca1, err := store.GetActiveCA()
	if err != nil {
		t.Fatalf("first GetActiveCA failed: %v", err)
	}

	// Invalidate cache
	store.Invalidate()

	// Next GetActiveCA should reload (different pointer)
	ca2, err := store.GetActiveCA()
	if err != nil {
		t.Fatalf("second GetActiveCA failed: %v", err)
	}

	if ca1 == ca2 {
		t.Error("expected different pointer after Invalidate, got same")
	}
	// But the data should be equivalent
	if ca2 == nil {
		t.Fatal("expected non-nil ca2 after reload")
	}
}
