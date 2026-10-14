package proxy

// ALPS 剥除门禁（2026-10-07 晚深挖沉淀）：镜像 spec 不得携带 0x44CD
// application_settings——它声明"客户端栈会处理服务器 EE 里的 ALPS 子消息"，
// 中转栈不履行 → google 系前端按 ALPS 分支走 TLS 状态机 → unexpected_message。
// 逐扩展二分实锤：仅剥 0x44CD，youtube 全域 h2 会话恢复；其余指纹原样保留。

import (
	"testing"

	utls "github.com/refraction-networking/utls"
)

func TestDropInvalidCredentialsRemovesALPS(t *testing.T) {
	spec := &utls.ClientHelloSpec{
		Extensions: []utls.TLSExtension{
			&utls.SNIExtension{},
			&utls.GenericExtension{Id: 0x44cd, Data: []byte{0x00, 0x03, 0x02, 'h', '2'}},
			&utls.ALPNExtension{AlpnProtocols: []string{"h2", "http/1.1"}},
			&utls.GenericExtension{Id: 0xca34, Data: []byte{0x01, 0x02}},
			&utls.KeyShareExtension{},
		},
	}
	dropInvalidCredentials(spec)
	for _, ext := range spec.Extensions {
		if g, ok := ext.(*utls.GenericExtension); ok && g.Id == 0x44cd {
			t.Fatalf("0x44cd (ALPS) 未被剥除——会话期 unexpected_message 会复发")
		}
	}
	if len(spec.Extensions) != 4 {
		t.Fatalf("保留扩展数=%d, want 4（ALPN/0xca34/SNI/keyshare 必须原样保留）", len(spec.Extensions))
	}
}
