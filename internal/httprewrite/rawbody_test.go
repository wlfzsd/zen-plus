package httprewrite

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf16"
)

// [2026-10-14 空body解码失败修复] 回归：GBK 声明 + 空 body 的响应曾令
// charset.NewReader 嗅探编码时报 EOF，旧错误路径 Close 了原始 res.Body，
// 调用方（"记日志继续转发"）随后读 body → http2 errClosedResponseBody →
// panic(ErrAbortHandler) → 整条入站 h2 连接被掐（login.1688.com 主文档
// ERR_HTTP2_PROTOCOL_ERROR，6/6 复现）。修复=解码失败降级为原文直通，
// body 永远存活。本测试钉死该语义：
// 1) 空 body + GBK 声明 → openRawBody 不报错，body 可读到 EOF；
// 2) 非 GBK 空 body 同理；
// 3) 正常 GBK body 仍正确解码为 UTF-8（降级不误伤正常路径）。
func TestOpenRawBodyEmptyBodyGBKNoError(t *testing.T) {
	res := &http.Response{
		Header: http.Header{"Content-Type": []string{"text/html;charset=GBK"}},
		Body:   io.NopCloser(bytes.NewReader(nil)),
	}
	raw, err := openRawBody(res)
	if err != nil {
		t.Fatalf("empty GBK body must not error (got %v) — the fix keeps the body alive", err)
	}
	if raw.mimeType != "text/html" {
		t.Fatalf("mime = %q, want text/html", raw.mimeType)
	}
	b, rerr := io.ReadAll(raw.reader)
	if rerr != nil {
		t.Fatalf("reading the surviving body errored: %v", rerr)
	}
	if len(b) != 0 {
		t.Fatalf("body = %q, want empty", b)
	}
	raw.close()
}

func TestOpenRawBodyEmptyBodyNoEncoding(t *testing.T) {
	res := &http.Response{
		Header: http.Header{"Content-Type": []string{"text/html"}},
		Body:   io.NopCloser(strings.NewReader("")),
	}
	raw, err := openRawBody(res)
	if err != nil {
		t.Fatalf("empty body without encoding must not error: %v", err)
	}
	if _, rerr := io.ReadAll(raw.reader); rerr != nil {
		t.Fatalf("read errored: %v", rerr)
	}
	raw.close()
}

func TestOpenRawBodyGBKStillDecodes(t *testing.T) {
	// "登录" 的 GBK 编码字节
	gbk := []byte{0xB5, 0xC7, 0xC2, 0xBC}
	res := &http.Response{
		Header: http.Header{"Content-Type": []string{"text/html;charset=GBK"}},
		Body:   io.NopCloser(bytes.NewReader(gbk)),
	}
	raw, err := openRawBody(res)
	if err != nil {
		t.Fatalf("normal GBK body must not error: %v", err)
	}
	b, rerr := io.ReadAll(raw.reader)
	if rerr != nil {
		t.Fatalf("read errored: %v", rerr)
	}
	if !strings.Contains(string(b), "登录") {
		t.Fatalf("GBK decode lost content: %q", b)
	}
	raw.close()
}

// TestBufferRewriteKeepsOriginalCharset pins the 2026-11-20 W2 invariant
// (learn-from-upstream 0cc69c4): a legacy-charset body is rewritten in UTF-8
// but must leave as the bytes its Content-Type promises, and the Content-Type
// itself must keep its charset parameter.
func TestBufferRewriteKeepsOriginalCharset(t *testing.T) {
	gbkBody := []byte{0xB5, 0xC7, 0xC2, 0xBC} // 登录
	res := &http.Response{
		Header:        http.Header{"Content-Type": []string{"text/html;charset=GBK"}},
		Body:          io.NopCloser(bytes.NewReader(gbkBody)),
		ContentLength: int64(len(gbkBody)),
	}

	err := BufferRewrite(res, func(src []byte) []byte {
		if !strings.Contains(string(src), "登录") {
			t.Fatalf("processor saw %q, want decoded UTF-8 登录", src)
		}
		return bytes.ReplaceAll(src, []byte("登录"), []byte("注册"))
	})
	if err != nil {
		t.Fatalf("BufferRewrite() = %v", err)
	}

	if got := res.Header.Get("Content-Type"); got != "text/html;charset=GBK" {
		t.Fatalf("Content-Type = %q, want the original header", got)
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read rewritten body: %v", err)
	}
	want, _ := wantGBK("注册")
	if !bytes.Equal(body, want) {
		t.Fatalf("rewritten body = % x, want GBK bytes % x (mojibake would break rendering)", body, want)
	}
}

// wantGBK encodes s to GBK using the same codec the rewriter uses.
func wantGBK(s string) ([]byte, error) {
	_, enc, _ := bodyCharsetCodec("gbk")
	return enc.NewEncoder().Bytes([]byte(s))
}

// TestBufferRewriteKeepsUTF8Header: a UTF-8 body must not be relabelled — the
// old code always stamped "; charset=utf-8", which turned `text/html` into
// `text/html; charset=utf-8` on every rewritten response.
func TestBufferRewriteKeepsUTF8Header(t *testing.T) {
	res := &http.Response{
		Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:   io.NopCloser(strings.NewReader("<html>hi</html>")),
	}
	if err := BufferRewrite(res, func(src []byte) []byte { return src }); err != nil {
		t.Fatalf("BufferRewrite() = %v", err)
	}
	if got := res.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want the original header", got)
	}
}

// TestBufferRewriteUTF16BecomesUTF8: the one case the upstream fix relabels —
// UTF-16 has no byte-wise re-encode target here, so it is converted and the
// header is updated to say so.
func TestBufferRewriteUTF16BecomesUTF8(t *testing.T) {
	codes := utf16.Encode([]rune("<html>登录</html>"))
	buf := bytes.NewBuffer(nil)
	buf.Write([]byte{0xFF, 0xFE}) // little-endian BOM
	for _, code := range codes {
		buf.Write([]byte{byte(code), byte(code >> 8)})
	}

	res := &http.Response{
		Header: http.Header{"Content-Type": []string{"text/html; charset=utf-16"}},
		Body:   io.NopCloser(bytes.NewReader(buf.Bytes())),
	}
	if err := BufferRewrite(res, func(src []byte) []byte { return src }); err != nil {
		t.Fatalf("BufferRewrite() = %v", err)
	}

	if got := res.Header.Get("Content-Type"); !strings.Contains(got, "charset=utf-8") {
		t.Fatalf("Content-Type = %q, want charset=utf-8 after UTF-16 conversion", got)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !utf8Contains(body, "登录") {
		t.Fatalf("body = %q, want UTF-8 登录", body)
	}
}

func utf8Contains(b []byte, s string) bool {
	return bytes.Contains(b, []byte(s))
}
