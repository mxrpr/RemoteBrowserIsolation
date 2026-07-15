package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// makeCAPFXForHTTP creates a test CA certificate in PKCS#12 format locally (duplicating
// the internal/rootca test helper since unexported test helpers can't be imported across packages).
func makeCAPFXForHTTP(t *testing.T, password string) ([]byte, *x509.Certificate) {
	t.Helper()

	// Generate RSA 2048 private key
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	// Create a CA certificate template
	now := time.Now().UTC()
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("failed to generate serial number: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: "Test CA",
		},
		NotBefore:             now,
		NotAfter:              now.Add(30 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}

	// Self-sign the certificate
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	// Parse the certificate to return it
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	// Encode to PKCS#12 format
	pfxBytes, err := pkcs12.Encode(rand.Reader, key, cert, nil, password)
	if err != nil {
		t.Fatalf("failed to encode PFX: %v", err)
	}

	return pfxBytes, cert
}

// buildPFXMultipart creates a multipart/form-data body with pfx file and password.
func buildPFXMultipart(t *testing.T, pfxBytes []byte, password string) (io.Reader, string) {
	t.Helper()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	// Add "pfx" file field
	part, err := w.CreateFormFile("pfx", "test.pfx")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	if _, err := part.Write(pfxBytes); err != nil {
		t.Fatalf("failed to write PFX to form: %v", err)
	}

	// Add "password" text field
	if err := w.WriteField("password", password); err != nil {
		t.Fatalf("failed to write password field: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("failed to close multipart writer: %v", err)
	}

	return &buf, w.FormDataContentType()
}

// === handleGetRootCa tests ===

// TestHandleGetRootCa_NoJWT_Returns401 verifies that missing JWT returns 401.
func TestHandleGetRootCa_NoJWT_Returns401(t *testing.T) {
	store, _ := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)

	handler := handleGetRootCa(store, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/rootca", nil)
	// No Authorization header
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandleGetRootCa_NoCA_Returns404WithErrorJSON verifies that 404 is returned when no CA exists.
func TestHandleGetRootCa_NoCA_Returns404WithErrorJSON(t *testing.T) {
	store, _ := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleGetRootCa(store, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/rootca", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp errorResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal error response: %v", err)
	}
	if resp.Error == "" {
		t.Error("expected non-empty error message")
	}
}

// TestHandleGetRootCa_WithCA_Returns200WithMetadataJSON verifies that 200 is returned with CA metadata.
func TestHandleGetRootCa_WithCA_Returns200WithMetadataJSON(t *testing.T) {
	store, _ := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Save a CA
	pfxBytes, cert := makeCAPFXForHTTP(t, "testpass")
	_, err := store.Save(pfxBytes, "testpass", cert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	handler := handleGetRootCa(store, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/rootca", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp rootCaResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Id == 0 {
		t.Error("expected non-zero Id")
	}
	if resp.Thumbprint == "" {
		t.Error("expected non-empty Thumbprint")
	}
}

// TestHandleGetRootCa_ResponseExcludesPfxBytes verifies that response doesn't include PFX bytes.
func TestHandleGetRootCa_ResponseExcludesPfxBytes(t *testing.T) {
	store, _ := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Save a CA
	pfxBytes, cert := makeCAPFXForHTTP(t, "testpass")
	_, err := store.Save(pfxBytes, "testpass", cert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	handler := handleGetRootCa(store, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/rootca", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	// Response should NOT contain PfxBytes or PfxPassword fields
	bodyStr := string(body)
	if bytes.Contains(body, []byte("pfxBytes")) || bytes.Contains(body, []byte("pfxPassword")) {
		t.Errorf("response should not contain PFX fields, got: %s", bodyStr)
	}
}

// === handlePostRootCa tests ===

// TestHandlePostRootCa_NoJWT_Returns401 verifies that missing JWT returns 401.
func TestHandlePostRootCa_NoJWT_Returns401(t *testing.T) {
	store, minter := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)

	pfxBytes, _ := makeCAPFXForHTTP(t, "testpass")
	body, contentType := buildPFXMultipart(t, pfxBytes, "testpass")

	handler := handlePostRootCa(store, minter, authSvc)
	req := httptest.NewRequest("POST", "/api/admin/rootca", body)
	req.Header.Set("Content-Type", contentType)
	// No Authorization header
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandlePostRootCa_ValidCAPFX_Returns200WithRow verifies that valid CA returns 200 with row.
func TestHandlePostRootCa_ValidCAPFX_Returns200WithRow(t *testing.T) {
	store, minter := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	pfxBytes, cert := makeCAPFXForHTTP(t, "testpass")
	body, contentType := buildPFXMultipart(t, pfxBytes, "testpass")

	handler := handlePostRootCa(store, minter, authSvc)
	req := httptest.NewRequest("POST", "/api/admin/rootca", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	respBody, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp rootCaResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Id == 0 {
		t.Error("expected non-zero Id")
	}
	if resp.Subject != cert.Subject.String() {
		t.Errorf("expected Subject %q, got %q", cert.Subject.String(), resp.Subject)
	}
}

// TestHandlePostRootCa_MissingPfxField_Returns400 verifies that missing pfx field returns 400.
func TestHandlePostRootCa_MissingPfxField_Returns400(t *testing.T) {
	store, minter := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Build multipart with only password field
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("password", "testpass")
	w.Close()

	handler := handlePostRootCa(store, minter, authSvc)
	req := httptest.NewRequest("POST", "/api/admin/rootca", &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

// TestHandlePostRootCa_EmptyPfxFile_Returns400 verifies that empty pfx file returns 400.
func TestHandlePostRootCa_EmptyPfxFile_Returns400(t *testing.T) {
	store, minter := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	body, contentType := buildPFXMultipart(t, []byte{}, "testpass")

	handler := handlePostRootCa(store, minter, authSvc)
	req := httptest.NewRequest("POST", "/api/admin/rootca", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// TestHandlePostRootCa_MalformedPFX_Returns400 verifies that malformed PFX returns 400.
func TestHandlePostRootCa_MalformedPFX_Returns400(t *testing.T) {
	store, minter := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	body, contentType := buildPFXMultipart(t, []byte("not a valid pfx"), "testpass")

	handler := handlePostRootCa(store, minter, authSvc)
	req := httptest.NewRequest("POST", "/api/admin/rootca", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// TestHandlePostRootCa_WrongPassword_Returns400 verifies that wrong password returns 400.
func TestHandlePostRootCa_WrongPassword_Returns400(t *testing.T) {
	store, minter := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	pfxBytes, _ := makeCAPFXForHTTP(t, "correctpass")
	body, contentType := buildPFXMultipart(t, pfxBytes, "wrongpass")

	handler := handlePostRootCa(store, minter, authSvc)
	req := httptest.NewRequest("POST", "/api/admin/rootca", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// TestHandlePostRootCa_NonCACert_Returns400WithCAMessage verifies that non-CA cert returns 400.
func TestHandlePostRootCa_NonCACert_Returns400WithCAMessage(t *testing.T) {
	store, minter := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Create a leaf cert (IsCA=false)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	now := time.Now().UTC()
	serialNumber, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: "Test Leaf",
		},
		NotBefore:             now,
		NotAfter:              now.Add(30 * 24 * time.Hour),
		IsCA:                  false,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"example.com"},
	}

	certDER, _ := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	cert, _ := x509.ParseCertificate(certDER)
	pfxBytes, _ := pkcs12.Encode(rand.Reader, key, cert, nil, "testpass")

	body, contentType := buildPFXMultipart(t, pfxBytes, "testpass")

	handler := handlePostRootCa(store, minter, authSvc)
	req := httptest.NewRequest("POST", "/api/admin/rootca", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}

	respBody, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp errorResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Error == "" {
		t.Error("expected non-empty error message")
	}
	// Error message should mention "not a CA" or "leaf cert"
	if !bytes.Contains([]byte(resp.Error), []byte("CA")) &&
		!bytes.Contains([]byte(resp.Error), []byte("leaf")) {
		t.Errorf("expected error to mention CA or leaf, got: %s", resp.Error)
	}
}

// TestHandlePostRootCa_ReplaceSemantics_SecondUploadReplacesFirst verifies that second upload replaces first.
func TestHandlePostRootCa_ReplaceSemantics_SecondUploadReplacesFirst(t *testing.T) {
	store, minter := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	pfxBytes1, _ := makeCAPFXForHTTP(t, "pass1")
	pfxBytes2, cert2 := makeCAPFXForHTTP(t, "pass2")

	handler := handlePostRootCa(store, minter, authSvc)

	// First upload
	body1, contentType1 := buildPFXMultipart(t, pfxBytes1, "pass1")
	req1 := httptest.NewRequest("POST", "/api/admin/rootca", body1)
	req1.Header.Set("Authorization", "Bearer "+token)
	req1.Header.Set("Content-Type", contentType1)
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first upload failed with status %d", w1.Code)
	}

	// Second upload
	body2, contentType2 := buildPFXMultipart(t, pfxBytes2, "pass2")
	req2 := httptest.NewRequest("POST", "/api/admin/rootca", body2)
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Content-Type", contentType2)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("second upload failed with status %d", w2.Code)
	}

	// Verify only second one exists
	meta, err := store.GetMetadata()
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta == nil {
		t.Fatal("expected metadata, got nil")
	}
	if meta.Subject != cert2.Subject.String() {
		t.Errorf("expected second cert to be stored, got %q", meta.Subject)
	}
}

// TestHandlePostRootCa_ClearsMinterCache verifies that POST clears minter cache.
func TestHandlePostRootCa_ClearsMinterCache(t *testing.T) {
	store, minter := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	pfxBytes1, cert1 := makeCAPFXForHTTP(t, "pass1")
	pfxBytes2, _ := makeCAPFXForHTTP(t, "pass2")

	// Save first CA
	_, err := store.Save(pfxBytes1, "pass1", cert1)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Mint a cert with first CA
	cert1Mint, err := minter.GetOrMint("example.com")
	if err != nil {
		t.Fatalf("first mint failed: %v", err)
	}

	// Upload second CA (should clear cache)
	handler := handlePostRootCa(store, minter, authSvc)
	body2, contentType2 := buildPFXMultipart(t, pfxBytes2, "pass2")
	req2 := httptest.NewRequest("POST", "/api/admin/rootca", body2)
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Content-Type", contentType2)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("second upload failed with status %d", w2.Code)
	}

	// Mint again (should get different cert due to cache clear)
	cert2Mint, err := minter.GetOrMint("example.com")
	if err != nil {
		t.Fatalf("second mint failed: %v", err)
	}

	if cert1Mint == cert2Mint {
		t.Error("expected cache to be cleared, got same cert pointer")
	}
}

// TestHandlePostRootCa_NotMultipart_Returns400 verifies that non-multipart returns 400.
func TestHandlePostRootCa_NotMultipart_Returns400(t *testing.T) {
	store, minter := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handlePostRootCa(store, minter, authSvc)
	req := httptest.NewRequest("POST", "/api/admin/rootca", bytes.NewReader([]byte("not multipart")))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// === handleDeleteRootCa tests ===

// TestHandleDeleteRootCa_NoJWT_Returns401 verifies that missing JWT returns 401.
func TestHandleDeleteRootCa_NoJWT_Returns401(t *testing.T) {
	store, minter := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)

	handler := handleDeleteRootCa(store, minter, authSvc)
	req := httptest.NewRequest("DELETE", "/api/admin/rootca", nil)
	// No Authorization header
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandleDeleteRootCa_WithCA_Returns204 verifies that DELETE with CA returns 204.
func TestHandleDeleteRootCa_WithCA_Returns204(t *testing.T) {
	store, minter := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Save a CA
	pfxBytes, cert := makeCAPFXForHTTP(t, "testpass")
	_, err := store.Save(pfxBytes, "testpass", cert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	handler := handleDeleteRootCa(store, minter, authSvc)
	req := httptest.NewRequest("DELETE", "/api/admin/rootca", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", w.Code)
	}
}

// TestHandleDeleteRootCa_NoCA_Returns204 verifies that DELETE with no CA returns 204.
func TestHandleDeleteRootCa_NoCA_Returns204(t *testing.T) {
	store, minter := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleDeleteRootCa(store, minter, authSvc)
	req := httptest.NewRequest("DELETE", "/api/admin/rootca", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", w.Code)
	}
}

// TestHandleDeleteRootCa_ClearsStore_GetReturns404After verifies that DELETE clears store.
func TestHandleDeleteRootCa_ClearsStore_GetReturns404After(t *testing.T) {
	store, minter := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Save a CA
	pfxBytes, cert := makeCAPFXForHTTP(t, "testpass")
	_, err := store.Save(pfxBytes, "testpass", cert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify it exists
	meta, err := store.GetMetadata()
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta == nil {
		t.Fatal("expected CA before delete")
	}

	// Delete
	deleteHandler := handleDeleteRootCa(store, minter, authSvc)
	deleteReq := httptest.NewRequest("DELETE", "/api/admin/rootca", nil)
	deleteReq.Header.Set("Authorization", "Bearer "+token)
	deleteRec := httptest.NewRecorder()
	deleteHandler.ServeHTTP(deleteRec, deleteReq)

	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete failed with status %d", deleteRec.Code)
	}

	// Verify GET now returns 404
	getHandler := handleGetRootCa(store, authSvc)
	getReq := httptest.NewRequest("GET", "/api/admin/rootca", nil)
	getReq.Header.Set("Authorization", "Bearer "+token)
	getRec := httptest.NewRecorder()
	getHandler.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", getRec.Code)
	}
}

// TestHandleDeleteRootCa_ClearsMinterCache verifies that DELETE clears minter cache.
func TestHandleDeleteRootCa_ClearsMinterCache(t *testing.T) {
	store, minter := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Save CA and mint
	pfxBytes, cert := makeCAPFXForHTTP(t, "testpass")
	_, err := store.Save(pfxBytes, "testpass", cert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	cert1, err := minter.GetOrMint("example.com")
	if err != nil {
		t.Fatalf("mint failed: %v", err)
	}
	if cert1 == nil {
		t.Fatal("expected cert1 before delete, got nil")
	}

	// Delete (should clear cache)
	handler := handleDeleteRootCa(store, minter, authSvc)
	req := httptest.NewRequest("DELETE", "/api/admin/rootca", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Try to mint again (should fail because CA is gone)
	cert2, err := minter.GetOrMint("example.com")
	if err != nil {
		t.Fatalf("mint after delete failed: %v", err)
	}

	if cert2 != nil {
		t.Error("expected nil cert after CA delete, got non-nil")
	}
}

// === handleGetRootCaCertificate tests ===

// TestHandleGetRootCaCertificate_NoJWT_Returns401 verifies that missing JWT returns 401.
func TestHandleGetRootCaCertificate_NoJWT_Returns401(t *testing.T) {
	store, _ := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)

	handler := handleGetRootCaCertificate(store, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/rootca/certificate", nil)
	// No Authorization header
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandleGetRootCaCertificate_NoCA_Returns404 verifies that 404 is returned when no CA.
func TestHandleGetRootCaCertificate_NoCA_Returns404(t *testing.T) {
	store, _ := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleGetRootCaCertificate(store, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/rootca/certificate", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

// TestHandleGetRootCaCertificate_WithCA_Returns200DERBody verifies that 200 is returned with DER body.
func TestHandleGetRootCaCertificate_WithCA_Returns200DERBody(t *testing.T) {
	caStore, _ := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Save a CA
	pfxBytes, cert := makeCAPFXForHTTP(t, "testpass")
	_, err := caStore.Save(pfxBytes, "testpass", cert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	handler := handleGetRootCaCertificate(caStore, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/rootca/certificate", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if w.Body.Len() == 0 {
		t.Error("expected non-empty response body")
	}
}

// TestHandleGetRootCaCertificate_DERBodyIsParseable verifies that DER body is valid.
func TestHandleGetRootCaCertificate_DERBodyIsParseable(t *testing.T) {
	caStore, _ := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Save a CA
	pfxBytes, expectedCert := makeCAPFXForHTTP(t, "testpass")
	_, err := caStore.Save(pfxBytes, "testpass", expectedCert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	handler := handleGetRootCaCertificate(caStore, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/rootca/certificate", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	// Parse the response as a certificate
	derBytes := w.Body.Bytes()
	parsedCert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	if parsedCert.Subject.CommonName != expectedCert.Subject.CommonName {
		t.Errorf("expected CN %q, got %q", expectedCert.Subject.CommonName, parsedCert.Subject.CommonName)
	}
}

// TestHandleGetRootCaCertificate_ContentDispositionHeader verifies Content-Disposition header.
func TestHandleGetRootCaCertificate_ContentDispositionHeader(t *testing.T) {
	caStore, _ := newTestCaStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Save a CA
	pfxBytes, cert := makeCAPFXForHTTP(t, "testpass")
	_, err := caStore.Save(pfxBytes, "testpass", cert)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	handler := handleGetRootCaCertificate(caStore, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/rootca/certificate", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	contentDisposition := w.Header().Get("Content-Disposition")
	if contentDisposition == "" {
		t.Error("expected Content-Disposition header, got empty")
	}
	if !bytes.Contains([]byte(contentDisposition), []byte("attachment")) {
		t.Errorf("expected Content-Disposition to contain 'attachment', got %q", contentDisposition)
	}
}
