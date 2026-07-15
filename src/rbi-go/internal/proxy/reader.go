// Package proxy implements the TLS-intercepting forward proxy. It binds a raw
// TCP listener (not the net/http server), parses CONNECT and plain-HTTP
// absolute-URI proxy requests, enforces site policy, mints leaf TLS certs, and
// forwards requests to origin — matching the C# TlsInterceptingProxyServer.
package proxy

import (
	"io"
)

// streamReader is a minimal buffered reader over a raw io.Reader, purpose-built
// for parsing HTTP/1.1 request lines, headers, and chunked bodies without
// depending on net/http (which cannot "accept a plain CONNECT line, then start
// TLS on the same socket" — the actual forward-proxy protocol). Mirrors the C#
// ProxyStreamReader: buffers for efficiency and exposes DrainBuffered so a
// caller that decides mid-read to blind-tunnel can hand unconsumed bytes to the
// origin connection before splicing.
type streamReader struct {
	r        io.Reader
	buf      [8192]byte
	bufStart int
	bufEnd   int
}

// newStreamReader wraps r in a streamReader.
func newStreamReader(r io.Reader) *streamReader {
	return &streamReader{r: r}
}

// readLine reads one CRLF- or bare-LF-terminated line, consuming the newline
// but not including it in the result. Returns ("", io.EOF) on a clean EOF
// before any byte of this line is read; returns (partial, nil) if the stream
// closes mid-line (mirrors C# ProxyStreamReader.ReadLineAsync null-on-clean-EOF
// / partial-on-mid-line behaviour).
func (r *streamReader) readLine() (string, error) {
	var line []byte
	for {
		b, err := r.readByte()
		if err != nil {
			if err == io.EOF && len(line) == 0 {
				return "", io.EOF
			}
			// Mid-line EOF: return what we have.
			return string(line), nil
		}
		if b == '\n' {
			// Trim trailing CR if present.
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			return string(line), nil
		}
		line = append(line, b)
	}
}

// readExact reads exactly n bytes, blocking until n bytes are available or the
// stream closes. If the stream closes early the returned slice is shorter than
// n; callers that need an exact-length guarantee must check len(result).
// Mirrors C# ProxyStreamReader.ReadExactAsync.
func (r *streamReader) readExact(n int) ([]byte, error) {
	result := make([]byte, n)
	filled := 0
	for filled < n {
		if r.bufStart == r.bufEnd {
			if err := r.fill(); err != nil {
				// Return what we managed to read so far.
				return result[:filled], err
			}
			if r.bufStart == r.bufEnd {
				break // EOF
			}
		}
		take := n - filled
		if avail := r.bufEnd - r.bufStart; avail < take {
			take = avail
		}
		copy(result[filled:], r.buf[r.bufStart:r.bufStart+take])
		r.bufStart += take
		filled += take
	}
	if filled == n {
		return result, nil
	}
	return result[:filled], nil
}

// readByte reads one byte from the buffered reader.
func (r *streamReader) readByte() (byte, error) {
	if r.bufStart == r.bufEnd {
		if err := r.fill(); err != nil {
			return 0, err
		}
		if r.bufStart == r.bufEnd {
			return 0, io.EOF
		}
	}
	b := r.buf[r.bufStart]
	r.bufStart++
	return b, nil
}

// drainBuffered returns (and clears) any bytes already pulled from the
// underlying reader into the internal buffer but not yet consumed. Must be
// written to the origin connection FIRST when switching to a raw splice so
// those bytes are not silently dropped. Mirrors C# ProxyStreamReader.DrainBuffered.
func (r *streamReader) drainBuffered() []byte {
	if r.bufStart == r.bufEnd {
		return nil
	}
	data := make([]byte, r.bufEnd-r.bufStart)
	copy(data, r.buf[r.bufStart:r.bufEnd])
	r.bufStart = r.bufEnd
	return data
}

// fill reads a new batch from the underlying io.Reader into buf.
func (r *streamReader) fill() error {
	r.bufStart = 0
	n, err := r.r.Read(r.buf[:])
	r.bufEnd = n
	if n > 0 {
		return nil // data available; ignore any concurrent EOF
	}
	return err
}
