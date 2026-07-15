package proxy

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"

	"rbi-go/internal/config"
	"rbi-go/internal/db"
	"rbi-go/internal/policy"
	"rbi-go/internal/rootca"
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
func seedPolicy(t *testing.T, eng *policy.Engine, hostPattern string, mode db.ViewMode) int64 {
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
	eng.Invalidate()
	return id
}

// makeTestCA generates a self-signed RSA 2048 CA certificate and returns it as
// PKCS#12-encoded bytes along with the parsed certificate. Uses a package-level
// sync.Once-cached CA to avoid repeated slow keygen across tests.
func makeTestCA(t *testing.T) (pfxBytes []byte, caCert *x509.Certificate) {
	t.Helper()

	var cachedPfx []byte
	var cachedCert *x509.Certificate
	var once sync.Once
	var err error

	once.Do(func() {
		// Generate RSA 2048 private key
		key, keyErr := rsa.GenerateKey(rand.Reader, 2048)
		if keyErr != nil {
			err = fmt.Errorf("generate RSA key: %w", keyErr)
			return
		}

		// Create a CA certificate template
		now := time.Now().UTC()
		serialNumber, serialErr := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if serialErr != nil {
			err = fmt.Errorf("generate serial number: %w", serialErr)
			return
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
		certDER, certErr := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if certErr != nil {
			err = fmt.Errorf("create certificate: %w", certErr)
			return
		}

		// Parse the certificate to return it
		cert, parseErr := x509.ParseCertificate(certDER)
		if parseErr != nil {
			err = fmt.Errorf("parse certificate: %w", parseErr)
			return
		}

		// Encode to PKCS#12 format
		pfx, pfxErr := pkcs12.Encode(rand.Reader, key, cert, nil, "testpass")
		if pfxErr != nil {
			err = fmt.Errorf("encode PFX: %w", pfxErr)
			return
		}

		cachedPfx = pfx
		cachedCert = cert
	})

	if err != nil {
		t.Fatalf("makeTestCA: %v", err)
	}

	return cachedPfx, cachedCert
}

// newProxyTestEnv sets up a complete proxy test environment with:
// - in-memory database
// - policy.Engine with seeded policies
// - rootca.Store with test CA
// - rootca.Minter wired to the store
// - ProxyConfig with InterceptPorts:[443] and SelfHosts list
// - loopback httptest.Server acting as "self" (HTTP server)
// - proxy.Server started via net.Listen and Server.Run in a goroutine
// Returns: proxy listener address, origin httptest.Server, test CA certificate
func newProxyTestEnv(t *testing.T, policies map[string]db.ViewMode) (proxyAddr string, originServer *httptest.Server, caCert *x509.Certificate) {
	t.Helper()

	// Create in-memory database and policy engine
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)

	// Seed policies
	for host, mode := range policies {
		seedPolicy(t, eng, host, mode)
	}

	// Create and save test CA
	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, err := store.Save(pfxBytes, "testpass", caCert)
	if err != nil {
		t.Fatalf("newProxyTestEnv: save CA: %v", err)
	}

	// Create minter from the store
	minter := rootca.NewMinter(store)

	// Create and start httptest server to act as "self"
	originServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "test response")
	}))
	t.Cleanup(func() { originServer.Close() })

	// Parse the origin server's address to extract the port
	originAddr := originServer.Listener.Addr().String()

	// Create proxy config
	proxyCfg := &config.ProxyConfig{
		Port:           0, // will be set after listening
		Bind:           "127.0.0.1",
		InterceptPorts: []int{443},
		SelfHosts:      []string{"localhost", "127.0.0.1"},
	}

	// Create proxy server
	srv := NewServer(proxyCfg, originAddr, eng, minter)

	// Listen on a random port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("newProxyTestEnv: listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	proxyAddr = listener.Addr().String()

	// Start the proxy server in a goroutine
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Close the listener when context is cancelled
		go func() {
			<-ctx.Done()
			_ = listener.Close()
		}()
		// Extract the port from the listener address for Run()
		_, portStr, _ := net.SplitHostPort(listener.Addr().String())
		port := 0
		fmt.Sscanf(portStr, "%d", &port)
		proxyCfg.Port = port
		proxyCfg.Bind = "127.0.0.1"
		_ = srv.Run(ctx)
	}()
	t.Cleanup(cancel)

	return proxyAddr, originServer, caCert
}

// tlsClientConn establishes a TLS connection to the proxy for CONNECT-based tunneling.
// It dials the proxy, sends "CONNECT targetHost:443 HTTP/1.1", reads until the "200
// Connection Established" response, then wraps the connection with TLS using the
// provided CA certificate in the root pool. Returns the established *tls.Conn.
func tlsClientConn(t *testing.T, proxyAddr, targetHost string, caCert *x509.Certificate) *tls.Conn {
	t.Helper()

	// Dial the proxy
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("tlsClientConn: dial proxy: %v", err)
	}

	// Send CONNECT request
	connectReq := fmt.Sprintf("CONNECT %s:443 HTTP/1.1\r\nHost: %s:443\r\n\r\n", targetHost, targetHost)
	if _, err := conn.Write([]byte(connectReq)); err != nil {
		t.Fatalf("tlsClientConn: write CONNECT: %v", err)
	}

	// Read response until blank line (200 Connection Established\r\n\r\n)
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("tlsClientConn: read CONNECT response: %v", err)
	}

	response := string(buf[:n])
	if !strings.Contains(response, "200") {
		t.Fatalf("tlsClientConn: unexpected CONNECT response: %s", response)
	}

	// Create a certificate pool with the test CA
	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	// Wrap the connection with TLS
	tlsConn := tls.Client(conn, &tls.Config{
		ServerName: targetHost,
		RootCAs:    pool,
	})

	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("tlsClientConn: TLS handshake: %v", err)
	}

	t.Cleanup(func() { _ = tlsConn.Close() })
	return tlsConn
}
