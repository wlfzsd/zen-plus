package proxy

// 回退判定形状测试（2026-10-07 晚审计沉淀）：三种必须回退的错误形态钉死，
// 防止未来改动丢掉任一文案（丢任一 = 对应站点 502 + 误入透明名单）。

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsAlpnFallbackShape(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"alpnFallbackError 包装", &alpnFallbackError{addr: "x:443", proto: "http/1.1"}, true},
		{"Go 服务端复数文案", fmt.Errorf("mimic TLS handshake(x:443): remote error: tls: client requested unsupported application protocols ([\"h2\"])"), true},
		{"utls 单数对端告警", fmt.Errorf("mimic TLS handshake(x:443): remote error: tls: no application protocol"), true},
		{"google 会话期 unexpected message", fmt.Errorf("remote error: tls: unexpected message"), true},
		{"普通 EOF 不可回退", fmt.Errorf("mimic TLS handshake(x:443): EOF"), false},
		{"context canceled 不可回退", fmt.Errorf("roundtrip: context canceled"), false},
	}
	for _, tc := range cases {
		var ae *alpnFallbackError
		if got := isAlpnFallbackShape(tc.err, &ae); got != tc.want {
			t.Errorf("%s: isAlpnFallbackShape=%v, want %v", tc.name, got, tc.want)
		}
	}
	// errors.As 分支必须正确回填 alpnErr
	var ae *alpnFallbackError
	if !isAlpnFallbackShape(&alpnFallbackError{addr: "x:443"}, &ae) || ae == nil {
		t.Errorf("alpnFallbackError 分支未回填 alpnErr")
	}
	ae = nil
	if err := errors.New("plain"); isAlpnFallbackShape(err, &ae) || ae != nil {
		t.Errorf("普通错误不应命中")
	}
}
