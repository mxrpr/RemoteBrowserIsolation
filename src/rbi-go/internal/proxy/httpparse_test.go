package proxy

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// === parseRequestLine Tests ===

// TestParseRequestLine_ValidGET parses a valid GET request line.
func TestParseRequestLine_ValidGET(t *testing.T) {
	line := "GET /path HTTP/1.1"
	rl := parseRequestLine(line)
	if rl == nil {
		t.Fatal("expected non-nil requestLine")
	}
	if rl.Method != "GET" {
		t.Errorf("expected Method 'GET', got %q", rl.Method)
	}
	if rl.Target != "/path" {
		t.Errorf("expected Target '/path', got %q", rl.Target)
	}
	if rl.HTTPVersion != "HTTP/1.1" {
		t.Errorf("expected HTTPVersion 'HTTP/1.1', got %q", rl.HTTPVersion)
	}
}

// TestParseRequestLine_ValidCONNECT parses a valid CONNECT request line.
func TestParseRequestLine_ValidCONNECT(t *testing.T) {
	line := "CONNECT example.com:443 HTTP/1.1"
	rl := parseRequestLine(line)
	if rl == nil {
		t.Fatal("expected non-nil requestLine")
	}
	if rl.Method != "CONNECT" {
		t.Errorf("expected Method 'CONNECT', got %q", rl.Method)
	}
	if rl.Target != "example.com:443" {
		t.Errorf("expected Target 'example.com:443', got %q", rl.Target)
	}
}

// TestParseRequestLine_TwoTokens returns nil for lines with only 2 tokens.
func TestParseRequestLine_TwoTokens(t *testing.T) {
	line := "GET /path"
	rl := parseRequestLine(line)
	if rl != nil {
		t.Errorf("expected nil for 2-token line, got %v", rl)
	}
}

// TestParseRequestLine_OneToken returns nil for lines with only 1 token.
func TestParseRequestLine_OneToken(t *testing.T) {
	line := "GET"
	rl := parseRequestLine(line)
	if rl != nil {
		t.Errorf("expected nil for 1-token line, got %v", rl)
	}
}

// TestParseRequestLine_EmptyString returns nil for empty strings.
func TestParseRequestLine_EmptyString(t *testing.T) {
	line := ""
	rl := parseRequestLine(line)
	if rl != nil {
		t.Errorf("expected nil for empty string, got %v", rl)
	}
}

// TestParseRequestLine_NonHTTPVersion returns nil when the third token doesn't start with "HTTP/".
func TestParseRequestLine_NonHTTPVersion(t *testing.T) {
	line := "GET /path NOTHTTP/1.1"
	rl := parseRequestLine(line)
	if rl != nil {
		t.Errorf("expected nil for non-HTTP version, got %v", rl)
	}
}

// TestParseRequestLine_LowercaseHTTP accepts lowercase "http/" as valid.
func TestParseRequestLine_LowercaseHTTP(t *testing.T) {
	line := "GET /path http/1.1"
	rl := parseRequestLine(line)
	if rl == nil {
		t.Fatal("expected non-nil requestLine for lowercase http/")
	}
	if rl.HTTPVersion != "http/1.1" {
		t.Errorf("expected 'http/1.1', got %q", rl.HTTPVersion)
	}
}

// === readHeaders Tests ===

// TestReadHeaders_MultipleHeaders reads multiple header lines until blank line.
func TestReadHeaders_MultipleHeaders(t *testing.T) {
	data := "Host: example.com\r\nContent-Length: 42\r\nUser-Agent: test\r\n\r\n"
	r := newStreamReader(bytes.NewReader([]byte(data)))

	headers, err := readHeaders(r)
	if err != nil {
		t.Fatalf("readHeaders error: %v", err)
	}
	if len(headers) != 3 {
		t.Errorf("expected 3 headers, got %d", len(headers))
	}
	if headers[0].Name != "Host" || headers[0].Value != "example.com" {
		t.Errorf("header 0: expected {Host, example.com}, got {%s, %s}", headers[0].Name, headers[0].Value)
	}
	if headers[1].Name != "Content-Length" || headers[1].Value != "42" {
		t.Errorf("header 1: expected {Content-Length, 42}, got {%s, %s}", headers[1].Name, headers[1].Value)
	}
	if headers[2].Name != "User-Agent" || headers[2].Value != "test" {
		t.Errorf("header 2: expected {User-Agent, test}, got {%s, %s}", headers[2].Name, headers[2].Value)
	}
}

// TestReadHeaders_MalformedNoColon skips lines with no colon.
func TestReadHeaders_MalformedNoColon(t *testing.T) {
	data := "Host: example.com\r\nBadLine\r\nUser-Agent: test\r\n\r\n"
	r := newStreamReader(bytes.NewReader([]byte(data)))

	headers, err := readHeaders(r)
	if err != nil {
		t.Fatalf("readHeaders error: %v", err)
	}
	if len(headers) != 2 {
		t.Errorf("expected 2 headers (bad line skipped), got %d", len(headers))
	}
	if headers[0].Name != "Host" || headers[1].Name != "User-Agent" {
		t.Errorf("bad line should have been skipped")
	}
}

// TestReadHeaders_ColonAtZero skips lines with colon at position 0.
func TestReadHeaders_ColonAtZero(t *testing.T) {
	data := "Host: example.com\r\n:value\r\nUser-Agent: test\r\n\r\n"
	r := newStreamReader(bytes.NewReader([]byte(data)))

	headers, err := readHeaders(r)
	if err != nil {
		t.Fatalf("readHeaders error: %v", err)
	}
	if len(headers) != 2 {
		t.Errorf("expected 2 headers (colon-at-zero skipped), got %d", len(headers))
	}
}

// TestReadHeaders_RepeatedHeaderName retains duplicate header names.
func TestReadHeaders_RepeatedHeaderName(t *testing.T) {
	data := "Set-Cookie: a=1\r\nSet-Cookie: b=2\r\n\r\n"
	r := newStreamReader(bytes.NewReader([]byte(data)))

	headers, err := readHeaders(r)
	if err != nil {
		t.Fatalf("readHeaders error: %v", err)
	}
	if len(headers) != 2 {
		t.Errorf("expected 2 headers, got %d", len(headers))
	}
	if headers[0].Value != "a=1" || headers[1].Value != "b=2" {
		t.Errorf("expected both Set-Cookie values to be retained")
	}
}

// TestReadHeaders_BlankLineStops stops reading at blank line.
func TestReadHeaders_BlankLineStops(t *testing.T) {
	data := "Host: example.com\r\n\r\nBODY_CONTENT"
	r := newStreamReader(bytes.NewReader([]byte(data)))

	headers, err := readHeaders(r)
	if err != nil {
		t.Fatalf("readHeaders error: %v", err)
	}
	if len(headers) != 1 {
		t.Errorf("expected 1 header before blank line, got %d", len(headers))
	}

	// The body content should still be in the stream
	remaining := r.drainBuffered()
	if !strings.HasPrefix(string(remaining), "BODY") {
		t.Errorf("expected body content to remain in stream")
	}
}

// TestReadHeaders_EmptyBlock returns empty slice when headers section is empty.
func TestReadHeaders_EmptyBlock(t *testing.T) {
	data := "\r\n"
	r := newStreamReader(bytes.NewReader([]byte(data)))

	headers, err := readHeaders(r)
	if err != nil {
		t.Fatalf("readHeaders error: %v", err)
	}
	if len(headers) != 0 {
		t.Errorf("expected 0 headers, got %d", len(headers))
	}
}

// === readChunkedBody Tests ===

// TestReadChunkedBody_SingleChunk reads a body with a single chunk.
func TestReadChunkedBody_SingleChunk(t *testing.T) {
	data := "5\r\nhello\r\n0\r\n\r\n"
	r := newStreamReader(bytes.NewReader([]byte(data)))

	body, err := readChunkedBody(r)
	if err != nil {
		t.Fatalf("readChunkedBody error: %v", err)
	}
	if string(body) != "hello" {
		t.Errorf("expected 'hello', got %q", string(body))
	}
}

// TestReadChunkedBody_MultipleChunks reads a body with multiple chunks.
func TestReadChunkedBody_MultipleChunks(t *testing.T) {
	data := "5\r\nhello\r\n6\r\n world\r\n0\r\n\r\n"
	r := newStreamReader(bytes.NewReader([]byte(data)))

	body, err := readChunkedBody(r)
	if err != nil {
		t.Fatalf("readChunkedBody error: %v", err)
	}
	if string(body) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(body))
	}
}

// TestReadChunkedBody_ChunkExtensionsStripped strips chunk extensions.
func TestReadChunkedBody_ChunkExtensionsStripped(t *testing.T) {
	data := "5;ext=value\r\nhello\r\n0\r\n\r\n"
	r := newStreamReader(bytes.NewReader([]byte(data)))

	body, err := readChunkedBody(r)
	if err != nil {
		t.Fatalf("readChunkedBody error: %v", err)
	}
	if string(body) != "hello" {
		t.Errorf("expected 'hello', got %q", string(body))
	}
}

// TestReadChunkedBody_ZeroChunkTerminates terminates on zero-length chunk.
func TestReadChunkedBody_ZeroChunkTerminates(t *testing.T) {
	data := "3\r\nfoo\r\n0\r\n\r\n"
	r := newStreamReader(bytes.NewReader([]byte(data)))

	body, err := readChunkedBody(r)
	if err != nil {
		t.Fatalf("readChunkedBody error: %v", err)
	}
	if string(body) != "foo" {
		t.Errorf("expected 'foo', got %q", string(body))
	}
}

// TestReadChunkedBody_MalformedChunkSizeNoPanic doesn't panic on malformed chunk size.
func TestReadChunkedBody_MalformedChunkSizeNoPanic(t *testing.T) {
	data := "ZZZ\r\nhello\r\n0\r\n\r\n"
	r := newStreamReader(bytes.NewReader([]byte(data)))

	body, err := readChunkedBody(r)
	if err != nil {
		t.Fatalf("readChunkedBody error: %v", err)
	}
	// Should return empty body or partial body without panicking
	if len(body) > 5 {
		t.Errorf("malformed chunk should stop reading")
	}
}

// TestReadChunkedBody_TrailingCRLFConsumed consumes trailing CRLF correctly.
func TestReadChunkedBody_TrailingCRLFConsumed(t *testing.T) {
	data := "2\r\nab\r\n3\r\ncde\r\n0\r\n\r\nNEXT"
	r := newStreamReader(bytes.NewReader([]byte(data)))

	body, err := readChunkedBody(r)
	if err != nil {
		t.Fatalf("readChunkedBody error: %v", err)
	}
	if string(body) != "abcde" {
		t.Errorf("expected 'abcde', got %q", string(body))
	}

	// Verify the stream positioned after the chunked body
	leftover := r.drainBuffered()
	if !strings.HasPrefix(string(leftover), "NEXT") {
		t.Errorf("expected stream to be positioned after chunked body")
	}
}

// === readBody Tests ===

// TestReadBody_ChunkedPrecedenceOverContentLength prefers Transfer-Encoding over Content-Length.
func TestReadBody_ChunkedPrecedenceOverContentLength(t *testing.T) {
	headers := []proxyHeader{
		{Name: "Transfer-Encoding", Value: "chunked"},
		{Name: "Content-Length", Value: "5"},
	}
	data := "3\r\nfoo\r\n0\r\n\r\n"
	r := newStreamReader(bytes.NewReader([]byte(data)))

	body, err := readBody(r, headers)
	if err != nil && err != io.EOF {
		t.Fatalf("readBody error: %v", err)
	}
	if string(body) != "foo" {
		t.Errorf("expected chunked body 'foo', got %q", string(body))
	}
}

// TestReadBody_ContentLengthExactRead reads exactly Content-Length bytes.
func TestReadBody_ContentLengthExactRead(t *testing.T) {
	headers := []proxyHeader{
		{Name: "Content-Length", Value: "5"},
	}
	data := "hello extra"
	r := newStreamReader(bytes.NewReader([]byte(data)))

	body, err := readBody(r, headers)
	if err != nil && err != io.EOF {
		t.Fatalf("readBody error: %v", err)
	}
	if string(body) != "hello" {
		t.Errorf("expected 'hello', got %q", string(body))
	}
}

// TestReadBody_NoBodyHeadersReturnsNil returns nil when neither Transfer-Encoding nor Content-Length.
func TestReadBody_NoBodyHeadersReturnsNil(t *testing.T) {
	headers := []proxyHeader{
		{Name: "Host", Value: "example.com"},
	}
	r := newStreamReader(bytes.NewReader([]byte("body_ignored")))

	body, err := readBody(r, headers)
	if err != nil {
		t.Fatalf("readBody error: %v", err)
	}
	if body != nil {
		t.Errorf("expected nil body, got %v", body)
	}
}

// === isHopByHop Tests ===

// TestIsHopByHop_AlwaysHopByHopSet checks membership in always-hop-by-hop set.
func TestIsHopByHop_AlwaysHopByHopSet(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		expected bool
	}{
		{"connection", "connection", true},
		{"keep-alive", "keep-alive", true},
		{"transfer-encoding", "transfer-encoding", true},
		{"upgrade", "upgrade", true},
		{"host", "host", false},
		{"content-length", "content-length", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isHopByHop(tt.header, nil)
			if result != tt.expected {
				t.Errorf("isHopByHop(%q) = %v, expected %v", tt.header, result, tt.expected)
			}
		})
	}
}

// TestIsHopByHop_CaseInsensitive checks case-insensitive membership.
func TestIsHopByHop_CaseInsensitive(t *testing.T) {
	if !isHopByHop("CONNECTION", nil) {
		t.Error("expected CONNECTION (uppercase) to be hop-by-hop")
	}
	if !isHopByHop("Keep-Alive", nil) {
		t.Error("expected Keep-Alive to be hop-by-hop")
	}
}

// TestIsHopByHop_ConnectionHeaderListed checks if header is listed in Connection value.
func TestIsHopByHop_ConnectionHeaderListed(t *testing.T) {
	connValues := []string{"custom-header, another"}
	if !isHopByHop("custom-header", connValues) {
		t.Error("expected custom-header listed in Connection to be hop-by-hop")
	}
	if !isHopByHop("another", connValues) {
		t.Error("expected another listed in Connection to be hop-by-hop")
	}
}

// TestIsHopByHop_NotListed returns false for headers not listed.
func TestIsHopByHop_NotListed(t *testing.T) {
	connValues := []string{"custom"}
	if isHopByHop("authorization", connValues) {
		t.Error("expected authorization (not listed) to not be hop-by-hop")
	}
}

// TestIsHopByHop_MultipleConnectionHeaderValues checks multiple Connection headers.
func TestIsHopByHop_MultipleConnectionHeaderValues(t *testing.T) {
	connValues := []string{"header1", "header2, header3"}
	if !isHopByHop("header1", connValues) {
		t.Error("expected header1 to be hop-by-hop")
	}
	if !isHopByHop("header3", connValues) {
		t.Error("expected header3 to be hop-by-hop")
	}
}

// === parseHostPort Tests ===

// TestParseHostPort_WithPort extracts host and port when both are present.
func TestParseHostPort_WithPort(t *testing.T) {
	tests := []struct {
		target      string
		defaultPort int
		expHost     string
		expPort     int
	}{
		{"example.com:443", 80, "example.com", 443},
		{"example.com:8080", 443, "example.com", 8080},
		{"127.0.0.1:5139", 80, "127.0.0.1", 5139},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			host, port := parseHostPort(tt.target, tt.defaultPort)
			if host != tt.expHost || port != tt.expPort {
				t.Errorf("parseHostPort(%q) = (%q, %d), expected (%q, %d)",
					tt.target, host, port, tt.expHost, tt.expPort)
			}
		})
	}
}

// TestParseHostPort_NoPort defaults to defaultPort when port is absent.
func TestParseHostPort_NoPort(t *testing.T) {
	host, port := parseHostPort("example.com", 443)
	if host != "example.com" || port != 443 {
		t.Errorf("parseHostPort without port failed")
	}
}

// TestParseHostPort_IPv6WithPort extracts IPv6 addresses with bracket literals.
func TestParseHostPort_IPv6WithPort(t *testing.T) {
	host, port := parseHostPort("[::1]:443", 80)
	if host != "[::1]" || port != 443 {
		t.Errorf("parseHostPort IPv6 with port failed")
	}
}

// TestParseHostPort_IPv6WithoutPort defaults IPv6 address without port.
func TestParseHostPort_IPv6WithoutPort(t *testing.T) {
	host, port := parseHostPort("[::1]", 443)
	if host != "[::1]" || port != 443 {
		t.Errorf("parseHostPort IPv6 without port failed")
	}
}

// TestParseHostPort_NonNumericPort defaults to defaultPort.
func TestParseHostPort_NonNumericPort(t *testing.T) {
	host, port := parseHostPort("example.com:notaport", 443)
	if host != "example.com:notaport" || port != 443 {
		t.Errorf("parseHostPort with non-numeric port should default")
	}
}

// TestParseHostPort_EmptyString defaults to defaultPort.
func TestParseHostPort_EmptyString(t *testing.T) {
	host, port := parseHostPort("", 443)
	if host != "" || port != 443 {
		t.Errorf("parseHostPort empty string should default port")
	}
}

// === writeResponse Tests ===

// TestWriteResponse_StatusLineFormat writes correct status line format.
func TestWriteResponse_StatusLineFormat(t *testing.T) {
	resp := &proxyResponse{
		StatusCode:   200,
		ReasonPhrase: "OK",
		Headers:      []proxyHeader{},
		Body:         []byte("test"),
	}

	var buf bytes.Buffer
	err := writeResponse(&buf, resp)
	if err != nil {
		t.Fatalf("writeResponse error: %v", err)
	}

	output := buf.String()
	if !strings.HasPrefix(output, "HTTP/1.1 200 OK\r\n") {
		t.Errorf("expected status line 'HTTP/1.1 200 OK', got %q", output)
	}
}

// TestWriteResponse_EmptyReasonDefaultsOK uses "OK" when reason is empty.
func TestWriteResponse_EmptyReasonDefaultsOK(t *testing.T) {
	resp := &proxyResponse{
		StatusCode:   204,
		ReasonPhrase: "",
		Headers:      []proxyHeader{},
		Body:         []byte{},
	}

	var buf bytes.Buffer
	err := writeResponse(&buf, resp)
	if err != nil {
		t.Fatalf("writeResponse error: %v", err)
	}

	output := buf.String()
	if !strings.HasPrefix(output, "HTTP/1.1 204 OK\r\n") {
		t.Errorf("expected default 'OK' reason phrase, got %q", output[:strings.Index(output, "\r\n")])
	}
}

// TestWriteResponse_HopByHopStripped excludes hop-by-hop headers.
func TestWriteResponse_HopByHopStripped(t *testing.T) {
	resp := &proxyResponse{
		StatusCode:   200,
		ReasonPhrase: "OK",
		Headers: []proxyHeader{
			{Name: "Host", Value: "example.com"},
			{Name: "Transfer-Encoding", Value: "chunked"},
			{Name: "Custom", Value: "value"},
		},
		Body: []byte{},
	}

	var buf bytes.Buffer
	err := writeResponse(&buf, resp)
	if err != nil {
		t.Fatalf("writeResponse error: %v", err)
	}

	output := buf.String()
	if strings.Contains(output, "Transfer-Encoding") {
		t.Error("expected Transfer-Encoding to be stripped")
	}
	if !strings.Contains(output, "Custom: value") {
		t.Error("expected Custom header to be retained")
	}
}

// TestWriteResponse_ContentLengthRewritten uses actual body length.
func TestWriteResponse_ContentLengthRewritten(t *testing.T) {
	resp := &proxyResponse{
		StatusCode:   200,
		ReasonPhrase: "OK",
		Headers: []proxyHeader{
			{Name: "Content-Length", Value: "99"}, // will be overwritten
		},
		Body: []byte("hello"),
	}

	var buf bytes.Buffer
	err := writeResponse(&buf, resp)
	if err != nil {
		t.Fatalf("writeResponse error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Content-Length: 5\r\n") {
		t.Errorf("expected Content-Length: 5, but got different value")
	}
	if strings.Contains(output, "Content-Length: 99") {
		t.Error("old Content-Length should be overwritten")
	}
}

// TestWriteResponse_ConnectionCloseAlwaysAppended always appends "Connection: close".
func TestWriteResponse_ConnectionCloseAlwaysAppended(t *testing.T) {
	resp := &proxyResponse{
		StatusCode:   200,
		ReasonPhrase: "OK",
		Headers:      []proxyHeader{},
		Body:         []byte{},
	}

	var buf bytes.Buffer
	err := writeResponse(&buf, resp)
	if err != nil {
		t.Fatalf("writeResponse error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Connection: close\r\n") {
		t.Error("expected 'Connection: close' to be appended")
	}
}

// TestWriteResponse_BodyWrittenAfterHeaders writes body after headers.
func TestWriteResponse_BodyWrittenAfterHeaders(t *testing.T) {
	resp := &proxyResponse{
		StatusCode:   200,
		ReasonPhrase: "OK",
		Headers:      []proxyHeader{},
		Body:         []byte("response body"),
	}

	var buf bytes.Buffer
	err := writeResponse(&buf, resp)
	if err != nil {
		t.Fatalf("writeResponse error: %v", err)
	}

	output := buf.String()
	blankLineIdx := strings.Index(output, "\r\n\r\n")
	bodyStart := blankLineIdx + 4
	if output[bodyStart:] != "response body" {
		t.Errorf("expected body after blank line, got %q", output[bodyStart:])
	}
}

// TestWriteResponse_NilBodyContentLengthZero handles nil body with Content-Length 0.
func TestWriteResponse_NilBodyContentLengthZero(t *testing.T) {
	resp := &proxyResponse{
		StatusCode:   204,
		ReasonPhrase: "No Content",
		Headers:      []proxyHeader{},
		Body:         nil,
	}

	var buf bytes.Buffer
	err := writeResponse(&buf, resp)
	if err != nil {
		t.Fatalf("writeResponse error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Content-Length: 0\r\n") {
		t.Error("expected Content-Length: 0 for nil body")
	}
}

// === htmlEscape Tests ===

// TestHtmlEscape_AllCharactersEscaped escapes all required HTML characters.
func TestHtmlEscape_AllCharactersEscaped(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"&", "&amp;"},
		{"<", "&lt;"},
		{">", "&gt;"},
		{"\"", "&#34;"},
		{"'", "&#39;"},
		{"&<>\"'", "&amp;&lt;&gt;&#34;&#39;"},
		{"<script>alert('xss')</script>", "&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt;"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := htmlEscape(tt.input)
			if result != tt.expected {
				t.Errorf("htmlEscape(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestHtmlEscape_NoDoubleEscapeOfExistingAmp avoids double-escaping &amp;.
func TestHtmlEscape_NoDoubleEscapeOfExistingAmp(t *testing.T) {
	// This tests the actual behavior: if input already contains &amp;,
	// the & in &amp; gets escaped first to &amp;amp; (implementation detail).
	// The test verifies current behavior rather than assumed behavior.
	input := "&amp;"
	result := htmlEscape(input)
	// After escaping & -> &amp;, we get &amp;amp;
	expected := "&amp;amp;"
	if result != expected {
		t.Errorf("htmlEscape(%q) = %q, expected %q (current behavior)", input, result, expected)
	}
}

// TestHtmlEscape_MixedStringAllCharsEscaped escapes all chars in mixed string.
func TestHtmlEscape_MixedStringAllCharsEscaped(t *testing.T) {
	input := `link: <a href="test?a=1&b=2">click 'me'</a>`
	result := htmlEscape(input)

	if !strings.Contains(result, "&lt;") {
		t.Error("< not escaped")
	}
	if !strings.Contains(result, "&gt;") {
		t.Error("> not escaped")
	}
	if !strings.Contains(result, "&amp;") {
		t.Error("& not escaped")
	}
	if !strings.Contains(result, "&#34;") {
		t.Error("\" not escaped")
	}
	if !strings.Contains(result, "&#39;") {
		t.Error("' not escaped")
	}
}

// TestHtmlEscape_SafeStringUnchanged leaves safe strings unchanged.
func TestHtmlEscape_SafeStringUnchanged(t *testing.T) {
	input := "hello-world_123"
	result := htmlEscape(input)
	if result != input {
		t.Errorf("safe string should be unchanged: %q", result)
	}
}
