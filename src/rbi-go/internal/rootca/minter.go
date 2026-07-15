package rootca

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"sync"
	"time"
)

// leafValidity is the validity window applied to every freshly minted leaf cert.
// Mirrors the C# LeafCertificateMinter.LeafValidity constant (7 days).
const leafValidity = 7 * 24 * time.Hour

// renewalWindow is how close to expiry a cached leaf must be before it is re-minted,
// ensuring a long-lived process never serves an expired cert. Mirrors C# (1 day).
const renewalWindow = 24 * time.Hour

// Minter mints short-lived RSA leaf certificates on demand, signed by the active root
// CA from Store. The mint cache is keyed by hostname and must be cleared whenever the
// active CA changes — call ClearCache after any Store.Save or Store.Delete call.
// Safe for concurrent use. Mirrors C# LeafCertificateMinter.
type Minter struct {
	store *Store
	mu    sync.Mutex
	cache map[string]*tls.Certificate
}

// NewMinter constructs a Minter backed by the given Store. The Store must outlive the
// Minter (typically both are singletons).
func NewMinter(store *Store) *Minter {
	return &Minter{
		store: store,
		cache: make(map[string]*tls.Certificate),
	}
}

// GetOrMint returns a cached or freshly-minted TLS certificate for hostname, signed by
// the active root CA. Returns nil, nil if no root CA is currently configured (the TLS
// proxy must treat this as "cannot intercept — no CA uploaded yet"). Mirrors the C#
// ILeafCertificateMinter.GetOrMintAsync method.
func (m *Minter) GetOrMint(hostname string) (*tls.Certificate, error) {
	// Fast path: check the cache without doing any crypto work.
	m.mu.Lock()
	if cached, ok := m.cache[hostname]; ok && !isNearExpiry(cached) {
		m.mu.Unlock()
		return cached, nil
	}
	m.mu.Unlock()

	// Slow path: fetch the CA (its own mutex) and mint a new cert outside our lock
	// so CA load latency doesn't block other cache readers.
	leaf, err := m.mintFresh(hostname)
	if err != nil {
		return nil, err
	}
	if leaf == nil {
		return nil, nil
	}

	// Store in cache; a concurrent goroutine may have already minted the same
	// hostname, in which case we just overwrite with an equally-valid cert.
	m.mu.Lock()
	m.cache[hostname] = leaf
	m.mu.Unlock()

	return leaf, nil
}

// ClearCache drops every cached leaf cert. The next GetOrMint call for any hostname
// will re-mint against the current active CA. Must be called after Store.Save or
// Store.Delete to prevent stale leaves signed by the old CA from being served.
// Mirrors C# ILeafCertificateMinter.ClearCache.
func (m *Minter) ClearCache() {
	m.mu.Lock()
	m.cache = make(map[string]*tls.Certificate)
	m.mu.Unlock()
}

// mintFresh generates a fresh RSA 2048 leaf certificate signed by the active CA.
// Returns nil if the store has no active CA. Does not hold m.mu — CA load has its
// own lock inside Store.
func (m *Minter) mintFresh(hostname string) (*tls.Certificate, error) {
	ca, err := m.store.GetActiveCA()
	if err != nil {
		return nil, fmt.Errorf("minter: get active CA: %w", err)
	}
	if ca == nil {
		return nil, nil
	}

	rsaKey, ok := ca.Key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("minter: active CA has no RSA private key (only RSA-keyed CAs are supported)")
	}

	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("minter: generate leaf RSA key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("minter: generate serial number: %w", err)
	}

	notBefore := time.Now().UTC().Add(-5 * time.Minute)
	notAfter := notBefore.Add(leafValidity)
	// Never mint a leaf that outlives its own issuing CA — mirrors C# behaviour.
	if notAfter.After(ca.Cert.NotAfter) {
		notAfter = ca.Cert.NotAfter
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{CommonName: hostname},
		// SAN DNS entry is required — modern browsers reject certs that set only CN.
		DNSNames:  []string{hostname},
		NotBefore: notBefore,
		NotAfter:  notAfter,
		// digitalSignature + keyEncipherment — required for RSA TLS server auth.
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}

	leafDER, err := x509.CreateCertificate(rand.Reader, template, ca.Cert, &leafKey.PublicKey, rsaKey)
	if err != nil {
		return nil, fmt.Errorf("minter: sign leaf certificate: %w", err)
	}

	leafCert, err := x509.ParseCertificate(leafDER)
	if err != nil {
		return nil, fmt.Errorf("minter: parse minted DER: %w", err)
	}

	return &tls.Certificate{
		Certificate: [][]byte{leafDER},
		PrivateKey:  leafKey,
		Leaf:        leafCert,
	}, nil
}

// isNearExpiry reports whether the certificate's leaf is within the renewal window of
// its NotAfter deadline. Returns true for any cert with a nil Leaf (forces re-mint).
func isNearExpiry(cert *tls.Certificate) bool {
	if cert.Leaf == nil {
		return true
	}
	return time.Until(cert.Leaf.NotAfter) < renewalWindow
}
