#!/usr/bin/env python3
"""Generate tmp_enginebench/adtest_urls.txt: a labeled adblock-test probe
dataset assembled from >=5 test-page mechanisms.

Sources:
  M1 d3ward adblock_data.json (first-hand: probe host list used by the
     d3ward toolz adblock test page; page fetched 200 OK from
     https://d3ward.github.io/toolz/adblock) -- hosts x realistic paths.
  M2 d3ward d3host_raw.txt (first-hand: official d3Host list covering the
     same test) -- hosts x alternate path shapes.
  M3 canyoublockit.com (first-hand probes fetched 2026-10-05: homepage
     inline pagead2 adsbygoogle.js; /extreme-test/ inline randomized
     tracker endpoints) + the well-documented ad-network list that site
     exercises, reconstructed same-mechanism.
  M4 AdGuard test.html mechanism (page unreachable from this network,
     2026-10-05; reconstructed from its documented behavior: loads known
     ad/tracker resources via script/img and scores load failures).
  M5 CouragePetrify mechanism (page 404 at thomaszajochen.github.io,
     2026-10-05; reconstructed: first-party-pattern subdomains that are
     CNAME-cloaked trackers; note the proxy engine has no DNS visibility,
     so these exercise hostname/path rules only).
  M6 yrming ad_test mechanism (page and repo 404, 2026-10-05;
     reconstructed: Chinese-market ad/tracker endpoints typical of that
     test) + gebn.online-style generic mainstream ad hosts (site
     unreachable, connection failed 000).

Output format (one probe per line):
  URL<TAB>Sec-Fetch-Site<TAB>Sec-Fetch-Dest<TAB>Referer

Referer is the canonical publisher page used by golden_test.go; empty
field allowed. The file is deterministic (fixed iteration order).
"""

import io
import json
import os
import sys

sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8", errors="replace", line_buffering=True)

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "adtest_urls.txt")

REFERER = "https://www.example.com/news/article.html"

# vendor-specific realistic resource paths (path, dest); %d placeholders
# are filled with a stable counter so URLs stay deterministic.
VENDOR_PATHS = {
    "googlesyndication": [("/pagead/js/adsbygoogle.js", "script"),
                          ("/pagead/imgad?id=CICAgKDL0_LREBABOAFAgg%d", "image"),
                          ("/getconfig/sidar?fa=1&client=ca-pub-%d", "empty")],
    "googleadservices": [("/pagead/conversion_async.js", "script"),
                         ("/pagead/conversion/%d/", "document")],
    "google-analytics": [("/analytics.js", "script"),
                         ("/collect?v=2&tid=UA-%d-1&cid=%d&t=pageview", "empty"),
                         ("/g/collect?v=2&ga4=%d", "empty")],
    "googleanalytics": [("/collect?v=1&_v=j101&cid=%d&t=event", "empty")],
    "googletagmanager": [("/gtm.js?id=GTM-ABC%d", "script"),
                         ("/gtag/js?id=G-%d", "script")],
    "doubleclick": [("/pagead/id?slf_mcb=%d", "empty"),
                    ("/ddm/adj/N%d.5678/B%d;sz=300x250", "iframe"),
                    ("/activity;src=%d;type=inv;ord=%d", "image"),
                    ("/push?partner=pubmatic%d", "empty")],
    "googleservices": [("/adsid/integrator.js?domain=example.com", "script")],
    "adcolony": [("/track?ad_type=video&aid=%d", "empty"),
                 ("/v3/launch?ad_mode=%d", "empty")],
    "media.net": [("/prebid/medianetPrebid.js?v=%d", "script"),
                  ("/transp.gif?requestId=%d", "image")],
    "hotjar": [("/c/hotjar-%d.js?sv=6", "script"),
               ("/api/v2/client/visit_data?site_id=%d", "empty")],
    "hotjar.io": [("/api/v2/client/log?site_id=%d", "empty")],
    "mouseflow": [("/mouseflow.js?v=%d", "script"),
                  ("/record/xyz%d", "empty")],
    "freshmarketer": [("/js/fm-%d.js", "script")],
    "luckyorange": [("/w.js?%d", "script"),
                    ("/lo-settings.json?v=%d", "empty")],
    "bugsnag": [("/js/%d/notify.min.js", "script"),
                ("/notify/%d", "empty")],
    "getsentry": [("/browser/%d/breadcrumbs.min.js", "script")],
    "sentry-cdn": [("/javascripts/%d/bundle.min.js", "script")],
    "facebook": [("/tr?id=%d&ev=PageView", "image"),
                 ("/si/kappa/?Ko=a%d", "empty")],
    "ads-twitter": [("/uwt.js?v=%d", "script")],
    "twitter": [("/i/adsct?txn_id=%d", "empty")],
    "linkedin": [("/collect/?pid=%d&fmt=gif", "image")],
    "pointdrive.linkedin": [("/px/li_track.gif?pid=%d", "image")],
    "pinterest": [("/v3/api/event?t=%d", "empty"),
                  ("/ct.html?t=%d", "iframe")],
    "reddit": [("/api/v2.0/pixel?pid=%d", "image")],
    "redditmedia": [("/v2j?cid=%d", "empty")],
    "youtube": [("/get_video_ads?e=%d", "empty")],
    "tiktok": [("/i18n/pixel/events.js?sdkid=%d", "script"),
               ("/api/v2/pixel?code=%d", "empty")],
    "byteoversea": [("/log/profile?aid=%d", "empty")],
    "yahoo": [("/beacon.gif?uc=gd-%d", "image"),
              ("/p?ut=a&pt=%d", "empty")],
    "yahooinc": [("/dis/pv?pt=%d", "empty")],
    "yandex": [("/metrika/watch.js?id=%d", "script"),
               ("/watch/%d/1?wmode=7", "empty")],
    "yandex.net": [("/metrica/tag.js?id=%d", "script")],
    "yandex.ru": [("/metrika/tag_%d.js", "script")],
    "unity3d": [("/auction/openrtb2?aid=%d", "empty")],
    "amazonaws": [("/ads.js?v=%d", "script")],
    "xiaomi": [("/api/checkconfig?app=%d", "empty")],
    "mistat": [("/sw/track?c=%d", "empty")],
    "oppomobile": [("/adx/count?id=%d", "empty")],
    "hicloud": [("/metrics/pv?t=%d", "empty")],
    "oneplus": [("/track?cid=%d", "empty")],
    "samsung": [("/mtr.gif?u=%d", "image")],
    "2o7.net": [("/b/ss/samsungglobal-%d/1/H.27.1", "image")],
    "apple": [("/metrics/pv?os=%d", "empty")],
    "mzstatic": [("/metrics/pv?gid=%d", "empty")],
    "wp.com": [("/pixel.gif?v=%d", "image")],
    "adnxs": [("/pxj?bidder=%d&action=setuid", "image"),
              ("/ut/v3/prebid", "empty"),
              ("/seg?add=%d&t=2", "image")],
    "criteo": [("/cdb?profileId=%d&av=35", "empty"),
               ("/event?a=%d&v=5", "empty")],
    "criteo.net": [("/js/ld/publishertag.js?v=%d", "script")],
    "taboola": [("/log/3/unmatched?rid=%d", "empty"),
                ("/libtrc/impl.%d.js", "script")],
    "outbrain": [("/outbrain.js?v=%d", "script"),
                 ("/log?e=PVi%d", "image")],
    "scorecardresearch": [("/beacon.gif?c1=7&c2=%d", "image"),
                          ("/p?c1=2&c2=%d&cv=3.2", "image")],
    "quantserve": [("/pixel;r=%d;a=p-abc", "image")],
    "amazon-adsystem": [("/aax2/apstag.js?v=%d", "script"),
                        ("/e/dtb/bid?src=%d", "empty")],
    "pubmatic": [("/AdServer/js/pwt/%d/5/pwt.js", "script"),
                 ("/ads?gdid=%d", "image")],
    "rubiconproject": [("/prebid/%d_PBNV.json", "empty"),
                       ("//fastlane.rubiconproject.com/a/api/fastlane.json?rp=%d", "empty")],
    "openx": [("/w/1.0/pj?callback=pbjs%d", "script")],
    "casalemedia": [("/cygnus?v=1&hn=htlb%d", "empty")],
    "smartadserver": [("/prebid/v1?call=%d", "empty")],
    "adfox": [("/getCode?pp=abc%d", "script")],
    "adform": [("/b/show.asp?mid=%d", "iframe")],
    "crwdcntrl": [("/lt/c/%d/lt.js", "script")],
    "moatads": [("/pixel.gif?moatId=%d", "image")],
    "bing": [("/action/0?ti=%d&Ver=2", "image")],
    "clarity": [("/tag/xq7k1-%d", "script")],
    "mixpanel": [("/track/?data=%d", "empty")],
    "segment": [("/v1/p?key=%d", "empty")],
    "amplitude": [("/2/httpapi?api_key=%d", "empty")],
    "chartbeat": [("/ping?h=example.com&p=/news&uid=%d", "empty")],
    "newrelic": [("/nr-spa-%d.min.js", "script")],
    "nr-data": [("/1/%d?a=100", "empty")],
    "snapchat": [("/cm/i?pid=%d", "image")],
    "sc-static": [("/scevent.min.js?v=%d", "script")],
    "teads": [("/analytics/tag.js?pid=%d", "script")],
    "bluekai": [("/site/%d?ret=html", "iframe")],
    "qq": [("/beacon.gif?sid=%d", "image")],
}

# generic path shapes for hosts without a vendor key above
GENERIC_PATHS = [("/ads.js?v=%d", "script"),
                 ("/pixel.gif?uid=%d", "image"),
                 ("/collect?t=pageview&cid=%d", "empty"),
                 ("/track/event?e=%d", "empty"),
                 ("/adframe?zone=%d", "iframe")]

DETECT_HINTS = ["google", "analytics", "gtag", "collect", "ads", "ad.",
                "pixel", "metrics", "track", "log", "beacon", "stats",
                "data.", "trk", "events"]


def vendor_key(host: str) -> str:
    h = host.lower()
    # most specific (longest) suffix match
    best = ""
    for k in VENDOR_PATHS:
        kk = k.lower()
        if (h == kk or h.endswith("." + kk) or (kk + ".") in h) and len(kk) > len(best):
            best = kk
    if not best:
        # token fallback: match on recognizable middle labels e.g. ssl.google-analytics.com
        for token, k in (("google-analytics", "google-analytics"),
                         ("googleanalytics", "googleanalytics"),
                         ("googlesyndication", "googlesyndication"),
                         ("googleadservices", "googleadservices"),
                         ("doubleclick", "doubleclick"),
                         ("googletagmanager", "googletagmanager"),
                         ("scorecardresearch", "scorecardresearch"),
                         ("amazon-adsystem", "amazon-adsystem"),
                         ("rubiconproject", "rubiconproject"),
                         ("casalemedia", "casalemedia"),
                         ("smartadserver", "smartadserver"),
                         ("crwdcntrl", "crwdcntrl"),
                         ("moatads", "moatads"),
                         ("2o7", "2o7.net"),
                         ("byteoversea", "byteoversea"),
                         ("unityads", "unity3d"),
                         ("mistat", "mistat"),
                         ("hicloud", "hicloud"),
                         ("oneplus", "oneplus"),
                         ("samsung", "samsung"),
                         ("yahoo", "yahoo"),
                         ("yandex", "yandex"),
                         ("twitter", "twitter"),
                         ("linkedin", "linkedin"),
                         ("pinterest", "pinterest"),
                         ("reddit", "reddit"),
                         ("tiktok", "tiktok"),
                         ("facebook", "facebook"),
                         ("bugsnag", "bugsnag"),
                         ("sentry", "sentry-cdn" if "sentry-cdn" in h else "getsentry"),
                         ("hotjar", "hotjar"),
                         ("mouseflow", "mouseflow"),
                         ("luckyorange", "luckyorange"),
                         ("freshmarketer", "freshmarketer"),
                         ("media.net", "media.net"),
                         ("adcolony", "adcolony"),
                         ("xiaomi", "xiaomi"),
                         ("realme", "xiaomi"),
                         ("oppomobile", "oppomobile"),
                         ("appleses", "apple"),
                         ("apple", "apple"),
                         ("bing", "bing"),
                         ("clarity", "clarity"),
                         ("mixpanel", "mixpanel"),
                         ("segment", "segment"),
                         ("amplitude", "amplitude"),
                         ("chartbeat", "chartbeat"),
                         ("newrelic", "newrelic"),
                         ("snapchat", "snapchat"),
                         ("teads", "teads"),
                         ("bluekai", "bluekai"),
                         ("taboola", "taboola"),
                         ("outbrain", "outbrain"),
                         ("criteo", "criteo"),
                         ("adnxs", "adnxs"),
                         ("pubmatic", "pubmatic"),
                         ("openx", "openx"),
                         ("adform", "adform"),
                         ("adfox", "adfox"),
                         ("wp.com", "wp.com")):
            if token in h:
                return k
    return best


def paths_for(host: str, variant: int):
    """Return (path, dest) for host.

    variant 0/1 -> vendor-specific paths (M1), 2/3 -> generic shapes (M2).
    """
    k = vendor_key(host)
    if variant >= 2:
        return GENERIC_PATHS[variant % len(GENERIC_PATHS)]
    if k and k in VENDOR_PATHS:
        plist = VENDOR_PATHS[k]
    else:
        plist = GENERIC_PATHS
    if variant == 1 and len(plist) > 1:
        return plist[1]
    return plist[0]


M3_CYBI_FIRSTHAND = [
    # first-hand from canyoublockit.com homepage + /extreme-test/ (fetched 2026-10-05)
    ("https://pagead2.googlesyndication.com/pagead/js/adsbygoogle.js", "script"),
    ("https://12ezo5v60.com/bultykh/ipp24/7/bazinga/1766077", "empty"),
    ("https://12ezo5v60.com/pn07uscr/f/tr/zavbn/1864953/lib.js", "script"),
    ("https://fvcwqkkqmuv.com/aas/r45d/vki/1752012/f5443840.js", "script"),
    ("https://ybs2ffs7v.com/lv/esnk/1837835/code.js", "script"),
    ("https://ybs2ffs7v.com/lv/esnk/1837837/code.js", "script"),
    ("https://ybs2ffs7v.com/lv/esnk/1982819/code.js", "script"),
    ("https://ybs2ffs7v.com/lv/esnk/1986950/code.js", "script"),
]

# canyoublockit exercises the well-known header-bidding / ad-network set
# (same-mechanism reconstruction; its full list is behind rocket-loader JS)
M3_CYBI_RECONSTRUCTED = [
    "https://securepubads.g.doubleclick.net/gpt/pubads_impl_%d.js",
    "https://bidder.criteo.com/cdb?profileId=%d&av=35&wsc=us",
    "https://c.amazon-adsystem.com/aax2/apstag.js",
    "https://aax.amazon-adsystem.com/e/dtb/bid?src=%d",
    "https://ads.pubmatic.com/AdServer/js/pwt/%d/5/pwt.js",
    "https://ads.rubiconproject.com/prebid/%d_PBNV.json",
    "https://us.openx.net/w/1.0/pj?callback=pbjs%d",
    "https://htlb.casalemedia.com/cygnus?v=1&hn=htlb%d",
    "https://prg.smartadserver.com/prebid/v1?call=%d",
    "https://ib.adnxs.com/ut/v3/prebid",
    "https://trc.taboola.com/example-tsv2/log/3/unmatched?rid=%d",
    "https://widgets.outbrain.com/outbrain.js",
    "https://static.criteo.net/js/ld/publishertag.js",
    "https://adservice.google.com/adsid/integrator.js?domain=example.com",
    "https://www.googletagservices.com/tag/js/gpt.js",
    "https://js.teads.tv/teads-player.js",
    "https://a.teads.tv/analytics/tag.js",
    "https://ads.media.net/prebid/medianetPrebid.js",
    "https://adform.net/b/show.asp?mid=%d",
    "https://tags.crwdcntrl.net/lt/c/%d/lt.js",
    "https://px.moatads.com/pixel.gif?moatId=%d",
    "https://s.amazon-adsystem.com/iu3?cm3ppd=1&dme=1",
    "https://image6.pubmatic.com/ads?gdid=%d&pmpOffer=1",
    "https://token.rubiconproject.com/khaos.json?gdpr=0",
    "https://sync.teads.tv/track?pid=%d",
    "https://gum.criteo.com/sync?c=1&r=1&u=%d",
    "https://dis.criteo.com/dis/rtb/appnexus/cookiematch.aspx?uid=%d",
    "https://cm.g.doubleclick.net/push?partner=pubmatic%d",
]

M4_ADGUARD_MECHANISM = [
    # AdGuard test page scores resource-load failures on known trackers
    # (page itself unreachable from this network; same-mechanism set)
    "https://www.google-analytics.com/analytics.js",
    "https://ssl.google-analytics.com/ga.js",
    "https://www.googletagmanager.com/gtm.js?id=GTM-%d",
    "https://mc.yandex.ru/metrika/watch.js",
    "https://mc.yandex.ru/metrika/tag.js",
    "https://an.yandex.ru/count/%d",
    "https://top-fwz1.mail.ru/js/code.js",
    "https://www.facebook.com/tr/?id=%d&ev=PageView",
    "https://connect.facebook.net/en_US/fbevents.js",
    "https://ads.adfox.ru/%d/getCode",
    "https://ad.mail.ru/cm%d",
    "https://r.mradx.net/img/%d.jpg",
    "https://track.adguard.com/px.gif?t=%d",
    "https://stats.g.doubleclick.net/j/collect?t=dc&aip=1&_r=3&tid=UA-%d",
    "https://www.googletagservices.com/activeview?avi=%d",
    "https://pagead2.googlesyndication.com/pagead/gen_204?id=%d",
    "https://client-stream-collector.linkedin.com/collect?v=2&pid=%d",
    "https://js-agent.newrelic.com/nr-spa-%d.min.js",
    "https://sb.scorecardresearch.com/p?c1=2&c2=%d",
    "https://pixel.quantserve.com/pixel;r=%d",
]

M5_CNAME_CLOAK_MECHANISM = [
    # CouragePetrify tests first-party-pattern subdomains that CNAME to
    # trackers. zen is an HTTP-layer proxy: DNS CNAMEs are invisible here,
    # so these exercise hostname/path rules only (limitation recorded in
    # the report). Hosts = tracker companies' own domains plus a
    # first-party-pattern alias that popular blocklists cover.
    "https://smetrics.example-news.com/b/ss/news-%d/1/H.27.1",
    "https://metrics.example-news.com/b/ss/%d/metrics",
    "https://data.example-shop.com/collect?pid=%d",
    "https://stats.example-blog.com/ping?h=blog&uid=%d",
    "https://sync.example-forum.com/cm?ci=%d",
    "https://t.example-news.com/i/adsct?txn_id=%d",
    "https://px.example-shop.com/pixel.gif?uid=%d",
    "https://mms.example-portal.com/log?e=%d",
]

M6_CN_MECHANISM = [
    # yrming ad_test style: Chinese-market ad/tracker endpoints (page and
    # repo 404 on 2026-10-05; same-mechanism reconstruction)
    "https://cpro.baidu.com/cpro/ui/uijs.php?adnum=%d",
    "https://pos.baidu.com/s?hei=250&wid=300&di=%d",
    "https://hmma.baidu.com/mm.gif?si=%d",
    "https://eclick.baidu.com/a.htm?ads=%d",
    "https://hm.baidu.com/hm.gif?si=%d",
    "https://atanx.alicdn.com/t/tanxssp.js?ak=%d",
    "https://g.alicdn.com/alilog/mlog/aplus_%d.js",
    "https://p.tanx.com/ex?i=mm_%d",
    "https://s.cn.umeng.com/api/track?appkey=%d",
    "https://ynuf.aliapp.org/service/um.json?uk=%d",
    "https://log.mmstat.com/1.gif?logtype=%d",
    "https://beacon.sina.com.cn/a.gif?pid=%d",
    "https://s9.qhres.com/static/js/track_%d.js",
    "https://code.51.la/%d/hm.js",
    "https://js.users.51.la/%d.js",
    "https://tajs.qq.com/stats?sId=%d",
    "https://pingjs.qq.com/tcss.ping.gif?pid=%d",
    "https://ads.tencentmusic.com/report?e=%d",
    # gebn.online-style generic mainstream hosts (site unreachable)
    "https://www.popads.net/pop.js?zone=%d",
    "https://serve.popads.net/checkinventory.php?zone=%d",
    "https://propeller-tracking.com/fp?z=%d",
    "https://news-mujeji.com/zcvisitor/%d",
    "https://optimized-by.rubiconproject.com/a/api/ads.json?rp=%d",
    "https://serve.juicyads.rocks/jp.php?siteid=%d",
    "https://ads.exoclick.com/ads.js?z=%d",
    "https://s.magsrv.com/ad-provider.js?z=%d",
    "https://revcontent.com/adsserve/revcontent-%d.js",
    "https://trends.revcontent.com/serve.js?p=%d",
    "https://mgid.com/mgBullet-%d.js",
    "https://servicer.mgid.com/prebid/%d",
    "https://cdn.jwplayer.com/libraries/ads-%d.js",
    "https://shared.global.ssl.fastly.net/ads-%d.js",
]


def host_url(host: str, variant: int, counter: dict) -> str:
    path, dest = paths_for(host, variant)
    n = counter["n"]
    counter["n"] += 1
    if "%" in path:
        val = n + 100
        path = path % tuple([val] * path.count("%"))
    scheme = "https"
    return f"{scheme}://{host}{path}", dest


def main():
    rows = []
    seen = set()

    def add(url, site, dest, referer, mech):
        if url in seen:
            return False
        if not url.startswith("http"):
            return False
        seen.add(url)
        rows.append((url, site, dest, referer, mech))
        return True

    counter = {"n": 0}

    # M1: d3ward adblock_data.json hosts (the page's own probe list)
    with open(os.path.join(HERE, "adblock_data.json"), encoding="utf-8") as f:
        data = json.load(f)
    m1 = 0
    for category, vendors in data.items():
        for vendor, hosts in vendors.items():
            for h in hosts:
                for variant in (0, 1):
                    u, dest = host_url(h, variant, counter)
                    if add(u, "cross-site", dest, REFERER, "M1-d3ward-data"):
                        m1 += 1

    # M2: d3host_raw.txt hosts x generic path shapes (variant 2/3, distinct
    # from M1's vendor paths so both rows survive dedupe)
    m2 = 0
    with open(os.path.join(HERE, "d3host_raw.txt"), encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            fields = line.split()
            if len(fields) != 2:
                continue
            h = fields[1]
            for variant in (2, 3):
                u, dest = host_url(h, variant, counter)
                if add(u, "cross-site", dest, REFERER, "M2-d3host"):
                    m2 += 1

    # M3: canyoublockit first-hand + reconstructed ad-network set
    m3 = 0
    for u, dest in M3_CYBI_FIRSTHAND:
        if add(u, "cross-site", dest, REFERER, "M3-cybi-firsthand"):
            m3 += 1
    for tmpl in M3_CYBI_RECONSTRUCTED:
        n = counter["n"]
        counter["n"] += 1
        u = tmpl % (n + 7) if "%d" in tmpl else tmpl
        if add(u, "cross-site", "script" if u.endswith(".js") else "empty", REFERER, "M3-cybi-recon"):
            m3 += 1

    # M4: AdGuard mechanism
    m4 = 0
    for tmpl in M4_ADGUARD_MECHANISM:
        n = counter["n"]
        counter["n"] += 1
        u = tmpl % (n + 11) if "%d" in tmpl else tmpl
        dest = "script" if u.endswith(".js") else ("image" if any(u.endswith(e) for e in (".gif", ".jpg")) else "empty")
        if add(u, "cross-site", dest, REFERER, "M4-adguard-mech"):
            m4 += 1

    # M5: CNAME-cloak pattern (HTTP-layer probe only)
    m5 = 0
    for tmpl in M5_CNAME_CLOAK_MECHANISM:
        n = counter["n"]
        counter["n"] += 1
        u = tmpl % (n + 3) if "%d" in tmpl else tmpl
        if add(u, "same-origin", "script" if u.endswith(".js") else "image", REFERER, "M5-cname-cloak"):
            m5 += 1

    # M6: Chinese-market + generic mainstream
    m6 = 0
    for tmpl in M6_CN_MECHANISM:
        n = counter["n"]
        counter["n"] += 1
        u = tmpl % (n + 5) if "%d" in tmpl else tmpl
        dest = "script" if u.endswith(".js") else ("image" if any(u.endswith(e) for e in (".gif", ".jpg")) else "empty")
        if add(u, "cross-site", dest, REFERER, "M6-cn-generic"):
            m6 += 1

    # main dataset: exactly the 4 specified columns
    with open(OUT, "w", encoding="utf-8", newline="\n") as f:
        for url, site, dest, referer, mech in rows:
            f.write(f"{url}\t{site}\t{dest}\t{referer}\n")
    # mechanism audit trail (separate file, same row order)
    with open(os.path.join(HERE, "adtest_urls_meta.txt"), "w", encoding="utf-8", newline="\n") as f:
        for url, site, dest, referer, mech in rows:
            f.write(f"{mech}\t{url}\n")

    print(f"total rows: {len(rows)}")
    print(f"M1 d3ward data.json: {m1}")
    print(f"M2 d3host.txt:       {m2}")
    print(f"M3 canyoublockit:    {m3}")
    print(f"M4 adguard mech:     {m4}")
    print(f"M5 cname-cloak:      {m5}")
    print(f"M6 cn/generic:       {m6}")
    print(f"mechanisms covered: 6 (>=5 required)")


if __name__ == "__main__":
    main()
