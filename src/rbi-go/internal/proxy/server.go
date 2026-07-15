package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"strings"

	"rbi-go/internal/config"
	"rbi-go/internal/db"
	"rbi-go/internal/htmlinject"
	"rbi-go/internal/policy"
	"rbi-go/internal/rootca"
)

// Server is the TLS-intercepting forward proxy. It binds a raw TCP listener
// (not the net/http server), dispatches CONNECT and plain-HTTP absolute-URI
// requests, enforces site policy, mints leaf TLS certificates, and forwards
// requests to origin. Registered in cmd/server/main.go and started alongside
// the HTTP server. Mirrors the C# TlsInterceptingProxyServer.
type Server struct {
	// cfg is the proxy-specific config section (port, bind, intercept ports, self-hosts).
	cfg *config.ProxyConfig
	// selfOriginBaseUrl is the base URL of the HTTP server (e.g. "http://localhost:5139"),
	// used to build the video-mode interstitial link. Resolved once from the actual
	// bound HTTP server address passed at construction, matching C#'s ResolveSelfOriginPortAsync.
	selfOriginBaseUrl string
	// httpPort is the actual bound port of the HTTP server, used to dial self-origin
	// blind-tunnel connections.
	httpPort int
	// policyEng resolves per-host policy (deny-by-default, longest-match).
	policyEng *policy.Engine
	// minter mints short-lived RSA leaf certificates signed by the active root CA.
	minter *rootca.Minter
	// fwd forwards parsed browser requests to their origin.
	fwd *originForwarder
}

// NewServer constructs a Server. httpServerAddr is the actual bound address of
// the co-located HTTP server (e.g. "0.0.0.0:5139" or "[::]:5139") — its port
// is used for self-origin blind tunnelling and its normalised form (localhost)
// is embedded in the video interstitial link. eng and minter must outlive the
// Server (both are singletons in main.go).
func NewServer(
	cfg *config.ProxyConfig,
	httpServerAddr string,
	eng *policy.Engine,
	minter *rootca.Minter,
) *Server {
	// Normalise the HTTP server address to a client-facing base URL.
	// Mirrors C# ResolveSelfOriginPortAsync: wildcard bind hosts (0.0.0.0, [::],
	// ::) are substituted with "localhost" since they cannot be used as a link
	// target and are not in SelfHosts.
	host, portStr, err := net.SplitHostPort(httpServerAddr)
	if err != nil || host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "localhost"
	}
	httpPort := 0
	if p, err := net.LookupPort("tcp", portStr); err == nil {
		httpPort = p
	}
	selfOriginBaseUrl := fmt.Sprintf("http://%s:%s", host, portStr)

	return &Server{
		cfg:               cfg,
		selfOriginBaseUrl: selfOriginBaseUrl,
		httpPort:          httpPort,
		policyEng:         eng,
		minter:            minter,
		fwd:               newOriginForwarder(),
	}
}

// Run binds the configured TCP listener and serves connections until ctx is
// cancelled. Returns only after the listener has been closed and all
// already-dispatched per-connection goroutines have been given their
// cancellation signal (they check ctx on blocking I/O). Mirrors the C#
// TlsInterceptingProxyServer.ExecuteAsync accept loop.
func (s *Server) Run(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Bind, s.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("proxy: listen %s: %w", addr, err)
	}
	slog.Info("TLS-intercepting proxy listening", "addr", addr)

	// Close the listener when ctx is cancelled so AcceptTCP unblocks.
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil // normal shutdown
			default:
				return fmt.Errorf("proxy: accept: %w", err)
			}
		}
		// Fire-and-forget per connection, matching the C# async fire-and-forget
		// pattern used by WebRtcSessionManager.
		go s.handleConn(ctx, conn)
	}
}

// handleConn is the top-level per-connection dispatcher: reads the first
// request line to decide whether this is a CONNECT (HTTPS interception path)
// or an absolute-URI request (plain-HTTP proxying path). An unrecognised first
// line is dropped — a browser configured with an HTTP proxy only ever sends one
// of these two forms. Mirrors C# HandleConnectionAsync.
func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	clientIP := ""
	if addr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		clientIP = addr.IP.String()
	}

	r := newStreamReader(conn)
	firstLine, err := r.readLine()
	if err != nil || firstLine == "" {
		return
	}

	rl := parseRequestLine(firstLine)
	if rl == nil {
		return
	}

	if strings.EqualFold(rl.Method, "CONNECT") {
		s.handleConnect(ctx, conn, r, rl, clientIP)
		return
	}

	// Plain-HTTP absolute-URI path: the target must be an absolute http:// or
	// https:// URL.
	parsed, parseErr := url.Parse(rl.Target)
	if parseErr == nil &&
		(strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) {
		s.handlePlainHTTP(ctx, conn, r, rl, parsed, clientIP)
		return
	}
	// Neither CONNECT nor absolute-URI — drop.
}

// handleConnect handles "CONNECT host:port HTTP/1.1" — self-origin bypass,
// blind tunnel for non-intercepted ports, or policy-check + TLS-intercept for
// everything else. Mirrors C# HandleConnectAsync.
func (s *Server) handleConnect(ctx context.Context, conn net.Conn, r *streamReader, rl *requestLine, clientIP string) {
	host, port := parseHostPort(rl.Target, 443)

	// 1. Self-host: tunnel straight to the HTTP server's own port.
	if s.isSelfHost(host) {
		s.blindTunnelToSelf(ctx, conn, r, true, rl, nil)
		return
	}

	// 2. Non-intercept port: blind tunnel without TLS termination.
	if !s.interceptPort(port) {
		s.blindTunnelToOrigin(ctx, conn, r, host, port, true)
		return
	}

	// 3. Policy check.
	probeURL := fmt.Sprintf("https://%s/", host)
	mode, err := s.policyEng.Resolve(host)
	if err != nil {
		slog.Error("proxy: policy resolve", "host", host, "err", err)
		_ = writeRaw(conn, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n")
		return
	}
	if mode == nil {
		// No matching policy — deny.
		s.logRequest(probeURL, host, "deny", false, clientIP)
		_ = writeRaw(conn, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n")
		return
	}

	// Allowed: ack the CONNECT, then start TLS interception.
	s.logRequest(probeURL, host, mode.String(), true, clientIP)
	if err := writeRaw(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}

	// 4. TLS handshake with a minted leaf cert, ALPN limited to http/1.1 only
	//    (no h2 — parsing HTTP/2 frames is out of scope). Mirrors C# HandleConnectAsync.
	tlsCfg := &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			// SNI is authoritative; fall back to CONNECT host if no SNI sent.
			sni := hello.ServerName
			if sni == "" {
				sni = host
			}
			cert, mintErr := s.minter.GetOrMint(sni)
			if mintErr != nil {
				return nil, mintErr
			}
			if cert == nil {
				return nil, fmt.Errorf("proxy: no leaf cert for %q — is a root CA configured?", sni)
			}
			return cert, nil
		},
		NextProtos: []string{"http/1.1"},
	}

	tlsConn := tls.Server(conn, tlsCfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		slog.Warn("proxy: TLS handshake failed", "host", host, "err", err)
		return
	}
	defer tlsConn.Close()

	s.processSingleExchange(ctx, tlsConn, host, *mode, clientIP, "https")
}

// handlePlainHTTP handles a plain-HTTP absolute-URI proxy request (e.g.
// "GET http://host/path HTTP/1.1", no CONNECT). Same self-origin and policy
// logic as the CONNECT path, minus any TLS step. Mirrors C# HandlePlainHttpAsync.
func (s *Server) handlePlainHTTP(ctx context.Context, conn net.Conn, r *streamReader, rl *requestLine, target *url.URL, clientIP string) {
	if s.isSelfHost(target.Hostname()) {
		s.blindTunnelToSelf(ctx, conn, r, false, rl, target)
		return
	}

	mode, err := s.policyEng.Resolve(target.Hostname())
	if err != nil {
		slog.Error("proxy: policy resolve", "host", target.Hostname(), "err", err)
		_ = writeRaw(conn, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n")
		return
	}
	if mode == nil {
		s.logRequest(target.String(), target.Hostname(), "deny", false, clientIP)
		_ = writeRaw(conn, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n")
		return
	}

	// Read headers + body off the plain connection.
	headers, err := readHeaders(r)
	if err != nil {
		return
	}
	body, err := readBody(r, headers)
	if err != nil {
		return
	}

	resp, err := s.buildResponse(ctx, rl.Method, target, headers, body, *mode)
	if err != nil {
		slog.Error("proxy: build response", "url", target.String(), "err", err)
		_ = writeRaw(conn, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n")
		return
	}
	s.logRequest(target.String(), target.Hostname(), mode.String(), true, clientIP)
	if err := writeResponse(conn, resp); err != nil {
		slog.Warn("proxy: write plain-http response", "err", err)
	}
}

// processSingleExchange reads exactly one HTTP/1.1 request off an already-established
// tunnel (post-CONNECT TLS stream or plain-HTTP conn) and responds to it, then
// the caller closes the connection. Single-exchange design matches C# (avoids
// needing unambiguous request/response boundary tracking across multiple exchanges).
// Mirrors C# ProcessSingleExchangeAsync.
func (s *Server) processSingleExchange(ctx context.Context, tunnel io.ReadWriter, host string, mode db.ViewMode, clientIP, scheme string) {
	r := newStreamReader(tunnel)
	line, err := r.readLine()
	if err != nil || line == "" {
		return
	}
	rl := parseRequestLine(line)
	if rl == nil {
		return
	}

	headers, err := readHeaders(r)
	if err != nil {
		return
	}
	body, err := readBody(r, headers)
	if err != nil {
		return
	}

	// Requests inside a CONNECT tunnel use origin-form paths ("/path?q"), not
	// absolute-URI — reconstruct the full URL from the CONNECT host + this path.
	path := rl.Target
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	rawURL := fmt.Sprintf("%s://%s%s", scheme, host, path)
	targetURL, err := url.Parse(rawURL)
	if err != nil {
		return
	}

	resp, err := s.buildResponse(ctx, rl.Method, targetURL, headers, body, mode)
	if err != nil {
		slog.Error("proxy: build response in tunnel", "url", rawURL, "err", err)
		_ = writeRaw(tunnel, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n")
		return
	}
	s.logRequest(rawURL, host, mode.String(), true, clientIP)
	if err := writeResponse(tunnel, resp); err != nil {
		slog.Warn("proxy: write tunnel response", "err", err)
	}
}

// buildResponse selects the appropriate response for a given ViewMode: video
// modes get the static interstitial regardless of the request; HTML modes are
// forwarded to origin, with HtmlNoInput additionally running the response body
// through htmlinject.Inject. ctx is forwarded to the origin HTTP request so
// mid-flight requests can be cancelled on shutdown. Mirrors C# BuildResponseAsync.
func (s *Server) buildResponse(ctx context.Context, method string, target *url.URL, headers []proxyHeader, body []byte, mode db.ViewMode) (*proxyResponse, error) {
	if mode == db.ViewModeVideoAllowInput || mode == db.ViewModeVideoNoInput {
		return s.buildVideoInterstitial(target), nil
	}

	req := &proxyRequest{
		Method:  method,
		URL:     target.String(),
		Headers: headers,
		Body:    body,
	}
	resp, err := s.fwd.forward(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("buildResponse: forward: %w", err)
	}

	if mode != db.ViewModeHtmlNoInput {
		return resp, nil
	}

	// HtmlNoInput: check if response is HTML, then inject the no-input style.
	isHTML := false
	for _, h := range resp.Headers {
		if strings.EqualFold(h.Name, "Content-Type") &&
			strings.Contains(strings.ToLower(h.Value), "html") {
			isHTML = true
			break
		}
	}
	if !isHTML {
		return resp, nil
	}

	// Find Content-Encoding so htmlinject.Inject can decompress before parsing.
	// OriginForwarder does NOT auto-decompress (DisableCompression: true), so the
	// body may still be compressed at this point. htmlinject.Inject handles
	// decompression internally and always returns uncompressed bytes, so the
	// Content-Encoding header must be stripped from the response. Mirrors C#
	// BuildResponseAsync + DecompressBody flow.
	contentEncoding := ""
	for _, h := range resp.Headers {
		if strings.EqualFold(h.Name, "Content-Encoding") {
			contentEncoding = h.Value
			break
		}
	}

	injected, err := htmlinject.Inject(resp.Body, contentEncoding)
	if err != nil {
		// Non-fatal: log and return the original unmodified response rather than
		// serving a 502 for a minor injection failure.
		slog.Warn("proxy: htmlinject failed, returning original body", "url", target.String(), "err", err)
		return resp, nil
	}

	// Strip Content-Encoding because the injected body is no longer compressed.
	// Mirrors C# comment: "Content-Encoding must be dropped explicitly (unlike
	// Transfer-Encoding, it's an end-to-end header)".
	var filteredHeaders []proxyHeader
	for _, h := range resp.Headers {
		if strings.EqualFold(h.Name, "Content-Encoding") {
			continue
		}
		filteredHeaders = append(filteredHeaders, h)
	}

	return &proxyResponse{
		StatusCode:   resp.StatusCode,
		ReasonPhrase: resp.ReasonPhrase,
		Headers:      filteredHeaders,
		Body:         injected,
	}, nil
}

// buildVideoInterstitial returns the static interstitial HTML page for a
// VideoAllowInput/VideoNoInput host, linking to the WebRTC video viewer at
// index.html?url=<targetUrl>. Uses selfOriginBaseUrl resolved at construction
// time. Mirrors C# BuildVideoInterstitialResponseAsync.
func (s *Server) buildVideoInterstitial(target *url.URL) *proxyResponse {
	viewerURL := fmt.Sprintf("%s/index.html?url=%s", s.selfOriginBaseUrl, url.QueryEscape(target.String()))
	// HTML-encode only the characters that are unsafe inside href/text content.
	htmlEncTarget := htmlEscape(target.Host)
	htmlEncViewer := htmlEscape(viewerURL)
	html := "<!doctype html>\n" +
		"<html><head><meta charset=\"utf-8\"><title>Video mode required</title></head>\n" +
		"<body style=\"font-family:sans-serif;max-width:560px;margin:80px auto;text-align:center;\">\n" +
		"  <h1>This site is only viewable in video mode</h1>\n" +
		"  <p>Policy requires <strong>" + htmlEncTarget + "</strong> to be shown through the isolated video viewer, not directly in this browser.</p>\n" +
		"  <p><a href=\"" + htmlEncViewer + "\">Open in video viewer</a></p>\n" +
		"</body></html>\n"

	return &proxyResponse{
		StatusCode:   200,
		ReasonPhrase: "OK",
		Headers:      []proxyHeader{{Name: "Content-Type", Value: "text/html; charset=utf-8"}},
		Body:         []byte(html),
	}
}

// htmlEscape replaces the five characters that must be escaped inside HTML
// attribute values and text content (&, <, >, ", '). Equivalent to
// net/html.EscapeString — replicated here to avoid importing the html package
// (already used via htmlinject) for a trivial operation.
func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&#34;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}

// blindTunnelToOrigin connects to host:port and splices bytes bidirectionally.
// Used for non-intercepted ports where no TLS termination or policy check is
// needed. If writeConnectOk is true, sends "200 Connection Established" before
// splicing (CONNECT path); for plain-HTTP callers it is false (no CONNECT ACK).
// Mirrors C# BlindTunnelToOriginAsync.
func (s *Server) blindTunnelToOrigin(ctx context.Context, conn net.Conn, r *streamReader, host string, port int, writeConnectOk bool) {
	originAddr := fmt.Sprintf("%s:%d", host, port)
	origin, err := (&net.Dialer{}).DialContext(ctx, "tcp", originAddr)
	if err != nil {
		_ = writeRaw(conn, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n")
		return
	}
	defer origin.Close()

	if writeConnectOk {
		if err := writeRaw(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
	}

	// Flush any bytes already buffered in the streamReader to origin before splicing.
	if leftover := r.drainBuffered(); len(leftover) > 0 {
		_, _ = origin.Write(leftover)
	}

	splice(ctx, conn, origin)
}

// blindTunnelToSelf connects to 127.0.0.1 on the HTTP server's port and
// splices bytes bidirectionally, so that the browser's TLS negotiation (or
// plain HTTP exchange) goes directly to the HTTP server. Used when the
// CONNECT/absolute-URI target matches Config.Proxy.SelfHosts. No policy check
// and no TLS interception — required for the admin UI and video viewer to work
// when the browser has the proxy configured globally. Mirrors C#
// BlindTunnelToSelfOriginAsync.
func (s *Server) blindTunnelToSelf(ctx context.Context, conn net.Conn, r *streamReader, isConnect bool, rl *requestLine, absoluteURI *url.URL) {
	selfAddr := fmt.Sprintf("127.0.0.1:%d", s.httpPort)
	origin, err := (&net.Dialer{}).DialContext(ctx, "tcp", selfAddr)
	if err != nil {
		_ = writeRaw(conn, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n")
		return
	}
	defer origin.Close()

	if isConnect {
		// CONNECT: ACK the tunnel, then splice everything else through.
		if err := writeRaw(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
	} else {
		// Plain-HTTP absolute-URI: Kestrel/net/http expects origin-form ("GET /path
		// HTTP/1.1"), not the proxy's absolute-URI form — rewrite just this one line,
		// then splice the rest (headers/body) through unparsed.
		// Mirrors C# BlindTunnelToSelfOriginAsync plain-HTTP branch.
		path := absoluteURI.RequestURI() // includes query string
		originLine := fmt.Sprintf("%s %s %s\r\n", rl.Method, path, rl.HTTPVersion)
		if _, err := io.WriteString(origin, originLine); err != nil {
			return
		}
	}

	// Flush any bytes buffered by the streamReader before splicing.
	if leftover := r.drainBuffered(); len(leftover) > 0 {
		_, _ = origin.Write(leftover)
	}

	splice(ctx, conn, origin)
}

// splice pumps bytes in both directions between a and b until either side
// closes or ctx is cancelled. The normal way a tunnel ends is one side
// closing/resetting; those errors are swallowed. Works with any net.Conn
// implementation (including tls.Conn wrappers). Mirrors C# SpliceAsync.
func splice(ctx context.Context, a, b net.Conn) {
	done := make(chan struct{}, 2)
	copyHalf := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		done <- struct{}{}
	}

	go copyHalf(a, b)
	go copyHalf(b, a)

	select {
	case <-done:
	case <-ctx.Done():
	}
	// Close both sides so the other goroutine's io.Copy unblocks and exits.
	_ = a.Close()
	_ = b.Close()
	<-done // drain the second completion signal
}

// isSelfHost returns true if host (case-insensitive) matches any entry in
// Config.Proxy.SelfHosts. Mirrors C# TlsInterceptingProxyServer.IsSelfHost.
func (s *Server) isSelfHost(host string) bool {
	for _, self := range s.cfg.SelfHosts {
		if strings.EqualFold(host, self) {
			return true
		}
	}
	return false
}

// interceptPort returns true if port is in Config.Proxy.InterceptPorts.
func (s *Server) interceptPort(port int) bool {
	for _, p := range s.cfg.InterceptPorts {
		if p == port {
			return true
		}
	}
	return false
}

// logRequest writes one row to the RequestLogs table. Errors are logged but
// treated as non-fatal so a DB hiccup does not interrupt the request flow.
// Mirrors C# IRequestLogService.LogAsync (via policy.WriteRequestLog).
func (s *Server) logRequest(rawURL, host, decision string, allowed bool, clientIP string) {
	if err := policy.WriteRequestLog(s.policyEng.SQLDB(), rawURL, host, decision, allowed, clientIP); err != nil {
		slog.Warn("proxy: write request log", "err", err)
	}
}
