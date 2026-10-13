package csp

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/irbis-sh/zen-desktop/internal/httprewrite"
)

// A/B benchmark for the 2026-10-13 batch-1 document-rewrite change:
// old pipeline = PatchHeadersBatch (full-body meta scan) +
// httprewrite.AppendHTMLHeadContents (second tokenizer pass)
// new pipeline = PatchCSPHeaders + PatchMetaCSPsAndInjectHead (single pass,
// </head> early stop, zero-alloc non-meta gate).

func buildDoc(metaCSP bool, kb int) string {
	var b strings.Builder
	b.WriteString("<html><head><title>t</title>")
	if metaCSP {
		b.WriteString(`<meta http-equiv="content-security-policy" content="default-src 'self'">`)
	}
	b.WriteString("</head><body>")
	para := "<p class=\"x\">Lorem ipsum dolor sit amet, consectetur adipiscing elit. </p>\n"
	for b.Len() < kb*1024 {
		b.WriteString(para)
	}
	b.WriteString("</body></html>")
	return b.String()
}

var benchOps = []PatchOperation{
	{Nonce: "AAAAAAAAAAAAAAAAAAAAAA", Kind: Script, ResourceURL: ""},
	{Nonce: "BBBBBBBBBBBBBBBBBBBBBB", Kind: Style, ResourceURL: ""},
}

func benchDocRewrite(b *testing.B, newPipeline bool, metaCSP bool, kb int) {
	doc := buildDoc(metaCSP, kb)
	injection := []byte(`<script src="/inject.js" nonce="N"></script>`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Body:       io.NopCloser(strings.NewReader(doc)),
		}
		if newPipeline {
			PatchCSPHeaders(res, benchOps)
			if err := PatchMetaCSPsAndInjectHead(res, injection, benchOps); err != nil {
				b.Fatal(err)
			}
		} else {
			if err := PatchHeadersBatch(res, benchOps); err != nil {
				b.Fatal(err)
			}
			if err := httprewrite.AppendHTMLHeadContents(res, injection); err != nil {
				b.Fatal(err)
			}
		}
		if _, err := io.Copy(io.Discard, res.Body); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDocRewriteOld1MB(b *testing.B) { benchDocRewrite(b, false, true, 1024) }
func BenchmarkDocRewriteNew1MB(b *testing.B) { benchDocRewrite(b, true, true, 1024) }
func BenchmarkDocRewriteOld32KB(b *testing.B) { benchDocRewrite(b, false, true, 32) }
func BenchmarkDocRewriteNew32KB(b *testing.B) { benchDocRewrite(b, true, true, 32) }
