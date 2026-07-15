package rootca

import (
	"crypto/x509"
	"testing"
	"time"

	"rbi-go/internal/db"
)

// TestMinter_GetOrMint_NoCA_ReturnsNil verifies that GetOrMint returns nil when no CA exists.
func TestMinter_GetOrMint_NoCA_ReturnsNil(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	minter := NewMinter(store)

	cert, err := minter.GetOrMint("example.com")

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if cert != nil {
		t.Error("expected nil cert when no CA, got non-nil")
	}
}

// TestMinter_GetOrMint_MintsLeaf_HappyPath verifies that GetOrMint mints a leaf cert.
func TestMinter_GetOrMint_MintsLeaf_HappyPath(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, caCert := makeCAPFX(t, "testpass")
	_, err = store.Save(pfxBytes, "testpass", caCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	minter := NewMinter(store)
	cert, err := minter.GetOrMint("example.com")

	if err != nil {
		t.Fatalf("GetOrMint failed: %v", err)
	}
	if cert == nil {
		t.Fatal("expected non-nil cert, got nil")
	}
	if cert.Leaf == nil {
		t.Error("expected non-nil Leaf, got nil")
	}
	if cert.PrivateKey == nil {
		t.Error("expected non-nil PrivateKey, got nil")
	}
	if len(cert.Certificate) == 0 {
		t.Error("expected non-empty Certificate chain")
	}
}

// TestMinter_GetOrMint_SANContainsHostname verifies that SAN contains the hostname.
func TestMinter_GetOrMint_SANContainsHostname(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, caCert := makeCAPFX(t, "testpass")
	_, err = store.Save(pfxBytes, "testpass", caCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	minter := NewMinter(store)
	hostname := "subdomain.example.com"
	cert, err := minter.GetOrMint(hostname)

	if err != nil {
		t.Fatalf("GetOrMint failed: %v", err)
	}
	if cert == nil || cert.Leaf == nil {
		t.Fatal("expected non-nil cert")
	}

	// Check SAN
	found := false
	for _, san := range cert.Leaf.DNSNames {
		if san == hostname {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected hostname %q in SAN, got %v", hostname, cert.Leaf.DNSNames)
	}
}

// TestMinter_GetOrMint_CNMatchesHostname verifies that CN matches hostname.
func TestMinter_GetOrMint_CNMatchesHostname(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, caCert := makeCAPFX(t, "testpass")
	_, err = store.Save(pfxBytes, "testpass", caCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	minter := NewMinter(store)
	hostname := "example.com"
	cert, err := minter.GetOrMint(hostname)

	if err != nil {
		t.Fatalf("GetOrMint failed: %v", err)
	}
	if cert == nil || cert.Leaf == nil {
		t.Fatal("expected non-nil cert")
	}

	if cert.Leaf.Subject.CommonName != hostname {
		t.Errorf("expected CN %q, got %q", hostname, cert.Leaf.Subject.CommonName)
	}
}

// TestMinter_GetOrMint_IsCA_False verifies that leaf cert has IsCA=false.
func TestMinter_GetOrMint_IsCA_False(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, caCert := makeCAPFX(t, "testpass")
	_, err = store.Save(pfxBytes, "testpass", caCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	minter := NewMinter(store)
	cert, err := minter.GetOrMint("example.com")

	if err != nil {
		t.Fatalf("GetOrMint failed: %v", err)
	}
	if cert == nil || cert.Leaf == nil {
		t.Fatal("expected non-nil cert")
	}

	if cert.Leaf.IsCA {
		t.Error("expected leaf cert IsCA=false, got true")
	}
}

// TestMinter_GetOrMint_LeafVerifiesAgainstCAPool verifies that leaf verifies against CA.
func TestMinter_GetOrMint_LeafVerifiesAgainstCAPool(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, caCert := makeCAPFX(t, "testpass")
	_, err = store.Save(pfxBytes, "testpass", caCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	minter := NewMinter(store)
	cert, err := minter.GetOrMint("example.com")

	if err != nil {
		t.Fatalf("GetOrMint failed: %v", err)
	}
	if cert == nil || cert.Leaf == nil {
		t.Fatal("expected non-nil cert")
	}

	// Create a CAPool with just the CA cert
	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	// Verify the leaf against the pool
	_, err = cert.Leaf.Verify(x509.VerifyOptions{
		Roots: pool,
		DNSName: "example.com",
	})

	if err != nil {
		t.Errorf("expected leaf to verify against CA, got error: %v", err)
	}
}

// TestMinter_GetOrMint_CacheReuse_SecondCallSamePointer verifies cache reuse.
func TestMinter_GetOrMint_CacheReuse_SecondCallSamePointer(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, caCert := makeCAPFX(t, "testpass")
	_, err = store.Save(pfxBytes, "testpass", caCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	minter := NewMinter(store)
	hostname := "example.com"

	// First call
	cert1, err := minter.GetOrMint(hostname)
	if err != nil {
		t.Fatalf("first GetOrMint failed: %v", err)
	}

	// Second call
	cert2, err := minter.GetOrMint(hostname)
	if err != nil {
		t.Fatalf("second GetOrMint failed: %v", err)
	}

	// Should be same pointer (cached)
	if cert1 != cert2 {
		t.Error("expected cached cert to return same pointer, got different pointers")
	}
}

// TestMinter_GetOrMint_NotAfterClampedToCA verifies that NotAfter is clamped to CA's NotAfter.
func TestMinter_GetOrMint_NotAfterClampedToCA(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	// Create a very short-lived CA (expires in 1 hour)
	pfxBytes, caCert := makeShortLivedCAPFX(t, 1*time.Hour, "testpass")
	_, err = store.Save(pfxBytes, "testpass", caCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	minter := NewMinter(store)
	cert, err := minter.GetOrMint("example.com")

	if err != nil {
		t.Fatalf("GetOrMint failed: %v", err)
	}
	if cert == nil || cert.Leaf == nil {
		t.Fatal("expected non-nil cert")
	}

	// Leaf NotAfter should not be after CA NotAfter
	if cert.Leaf.NotAfter.After(caCert.NotAfter) {
		t.Errorf("leaf NotAfter %v should not be after CA NotAfter %v", cert.Leaf.NotAfter, caCert.NotAfter)
	}
}

// TestMinter_GetOrMint_NotAfter_NormalCA_SevenDaysOut verifies that normal CA mints 7-day leaves.
func TestMinter_GetOrMint_NotAfter_NormalCA_SevenDaysOut(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, caCert := makeCAPFX(t, "testpass") // 30-day CA
	_, err = store.Save(pfxBytes, "testpass", caCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	minter := NewMinter(store)
	cert, err := minter.GetOrMint("example.com")

	if err != nil {
		t.Fatalf("GetOrMint failed: %v", err)
	}
	if cert == nil || cert.Leaf == nil {
		t.Fatal("expected non-nil cert")
	}

	// Leaf should be valid for approximately 7 days (leafValidity constant)
	expectedValidity := 7 * 24 * time.Hour
	actualValidity := cert.Leaf.NotAfter.Sub(cert.Leaf.NotBefore)

	// Allow 1 minute tolerance
	tolerance := 1 * time.Minute
	if actualValidity < expectedValidity-tolerance || actualValidity > expectedValidity+tolerance {
		t.Errorf("expected validity ~%v, got %v", expectedValidity, actualValidity)
	}
}

// TestMinter_GetOrMint_NotBefore_SlightlyInPast verifies that NotBefore is slightly in the past.
func TestMinter_GetOrMint_NotBefore_SlightlyInPast(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, caCert := makeCAPFX(t, "testpass")
	_, err = store.Save(pfxBytes, "testpass", caCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	now := time.Now().UTC()
	minter := NewMinter(store)
	cert, err := minter.GetOrMint("example.com")

	if err != nil {
		t.Fatalf("GetOrMint failed: %v", err)
	}
	if cert == nil || cert.Leaf == nil {
		t.Fatal("expected non-nil cert")
	}

	// NotBefore should be 5 minutes before now (see minter.go)
	expectedNotBefore := now.Add(-5 * time.Minute)
	timeDiff := cert.Leaf.NotBefore.Sub(expectedNotBefore).Abs()

	// Allow 10 second tolerance
	tolerance := 10 * time.Second
	if timeDiff > tolerance {
		t.Errorf("expected NotBefore ~%v, got %v (diff: %v)", expectedNotBefore, cert.Leaf.NotBefore, timeDiff)
	}
}

// TestMinter_GetOrMint_DifferentHostnames_MintSeparateLeaves verifies different hostnames mint different certs.
func TestMinter_GetOrMint_DifferentHostnames_MintSeparateLeaves(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, caCert := makeCAPFX(t, "testpass")
	_, err = store.Save(pfxBytes, "testpass", caCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	minter := NewMinter(store)

	cert1, err := minter.GetOrMint("example.com")
	if err != nil {
		t.Fatalf("first GetOrMint failed: %v", err)
	}

	cert2, err := minter.GetOrMint("other.com")
	if err != nil {
		t.Fatalf("second GetOrMint failed: %v", err)
	}

	if cert1 == cert2 {
		t.Error("expected different certs for different hostnames, got same pointer")
	}
	if cert1.Leaf.Subject.CommonName == cert2.Leaf.Subject.CommonName {
		t.Error("expected different CNs for different hostnames, got same")
	}
}

// TestMinter_ClearCache_ForcesReMint verifies that ClearCache forces re-minting.
func TestMinter_ClearCache_ForcesReMint(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, caCert := makeCAPFX(t, "testpass")
	_, err = store.Save(pfxBytes, "testpass", caCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	minter := NewMinter(store)
	hostname := "example.com"

	// First call
	cert1, err := minter.GetOrMint(hostname)
	if err != nil {
		t.Fatalf("first GetOrMint failed: %v", err)
	}

	// Clear cache
	minter.ClearCache()

	// Second call should re-mint (different pointer)
	cert2, err := minter.GetOrMint(hostname)
	if err != nil {
		t.Fatalf("second GetOrMint failed: %v", err)
	}

	if cert1 == cert2 {
		t.Error("expected different cert after ClearCache, got same pointer")
	}
}

// TestMinter_ClearCache_EmptyCache_NoPanic verifies that ClearCache on empty cache doesn't panic.
func TestMinter_ClearCache_EmptyCache_NoPanic(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	minter := NewMinter(store)

	// Should not panic
	minter.ClearCache()
}

// TestIsNearExpiry_NilLeaf_ReturnsTrue verifies that nil Leaf returns true.
func TestIsNearExpiry_NilLeaf_ReturnsTrue(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, caCert := makeCAPFX(t, "testpass")
	_, err = store.Save(pfxBytes, "testpass", caCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	minter := NewMinter(store)
	cert, err := minter.GetOrMint("example.com")
	if err != nil {
		t.Fatalf("GetOrMint failed: %v", err)
	}

	// Manually set Leaf to nil to test isNearExpiry
	cert.Leaf = nil

	if !isNearExpiry(cert) {
		t.Error("expected isNearExpiry to return true for nil Leaf")
	}
}

// TestIsNearExpiry_FarFromExpiry_ReturnsFalse verifies that cert far from expiry returns false.
func TestIsNearExpiry_FarFromExpiry_ReturnsFalse(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	pfxBytes, caCert := makeCAPFX(t, "testpass")
	_, err = store.Save(pfxBytes, "testpass", caCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	minter := NewMinter(store)
	cert, err := minter.GetOrMint("example.com")
	if err != nil {
		t.Fatalf("GetOrMint failed: %v", err)
	}

	// Newly minted cert should not be near expiry
	if isNearExpiry(cert) {
		t.Error("expected isNearExpiry to return false for fresh cert")
	}
}

// TestIsNearExpiry_WithinRenewalWindow_ReturnsTrue verifies that cert within renewal window returns true.
func TestIsNearExpiry_WithinRenewalWindow_ReturnsTrue(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database.Unwrap())
	// Create a CA that expires very soon
	pfxBytes, caCert := makeShortLivedCAPFX(t, 2*time.Hour, "testpass")
	_, err = store.Save(pfxBytes, "testpass", caCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	minter := NewMinter(store)
	cert, err := minter.GetOrMint("example.com")
	if err != nil {
		t.Fatalf("GetOrMint failed: %v", err)
	}

	// Leaf will be clamped to CA NotAfter, which is ~2 hours away
	// renewalWindow is 24 hours, so leaf expires before renewal window
	if !isNearExpiry(cert) {
		t.Error("expected isNearExpiry to return true for cert within renewal window")
	}
}
