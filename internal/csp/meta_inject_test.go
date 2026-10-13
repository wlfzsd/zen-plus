package csp

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// Tests for the single-pass document rewrite (2026-10-13): meta CSP patching
// and head-tag injection in one streaming tokenizer pass, replacing the old
// full-body meta scan (patchMetaCSPsBatch) plus a second head-insertion pass
// (httprewrite.AppendHTMLHeadContents).

func runRewrite(t *testing.T, html string, ops []PatchOperation) string {
	t.Helper()
	res := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/html"}},
		Body:       io.NopCloser(strings.NewReader(html)),
	}
	if err := PatchMetaCSPsAndInjectHead(res, []byte(`<script src="/inject.js" nonce="N1"></script>`), ops); err != nil {
		t.Fatalf("PatchMetaCSPsAndInjectHead: %v", err)
	}
	out, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read rewritten body: %v", err)
	}
	return string(out)
}

var oneScriptOp = []PatchOperation{{Nonce: "N1", Kind: Script, ResourceURL: ""}}

func TestRewritePatchesHeadMetaAndInjects(t *testing.T) {
	in := `<html><head><meta http-equiv="content-security-policy" content="default-src 'self'"></head><body><p>x</p></body></html>`
	out := runRewrite(t, in, oneScriptOp)

	if !strings.Contains(out, "nonce-N1") {
		t.Errorf("head meta CSP not patched:\n%s", out)
	}
	inj := `<script src="/inject.js" nonce="N1"></script>`
	if !strings.Contains(out, inj) {
		t.Errorf("injection missing:\n%s", out)
	}
	if i, j := strings.Index(out, inj), strings.LastIndex(out, "</head>"); i < 0 || j < 0 || i > j {
		t.Errorf("injection not immediately before </head>:\n%s", out)
	}
	// The rest of the document must pass through unchanged.
	if !strings.Contains(out, "<body><p>x</p></body>") {
		t.Errorf("body damaged:\n%s", out)
	}
}

func TestRewriteLeavesBodyMetaUntouched(t *testing.T) {
	// Browsers only honor meta CSP inside <head>; the single pass stops
	// there, so a body-deep meta is passed through verbatim (documented
	// browser-equivalent boundary vs the old full-body scan).
	in := `<html><head></head><body><meta http-equiv="content-security-policy" content="default-src 'self'"></body></html>`
	out := runRewrite(t, in, oneScriptOp)

	if strings.Contains(out, "nonce-N1") {
		t.Errorf("body meta must not be patched:\n%s", out)
	}
	if !strings.Contains(out, `<meta http-equiv="content-security-policy" content="default-src 'self'">`) {
		t.Errorf("body meta not passed through verbatim:\n%s", out)
	}
}

func TestRewriteNoMetaPassthrough(t *testing.T) {
	in := `<html><head><title>t</title></head><body></body></html>`
	out := runRewrite(t, in, oneScriptOp)

	if !strings.Contains(out, "/inject.js") {
		t.Errorf("no-meta document must still inject (only via head append):\n%s", out)
	}
	if strings.Contains(out, "http-equiv") {
		t.Errorf("unexpected meta in output:\n%s", out)
	}
}

func TestRewriteHeadlessDocumentPatchesToEOF(t *testing.T) {
	// No </head> anywhere: metas are patched along the way and nothing is
	// injected (same as the old AppendHTMLHeadContents for this case).
	in := `<html><body><meta http-equiv="content-security-policy" content="default-src 'self'"></body></html>`
	out := runRewrite(t, in, oneScriptOp)

	if !strings.Contains(out, "nonce-N1") {
		t.Errorf("headless document meta not patched:\n%s", out)
	}
	if strings.Contains(out, "/inject.js") {
		t.Errorf("headless document must not inject:\n%s", out)
	}
}

func TestRewriteUppercaseMetaPatched(t *testing.T) {
	in := `<html><head><META HTTP-EQUIV="content-security-policy" CONTENT="default-src 'self'"></head></html>`
	out := runRewrite(t, in, oneScriptOp)

	if !strings.Contains(out, "nonce-N1") {
		t.Errorf("uppercase <META> not patched:\n%s", out)
	}
}

func TestRewriteNonCSPMetaUntouched(t *testing.T) {
	in := `<html><head><meta name="viewport" content="width=device-width"></head><body></body></html>`
	out := runRewrite(t, in, oneScriptOp)

	if strings.Contains(out, "nonce-N1") {
		t.Errorf("viewport meta must not be patched:\n%s", out)
	}
	if !strings.Contains(out, `<meta name="viewport" content="width=device-width">`) {
		t.Errorf("viewport meta not passed through verbatim:\n%s", out)
	}
}
