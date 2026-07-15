package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// originForwarder forwards a parsed proxy request to its actual origin server
// and returns the fully-buffered response. It uses a purpose-configured
// http.Client that:
//   - Does NOT follow redirects (relay 3xx back to the browser).
//   - Does NOT use a cookie jar (browser owns cookies; this just relays
//     Cookie/Set-Cookie as plain headers).
//   - Does NOT decompress responses (so Content-Encoding and body bytes remain
//     consistent — the htmlinject layer must decompress before parsing HTML if
//     needed).
//
// Mirrors the C# OriginForwarder / IOriginForwarder.
type originForwarder struct {
	client *http.Client
}

// newOriginForwarder constructs a forwarder with the correct transport settings.
func newOriginForwarder() *originForwarder {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true // do not decompress — relay body as-is

	client := &http.Client{
		Transport: transport,
		// Return 3xx as-is to the browser rather than following them.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &originForwarder{client: client}
}

// proxyRequest bundles a parsed browser request ready to be forwarded.
// Mirrors the C# ProxyHttpRequest record.
type proxyRequest struct {
	Method  string
	URL     string // absolute URL string
	Headers []proxyHeader
	Body    []byte
}

// forward sends req to its origin and returns the response. ctx is used to
// cancel the underlying HTTP request if the connection is closed mid-flight.
// On success the response body is fully read and buffered — callers may
// inspect/rewrite it before sending it back to the browser.
// Mirrors C# OriginForwarder.ForwardAsync.
func (f *originForwarder) forward(ctx context.Context, req *proxyRequest) (*proxyResponse, error) {
	var bodyReader io.Reader
	if len(req.Body) > 0 {
		bodyReader = bytes.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("forward: build request: %w", err)
	}

	connVals := collectConnectionValues(req.Headers)
	for _, h := range req.Headers {
		// Host is set implicitly from the URL by net/http; forwarding the
		// browser's original Host header explicitly would conflict.
		if strings.EqualFold(h.Name, "Host") {
			continue
		}
		if isHopByHop(h.Name, connVals) {
			continue
		}
		// In Go's net/http all headers go on req.Header directly — no need
		// for the C# split between message.Headers and message.Content.Headers.
		httpReq.Header.Add(h.Name, h.Value)
	}

	resp, err := f.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("forward: do request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("forward: read body: %w", err)
	}

	var headers []proxyHeader
	for name, values := range resp.Header {
		for _, v := range values {
			headers = append(headers, proxyHeader{Name: name, Value: v})
		}
	}

	reasonPhrase := ""
	if idx := strings.IndexByte(resp.Status, ' '); idx >= 0 {
		reasonPhrase = resp.Status[idx+1:]
	}

	return &proxyResponse{
		StatusCode:   resp.StatusCode,
		ReasonPhrase: reasonPhrase,
		Headers:      headers,
		Body:         body,
	}, nil
}
