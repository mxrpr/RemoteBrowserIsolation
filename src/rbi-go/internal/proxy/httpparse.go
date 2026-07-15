package proxy

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// requestLine holds the three tokens of a parsed HTTP/1.1 request line.
// Mirrors the C# HttpMessageIO.RequestLine record.
type requestLine struct {
	Method      string
	Target      string
	HTTPVersion string
}

// parseRequestLine splits "METHOD target HTTP/x.y" into its three parts.
// Returns nil if the line does not have exactly three space-separated tokens
// or the last token does not start with "HTTP/". Mirrors C# HttpMessageIO.ParseRequestLine.
func parseRequestLine(line string) *requestLine {
	parts := strings.SplitN(line, " ", 3)
	if len(parts) != 3 {
		return nil
	}
	if !strings.HasPrefix(strings.ToUpper(parts[2]), "HTTP/") {
		return nil
	}
	return &requestLine{
		Method:      parts[0],
		Target:      parts[1],
		HTTPVersion: parts[2],
	}
}

// proxyHeader is one HTTP header name/value pair. A slice is used (not a map)
// because HTTP allows repeated header names and order can matter (e.g. Set-Cookie).
// Mirrors the C# ProxyHeader record.
type proxyHeader struct {
	Name  string
	Value string
}

// readHeaders reads HTTP/1.1 headers from r until a blank line, per RFC 7230 §3.2.
// Malformed lines (no colon, or colon at position 0) are silently skipped rather
// than aborting the whole request, matching C# HttpMessageIO.ReadHeadersAsync behaviour.
func readHeaders(r *streamReader) ([]proxyHeader, error) {
	var headers []proxyHeader
	for {
		line, err := r.readLine()
		if err != nil && err != io.EOF {
			return headers, err
		}
		if line == "" {
			break // blank line signals end of headers
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			continue // malformed — skip
		}
		headers = append(headers, proxyHeader{
			Name:  strings.TrimSpace(line[:colon]),
			Value: strings.TrimSpace(line[colon+1:]),
		})
		if err == io.EOF {
			break
		}
	}
	return headers, nil
}

// readBody reads the request/response body from r according to headers.
// Priority: Transfer-Encoding: chunked > Content-Length > no body (nil).
// Mirrors C# HttpMessageIO.ReadBodyAsync.
func readBody(r *streamReader, headers []proxyHeader) ([]byte, error) {
	// Check for chunked transfer encoding first.
	for _, h := range headers {
		if strings.EqualFold(h.Name, "Transfer-Encoding") &&
			strings.Contains(strings.ToLower(h.Value), "chunked") {
			return readChunkedBody(r)
		}
	}

	// Fall back to Content-Length.
	for _, h := range headers {
		if strings.EqualFold(h.Name, "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(h.Value))
			if err != nil || n <= 0 {
				return nil, nil
			}
			data, err := r.readExact(n)
			if err != nil && err != io.EOF {
				return data, err
			}
			return data, nil
		}
	}

	return nil, nil
}

// readChunkedBody reads a chunked-transfer-encoded body, reassembling the
// chunks into a single flat byte slice. Chunk extensions (";name=value") are
// parsed but ignored. Trailers after the zero-length chunk are consumed and
// discarded (unsupported per the plan's non-goals). Mirrors C# ReadChunkedBodyAsync.
func readChunkedBody(r *streamReader) ([]byte, error) {
	var body []byte
	for {
		sizeLine, err := r.readLine()
		if err != nil && err != io.EOF {
			return body, err
		}
		if sizeLine == "" {
			break
		}
		// Strip chunk extensions.
		sizeToken := strings.SplitN(sizeLine, ";", 2)[0]
		sizeToken = strings.TrimSpace(sizeToken)
		chunkSize64, parseErr := strconv.ParseInt(sizeToken, 16, 64)
		if parseErr != nil || chunkSize64 < 0 {
			break
		}
		if chunkSize64 == 0 {
			// Terminal zero-length chunk. Consume the trailing CRLF.
			_, _ = r.readLine()
			break
		}
		chunk, readErr := r.readExact(int(chunkSize64))
		body = append(body, chunk...)
		// Consume the CRLF that follows each chunk's data.
		_, _ = r.readLine()
		if readErr != nil && readErr != io.EOF {
			return body, readErr
		}
	}
	return body, nil
}

// proxyResponse is a fully-buffered HTTP/1.1 response ready to be written back
// to the client tunnel. Mirrors the C# ProxyHttpResponse record.
type proxyResponse struct {
	StatusCode   int
	ReasonPhrase string
	Headers      []proxyHeader
	Body         []byte
}

// hopByHopAlways is the set of headers that are always hop-by-hop (never
// forwarded across a proxy hop), per RFC 7230 §6.1 and the C# ProxyHeaders class.
var hopByHopAlways = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
}

// isHopByHop returns true if headerName must not be forwarded across this proxy
// hop — either because it is in the always-hop-by-hop set, or because it was
// explicitly listed in one of the Connection header values in the message.
// Mirrors the C# ProxyHeaders.IsHopByHop method exactly.
func isHopByHop(name string, connectionValues []string) bool {
	if hopByHopAlways[strings.ToLower(name)] {
		return true
	}
	lower := strings.ToLower(name)
	for _, cv := range connectionValues {
		for _, token := range strings.Split(cv, ",") {
			if strings.TrimSpace(strings.ToLower(token)) == lower {
				return true
			}
		}
	}
	return false
}

// collectConnectionValues gathers all values from Connection headers in the
// given header list; used to determine per-message hop-by-hop headers per
// RFC 7230 §6.1.
func collectConnectionValues(headers []proxyHeader) []string {
	var vals []string
	for _, h := range headers {
		if strings.EqualFold(h.Name, "Connection") {
			vals = append(vals, h.Value)
		}
	}
	return vals
}

// writeResponse serialises a proxyResponse to w as a valid HTTP/1.1 response:
// status line, hop-by-hop-stripped headers, an explicit Content-Length, and
// "Connection: close" (always close after one exchange per the C# design).
// Mirrors C# HttpMessageIO.WriteResponseAsync.
func writeResponse(w io.Writer, resp *proxyResponse) error {
	reason := resp.ReasonPhrase
	if reason == "" {
		reason = "OK"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("HTTP/1.1 %d %s\r\n", resp.StatusCode, reason))

	connVals := collectConnectionValues(resp.Headers)
	for _, h := range resp.Headers {
		if isHopByHop(h.Name, connVals) {
			continue
		}
		if strings.EqualFold(h.Name, "Content-Length") {
			continue // rewritten below with actual body length
		}
		sb.WriteString(h.Name)
		sb.WriteString(": ")
		sb.WriteString(h.Value)
		sb.WriteString("\r\n")
	}

	sb.WriteString(fmt.Sprintf("Content-Length: %d\r\n", len(resp.Body)))
	// Always close after one exchange — same rationale as C# (avoids needing
	// unambiguous request/response boundary tracking across multiple exchanges).
	sb.WriteString("Connection: close\r\n")
	sb.WriteString("\r\n")

	if _, err := io.WriteString(w, sb.String()); err != nil {
		return err
	}
	if len(resp.Body) > 0 {
		_, err := w.Write(resp.Body)
		return err
	}
	return nil
}

// writeRaw writes a raw ASCII string (typically a short status line like
// "HTTP/1.1 200 Connection Established\r\n\r\n") directly to w.
// Mirrors C# TlsInterceptingProxyServer.WriteRawAsync.
func writeRaw(w io.Writer, text string) error {
	_, err := io.WriteString(w, text)
	return err
}

// parseHostPort splits a CONNECT target of the form "host:port" into its
// components. Falls back to defaultPort if no port is present or the port
// portion is not a valid integer. Handles IPv6 bracket-literal hosts
// ("[::1]:443"). Mirrors C# TlsInterceptingProxyServer.ParseHostPort.
func parseHostPort(target string, defaultPort int) (host string, port int) {
	lastColon := strings.LastIndexByte(target, ':')
	if lastColon > 0 {
		if p, err := strconv.Atoi(target[lastColon+1:]); err == nil {
			return target[:lastColon], p
		}
	}
	return target, defaultPort
}
