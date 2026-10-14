package csp

import (
	"io"
	"net/http"
	"strings"

	"github.com/irbis-sh/zen-desktop/internal/httprewrite"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// patchMetaCSPsBatch mutates HTML <meta> tags for multiple CSP operations in a single pass.
func patchMetaCSPsBatch(res *http.Response, operations []PatchOperation) error {
	if res.Body == nil || res.Body == http.NoBody {
		return nil
	}

	err := httprewrite.StreamRewrite(res, func(src io.ReadCloser, dst *io.PipeWriter) {
		defer src.Close()

		z := html.NewTokenizer(src)
		for {
			tt := z.Next()
			switch tt {
			case html.ErrorToken:
				dst.CloseWithError(z.Err())
				return

			case html.StartTagToken, html.SelfClosingTagToken:
				raw := append([]byte{}, z.Raw()...) // z.Token() modifies the underlying buffer, so we need to make a copy
				tok := z.Token()

				if tok.DataAtom != atom.Meta {
					dst.Write(raw)
					continue
				}

				if out := patchedMetaHTML(tok, operations); out != nil {
					dst.Write(out)
				} else {
					dst.Write(raw)
				}

			default:
				dst.Write(z.Raw())
			}
		}
	})

	return err
}

// patchedMetaHTML applies all CSP operations to a parsed <meta> tag's
// content-security-policy content. It returns the serialized replacement
// tag, or nil when the tag carries no patchable CSP (no http-equiv CSP, no
// content attribute, empty content, or no operation changed anything).
func patchedMetaHTML(tok html.Token, operations []PatchOperation) []byte {
	var hasCSP bool
	contentInd := -1 // Track the index of the content= attribute

	for i, a := range tok.Attr {
		if strings.EqualFold(a.Key, "http-equiv") &&
			strings.EqualFold(a.Val, "content-security-policy") {
			hasCSP = true
		}

		if strings.EqualFold(a.Key, "content") && contentInd == -1 {
			contentInd = i
		}
	}

	if !hasCSP || contentInd == -1 || tok.Attr[contentInd].Val == "" {
		return nil
	}

	// Apply all operations to this meta tag's CSP
	contentVal := tok.Attr[contentInd].Val
	patchedContent := contentVal
	changed := false

	for _, op := range operations {
		patched, patchChanged := patchPolicies([]string{patchedContent}, op.Nonce, op.Kind, op.ResourceURL)
		if patchChanged {
			patchedContent = patched[0]
			changed = true
		}
	}

	if !changed {
		return nil
	}

	tok.Attr[contentInd].Val = patchedContent
	return []byte(tok.String())
}

// tagNameAt reports whether raw's tag name — starting at byte offset start
// (1 for "<name", 2 for "</name") — equals want case-insensitively and ends
// at a name delimiter. This is the zero-allocation gate that lets the scan
// skip z.Token() (and its raw-buffer copy) for every non-target tag.
func tagNameAt(raw []byte, start int, want string) bool {
	if len(raw) <= start {
		return false
	}
	i := start
	for j := 0; j < len(want); j++ {
		if i >= len(raw) {
			return false
		}
		c := raw[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != want[j] {
			return false
		}
		i++
	}
	if i >= len(raw) {
		return false
	}
	switch raw[i] {
	case ' ', '\t', '\n', '\r', '\f', '/', '>':
		return true
	}
	return false
}

// PatchMetaCSPsAndInjectHead rewrites an HTML document in a single streaming
// pass: <meta http-equiv=content-security-policy> tags are patched and
// appendWith is inserted immediately before the </head> tag, after which the
// rest of the body is copied without tokenization.
//
// Meta CSP patching stops at </head>: browsers only honor meta CSP inside
// <head> (HTML spec), so this matches browser behavior while skipping the
// full-body scan the separate passes used to pay twice (2026-10-13). A
// document without </head> is tokenized to EOF — meta tags are patched along
// the way and nothing is injected, matching AppendHTMLHeadContents.
func PatchMetaCSPsAndInjectHead(res *http.Response, appendWith []byte, operations []PatchOperation) error {
	if res.Body == nil || res.Body == http.NoBody {
		return nil
	}

	err := httprewrite.StreamRewrite(res, func(src io.ReadCloser, dst *io.PipeWriter) {
		defer src.Close()

		z := html.NewTokenizer(src)
		for {
			tt := z.Next()
			switch tt {
			case html.ErrorToken:
				dst.CloseWithError(z.Err())
				return

			case html.StartTagToken, html.SelfClosingTagToken:
				raw0 := z.Raw()
				if !tagNameAt(raw0, 1, "meta") {
					dst.Write(raw0)
					continue
				}
				raw := append([]byte{}, raw0...) // z.Token() modifies the underlying buffer
				tok := z.Token()

				if tok.DataAtom != atom.Meta {
					dst.Write(raw)
					continue
				}

				if out := patchedMetaHTML(tok, operations); out != nil {
					dst.Write(out)
				} else {
					dst.Write(raw)
				}

			case html.EndTagToken:
				raw0 := z.Raw()
				if tagNameAt(raw0, 2, "head") {
					dst.Write(appendWith)
					dst.Write(raw0)
					dst.Write(z.Buffered())
					// Directly copy the remaining content, without the
					// overhead of tokenization.
					_, err := io.Copy(dst, src)
					dst.CloseWithError(err)
					return
				}
				dst.Write(raw0)

			default:
				dst.Write(z.Raw())
			}
		}
	})

	return err
}
