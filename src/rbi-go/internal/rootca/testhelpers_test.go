package rootca

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// makeCAPFX creates a self-signed CA certificate and encodes it as PKCS#12.
// The certificate has RSA 2048 key, IsCA:true, BasicConstraintsValid:true,
// KeyUsage: x509.KeyUsageCertSign|CRLSign, and NotAfter ~30 days out.
// Returns the encoded PFX bytes and the parsed certificate.
func makeCAPFX(t *testing.T, password string) ([]byte, *x509.Certificate) {
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

// makeLeafPFX creates a self-signed leaf certificate and encodes it as PKCS#12.
// Similar to makeCAPFX but with IsCA:false.
func makeLeafPFX(t *testing.T, password string) ([]byte, *x509.Certificate) {
	t.Helper()

	// Generate RSA 2048 private key
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	// Create a leaf certificate template
	now := time.Now().UTC()
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("failed to generate serial number: %v", err)
	}

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

// makeShortLivedCAPFX creates a CA certificate with a custom TTL.
// NotAfter is set to time.Now().Add(ttl).
func makeShortLivedCAPFX(t *testing.T, ttl time.Duration, password string) ([]byte, *x509.Certificate) {
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
			CommonName: "Test Short-Lived CA",
		},
		NotBefore:             now,
		NotAfter:              now.Add(ttl),
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
