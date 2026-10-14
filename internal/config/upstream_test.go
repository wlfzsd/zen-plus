package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newUpstreamTestConfig returns a Config backed by a temporary config
// directory, so saveLocked writes never touch the real user config.
func newUpstreamTestConfig(t *testing.T) *Config {
	t.Helper()
	prevConfigDir := ConfigDir
	ConfigDir = t.TempDir()
	t.Cleanup(func() { ConfigDir = prevConfigDir })
	return &Config{}
}

func TestSetUpstreamProxyValidation(t *testing.T) {
	cases := []struct {
		name    string
		up      UpstreamProxyConfig
		wantErr bool
	}{
		{
			name: "valid http",
			up:   UpstreamProxyConfig{Enabled: true, Type: UpstreamProxyHTTP, Host: "127.0.0.1", Port: 20122},
		},
		{
			name: "valid https with credentials",
			up:   UpstreamProxyConfig{Enabled: true, Type: UpstreamProxyHTTPS, Host: "proxy.example.com", Port: 8443, Username: "u", Password: "p"},
		},
		{
			name: "valid socks5",
			up:   UpstreamProxyConfig{Enabled: true, Type: UpstreamProxySOCKS5, Host: "127.0.0.1", Port: 1080},
		},
		{
			name: "type is case-insensitive",
			up:   UpstreamProxyConfig{Enabled: true, Type: "SOCKS5", Host: "127.0.0.1", Port: 1080},
		},
		{
			name:    "bad type",
			up:      UpstreamProxyConfig{Enabled: true, Type: "ftp", Host: "127.0.0.1", Port: 21},
			wantErr: true,
		},
		{
			name:    "empty type",
			up:      UpstreamProxyConfig{Enabled: true, Host: "127.0.0.1", Port: 1080},
			wantErr: true,
		},
		{
			name:    "empty host",
			up:      UpstreamProxyConfig{Enabled: true, Type: UpstreamProxyHTTP, Port: 8080},
			wantErr: true,
		},
		{
			name:    "zero port",
			up:      UpstreamProxyConfig{Enabled: true, Type: UpstreamProxyHTTP, Host: "127.0.0.1", Port: 0},
			wantErr: true,
		},
		{
			name:    "port out of range",
			up:      UpstreamProxyConfig{Enabled: true, Type: UpstreamProxyHTTP, Host: "127.0.0.1", Port: 65536},
			wantErr: true,
		},
		{
			// Disabled entries keep their address for toggling back on, so
			// validation is skipped.
			name: "disabled entry skips validation",
			up:   UpstreamProxyConfig{Enabled: false, Type: "", Host: "", Port: 0},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newUpstreamTestConfig(t)
			err := c.SetUpstreamProxy(tc.up)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("SetUpstreamProxy(%+v) = nil, want error", tc.up)
				}
				// A rejected save must not have written anything.
				if _, err := os.Stat(filepath.Join(ConfigDir, "config.json")); !os.IsNotExist(err) {
					data, _ := os.ReadFile(filepath.Join(ConfigDir, "config.json"))
					if len(data) > 0 {
						t.Fatalf("rejected save still wrote config: %s", data)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("SetUpstreamProxy(%+v): %v", tc.up, err)
			}
			// The type is normalised to lowercase on save.
			want := tc.up
			want.Type = UpstreamProxyType(strings.ToLower(string(want.Type)))
			got := c.GetUpstreamProxy()
			if got != want {
				t.Fatalf("stored = %+v, want %+v", got, want)
			}
			// The change must be on disk immediately (settings save on every
			// update).
			data, err := os.ReadFile(filepath.Join(ConfigDir, "config.json"))
			if err != nil {
				t.Fatalf("read saved config: %v", err)
			}
			var raw struct {
				Proxy struct {
					Upstream UpstreamProxyConfig `json:"upstream"`
				} `json:"proxy"`
			}
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatalf("unmarshal saved config: %v", err)
			}
			if raw.Proxy.Upstream != want {
				t.Fatalf("on-disk upstream = %+v, want %+v", raw.Proxy.Upstream, want)
			}
		})
	}
}

func TestUpstreamProxyURL(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		c := newUpstreamTestConfig(t)
		if err := c.SetUpstreamProxy(UpstreamProxyConfig{Enabled: false, Type: UpstreamProxyHTTP, Host: "127.0.0.1", Port: 20122}); err != nil {
			t.Fatalf("set: %v", err)
		}
		if _, ok := c.UpstreamProxyURL(); ok {
			t.Fatal("disabled upstream resolved, want not ok")
		}
	})

	t.Run("http no credentials", func(t *testing.T) {
		c := newUpstreamTestConfig(t)
		if err := c.SetUpstreamProxy(UpstreamProxyConfig{Enabled: true, Type: UpstreamProxyHTTP, Host: "127.0.0.1", Port: 20122}); err != nil {
			t.Fatalf("set: %v", err)
		}
		got, ok := c.UpstreamProxyURL()
		if !ok || got != "http://127.0.0.1:20122" {
			t.Fatalf("UpstreamProxyURL = %q (ok=%v), want http://127.0.0.1:20122", got, ok)
		}
	})

	t.Run("socks5 with escaped credentials", func(t *testing.T) {
		c := newUpstreamTestConfig(t)
		if err := c.SetUpstreamProxy(UpstreamProxyConfig{Enabled: true, Type: UpstreamProxySOCKS5, Host: "192.0.2.1", Port: 1080, Username: "ali@ce", Password: "s3:cr/et"}); err != nil {
			t.Fatalf("set: %v", err)
		}
		got, ok := c.UpstreamProxyURL()
		if !ok {
			t.Fatal("enabled upstream did not resolve")
		}
		// Credentials with URL-significant characters must be percent-encoded.
		want := "socks5://ali%40ce:s3%3Acr%2Fet@192.0.2.1:1080"
		if got != want {
			t.Fatalf("UpstreamProxyURL = %q, want %q", got, want)
		}
	})

	t.Run("ipv6 host bracketed", func(t *testing.T) {
		c := newUpstreamTestConfig(t)
		if err := c.SetUpstreamProxy(UpstreamProxyConfig{Enabled: true, Type: UpstreamProxyHTTP, Host: "::1", Port: 8080}); err != nil {
			t.Fatalf("set: %v", err)
		}
		got, ok := c.UpstreamProxyURL()
		if !ok || got != "http://[::1]:8080" {
			t.Fatalf("UpstreamProxyURL = %q (ok=%v), want http://[::1]:8080", got, ok)
		}
	})
}

func TestSetUpstreamFromLegacyURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    UpstreamProxyConfig
		wantErr bool
	}{
		{
			name: "plain http url",
			raw:  "http://127.0.0.1:20122",
			want: UpstreamProxyConfig{Enabled: true, Type: UpstreamProxyHTTP, Host: "127.0.0.1", Port: 20122},
		},
		{
			name: "bare host port",
			raw:  "10.0.0.1:7890",
			want: UpstreamProxyConfig{Enabled: true, Type: UpstreamProxyHTTP, Host: "10.0.0.1", Port: 7890},
		},
		{
			name: "bare host defaults to 80",
			raw:  "10.0.0.1",
			want: UpstreamProxyConfig{Enabled: true, Type: UpstreamProxyHTTP, Host: "10.0.0.1", Port: 80},
		},
		{
			name: "socks5 url",
			raw:  "socks5://127.0.0.1:20122",
			want: UpstreamProxyConfig{Enabled: true, Type: UpstreamProxySOCKS5, Host: "127.0.0.1", Port: 20122},
		},
		{
			name: "socks5h maps to socks5",
			raw:  "socks5h://192.0.2.1:1080",
			want: UpstreamProxyConfig{Enabled: true, Type: UpstreamProxySOCKS5, Host: "192.0.2.1", Port: 1080},
		},
		{
			name: "https url with credentials",
			raw:  "https://user:pw@192.0.2.1:8443",
			want: UpstreamProxyConfig{Enabled: true, Type: UpstreamProxyHTTPS, Host: "192.0.2.1", Port: 8443, Username: "user", Password: "pw"},
		},
		{
			name:    "unsupported scheme",
			raw:     "ftp://192.0.2.1:21",
			wantErr: true,
		},
		{
			name:    "empty",
			raw:     "   ",
			wantErr: true,
		},
		{
			name:    "missing host",
			raw:     "http://",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newUpstreamTestConfig(t)
			err := c.SetUpstreamFromLegacyURL(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("SetUpstreamFromLegacyURL(%q) = nil, want error", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("SetUpstreamFromLegacyURL(%q): %v", tc.raw, err)
			}
			if got := c.GetUpstreamProxy(); got != tc.want {
				t.Fatalf("stored = %+v, want %+v", got, tc.want)
			}
		})
	}
}
