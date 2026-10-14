package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"testing"
)

// [2026-10-13 h2腿挂死修复] h2 腿失败闩锁语义回归：
// 1) 窗口内连续 h2FailLatchThreshold 次失败 → 闩上 h1Only；
// 2) 阈值前不闩；
// 3) 成功清零后孤立失败永不累积成闩。
func TestH2FailureLatchPinsToH1(t *testing.T) {
	m := newMimicRoundTripper(&Proxy{}, func() *tls.Config { return nil })
	addr := "hang.example.com:443"
	for i := 1; i < h2FailLatchThreshold; i++ {
		if m.recordH2Failure(addr) {
			t.Fatalf("latched after %d failures, want latch only at %d", i, h2FailLatchThreshold)
		}
	}
	if m.isH1Only(addr) {
		t.Fatal("latched before threshold")
	}
	if !m.recordH2Failure(addr) {
		t.Fatal("threshold failure did not latch")
	}
	if !m.isH1Only(addr) {
		t.Fatal("latch did not pin h1Only")
	}
}

func TestH2FailureResetOnSuccess(t *testing.T) {
	m := newMimicRoundTripper(&Proxy{}, func() *tls.Config { return nil })
	addr := "blip.example.com:443"
	for i := 0; i < h2FailLatchThreshold-1; i++ {
		m.recordH2Failure(addr)
	}
	m.clearH2Failures(addr)
	if m.recordH2Failure(addr) {
		t.Fatal("stale streak latched after clearH2Failures")
	}
	if m.isH1Only(addr) {
		t.Fatal("h1Only must not be set without reaching threshold")
	}
}

// [2026-10-14 流级挂死修复] TTFB 守卫的错误分类语义回归：
// 1) 守卫到点（guardFired=true）的 cancel/deadline → TTFB 超时（进闩锁）；
// 2) 守卫未到点的 cancel → 客户端主动取消（不进闩锁）；
// 3) 守卫未到点的 dial deadline/RST 类错误 → 既有失败路径（进闩锁）；
// 4) TTFB 哨兵错误可被 errors.Is 识别（handoff 分支的判据）。
func TestClassifyH2RoundTripErr(t *testing.T) {
	wrappedTTFB := fmt.Errorf("%w: addr after 30s without response headers", errH2TTFBTimeout)
	cases := []struct {
		name                 string
		err                  error
		fired                bool
		wantTTFB, wantCancel bool
	}{
		// 包装哨兵（errH2TTFBTimeout）是分类之后的产物，永不再进 classify：
		// 契约上 classify 只吃 RoundTrip 原始错误——此用例钉死该契约边界。
		{"wrapped sentinel never re-enters classify", wrappedTTFB, true, false, false},
		{"raw guard cancel counts as ttfb", context.Canceled, true, true, false},
		{"client cancel not counted", context.Canceled, false, false, true},
		{"dial deadline not client cancel", context.DeadlineExceeded, false, false, false},
		{"rst style error", errors.New("connection reset by peer"), false, false, false},
	}
	for _, c := range cases {
		gotTTFB, gotCancel := classifyH2RoundTripErr(c.err, c.fired)
		if gotTTFB != c.wantTTFB || gotCancel != c.wantCancel {
			t.Fatalf("%s: got (ttfb=%v,cancel=%v), want (ttfb=%v,cancel=%v)", c.name, gotTTFB, gotCancel, c.wantTTFB, c.wantCancel)
		}
	}
	if !errors.Is(wrappedTTFB, errH2TTFBTimeout) {
		t.Fatal("sentinel not identifiable via errors.Is")
	}
}
