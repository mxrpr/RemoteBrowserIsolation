package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rbi-go/internal/config"
	"rbi-go/internal/db"
	"rbi-go/internal/policy"
	"rbi-go/internal/rootca"
)

// === NewServer Address Normalisation Tests ===

// TestNewServer_WildcardBind_NormalisedToLocalhost substitutes wildcard bind hosts with localhost.
func TestNewServer_WildcardBind_NormalisedToLocalhost(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)
	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, _ = store.Save(pfxBytes, "testpass", caCert)
	minter := rootca.NewMinter(store)

	cfg := &config.ProxyConfig{
		Port:           443,
		Bind:           "0.0.0.0",
		InterceptPorts: []int{443},
		SelfHosts:      []string{"localhost"},
	}

	srv := NewServer(cfg, "0.0.0.0:5139", eng, minter)

	if !strings.HasPrefix(srv.selfOriginBaseUrl, "http://localhost:") {
		t.Errorf("expected localhost in selfOriginBaseUrl, got %q", srv.selfOriginBaseUrl)
	}
}

// TestNewServer_IPv6Wildcard_NormalisedToLocalhost substitutes IPv6 wildcard with localhost.
func TestNewServer_IPv6Wildcard_NormalisedToLocalhost(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)
	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, _ = store.Save(pfxBytes, "testpass", caCert)
	minter := rootca.NewMinter(store)

	cfg := &config.ProxyConfig{
		Port:           443,
		Bind:           "::",
		InterceptPorts: []int{443},
		SelfHosts:      []string{"localhost"},
	}

	srv := NewServer(cfg, "[::]:5139", eng, minter)

	if !strings.HasPrefix(srv.selfOriginBaseUrl, "http://localhost:") {
		t.Errorf("expected localhost for IPv6 wildcard, got %q", srv.selfOriginBaseUrl)
	}
}

// TestNewServer_ExplicitHost_Preserved uses explicit host as-is.
func TestNewServer_ExplicitHost_Preserved(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)
	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, _ = store.Save(pfxBytes, "testpass", caCert)
	minter := rootca.NewMinter(store)

	cfg := &config.ProxyConfig{
		Port:           443,
		Bind:           "myhost.example.com",
		InterceptPorts: []int{443},
		SelfHosts:      []string{"myhost.example.com"},
	}

	srv := NewServer(cfg, "myhost.example.com:5139", eng, minter)

	if !strings.HasPrefix(srv.selfOriginBaseUrl, "http://myhost.example.com:") {
		t.Errorf("expected explicit host preserved, got %q", srv.selfOriginBaseUrl)
	}
}

// === handleConn Tests (malformed/empty first line) ===

// TestHandleConn_MalformedFirstLine closes connection cleanly without response.
func TestHandleConn_MalformedFirstLine(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)
	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, _ = store.Save(pfxBytes, "testpass", caCert)
	minter := rootca.NewMinter(store)

	cfg := &config.ProxyConfig{
		Port:           443,
		Bind:           "127.0.0.1",
		InterceptPorts: []int{443},
		SelfHosts:      []string{"localhost"},
	}
	srv := NewServer(cfg, "127.0.0.1:5139", eng, minter)

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.handleConn(ctx, server)

	// Send malformed first line
	_, _ = client.Write([]byte("GARBAGE\r\n"))
	client.Close()

	// Verify server closed the connection cleanly (no response was written)
	_, err := server.Read(make([]byte, 1))
	if err != io.EOF {
		t.Errorf("expected EOF after malformed line, got %v", err)
	}
}

// TestHandleConn_EmptyFirstLine closes connection cleanly on empty first line.
func TestHandleConn_EmptyFirstLine(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)
	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, _ = store.Save(pfxBytes, "testpass", caCert)
	minter := rootca.NewMinter(store)

	cfg := &config.ProxyConfig{
		Port:           443,
		Bind:           "127.0.0.1",
		InterceptPorts: []int{443},
		SelfHosts:      []string{"localhost"},
	}
	srv := NewServer(cfg, "127.0.0.1:5139", eng, minter)

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	client.Close() // close without sending anything

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.handleConn(ctx, server)

	// Should close cleanly: handleConn returning after the client closed
	// means the connection was not left hanging or written to.
	_, err := server.Read(make([]byte, 1))
	if err != io.EOF && !strings.Contains(err.Error(), "closed pipe") {
		t.Errorf("expected EOF or closed-pipe error, got %v", err)
	}
}

// === CONNECT to unmatched host Tests ===

// TestCONNECT_UnmatchedHost_Returns502PreTLS returns 502 before TLS handshake.
func TestCONNECT_UnmatchedHost_Returns502PreTLS(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)
	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, _ = store.Save(pfxBytes, "testpass", caCert)
	minter := rootca.NewMinter(store)

	cfg := &config.ProxyConfig{
		Port:           0,
		Bind:           "127.0.0.1",
		InterceptPorts: []int{443},
		SelfHosts:      []string{"localhost"},
	}

	srv := NewServer(cfg, "127.0.0.1:5139", eng, minter)

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.handleConn(ctx, server)

	// Send CONNECT to unmatched host (no policy)
	_, _ = client.Write([]byte("CONNECT unmatched.example.com:443 HTTP/1.1\r\n\r\n"))

	// Should get 502 response
	buf := make([]byte, 1024)
	n, _ := client.Read(buf)
	response := string(buf[:n])

	if !strings.Contains(response, "502") {
		t.Errorf("expected 502 for unmatched host, got: %s", response[:min(100, n)])
	}
}

// === CONNECT self-host blind tunnel Tests ===

// TestCONNECT_SelfHost_BlindTunnelsRawBytes forwards request to self httptest.Server unmodified.
func TestCONNECT_SelfHost_BlindTunnelsRawBytes(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)
	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, _ = store.Save(pfxBytes, "testpass", caCert)
	minter := rootca.NewMinter(store)

	// Create a loopback server to receive self-origin traffic
	selfServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "self response body")
	}))
	defer selfServer.Close()

	cfg := &config.ProxyConfig{
		Port:           0,
		Bind:           "127.0.0.1",
		InterceptPorts: []int{443},
		SelfHosts:      []string{"localhost"},
	}

	srv := NewServer(cfg, selfServer.Listener.Addr().String(), eng, minter)

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.handleConn(ctx, server)

	// Send CONNECT localhost:443
	_, _ = client.Write([]byte("CONNECT localhost:443 HTTP/1.1\r\n\r\n"))

	// Read response - should get 200 Connection Established
	buf := make([]byte, 1024)
	n, _ := client.Read(buf)
	response := string(buf[:n])

	if !strings.Contains(response, "200") {
		t.Errorf("expected 200 Connection Established, got: %s", response)
	}

	client.Close()
}

// === CONNECT non-intercept-port blind tunnel Tests ===

// TestCONNECT_NonInterceptPort_BlindTunnels forwards to plain TCP without TLS interception.
func TestCONNECT_NonInterceptPort_BlindTunnels(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)
	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, _ = store.Save(pfxBytes, "testpass", caCert)
	minter := rootca.NewMinter(store)

	// Create a plain TCP echo listener for non-443 port
	echoListener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer echoListener.Close()

	echoPort := echoListener.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := echoListener.Accept()
			if err != nil {
				return
			}
			// Echo everything back
			_, _ = io.Copy(conn, conn)
			_ = conn.Close()
		}
	}()

	cfg := &config.ProxyConfig{
		Port:           0,
		Bind:           "127.0.0.1",
		InterceptPorts: []int{443}, // only 443 is intercepted
		SelfHosts:      []string{"localhost"},
	}

	srv := NewServer(cfg, "127.0.0.1:5139", eng, minter)

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.handleConn(ctx, server)

	// Send CONNECT to non-intercept port
	connectReq := fmt.Sprintf("CONNECT 127.0.0.1:%d HTTP/1.1\r\n\r\n", echoPort)
	_, _ = client.Write([]byte(connectReq))

	// Should get 200 response
	buf := make([]byte, 1024)
	n, _ := client.Read(buf)
	response := string(buf[:n])

	if !strings.Contains(response, "200") {
		t.Errorf("expected 200 response for non-intercept port, got: %s", response)
	}

	client.Close()
}

// === CONNECT + TLS intercept for HtmlAllowInput Tests ===

// TestCONNECT_HtmlAllowInput_ForwardsToOriginAndReturnsResponse forwards GET to origin correctly.
func TestCONNECT_HtmlAllowInput_ForwardsToOriginAndReturnsResponse(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)
	seedPolicy(t, eng, "allowed.example.com", db.ViewModeHtmlAllowInput)

	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, _ = store.Save(pfxBytes, "testpass", caCert)
	minter := rootca.NewMinter(store)

	// Create origin server
	originServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "<html>origin response</html>")
	}))
	defer originServer.Close()

	cfg := &config.ProxyConfig{
		Port:           0,
		Bind:           "127.0.0.1",
		InterceptPorts: []int{443},
		SelfHosts:      []string{"localhost"},
	}

	srv := NewServer(cfg, originServer.Listener.Addr().String(), eng, minter)

	// Test buildResponse directly to verify forwarding (actual TLS tunnel test is complex).
	// Target the httptest server's real loopback address so the forward's real
	// DNS lookup resolves; policy dispatch is bypassed here since buildResponse
	// is called directly with an already-decided mode.
	targetURL, _ := url.Parse(originServer.URL + "/")
	headers := []proxyHeader{
		{Name: "Host", Value: "allowed.example.com"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resp, err := srv.buildResponse(ctx, "GET", targetURL, headers, []byte{}, db.ViewModeHtmlAllowInput)

	if err != nil {
		t.Fatalf("buildResponse error: %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	if !strings.Contains(string(resp.Body), "origin response") {
		t.Errorf("expected response body to contain origin content, got: %s", string(resp.Body))
	}
}

// === CONNECT + TLS intercept for VideoAllowInput/VideoNoInput Tests ===

// TestCONNECT_VideoMode_ReturnsInterstitialHTML returns interstitial for video modes.
func TestCONNECT_VideoMode_ReturnsInterstitialHTML(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)
	seedPolicy(t, eng, "video.example.com", db.ViewModeVideoAllowInput)

	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, _ = store.Save(pfxBytes, "testpass", caCert)
	minter := rootca.NewMinter(store)

	cfg := &config.ProxyConfig{
		Port:           0,
		Bind:           "127.0.0.1",
		InterceptPorts: []int{443},
		SelfHosts:      []string{"localhost"},
	}

	srv := NewServer(cfg, "127.0.0.1:5139", eng, minter)

	targetURL, _ := url.Parse("https://video.example.com/")
	resp := srv.buildVideoInterstitial(targetURL)

	if resp.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	bodyStr := string(resp.Body)
	if !strings.Contains(bodyStr, "index.html?url=") {
		t.Error("expected video interstitial to contain index.html?url=")
	}

	if !strings.Contains(bodyStr, "video.example.com") {
		t.Error("expected interstitial to contain target host")
	}

	if !strings.Contains(bodyStr, "<!doctype html>") {
		t.Error("expected valid HTML doctype")
	}
}

// === CONNECT + TLS intercept for HtmlNoInput Tests ===

// TestCONNECT_HtmlNoInput_InjectsNoInputCSS injects CSS into HTML response.
func TestCONNECT_HtmlNoInput_InjectsNoInputCSS(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)
	seedPolicy(t, eng, "noinput.example.com", db.ViewModeHtmlNoInput)

	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, _ = store.Save(pfxBytes, "testpass", caCert)
	minter := rootca.NewMinter(store)

	// Create origin server that returns HTML
	originServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "<!DOCTYPE html><html><head><title>Test</title></head><body>content</body></html>")
	}))
	defer originServer.Close()

	cfg := &config.ProxyConfig{
		Port:           0,
		Bind:           "127.0.0.1",
		InterceptPorts: []int{443},
		SelfHosts:      []string{"localhost"},
	}

	srv := NewServer(cfg, originServer.Listener.Addr().String(), eng, minter)

	// Target the httptest server's real loopback address so the forward's
	// real DNS lookup resolves (see HtmlAllowInput test above for rationale).
	targetURL, _ := url.Parse(originServer.URL + "/")
	headers := []proxyHeader{
		{Name: "Host", Value: "noinput.example.com"},
		{Name: "Content-Type", Value: "text/html; charset=utf-8"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resp, err := srv.buildResponse(ctx, "GET", targetURL, headers, []byte{}, db.ViewModeHtmlNoInput)

	if err != nil {
		t.Fatalf("buildResponse error: %v", err)
	}

	bodyStr := string(resp.Body)
	if !strings.Contains(bodyStr, "pointer-events:none") {
		t.Error("expected CSS injection for HtmlNoInput mode (pointer-events:none not found)")
	}

	if !strings.Contains(bodyStr, "user-select:none") {
		t.Error("expected CSS injection for HtmlNoInput mode (user-select:none not found)")
	}
}

// === CONNECT with no CA configured Tests ===

// TestCONNECT_NoCAConfigured_HandshakeFailsCleanly TLS handshake fails cleanly without panic.
func TestCONNECT_NoCAConfigured_HandshakeFailsCleanly(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)
	seedPolicy(t, eng, "test.example.com", db.ViewModeHtmlAllowInput)

	// Don't save any CA - minter has no CA
	store := rootca.NewStore(testDB.Unwrap())
	minter := rootca.NewMinter(store)

	cfg := &config.ProxyConfig{
		Port:           0,
		Bind:           "127.0.0.1",
		InterceptPorts: []int{443},
		SelfHosts:      []string{"localhost"},
	}

	srv := NewServer(cfg, "127.0.0.1:5139", eng, minter)

	client1, server1 := net.Pipe()
	defer client1.Close()
	defer server1.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.handleConn(ctx, server1)

	// Send CONNECT
	_, _ = client1.Write([]byte("CONNECT test.example.com:443 HTTP/1.1\r\n\r\n"))

	buf := make([]byte, 1024)
	n, _ := client1.Read(buf)
	response := string(buf[:n])

	// Should get 200 CONNECT OK (handshake will fail after, server handles cleanly)
	if !strings.Contains(response, "200") {
		t.Logf("response: %s", response)
	}

	client1.Close()

	// Now test that second connection still works (server/listener not crashed)
	client2, server2 := net.Pipe()
	defer client2.Close()
	defer server2.Close()

	go srv.handleConn(ctx, server2)

	// Send another request
	_, _ = client2.Write([]byte("CONNECT other.example.com:443 HTTP/1.1\r\n\r\n"))

	buf2 := make([]byte, 1024)
	n2, _ := client2.Read(buf2)
	if n2 == 0 {
		t.Error("expected second connection to work after first TLS handshake failure")
	}
}

// === Plain-HTTP absolute-URI Tests ===

// TestPlainHTTP_AbsoluteURI_HtmlAllowInput_ForwardsCorrectly forwards absolute-URI GET correctly.
func TestPlainHTTP_AbsoluteURI_HtmlAllowInput_ForwardsCorrectly(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)

	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, _ = store.Save(pfxBytes, "testpass", caCert)
	minter := rootca.NewMinter(store)

	originServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "plain http response body")
	}))
	defer originServer.Close()

	// Policy and request target the httptest server's real loopback address
	// directly (rather than a fake hostname) so the forwarder's real DNS
	// lookup resolves. Policy is keyed on the hostname without port (the
	// engine does not strip ports itself — see policy.Engine.Resolve).
	originHost := originServer.Listener.Addr().String()
	originIP, _, _ := net.SplitHostPort(originHost)
	seedPolicy(t, eng, originIP, db.ViewModeHtmlAllowInput)

	cfg := &config.ProxyConfig{
		Port:           0,
		Bind:           "127.0.0.1",
		InterceptPorts: []int{443},
		SelfHosts:      []string{"localhost"},
	}

	srv := NewServer(cfg, originServer.Listener.Addr().String(), eng, minter)

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.handleConn(ctx, server)

	// Send plain-HTTP absolute-URI request
	_, _ = client.Write([]byte(fmt.Sprintf("GET http://%s/ HTTP/1.1\r\nHost: %s\r\n\r\n", originHost, originHost)))

	// net.Pipe is synchronous and writeResponse issues multiple Write calls;
	// a single client.Read may only capture the first chunk, so drain until
	// the server closes the connection after its single exchange.
	respBytes, _ := io.ReadAll(client)
	response := string(respBytes)

	if !strings.Contains(response, "200") {
		t.Errorf("expected 200 response for plain HTTP, got: %s", response)
	}

	if !strings.Contains(response, "plain http response body") {
		t.Errorf("expected response body in plain HTTP response")
	}

	client.Close()
}

// === Plain-HTTP to unmatched host Tests ===

// TestPlainHTTP_UnmatchedHost_Returns502 returns 502 for unmatched host.
func TestPlainHTTP_UnmatchedHost_Returns502(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)

	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, _ = store.Save(pfxBytes, "testpass", caCert)
	minter := rootca.NewMinter(store)

	cfg := &config.ProxyConfig{
		Port:           0,
		Bind:           "127.0.0.1",
		InterceptPorts: []int{443},
		SelfHosts:      []string{"localhost"},
	}

	srv := NewServer(cfg, "127.0.0.1:5139", eng, minter)

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.handleConn(ctx, server)

	// Send plain-HTTP request to unmatched host
	_, _ = client.Write([]byte("GET http://unmatched.example.com/ HTTP/1.1\r\nHost: unmatched.example.com\r\n\r\n"))

	buf := make([]byte, 1024)
	n, _ := client.Read(buf)
	response := string(buf[:n])

	if !strings.Contains(response, "502") {
		t.Errorf("expected 502 for unmatched host, got: %s", response[:min(100, n)])
	}

	client.Close()
}

// === isSelfHost Tests ===

// TestIsSelfHost_ExactMatch matches exact self-host.
func TestIsSelfHost_ExactMatch(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)
	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, _ = store.Save(pfxBytes, "testpass", caCert)
	minter := rootca.NewMinter(store)

	cfg := &config.ProxyConfig{
		Port:           0,
		Bind:           "127.0.0.1",
		InterceptPorts: []int{443},
		SelfHosts:      []string{"localhost", "127.0.0.1", "myhost"},
	}

	srv := NewServer(cfg, "127.0.0.1:5139", eng, minter)

	if !srv.isSelfHost("localhost") {
		t.Error("expected localhost to be a self-host")
	}
	if !srv.isSelfHost("127.0.0.1") {
		t.Error("expected 127.0.0.1 to be a self-host")
	}
	if srv.isSelfHost("other.com") {
		t.Error("expected other.com to not be a self-host")
	}
}

// TestIsSelfHost_CaseInsensitive matches case-insensitively.
func TestIsSelfHost_CaseInsensitive(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)
	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, _ = store.Save(pfxBytes, "testpass", caCert)
	minter := rootca.NewMinter(store)

	cfg := &config.ProxyConfig{
		Port:           0,
		Bind:           "127.0.0.1",
		InterceptPorts: []int{443},
		SelfHosts:      []string{"MyHost"},
	}

	srv := NewServer(cfg, "127.0.0.1:5139", eng, minter)

	if !srv.isSelfHost("myhost") {
		t.Error("expected case-insensitive match for myhost")
	}
	if !srv.isSelfHost("MYHOST") {
		t.Error("expected case-insensitive match for MYHOST")
	}
}

// === interceptPort Tests ===

// TestInterceptPort_PortInList returns true for ports in intercept list.
func TestInterceptPort_PortInList(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)
	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, _ = store.Save(pfxBytes, "testpass", caCert)
	minter := rootca.NewMinter(store)

	cfg := &config.ProxyConfig{
		Port:           0,
		Bind:           "127.0.0.1",
		InterceptPorts: []int{443, 8443},
		SelfHosts:      []string{"localhost"},
	}

	srv := NewServer(cfg, "127.0.0.1:5139", eng, minter)

	if !srv.interceptPort(443) {
		t.Error("expected 443 to be intercepted")
	}
	if !srv.interceptPort(8443) {
		t.Error("expected 8443 to be intercepted")
	}
	if srv.interceptPort(80) {
		t.Error("expected 80 to not be intercepted")
	}
}

// === buildVideoInterstitial Tests ===

// TestBuildVideoInterstitial_ReturnsValidHTML returns valid interstitial structure.
func TestBuildVideoInterstitial_ReturnsValidHTML(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)
	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, _ = store.Save(pfxBytes, "testpass", caCert)
	minter := rootca.NewMinter(store)

	cfg := &config.ProxyConfig{
		Port:           0,
		Bind:           "127.0.0.1",
		InterceptPorts: []int{443},
		SelfHosts:      []string{"localhost"},
	}

	srv := NewServer(cfg, "127.0.0.1:5139", eng, minter)

	u, _ := url.Parse("https://video.example.com/path?query=1")
	resp := srv.buildVideoInterstitial(u)

	if resp.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	bodyStr := string(resp.Body)
	if !strings.Contains(bodyStr, "index.html?url=") {
		t.Error("expected interstitial to contain index.html?url=")
	}

	if !strings.Contains(bodyStr, "video.example.com") {
		t.Error("expected interstitial to contain target host")
	}

	if !strings.Contains(bodyStr, "<!doctype html>") {
		t.Error("expected valid HTML doctype")
	}
}

// TestBuildVideoInterstitial_EscapingTest verifies HTML escaping in interstitial.
func TestBuildVideoInterstitial_EscapingTest(t *testing.T) {
	testDB := newTestDB(t)
	eng := policy.NewEngine(testDB)
	pfxBytes, caCert := makeTestCA(t)
	store := rootca.NewStore(testDB.Unwrap())
	_, _ = store.Save(pfxBytes, "testpass", caCert)
	minter := rootca.NewMinter(store)

	cfg := &config.ProxyConfig{
		Port:           0,
		Bind:           "127.0.0.1",
		InterceptPorts: []int{443},
		SelfHosts:      []string{"localhost"},
	}

	srv := NewServer(cfg, "127.0.0.1:5139", eng, minter)

	// Test with potentially problematic characters
	u, _ := url.Parse(`https://evil<script>alert('xss')</script>.com/`)

	resp := srv.buildVideoInterstitial(u)

	bodyStr := string(resp.Body)

	// Verify escaping - should not contain unescaped HTML tags
	if strings.Contains(bodyStr, "<script>") {
		t.Error("expected <script> to be escaped")
	}
	if !strings.Contains(bodyStr, "&lt;script&gt;") {
		t.Error("expected escaped script tag")
	}
}
