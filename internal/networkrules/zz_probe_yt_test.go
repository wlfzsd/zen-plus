package networkrules

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func probeReq(t *testing.T, raw string) *http.Request {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url: %v", err)
	}
	return &http.Request{Method: http.MethodPost, URL: u, Host: u.Host, Header: http.Header{}}
}

func TestProbeYTAdRules(t *testing.T) {
	playerBody := `{"adPlacements":[{"a":1}],"adSlots":[{"b":2}],"playerAds":[{"c":3}],"streamingData":{"expire":"x"}}`
	getWatchBody := `[{"playerResponse":{"adPlacements":[{"a":1}],"adSlots":[{"b":2}],"playerAds":[{"c":3}],"streamingData":{"expire":"x"}},"trackingParams":"y"}]`

	type tc struct {
		name   string
		rule   string
		target string
		body   string
	}
	cases := []tc{
		{"zen-ads-L13-list-form", `||youtube.com/youtubei/v1/player$jsonprune=$['adPlacements'\, 'adSlots'\, 'playerAds']`, "https://www.youtube.com/youtubei/v1/player", playerBody},
		{"player-recursive-bare", `||youtube.com/youtubei/v1/player$jsonprune=$..[adPlacements\, adSlots\, playerAds]`, "https://www.youtube.com/youtubei/v1/player", playerBody},
		{"player-quoted-recursive", `||youtube.com/youtubei/v1/player$jsonprune=$..['adPlacements'\, 'adSlots'\, 'playerAds']`, "https://www.youtube.com/youtubei/v1/player", playerBody},
		{"getwatch-quoted-recursive", `||youtube.com/youtubei/v1/get_watch$jsonprune=$..['adPlacements'\, 'adSlots'\, 'playerAds']`, "https://www.youtube.com/youtubei/v1/get_watch", getWatchBody},
		{"getwatch-bare-recursive", `||youtube.com/youtubei/v1/get_watch$jsonprune=$..[adPlacements\, adSlots\, playerAds]`, "https://www.youtube.com/youtubei/v1/get_watch", getWatchBody},
		{"remove-js-constant-L9", `||youtube.com/watch?$remove-js-constant=ytInitialPlayerResponse.playerConfig.ssapConfig|ytInitialPlayerResponse.adPlacements|ytInitialPlayerResponse.adSlots|ytInitialPlayerResponse.playerAds`, "https://www.youtube.com/watch?v=abc", ""},
		{"googlevideo-exception-L16", `@@||googlevideo.com/videoplayback*ctier=L&*%2Cxpc%2Cctier%2C`, "https://rr1---sn-x.googlevideo.com/videoplayback?ctier=L&x=1", ""},
	}

	for _, c := range cases {
		nr := New()
		_, err := nr.ParseRule(c.rule, nil)
		if err != nil {
			t.Logf("[%s] PARSE-ERR: %v", c.name, err)
			continue
		}
		t.Logf("[%s] PARSE-OK", c.name)
		if c.target == "" {
			continue
		}
		// Compact is what production runs after all lists load; keep the path honest.
		nr.Compact()
		req := probeReq(t, c.target)
		res := &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(c.body)),
			Request:    req,
		}
		rules, mErr := nr.ModifyRes(req, res)
		out := ""
		if res.Body != nil {
			b, _ := io.ReadAll(res.Body)
			out = string(b)
		} else {
			b, _ := io.ReadAll(io.NopCloser(bytes.NewReader(nil)))
			out = string(b)
		}
		t.Logf("[%s] ModifyRes rules=%d err=%v body=%s", c.name, len(rules), mErr, out)
	}
}
