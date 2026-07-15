// Package htmlinject ports the C# HtmlNoInputInjector: it decompresses a
// response body (gzip/br/deflate), parses the HTML, normalises the charset
// <meta> to UTF-8, optionally appends the no-input <style> rule to <head>,
// and re-serialises.  Non-HTML bodies and unrecognised Content-Encoding values
// are passed through unchanged.  This package is consumed by the TLS proxy
// (Part 8) but is deliberately standalone so it can be unit-tested in
// isolation.
package htmlinject

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"fmt"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// noInputStyleRule is the exact CSS text injected by the C# HtmlNoInputInjector
// (originally from ContentRewriter's NoInputStyleRule, iteration 5).  It
// disables text entry on form controls while leaving links, buttons, and
// scrolling functional — a blanket pointer-events:none on the whole document
// would also break navigation.
const noInputStyleRule = "input,textarea,select,[contenteditable],[contenteditable=\"true\"]{" +
	"pointer-events:none!important;user-select:none!important;-webkit-user-select:none!important;}"

// Inject decompresses body per contentEncoding (empty string / "identity" →
// no-op; "gzip", "br", "deflate" → inflate), then parses it as HTML, applies
// charset-meta normalisation and the no-input <style> injection, and returns
// the re-serialised UTF-8 bytes.
//
// The caller is responsible for checking that the response Content-Type is
// text/html before calling Inject — non-HTML bytes handed to the HTML parser
// will produce mangled output just as they would in the C# version.
//
// On success the returned Content-Encoding is always empty (the body is no
// longer compressed), matching the C# proxy's behaviour of stripping the
// Content-Encoding header after processing.
func Inject(body []byte, contentEncoding string) ([]byte, error) {
	decompressed, err := decompress(body, contentEncoding)
	if err != nil {
		return nil, fmt.Errorf("htmlinject: decompress (%s): %w", contentEncoding, err)
	}

	doc, err := html.Parse(bytes.NewReader(decompressed))
	if err != nil {
		return nil, fmt.Errorf("htmlinject: html parse: %w", err)
	}

	headNode := findHead(doc)
	if headNode != nil {
		normalizeCharsetMeta(headNode)
		appendNoInputStyle(headNode)
	}

	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return nil, fmt.Errorf("htmlinject: html render: %w", err)
	}
	return buf.Bytes(), nil
}

// decompress inflates body per the given Content-Encoding value.  Unrecognised
// or absent encodings (including "identity") are returned unchanged, mirroring
// the C# DecompressBody pass-through default.
func decompress(body []byte, contentEncoding string) ([]byte, error) {
	enc := strings.TrimSpace(strings.ToLower(contentEncoding))
	switch enc {
	case "gzip":
		r, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(r)

	case "br":
		r := brotli.NewReader(bytes.NewReader(body))
		return io.ReadAll(r)

	case "deflate":
		r := flate.NewReader(bytes.NewReader(body))
		defer r.Close()
		return io.ReadAll(r)

	default:
		// "identity", empty string, or anything else — pass through.
		return body, nil
	}
}

// findHead performs a depth-first search of the node tree and returns the
// first <head> element found, or nil if the document has no head.
func findHead(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == atom.Head {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findHead(c); found != nil {
			return found
		}
	}
	return nil
}

// normalizeCharsetMeta ensures the <head> contains exactly one
// <meta charset="utf-8">, matching the C# NormalizeCharsetMeta logic:
//
//   - If a <meta charset="…"> exists → update its charset attribute to "utf-8"
//     and remove any stale http-equiv/content attributes.
//   - If a <meta http-equiv="Content-Type"> exists → replace it in-place with
//     a clean <meta charset="utf-8"> (same node, attributes replaced).
//   - Otherwise → prepend <meta charset="utf-8"> as the first child of <head>.
func normalizeCharsetMeta(head *html.Node) {
	var charsetMeta *html.Node

	for c := head.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.DataAtom != atom.Meta {
			continue
		}
		if hasAttr(c, "charset") {
			charsetMeta = c
			break
		}
		if strings.EqualFold(attrVal(c, "http-equiv"), "Content-Type") {
			charsetMeta = c
			break
		}
	}

	if charsetMeta != nil {
		// Remove http-equiv and content attributes, set charset="utf-8".
		var kept []html.Attribute
		for _, a := range charsetMeta.Attr {
			switch strings.ToLower(a.Key) {
			case "http-equiv", "content":
				// drop
			default:
				kept = append(kept, a)
			}
		}
		// Ensure charset attribute is present and set to utf-8.
		found := false
		for i, a := range kept {
			if strings.ToLower(a.Key) == "charset" {
				kept[i].Val = "utf-8"
				found = true
				break
			}
		}
		if !found {
			kept = append(kept, html.Attribute{Key: "charset", Val: "utf-8"})
		}
		charsetMeta.Attr = kept
	} else {
		// Prepend <meta charset="utf-8"> as first child of head.
		meta := &html.Node{
			Type:     html.ElementNode,
			DataAtom: atom.Meta,
			Data:     "meta",
			Attr:     []html.Attribute{{Key: "charset", Val: "utf-8"}},
		}
		head.InsertBefore(meta, head.FirstChild)
	}
}

// appendNoInputStyle appends a <style> element containing noInputStyleRule to
// the end of <head>, matching the C# behaviour of AppendChild after
// NormalizeCharsetMeta.
func appendNoInputStyle(head *html.Node) {
	style := &html.Node{
		Type:     html.ElementNode,
		DataAtom: atom.Style,
		Data:     "style",
	}
	text := &html.Node{
		Type: html.TextNode,
		Data: noInputStyleRule,
	}
	style.AppendChild(text)
	head.AppendChild(style)
}

// hasAttr reports whether node n has an attribute with the given key (case-insensitive).
func hasAttr(n *html.Node, key string) bool {
	key = strings.ToLower(key)
	for _, a := range n.Attr {
		if strings.ToLower(a.Key) == key {
			return true
		}
	}
	return false
}

// attrVal returns the value of the named attribute on n, or "" if absent
// (case-insensitive key lookup).
func attrVal(n *html.Node, key string) string {
	key = strings.ToLower(key)
	for _, a := range n.Attr {
		if strings.ToLower(a.Key) == key {
			return a.Val
		}
	}
	return ""
}
