package httprewrite

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

// [2026-10-14 空body解码失败修复] 回归：GBK 声明 + 空 body 的响应曾令
// charset.NewReader 嗅探编码时报 EOF，旧错误路径 Close 了原始 res.Body，
// 调用方（"记日志继续转发"）随后读 body → http2 errClosedResponseBody →
// panic(ErrAbortHandler) → 整条入站 h2 连接被掐（login.1688.com 主文档
// ERR_HTTP2_PROTOCOL_ERROR，6/6 复现）。修复=解码失败降级为原文直通，
// body 永远存活。本测试钉死该语义：
// 1) 空 body + GBK 声明 → getRawBodyReader 不报错，body 可读到 EOF；
// 2) 非 GBK 空 body 同理；
// 3) 正常 GBK body 仍正确解码为 UTF-8（降级不误伤正常路径）。
func TestGetRawBodyReaderEmptyBodyGBKNoError(t *testing.T) {
	res := &http.Response{
		Header: http.Header{"Content-Type": []string{"text/html;charset=GBK"}},
		Body:   io.NopCloser(bytes.NewReader(nil)),
	}
	rc, mimeType, err := getRawBodyReader(res)
	if err != nil {
		t.Fatalf("empty GBK body must not error (got %v) — the fix keeps the body alive", err)
	}
	if mimeType != "text/html" {
		t.Fatalf("mime = %q, want text/html", mimeType)
	}
	b, rerr := io.ReadAll(rc)
	if rerr != nil {
		t.Fatalf("reading the surviving body errored: %v", rerr)
	}
	if len(b) != 0 {
		t.Fatalf("body = %q, want empty", b)
	}
	_ = rc.Close()
}

func TestGetRawBodyReaderEmptyBodyNoEncoding(t *testing.T) {
	res := &http.Response{
		Header: http.Header{"Content-Type": []string{"text/html"}},
		Body:   io.NopCloser(strings.NewReader("")),
	}
	rc, _, err := getRawBodyReader(res)
	if err != nil {
		t.Fatalf("empty body without encoding must not error: %v", err)
	}
	if _, rerr := io.ReadAll(rc); rerr != nil {
		t.Fatalf("read errored: %v", rerr)
	}
	_ = rc.Close()
}

func TestGetRawBodyReaderGBKStillDecodes(t *testing.T) {
	// "登录" 的 GBK 编码字节
	gbk := []byte{0xB5, 0xC7, 0xC2, 0xBC}
	res := &http.Response{
		Header: http.Header{"Content-Type": []string{"text/html;charset=GBK"}},
		Body:   io.NopCloser(bytes.NewReader(gbk)),
	}
	rc, _, err := getRawBodyReader(res)
	if err != nil {
		t.Fatalf("normal GBK body must not error: %v", err)
	}
	b, rerr := io.ReadAll(rc)
	if rerr != nil {
		t.Fatalf("read errored: %v", rerr)
	}
	if !strings.Contains(string(b), "登录") {
		t.Fatalf("GBK decode lost content: %q", b)
	}
	_ = rc.Close()
}
