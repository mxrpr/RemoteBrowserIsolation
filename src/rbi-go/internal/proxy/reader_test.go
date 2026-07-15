package proxy

import (
	"bytes"
	"io"
	"testing"
)

// TestReadLine_CRLF reads a line terminated with CRLF.
func TestReadLine_CRLF(t *testing.T) {
	r := newStreamReader(bytes.NewReader([]byte("hello\r\n")))
	line, err := r.readLine()
	if err != nil {
		t.Fatalf("readLine error: %v", err)
	}
	if line != "hello" {
		t.Errorf("expected 'hello', got %q", line)
	}
}

// TestReadLine_LFOnly reads a line terminated with LF only (no CR).
func TestReadLine_LFOnly(t *testing.T) {
	r := newStreamReader(bytes.NewReader([]byte("hello\n")))
	line, err := r.readLine()
	if err != nil {
		t.Fatalf("readLine error: %v", err)
	}
	if line != "hello" {
		t.Errorf("expected 'hello', got %q", line)
	}
}

// TestReadLine_EOFClean returns ("", io.EOF) when the stream ends cleanly before any byte.
func TestReadLine_EOFClean(t *testing.T) {
	r := newStreamReader(bytes.NewReader([]byte("")))
	line, err := r.readLine()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
	if line != "" {
		t.Errorf("expected empty string, got %q", line)
	}
}

// TestReadLine_EOFMidLine returns the partial line when the stream closes mid-line.
func TestReadLine_EOFMidLine(t *testing.T) {
	r := newStreamReader(bytes.NewReader([]byte("incomplete")))
	line, err := r.readLine()
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if line != "incomplete" {
		t.Errorf("expected 'incomplete', got %q", line)
	}
}

// TestReadLine_EmptyLineCRLF reads an empty line (just CRLF).
func TestReadLine_EmptyLineCRLF(t *testing.T) {
	r := newStreamReader(bytes.NewReader([]byte("\r\n")))
	line, err := r.readLine()
	if err != nil {
		t.Fatalf("readLine error: %v", err)
	}
	if line != "" {
		t.Errorf("expected empty string, got %q", line)
	}
}

// TestReadLine_MultipleLines reads multiple lines in sequence.
func TestReadLine_MultipleLines(t *testing.T) {
	r := newStreamReader(bytes.NewReader([]byte("line1\r\nline2\r\nline3\r\n")))

	line1, err := r.readLine()
	if err != nil || line1 != "line1" {
		t.Errorf("line1: expected 'line1', got %q (err %v)", line1, err)
	}

	line2, err := r.readLine()
	if err != nil || line2 != "line2" {
		t.Errorf("line2: expected 'line2', got %q (err %v)", line2, err)
	}

	line3, err := r.readLine()
	if err != nil || line3 != "line3" {
		t.Errorf("line3: expected 'line3', got %q (err %v)", line3, err)
	}
}

// TestReadExact_Exact reads exactly the requested number of bytes.
func TestReadExact_Exact(t *testing.T) {
	r := newStreamReader(bytes.NewReader([]byte("hello")))
	data, err := r.readExact(5)
	if err != nil {
		t.Fatalf("readExact error: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("expected 'hello', got %q", string(data))
	}
}

// TestReadExact_ShortRead returns fewer bytes plus the underlying error when stream closes early.
func TestReadExact_ShortRead(t *testing.T) {
	r := newStreamReader(bytes.NewReader([]byte("hi")))
	data, err := r.readExact(5)
	if err == nil {
		t.Fatalf("expected an error on short stream, got nil")
	}
	if len(data) != 2 {
		t.Errorf("expected 2 bytes, got %d", len(data))
	}
	if string(data) != "hi" {
		t.Errorf("expected 'hi', got %q", string(data))
	}
}

// TestReadExact_Zero reads zero bytes.
func TestReadExact_Zero(t *testing.T) {
	r := newStreamReader(bytes.NewReader([]byte("hello")))
	data, err := r.readExact(0)
	if err != nil {
		t.Fatalf("readExact(0) error: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("expected 0 bytes, got %d", len(data))
	}
}

// TestDrainBuffered_AfterPartialRead drains buffered bytes after a partial read.
func TestDrainBuffered_AfterPartialRead(t *testing.T) {
	r := newStreamReader(bytes.NewReader([]byte("hello world")))

	// Read 5 bytes
	data, _ := r.readExact(5)
	if string(data) != "hello" {
		t.Errorf("readExact should have returned 'hello', got %q", string(data))
	}

	// Drain remaining buffered bytes
	leftover := r.drainBuffered()
	// The buffer contains " world" (6 bytes) after reading "hello"
	if string(leftover) != " world" {
		t.Errorf("expected ' world', got %q", string(leftover))
	}
}

// TestDrainBuffered_EmptyBuffer returns nil when buffer is empty.
func TestDrainBuffered_EmptyBuffer(t *testing.T) {
	r := newStreamReader(bytes.NewReader([]byte("")))

	leftover := r.drainBuffered()
	if leftover != nil {
		t.Errorf("expected nil, got %v", leftover)
	}
}

// TestDrainBuffered_FullyConsumedBuffer returns nil after all bytes are consumed.
func TestDrainBuffered_FullyConsumedBuffer(t *testing.T) {
	r := newStreamReader(bytes.NewReader([]byte("hi")))

	// Fully consume the buffer
	_, _ = r.readExact(2)

	// Drain should return nil since all bytes were consumed
	leftover := r.drainBuffered()
	if leftover != nil {
		t.Errorf("expected nil after consuming all bytes, got %v", leftover)
	}
}

// TestDrainBuffered_ClearsBuffer verifies that drainBuffered clears the internal buffer state.
func TestDrainBuffered_ClearsBuffer(t *testing.T) {
	r := newStreamReader(bytes.NewReader([]byte("hello")))

	// Read just one byte
	_, _ = r.readByte()

	// Drain the buffer
	leftover := r.drainBuffered()
	if string(leftover) != "ello" {
		t.Errorf("expected 'ello', got %q", string(leftover))
	}

	// Drain again should return nil since buffer was cleared
	leftover2 := r.drainBuffered()
	if leftover2 != nil {
		t.Errorf("expected nil on second drain, got %v", leftover2)
	}
}
