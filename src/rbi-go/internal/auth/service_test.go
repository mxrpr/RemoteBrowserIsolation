package auth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"rbi-go/internal/config"
	"rbi-go/internal/db"
)

// newTestSvc is a test helper that creates a fresh AdminAuthService backed by an in-memory SQLite database.
// It follows the openTestDB pattern from db_test.go and defers database cleanup via t.Cleanup.
func newTestSvc(t *testing.T) *AdminAuthService {
	t.Helper()
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("newTestSvc: open in-memory DB: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	jwtCfg := &config.JwtConfig{
		Key:        "test-signing-key-at-least-32-bytes-long!",
		Issuer:     "test-issuer",
		Audience:   "test-audience",
		TtlMinutes: 60,
	}
	return NewAdminAuthService(database, jwtCfg)
}

// === IsBootstrapped tests ===

// TestIsBootstrapped_FreshDB_ReturnsFalse verifies that IsBootstrapped returns false
// when no AdminUser row has been created yet.
func TestIsBootstrapped_FreshDB_ReturnsFalse(t *testing.T) {
	svc := newTestSvc(t)
	ok, err := svc.IsBootstrapped()
	if err != nil {
		t.Fatalf("IsBootstrapped failed: %v", err)
	}
	if ok {
		t.Errorf("Expected IsBootstrapped=false for fresh DB, got true")
	}
}

// TestIsBootstrapped_AfterFirstLogin_ReturnsTrue verifies that IsBootstrapped returns true
// after the first call to LoginOrBootstrap creates the admin account.
func TestIsBootstrapped_AfterFirstLogin_ReturnsTrue(t *testing.T) {
	svc := newTestSvc(t)

	// First call should bootstrap
	token, err := svc.LoginOrBootstrap("admin@example.com", "password123")
	if err != nil {
		t.Fatalf("LoginOrBootstrap failed: %v", err)
	}
	if token == "" {
		t.Fatal("LoginOrBootstrap returned empty token on bootstrap")
	}

	// Now IsBootstrapped should return true
	ok, err := svc.IsBootstrapped()
	if err != nil {
		t.Fatalf("IsBootstrapped failed: %v", err)
	}
	if !ok {
		t.Errorf("Expected IsBootstrapped=true after bootstrap, got false")
	}
}

// === LoginOrBootstrap bootstrap path tests ===

// TestLoginOrBootstrap_FirstCall_ReturnsNonEmptyToken verifies that the first call
// to LoginOrBootstrap returns a non-empty token.
func TestLoginOrBootstrap_FirstCall_ReturnsNonEmptyToken(t *testing.T) {
	svc := newTestSvc(t)
	token, err := svc.LoginOrBootstrap("admin@example.com", "password123")
	if err != nil {
		t.Fatalf("LoginOrBootstrap failed: %v", err)
	}
	if token == "" {
		t.Fatal("LoginOrBootstrap returned empty token on bootstrap")
	}
}

// TestLoginOrBootstrap_FirstCall_TokenIsValidJWT verifies that the token returned
// on first call is a valid JWT that can be parsed.
func TestLoginOrBootstrap_FirstCall_TokenIsValidJWT(t *testing.T) {
	svc := newTestSvc(t)
	token, err := svc.LoginOrBootstrap("admin@example.com", "password123")
	if err != nil {
		t.Fatalf("LoginOrBootstrap failed: %v", err)
	}

	// Validate the token
	validated, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}
	if !validated.Valid {
		t.Error("Token is not valid")
	}
}

// TestLoginOrBootstrap_FirstCall_TokenClaimsSubAndEmail verifies that the token
// contains "sub" and "email" claims matching the provided email.
func TestLoginOrBootstrap_FirstCall_TokenClaimsSubAndEmail(t *testing.T) {
	svc := newTestSvc(t)
	email := "admin@example.com"
	token, err := svc.LoginOrBootstrap(email, "password123")
	if err != nil {
		t.Fatalf("LoginOrBootstrap failed: %v", err)
	}

	validated, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}

	claims, ok := validated.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("Expected MapClaims, got %T", validated.Claims)
	}

	sub, ok := claims["sub"]
	if !ok {
		t.Fatal("Token missing 'sub' claim")
	}
	if sub != email {
		t.Errorf("Expected sub=%q, got %v", email, sub)
	}

	emailClaim, ok := claims["email"]
	if !ok {
		t.Fatal("Token missing 'email' claim")
	}
	if emailClaim != email {
		t.Errorf("Expected email=%q, got %v", email, emailClaim)
	}
}

// TestLoginOrBootstrap_FirstCall_SetsBootstrapped verifies that after the first call,
// the database has the bootstrapped flag set (i.e., IsBootstrapped returns true).
func TestLoginOrBootstrap_FirstCall_SetsBootstrapped(t *testing.T) {
	svc := newTestSvc(t)
	_, err := svc.LoginOrBootstrap("admin@example.com", "password123")
	if err != nil {
		t.Fatalf("LoginOrBootstrap failed: %v", err)
	}

	ok, err := svc.IsBootstrapped()
	if err != nil {
		t.Fatalf("IsBootstrapped failed: %v", err)
	}
	if !ok {
		t.Error("Expected IsBootstrapped=true after bootstrap")
	}
}

// === LoginOrBootstrap login path tests ===

// TestLoginOrBootstrap_SecondCallCorrectPassword_ReturnsToken verifies that a second call
// with correct credentials returns a token.
func TestLoginOrBootstrap_SecondCallCorrectPassword_ReturnsToken(t *testing.T) {
	svc := newTestSvc(t)
	email := "admin@example.com"
	password := "password123"

	// Bootstrap
	_, err := svc.LoginOrBootstrap(email, password)
	if err != nil {
		t.Fatalf("First LoginOrBootstrap failed: %v", err)
	}

	// Second call with correct credentials
	token, err := svc.LoginOrBootstrap(email, password)
	if err != nil {
		t.Fatalf("Second LoginOrBootstrap failed: %v", err)
	}
	if token == "" {
		t.Fatal("Second call with correct credentials returned empty token")
	}
}

// TestLoginOrBootstrap_SecondCallCorrectPassword_TokenValid verifies that the token
// returned on a successful login is valid.
func TestLoginOrBootstrap_SecondCallCorrectPassword_TokenValid(t *testing.T) {
	svc := newTestSvc(t)
	email := "admin@example.com"
	password := "password123"

	// Bootstrap
	_, err := svc.LoginOrBootstrap(email, password)
	if err != nil {
		t.Fatalf("First LoginOrBootstrap failed: %v", err)
	}

	// Second call with correct credentials
	token, err := svc.LoginOrBootstrap(email, password)
	if err != nil {
		t.Fatalf("Second LoginOrBootstrap failed: %v", err)
	}

	validated, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}
	if !validated.Valid {
		t.Error("Token is not valid")
	}
}

// TestLoginOrBootstrap_SecondCallWrongPassword_ReturnsEmptyToken verifies that a second call
// with wrong password returns an empty token (not an error).
func TestLoginOrBootstrap_SecondCallWrongPassword_ReturnsEmptyToken(t *testing.T) {
	svc := newTestSvc(t)
	email := "admin@example.com"
	password := "password123"

	// Bootstrap with first password
	_, err := svc.LoginOrBootstrap(email, password)
	if err != nil {
		t.Fatalf("First LoginOrBootstrap failed: %v", err)
	}

	// Second call with wrong password
	token, err := svc.LoginOrBootstrap(email, "wrongpassword")
	if err != nil {
		t.Fatalf("Second LoginOrBootstrap should not return error: %v", err)
	}
	if token != "" {
		t.Errorf("Expected empty token for wrong password, got %q", token)
	}
}

// TestLoginOrBootstrap_SecondCallDifferentEmail_ReturnsEmptyToken verifies that a second call
// with a different email returns an empty token.
func TestLoginOrBootstrap_SecondCallDifferentEmail_ReturnsEmptyToken(t *testing.T) {
	svc := newTestSvc(t)
	email := "admin@example.com"
	password := "password123"

	// Bootstrap with first email
	_, err := svc.LoginOrBootstrap(email, password)
	if err != nil {
		t.Fatalf("First LoginOrBootstrap failed: %v", err)
	}

	// Second call with different email
	token, err := svc.LoginOrBootstrap("different@example.com", password)
	if err != nil {
		t.Fatalf("Second LoginOrBootstrap should not return error: %v", err)
	}
	if token != "" {
		t.Errorf("Expected empty token for different email, got %q", token)
	}
}

// TestLoginOrBootstrap_SecondCallEmailCaseInsensitive_Succeeds verifies that email
// comparison is case-insensitive: a login with different casing should succeed.
func TestLoginOrBootstrap_SecondCallEmailCaseInsensitive_Succeeds(t *testing.T) {
	svc := newTestSvc(t)
	email := "admin@example.com"
	password := "password123"

	// Bootstrap with lowercase email
	_, err := svc.LoginOrBootstrap(email, password)
	if err != nil {
		t.Fatalf("First LoginOrBootstrap failed: %v", err)
	}

	// Second call with uppercase email (case-insensitive match)
	token, err := svc.LoginOrBootstrap("ADMIN@EXAMPLE.COM", password)
	if err != nil {
		t.Fatalf("Second LoginOrBootstrap failed: %v", err)
	}
	if token == "" {
		t.Fatal("Expected token for case-insensitive email match, got empty")
	}
}

// === ValidateToken tests ===

// TestValidateToken_ValidToken_ReturnsToken verifies that ValidateToken returns
// the parsed token for a valid JWT.
func TestValidateToken_ValidToken_ReturnsToken(t *testing.T) {
	svc := newTestSvc(t)
	email := "admin@example.com"

	// Issue a token via LoginOrBootstrap
	token, err := svc.LoginOrBootstrap(email, "password123")
	if err != nil {
		t.Fatalf("LoginOrBootstrap failed: %v", err)
	}

	// Validate it
	validated, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}
	if validated == nil {
		t.Fatal("ValidateToken returned nil token")
	}
	if !validated.Valid {
		t.Error("Token is not valid")
	}
}

// TestValidateToken_ExpiredToken_ReturnsError verifies that ValidateToken rejects
// a token with an expiration in the past.
func TestValidateToken_ExpiredToken_ReturnsError(t *testing.T) {
	svc := newTestSvc(t)

	// Create an expired token by hand using the same key/iss/aud as the service
	now := time.Now().UTC()
	expiredClaims := jwt.MapClaims{
		"sub":   "admin@example.com",
		"email": "admin@example.com",
		"iss":   svc.jwtCfg.Issuer,
		"aud":   []string{svc.jwtCfg.Audience},
		"iat":   now.Add(-2 * time.Hour).Unix(),
		"exp":   now.Add(-1 * time.Hour).Unix(), // Expired 1 hour ago
	}

	expiredToken := jwt.NewWithClaims(jwt.SigningMethodHS256, expiredClaims)
	signedExpiredToken, err := expiredToken.SignedString([]byte(svc.jwtCfg.Key))
	if err != nil {
		t.Fatalf("Failed to sign expired token: %v", err)
	}

	// Validate should fail
	_, err = svc.ValidateToken(signedExpiredToken)
	if err == nil {
		t.Fatal("ValidateToken should reject expired token, but returned nil error")
	}
}

// TestValidateToken_WrongSignature_ReturnsError verifies that ValidateToken rejects
// a token signed with a different key.
func TestValidateToken_WrongSignature_ReturnsError(t *testing.T) {
	svc := newTestSvc(t)

	// Create a valid-looking token but sign it with a different key
	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"sub":   "admin@example.com",
		"email": "admin@example.com",
		"iss":   svc.jwtCfg.Issuer,
		"aud":   []string{svc.jwtCfg.Audience},
		"iat":   now.Unix(),
		"exp":   now.Add(1 * time.Hour).Unix(),
	}

	wrongToken := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	wrongKey := "different-key-not-used-in-service"
	signedWrongToken, err := wrongToken.SignedString([]byte(wrongKey))
	if err != nil {
		t.Fatalf("Failed to sign with wrong key: %v", err)
	}

	// Validate should fail
	_, err = svc.ValidateToken(signedWrongToken)
	if err == nil {
		t.Fatal("ValidateToken should reject token with wrong signature")
	}
}

// TestValidateToken_NoneAlgToken_ReturnsError verifies that ValidateToken rejects
// tokens signed with "none" algorithm.
func TestValidateToken_NoneAlgToken_ReturnsError(t *testing.T) {
	svc := newTestSvc(t)

	// Hand-craft a none-alg token as raw base64url(header).base64url(payload). —
	// the jwt/v5 library refuses to sign with SigningMethodNone via the normal
	// API, so build the string directly instead.
	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"sub":   "admin@example.com",
		"email": "admin@example.com",
		"iss":   svc.jwtCfg.Issuer,
		"aud":   []string{svc.jwtCfg.Audience},
		"iat":   now.Unix(),
		"exp":   now.Add(1 * time.Hour).Unix(),
	}

	headerJSON, err := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	if err != nil {
		t.Fatalf("Failed to marshal none-alg header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("Failed to marshal claims: %v", err)
	}
	signedNoneToken := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON) + "."

	// Validate should fail (we only allow HS256)
	_, err = svc.ValidateToken(signedNoneToken)
	if err == nil {
		t.Fatal("ValidateToken should reject 'none' algorithm token")
	}
}

// TestValidateToken_MalformedToken_ReturnsError verifies that ValidateToken rejects
// malformed tokens.
func TestValidateToken_MalformedToken_ReturnsError(t *testing.T) {
	svc := newTestSvc(t)

	_, err := svc.ValidateToken("not.a.valid.jwt")
	if err == nil {
		t.Fatal("ValidateToken should reject malformed token")
	}
}

// TestValidateToken_WrongIssuer_ReturnsError verifies that ValidateToken rejects
// a token with a different issuer.
func TestValidateToken_WrongIssuer_ReturnsError(t *testing.T) {
	svc := newTestSvc(t)

	// Create a token with wrong issuer
	now := time.Now().UTC()
	wrongIssuerClaims := jwt.MapClaims{
		"sub":   "admin@example.com",
		"email": "admin@example.com",
		"iss":   "wrong-issuer",
		"aud":   []string{svc.jwtCfg.Audience},
		"iat":   now.Unix(),
		"exp":   now.Add(1 * time.Hour).Unix(),
	}

	wrongIssuerToken := jwt.NewWithClaims(jwt.SigningMethodHS256, wrongIssuerClaims)
	signedWrongIssuer, err := wrongIssuerToken.SignedString([]byte(svc.jwtCfg.Key))
	if err != nil {
		t.Fatalf("Failed to sign token with wrong issuer: %v", err)
	}

	// Validate should fail
	_, err = svc.ValidateToken(signedWrongIssuer)
	if err == nil {
		t.Fatal("ValidateToken should reject token with wrong issuer")
	}
}

// TestValidateToken_WrongAudience_ReturnsError verifies that ValidateToken rejects
// a token with a different audience.
func TestValidateToken_WrongAudience_ReturnsError(t *testing.T) {
	svc := newTestSvc(t)

	// Create a token with wrong audience
	now := time.Now().UTC()
	wrongAudClaims := jwt.MapClaims{
		"sub":   "admin@example.com",
		"email": "admin@example.com",
		"iss":   svc.jwtCfg.Issuer,
		"aud":   []string{"wrong-audience"},
		"iat":   now.Unix(),
		"exp":   now.Add(1 * time.Hour).Unix(),
	}

	wrongAudToken := jwt.NewWithClaims(jwt.SigningMethodHS256, wrongAudClaims)
	signedWrongAud, err := wrongAudToken.SignedString([]byte(svc.jwtCfg.Key))
	if err != nil {
		t.Fatalf("Failed to sign token with wrong audience: %v", err)
	}

	// Validate should fail
	_, err = svc.ValidateToken(signedWrongAud)
	if err == nil {
		t.Fatal("ValidateToken should reject token with wrong audience")
	}
}

// TestValidateToken_EmptyString_ReturnsError verifies that ValidateToken rejects
// an empty string.
func TestValidateToken_EmptyString_ReturnsError(t *testing.T) {
	svc := newTestSvc(t)

	_, err := svc.ValidateToken("")
	if err == nil {
		t.Fatal("ValidateToken should reject empty string")
	}
}

// === issueToken claims tests ===

// TestIssueToken_ClaimsAlgHS256 verifies that issueToken creates a token with
// algorithm HS256.
func TestIssueToken_ClaimsAlgHS256(t *testing.T) {
	svc := newTestSvc(t)

	token, err := svc.LoginOrBootstrap("admin@example.com", "password123")
	if err != nil {
		t.Fatalf("LoginOrBootstrap failed: %v", err)
	}

	validated, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}

	if alg := validated.Header["alg"]; alg != "HS256" {
		t.Errorf("Expected alg=HS256, got %v", alg)
	}
}

// TestIssueToken_ClaimsExp_IsFuture verifies that the exp claim is set to a future time.
func TestIssueToken_ClaimsExp_IsFuture(t *testing.T) {
	svc := newTestSvc(t)

	token, err := svc.LoginOrBootstrap("admin@example.com", "password123")
	if err != nil {
		t.Fatalf("LoginOrBootstrap failed: %v", err)
	}
	afterIssue := time.Now().UTC()

	validated, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}

	claims, ok := validated.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("Expected MapClaims, got %T", validated.Claims)
	}

	expInterface, ok := claims["exp"]
	if !ok {
		t.Fatal("Token missing 'exp' claim")
	}

	var expTime time.Time
	switch exp := expInterface.(type) {
	case float64:
		expTime = time.Unix(int64(exp), 0).UTC()
	default:
		t.Fatalf("Unexpected exp claim type: %T", expInterface)
	}

	// exp should be after the token was issued (plus some buffer for the TTL)
	if !expTime.After(afterIssue) {
		t.Errorf("exp time %v is not in the future relative to issue time %v", expTime, afterIssue)
	}

	// exp should be approximately 60 minutes in the future (the configured TTL)
	expectedExp := afterIssue.Add(time.Duration(svc.jwtCfg.TtlMinutes) * time.Minute)
	// Allow 1 second of clock skew
	if expTime.Before(expectedExp.Add(-1 * time.Second)) || expTime.After(expectedExp.Add(1 * time.Second)) {
		t.Errorf("exp time %v is not approximately 60 minutes in the future (expected ~%v)", expTime, expectedExp)
	}
}

// TestIssueToken_ClaimsIssAndAud verifies that the iss and aud claims match
// the configured values.
func TestIssueToken_ClaimsIssAndAud(t *testing.T) {
	svc := newTestSvc(t)

	token, err := svc.LoginOrBootstrap("admin@example.com", "password123")
	if err != nil {
		t.Fatalf("LoginOrBootstrap failed: %v", err)
	}

	validated, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}

	claims, ok := validated.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("Expected MapClaims, got %T", validated.Claims)
	}

	iss, ok := claims["iss"]
	if !ok {
		t.Fatal("Token missing 'iss' claim")
	}
	if iss != svc.jwtCfg.Issuer {
		t.Errorf("Expected iss=%q, got %v", svc.jwtCfg.Issuer, iss)
	}

	aud, ok := claims["aud"]
	if !ok {
		t.Fatal("Token missing 'aud' claim")
	}
	// aud is an array in our implementation
	audList, ok := aud.([]interface{})
	if !ok {
		t.Fatalf("Expected aud to be []interface{}, got %T", aud)
	}
	if len(audList) != 1 || audList[0] != svc.jwtCfg.Audience {
		t.Errorf("Expected aud=[%q], got %v", svc.jwtCfg.Audience, audList)
	}
}
