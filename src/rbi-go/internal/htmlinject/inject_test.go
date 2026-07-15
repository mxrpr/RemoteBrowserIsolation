package htmlinject

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// TestInject_PlainHTMLNoCharsetMeta_PrependsMetaAndAppendsStyle verifies that
// plain HTML with no charset meta gets a meta charset="utf-8" prepended to head
// and the noInputStyleRule style appended to head.
func TestInject_PlainHTMLNoCharsetMeta_PrependsMetaAndAppendsStyle(t *testing.T) {
	body := []byte(`<!DOCTYPE html>
<html>
<head>
<title>Test</title>
</head>
<body>
<p>Hello World</p>
</body>
</html>`)

	result, err := Inject(body, "")
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	resultStr := string(result)
	if !strings.Contains(resultStr, `<meta charset="utf-8"`) {
		t.Error("expected charset meta in output")
	}
	if !strings.Contains(resultStr, noInputStyleRule) {
		t.Error("expected noInputStyleRule in output")
	}

	// Verify only one charset meta
	doc, err := html.Parse(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	head := findHead(doc)
	if head == nil {
		t.Fatal("expected head in output")
	}

	charsetCount := 0
	for c := head.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == atom.Meta {
			if hasAttr(c, "charset") {
				charsetCount++
			}
		}
	}
	if charsetCount != 1 {
		t.Errorf("expected exactly 1 charset meta, got %d", charsetCount)
	}
}

// TestInject_MetaCharsetAttribute_UpdatedToUTF8InPlace verifies that an existing
// <meta charset> tag is updated to utf-8 in place without creating duplicates.
func TestInject_MetaCharsetAttribute_UpdatedToUTF8InPlace(t *testing.T) {
	body := []byte(`<!DOCTYPE html>
<html>
<head>
<meta charset="windows-1252"/>
<title>Test</title>
</head>
<body>
<p>Hello World</p>
</body>
</html>`)

	result, err := Inject(body, "")
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	doc, err := html.Parse(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	head := findHead(doc)
	if head == nil {
		t.Fatal("expected head in output")
	}

	// Find the charset meta
	var charsetMeta *html.Node
	for c := head.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == atom.Meta {
			if hasAttr(c, "charset") {
				charsetMeta = c
				break
			}
		}
	}
	if charsetMeta == nil {
		t.Fatal("expected charset meta in output")
	}

	val := attrVal(charsetMeta, "charset")
	if val != "utf-8" {
		t.Errorf("expected charset=utf-8, got charset=%s", val)
	}

	// Verify no http-equiv or content attrs
	if hasAttr(charsetMeta, "http-equiv") {
		t.Error("expected no http-equiv attr")
	}
	if hasAttr(charsetMeta, "content") {
		t.Error("expected no content attr")
	}

	// Verify exactly one charset meta
	charsetCount := 0
	for c := head.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == atom.Meta {
			if hasAttr(c, "charset") {
				charsetCount++
			}
		}
	}
	if charsetCount != 1 {
		t.Errorf("expected exactly 1 charset meta, got %d", charsetCount)
	}
}

// TestInject_MetaHttpEquivContentType_ReplacedWithCleanCharsetMeta verifies that
// a <meta http-equiv="Content-Type"> tag is replaced with a clean charset meta.
func TestInject_MetaHttpEquivContentType_ReplacedWithCleanCharsetMeta(t *testing.T) {
	body := []byte(`<!DOCTYPE html>
<html>
<head>
<meta http-equiv="Content-Type" content="text/html; charset=iso-8859-1"/>
<title>Test</title>
</head>
<body>
<p>Hello World</p>
</body>
</html>`)

	result, err := Inject(body, "")
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	doc, err := html.Parse(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	head := findHead(doc)
	if head == nil {
		t.Fatal("expected head in output")
	}

	// Find the charset meta (should have replaced the http-equiv one)
	var charsetMeta *html.Node
	for c := head.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == atom.Meta {
			if hasAttr(c, "charset") {
				charsetMeta = c
				break
			}
		}
	}
	if charsetMeta == nil {
		t.Fatal("expected charset meta in output")
	}

	val := attrVal(charsetMeta, "charset")
	if val != "utf-8" {
		t.Errorf("expected charset=utf-8, got charset=%s", val)
	}

	// Verify no http-equiv or content attrs on the charset meta
	if hasAttr(charsetMeta, "http-equiv") {
		t.Error("expected no http-equiv attr")
	}
	if hasAttr(charsetMeta, "content") {
		t.Error("expected no content attr")
	}

	// Verify exactly one charset meta
	charsetCount := 0
	for c := head.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == atom.Meta {
			if hasAttr(c, "charset") {
				charsetCount++
			}
		}
	}
	if charsetCount != 1 {
		t.Errorf("expected exactly 1 charset meta, got %d", charsetCount)
	}
}

// TestInject_MetaCharsetWithExtraAttributes_PreservesExtraAttrs verifies that
// extra attributes on a charset meta (like id) are preserved when updating.
func TestInject_MetaCharsetWithExtraAttributes_PreservesExtraAttrs(t *testing.T) {
	body := []byte(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-16" id="csm"/>
<title>Test</title>
</head>
<body>
<p>Hello World</p>
</body>
</html>`)

	result, err := Inject(body, "")
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	doc, err := html.Parse(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	head := findHead(doc)
	if head == nil {
		t.Fatal("expected head in output")
	}

	// Find the charset meta
	var charsetMeta *html.Node
	for c := head.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == atom.Meta {
			if hasAttr(c, "charset") {
				charsetMeta = c
				break
			}
		}
	}
	if charsetMeta == nil {
		t.Fatal("expected charset meta in output")
	}

	// Check charset updated
	val := attrVal(charsetMeta, "charset")
	if val != "utf-8" {
		t.Errorf("expected charset=utf-8, got charset=%s", val)
	}

	// Check id preserved
	idVal := attrVal(charsetMeta, "id")
	if idVal != "csm" {
		t.Errorf("expected id=csm, got id=%s", idVal)
	}
}

// TestInject_MultipleMetaCharsetTags_OnlyFirstUpdated verifies that only the
// first charset meta is updated, and subsequent ones are left untouched.
func TestInject_MultipleMetaCharsetTags_OnlyFirstUpdated(t *testing.T) {
	body := []byte(`<!DOCTYPE html>
<html>
<head>
<meta charset="windows-1252"/>
<meta charset="iso-8859-1"/>
<title>Test</title>
</head>
<body>
<p>Hello World</p>
</body>
</html>`)

	result, err := Inject(body, "")
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	doc, err := html.Parse(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	head := findHead(doc)
	if head == nil {
		t.Fatal("expected head in output")
	}

	// Find all charset metas
	charsetMetas := []*html.Node{}
	for c := head.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == atom.Meta {
			if hasAttr(c, "charset") {
				charsetMetas = append(charsetMetas, c)
			}
		}
	}

	if len(charsetMetas) < 2 {
		t.Fatalf("expected at least 2 charset metas, got %d", len(charsetMetas))
	}

	// First one should be utf-8
	if val := attrVal(charsetMetas[0], "charset"); val != "utf-8" {
		t.Errorf("expected first meta charset=utf-8, got %s", val)
	}

	// Second one should be unchanged (iso-8859-1)
	if val := attrVal(charsetMetas[1], "charset"); val != "iso-8859-1" {
		t.Errorf("expected second meta charset=iso-8859-1 (unchanged), got %s", val)
	}
}

// TestInject_MetaHttpEquivCaseInsensitive_StillRecognised verifies that
// lowercase http-equiv="content-type" is recognized and replaced.
func TestInject_MetaHttpEquivCaseInsensitive_StillRecognised(t *testing.T) {
	body := []byte(`<!DOCTYPE html>
<html>
<head>
<meta http-equiv="content-type" content="text/html; charset=iso-8859-1"/>
<title>Test</title>
</head>
<body>
<p>Hello World</p>
</body>
</html>`)

	result, err := Inject(body, "")
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	doc, err := html.Parse(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	head := findHead(doc)
	if head == nil {
		t.Fatal("expected head in output")
	}

	// Find the charset meta (should have replaced the http-equiv one)
	var charsetMeta *html.Node
	for c := head.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == atom.Meta {
			if hasAttr(c, "charset") {
				charsetMeta = c
				break
			}
		}
	}
	if charsetMeta == nil {
		t.Fatal("expected charset meta in output (http-equiv should have been replaced)")
	}

	val := attrVal(charsetMeta, "charset")
	if val != "utf-8" {
		t.Errorf("expected charset=utf-8, got charset=%s", val)
	}

	// Verify no http-equiv or content attrs
	if hasAttr(charsetMeta, "http-equiv") {
		t.Error("expected no http-equiv attr")
	}
	if hasAttr(charsetMeta, "content") {
		t.Error("expected no content attr")
	}
}

// TestInject_StyleContent_MatchesExactNoInputStyleRuleConstant verifies that
// the injected style content matches the noInputStyleRule constant exactly.
func TestInject_StyleContent_MatchesExactNoInputStyleRuleConstant(t *testing.T) {
	body := []byte(`<!DOCTYPE html>
<html>
<head>
<title>Test</title>
</head>
<body>
<p>Hello World</p>
</body>
</html>`)

	result, err := Inject(body, "")
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	doc, err := html.Parse(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	head := findHead(doc)
	if head == nil {
		t.Fatal("expected head in output")
	}

	// Find the style element
	var styleNode *html.Node
	for c := head.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == atom.Style {
			styleNode = c
			break
		}
	}
	if styleNode == nil {
		t.Fatal("expected style element in head")
	}

	// Get the text content of the style node
	var styleContent string
	for c := styleNode.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			styleContent = c.Data
			break
		}
	}

	if styleContent != noInputStyleRule {
		t.Errorf("style content mismatch.\nExpected: %s\nGot: %s", noInputStyleRule, styleContent)
	}
}

// TestInject_StyleElement_AppendedAsLastChildOfHead verifies that the style
// element is appended as the last child of head.
func TestInject_StyleElement_AppendedAsLastChildOfHead(t *testing.T) {
	body := []byte(`<!DOCTYPE html>
<html>
<head>
<title>Test</title>
<meta charset="utf-8"/>
</head>
<body>
<p>Hello World</p>
</body>
</html>`)

	result, err := Inject(body, "")
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	doc, err := html.Parse(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	head := findHead(doc)
	if head == nil {
		t.Fatal("expected head in output")
	}

	// Find the last child that is an element
	var lastChild *html.Node
	for c := head.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			lastChild = c
		}
	}

	if lastChild == nil || lastChild.DataAtom != atom.Style {
		t.Error("expected style element as last child of head")
	}
}

// TestInject_MetaAppearsBeforeStyleInHead_OrderPreserved verifies that the
// charset meta appears before the style element in the head.
func TestInject_MetaAppearsBeforeStyleInHead_OrderPreserved(t *testing.T) {
	body := []byte(`<!DOCTYPE html>
<html>
<head>
<title>Test</title>
</head>
<body>
<p>Hello World</p>
</body>
</html>`)

	result, err := Inject(body, "")
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	doc, err := html.Parse(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	head := findHead(doc)
	if head == nil {
		t.Fatal("expected head in output")
	}

	// Find positions of charset meta and style
	var metaPos, stylePos int
	metaPos = -1
	stylePos = -1
	pos := 0

	for c := head.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			if c.DataAtom == atom.Meta && metaPos == -1 && hasAttr(c, "charset") {
				metaPos = pos
			}
			if c.DataAtom == atom.Style {
				stylePos = pos
			}
			pos++
		}
	}

	if metaPos == -1 {
		t.Fatal("charset meta not found")
	}
	if stylePos == -1 {
		t.Fatal("style element not found")
	}
	if metaPos >= stylePos {
		t.Errorf("meta should appear before style; meta pos=%d, style pos=%d", metaPos, stylePos)
	}
}

// TestInject_HTMLWithNoHead_DoesNotPanic verifies that HTML without an explicit
// head element does not panic and still injects the style.
func TestInject_HTMLWithNoHead_DoesNotPanic(t *testing.T) {
	body := []byte(`<html><body><p>hello</p></body></html>`)

	result, err := Inject(body, "")
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	resultStr := string(result)
	if !strings.Contains(resultStr, noInputStyleRule) {
		t.Error("expected noInputStyleRule in output")
	}
}

// TestInject_CompressedBody_DecompressesBeforeParse is a table-driven test
// verifying that gzip, brotli, and deflate compressed bodies are decompressed
// before parsing and injection.
func TestInject_CompressedBody_DecompressesBeforeParse(t *testing.T) {
	htmlSnippet := []byte(`<!DOCTYPE html>
<html>
<head>
<title>Test</title>
</head>
<body>
<p>Hello World</p>
</body>
</html>`)

	tests := []struct {
		name             string
		compressor       func([]byte) []byte
		contentEncoding  string
	}{
		{
			name: "gzip",
			compressor: func(data []byte) []byte {
				var buf bytes.Buffer
				w := gzip.NewWriter(&buf)
				w.Write(data)
				w.Close()
				return buf.Bytes()
			},
			contentEncoding: "gzip",
		},
		{
			name: "brotli",
			compressor: func(data []byte) []byte {
				var buf bytes.Buffer
				w := brotli.NewWriter(&buf)
				w.Write(data)
				w.Close()
				return buf.Bytes()
			},
			contentEncoding: "br",
		},
		{
			name: "deflate",
			compressor: func(data []byte) []byte {
				var buf bytes.Buffer
				w, _ := flate.NewWriter(&buf, flate.DefaultCompression)
				w.Write(data)
				w.Close()
				return buf.Bytes()
			},
			contentEncoding: "deflate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed := tt.compressor(htmlSnippet)
			result, err := Inject(compressed, tt.contentEncoding)
			if err != nil {
				t.Fatalf("Inject failed: %v", err)
			}

			resultStr := string(result)
			if !strings.Contains(resultStr, noInputStyleRule) {
				t.Error("expected noInputStyleRule in output")
			}
			if bytes.Contains(result, []byte{0x1f, 0x8b}) { // gzip magic
				t.Error("expected decompressed output, but found gzip magic bytes")
			}
		})
	}
}

// TestInject_MalformedGzip_ReturnsError verifies that malformed gzip data
// returns an error.
func TestInject_MalformedGzip_ReturnsError(t *testing.T) {
	body := []byte("this is not gzip")
	result, err := Inject(body, "gzip")
	if err == nil {
		t.Error("expected error for malformed gzip")
	}
	if result != nil {
		t.Error("expected nil result on error")
	}
}

// TestInject_MalformedDeflate_ReturnsError verifies that malformed deflate data
// returns an error.
func TestInject_MalformedDeflate_ReturnsError(t *testing.T) {
	body := []byte{0x00, 0xFF, 0xFE, 0x01}
	result, err := Inject(body, "deflate")
	if err == nil {
		t.Error("expected error for malformed deflate")
	}
	if result != nil {
		t.Error("expected nil result on error")
	}
}

// TestInject_MalformedBrotli_ReturnsError verifies that malformed brotli data
// returns an error.
func TestInject_MalformedBrotli_ReturnsError(t *testing.T) {
	body := []byte{0x00, 0xFF, 0xFE, 0x01}
	result, err := Inject(body, "br")
	if err == nil {
		t.Error("expected error for malformed brotli")
	}
	if result != nil {
		t.Error("expected nil result on error")
	}
}

// TestInject_EmptyBody_NoEncoding_DoesNotPanic verifies that an empty body
// with no encoding does not panic.
func TestInject_EmptyBody_NoEncoding_DoesNotPanic(t *testing.T) {
	body := []byte{}
	result, err := Inject(body, "")
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// TestInject_GarbageNonHTMLBytes_NoEncoding_DoesNotPanic verifies that garbage
// bytes without encoding do not panic (html.Parse is lenient).
func TestInject_GarbageNonHTMLBytes_NoEncoding_DoesNotPanic(t *testing.T) {
	body := []byte{0x00, 0xFF, 0xFE, 0x01}
	result, err := Inject(body, "")
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// TestInject_EmptyContentEncoding_PassesThroughUnchanged verifies that an empty
// content encoding passes the body through unchanged (after parsing/injection).
func TestInject_EmptyContentEncoding_PassesThroughUnchanged(t *testing.T) {
	body := []byte(`<!DOCTYPE html>
<html>
<head>
<title>Test</title>
</head>
<body>
<p>Hello World</p>
</body>
</html>`)

	result, err := Inject(body, "")
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Should contain the original content plus injections
	resultStr := string(result)
	if !strings.Contains(resultStr, "Hello World") {
		t.Error("expected original content in output")
	}
}

// TestInject_IdentityContentEncoding_PassesThroughUnchanged verifies that
// "identity" encoding is treated like empty (pass-through, no decompression).
func TestInject_IdentityContentEncoding_PassesThroughUnchanged(t *testing.T) {
	body := []byte(`<!DOCTYPE html>
<html>
<head>
<title>Test</title>
</head>
<body>
<p>Hello World</p>
</body>
</html>`)

	result, err := Inject(body, "identity")
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	resultStr := string(result)
	if !strings.Contains(resultStr, "Hello World") {
		t.Error("expected original content in output")
	}
}

// TestInject_UnknownContentEncoding_PassesThroughUnchanged verifies that an
// unknown encoding (e.g., "zstd") is passed through unchanged (no decompression).
func TestInject_UnknownContentEncoding_PassesThroughUnchanged(t *testing.T) {
	body := []byte(`<!DOCTYPE html>
<html>
<head>
<title>Test</title>
</head>
<body>
<p>Hello World</p>
</body>
</html>`)

	result, err := Inject(body, "zstd")
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	resultStr := string(result)
	if !strings.Contains(resultStr, "Hello World") {
		t.Error("expected original content in output")
	}
}

// TestDecompress_CaseInsensitiveAndWhitespaceTrimmed is a table-driven test
// verifying that decompress handles case variations and whitespace.
func TestDecompress_CaseInsensitiveAndWhitespaceTrimmed(t *testing.T) {
	originalData := []byte("Hello World Test Data")

	tests := []struct {
		name             string
		contentEncoding  string
		compressor       func([]byte) []byte
	}{
		{
			name:            "GZIP uppercase",
			contentEncoding: "GZIP",
			compressor: func(data []byte) []byte {
				var buf bytes.Buffer
				w := gzip.NewWriter(&buf)
				w.Write(data)
				w.Close()
				return buf.Bytes()
			},
		},
		{
			name:            "Gzip mixed case",
			contentEncoding: "Gzip",
			compressor: func(data []byte) []byte {
				var buf bytes.Buffer
				w := gzip.NewWriter(&buf)
				w.Write(data)
				w.Close()
				return buf.Bytes()
			},
		},
		{
			name:            "gzip with spaces",
			contentEncoding: "  gzip  ",
			compressor: func(data []byte) []byte {
				var buf bytes.Buffer
				w := gzip.NewWriter(&buf)
				w.Write(data)
				w.Close()
				return buf.Bytes()
			},
		},
		{
			name:            "BR uppercase",
			contentEncoding: "BR",
			compressor: func(data []byte) []byte {
				var buf bytes.Buffer
				w := brotli.NewWriter(&buf)
				w.Write(data)
				w.Close()
				return buf.Bytes()
			},
		},
		{
			name:            "Br mixed case",
			contentEncoding: "Br",
			compressor: func(data []byte) []byte {
				var buf bytes.Buffer
				w := brotli.NewWriter(&buf)
				w.Write(data)
				w.Close()
				return buf.Bytes()
			},
		},
		{
			name:            "DEFLATE uppercase",
			contentEncoding: "DEFLATE",
			compressor: func(data []byte) []byte {
				var buf bytes.Buffer
				w, _ := flate.NewWriter(&buf, flate.DefaultCompression)
				w.Write(data)
				w.Close()
				return buf.Bytes()
			},
		},
		{
			name:            "Deflate mixed case",
			contentEncoding: "Deflate",
			compressor: func(data []byte) []byte {
				var buf bytes.Buffer
				w, _ := flate.NewWriter(&buf, flate.DefaultCompression)
				w.Write(data)
				w.Close()
				return buf.Bytes()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed := tt.compressor(originalData)
			result, err := decompress(compressed, tt.contentEncoding)
			if err != nil {
				t.Fatalf("decompress failed: %v", err)
			}
			if !bytes.Equal(result, originalData) {
				t.Errorf("decompressed data mismatch.\nExpected: %s\nGot: %s", string(originalData), string(result))
			}
		})
	}
}
