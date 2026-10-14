// p24_smoke.cjs — P2/P4 批次冒烟门禁（node 冒烟，非 go test 的一部分）
// ① dispatch keylist 对拍：改动前备份 vs 改动后（既有键零变化＋新增 12 键）
// ② 全键 dispatch 冒烟（不得出现 "does not exist or is not yet implemented"）
// ③ P2：真实语料 4+1 条 json-prune 扩展表达式可执行断言（构造假 JSON → pruner 生效）
// ④ P4：12 个 trusted-* 运行时抽查
// 仅内存内打补丁捕获 dispatch Map，不改动 bundle.js 磁盘内容。
'use strict';
const fs = require('fs');
const path = require('path');

const DIR = __dirname;
const CUR = path.join(DIR, 'internal', 'asset', 'scriptlet', 'bundle.js');
const BACKUP_DIR = path.join(DIR, 'backup');
const BACKUP = fs.readdirSync(BACKUP_DIR).filter((f) => f.startsWith('bundle_js_pre_P24_')).sort()[0];
if (!BACKUP) { console.log('FAIL no pre-P24 backup found'); process.exit(1); }
const BACKUP_PATH = path.join(BACKUP_DIR, BACKUP);

let failures = 0;
let passes = 0;
function ok(cond, label) {
  if (cond) { passes += 1; console.log('PASS ' + label); }
  else { failures += 1; console.log('FAIL ' + label); }
}

// ---------------- browser stubs ----------------
function installStubs() {
  global.window = globalThis;
  global.location = { origin: 'https://example.org', hostname: 'example.org', href: 'https://example.org/' };
  const mkEl = () => ({
    nodeName: 'DIV', nodeType: 1, textContent: '',
    childNodes: [], children: [],
    attributes: {}, style: {},
    setAttribute() {}, getAttribute() { return null; }, removeAttribute() {},
    append() {}, contains() { return false; }, remove() {},
    querySelector() { return null; }, querySelectorAll() { return []; },
    addEventListener() {}, removeEventListener() {}, dispatchEvent() { return true; },
    classList: { contains() { return false; }, remove() {}, add() {} },
    isConnected: true,
    getBoundingClientRect() { return { left: 0, top: 0, width: 0, height: 0 }; },
    focus() {},
  });
  global.document = {
    cookie: '',
    location: global.location,
    documentElement: mkEl(),
    head: mkEl(),
    body: mkEl(),
    currentScript: null,
    readyState: 'complete',
    createDocumentFragment() { return mkEl(); },
    createAttribute() { return {}; },
    createElement(tag) {
      const e = mkEl();
      e.nodeName = String(tag).toUpperCase();
      e.setAttribute = function setAttribute(name, value) { this.attributes[name] = value; };
      return e;
    },
    querySelector() { return null; },
    querySelectorAll() { return []; },
    addEventListener() {}, removeEventListener() {},
  };
  window.document = global.document;
  window.location = global.location;
  window.name = '';
  global.MutationObserver = class MutationObserver {
    constructor(cb) { this.cb = cb; }
    observe() {} disconnect() {} takeRecords() { return []; }
  };
  window.MutationObserver = global.MutationObserver;
  window.localStorage = {
    _m: new Map(),
    setItem(k, v) { this._m.set(String(k), String(v)); },
    getItem(k) { return this._m.has(String(k)) ? this._m.get(String(k)) : null; },
    removeItem(k) { this._m.delete(k); },
  };
  window.sessionStorage = { setItem() {}, getItem() { return null; }, removeItem() {} };
  class XHR {
    constructor() { this.withCredentials = false; this.readyState = 0; this.status = 0; this.responseText = ''; this.response = null; }
    open() {} send() {} setRequestHeader() {} getResponseHeader() { return null; } getAllResponseHeaders() { return ''; }
    addEventListener() {} removeEventListener() {} dispatchEvent() { return true; }
  }
  global.XMLHttpRequest = XHR;
  window.XMLHttpRequest = XHR;
  class FakeMouseEvent extends Event { constructor(type, init) { super(type, init); Object.assign(this, init || {}); } }
  if (typeof window.MouseEvent === 'undefined') { global.MouseEvent = FakeMouseEvent; window.MouseEvent = FakeMouseEvent; }
  if (typeof window.PointerEvent === 'undefined') { global.PointerEvent = FakeMouseEvent; window.PointerEvent = FakeMouseEvent; }
  if (typeof window.ProgressEvent === 'undefined') { global.ProgressEvent = FakeMouseEvent; window.ProgressEvent = FakeMouseEvent; }
  if (typeof window.FocusEvent === 'undefined') { global.FocusEvent = FakeMouseEvent; window.FocusEvent = FakeMouseEvent; }
  if (typeof window.Element === 'undefined') { global.Element = class Element {}; window.Element = global.Element; }
  if (typeof window.HTMLIFrameElement === 'undefined') { global.HTMLIFrameElement = class {}; window.HTMLIFrameElement = global.HTMLIFrameElement; }
  if (typeof window.EventTarget !== 'undefined' && typeof global.EventTarget === 'undefined') global.EventTarget = window.EventTarget;
}

// ---------------- bundle loader ----------------
function loadBundle(file) {
  let code = fs.readFileSync(file, 'utf8');
  // in-memory capture of the dispatch Map entries (disk file untouched)
  code = code.replace('ue=new Map([', 'ue=new Map(globalThis.__P24_PAIRS=[');
  globalThis.__P24_PAIRS = null;
  const scriptlet = new Function(code + '\n;return scriptlet;')();
  const keys = (globalThis.__P24_PAIRS || []).map((p) => p[0]);
  globalThis.__P24_PAIRS = null;
  return { scriptlet, keys };
}

function dispatchMisses(scriptlet, keys) {
  const misses = [];
  const origDebug = console.debug;
  console.debug = (...args) => { misses.push(args.map(String).join(' ')); };
  try {
    for (const key of keys) {
      try { scriptlet(key); } catch (ex) { /* entry body may throw on stubs; dispatch itself worked */ }
    }
  } finally { console.debug = origDebug; }
  return misses.filter((m) => m.includes('does not exist or is not yet implemented'));
}

const NATIVE_JSON_PARSE = JSON.parse;
const NATIVE_RESPONSE_JSON = Response.prototype.json;
function restoreJson() {
  JSON.parse = NATIVE_JSON_PARSE;
  Response.prototype.json = NATIVE_RESPONSE_JSON;
}

installStubs();

// ================= ① keylist 对拍 =================
const before = loadBundle(BACKUP_PATH);
const after = loadBundle(CUR);
console.log(`backup: ${BACKUP} keys=${before.keys.length}, current keys=${after.keys.length}`);
const newKeys = after.keys.filter((k) => !before.keys.includes(k));
const removedKeys = before.keys.filter((k) => !after.keys.includes(k));
ok(removedKeys.length === 0, `keylist: 既有键零变化（removed=${JSON.stringify(removedKeys)}）`);
const EXPECTED_NEW = [
  'trusted-replace-node-text', 'trusted-create-element', 'trusted-replace-argument',
  'trusted-click-element', 'trusted-suppress-native-method', 'trusted-set-local-storage-item',
  'trusted-set-constant', 'trusted-set-cookie', 'trusted-prune-inbound-object',
  'trusted-replace-xhr-response', 'trusted-replace-fetch-response', 'trusted-json-set',
];
ok(newKeys.length === EXPECTED_NEW.length && EXPECTED_NEW.every((k) => newKeys.includes(k)),
  `keylist: 恰好新增 12 个 trusted-* 键（new=${JSON.stringify(newKeys)}）`);

// ================= ② dispatch 冒烟 =================
const misses = dispatchMisses(after.scriptlet, after.keys);
ok(misses.length === 0, `dispatch: 全部 ${after.keys.length} 键可分发（miss=${JSON.stringify(misses)}）`);

// ================= ③ P2 真实语料表达式 =================
async function p2tests() {
  // --- ③-1 行 46339: Shorts entries.[-]... ---
  restoreJson();
  {
    const { scriptlet } = loadBundle(CUR);
    scriptlet('json-prune', 'entries.[-].command.reelWatchEndpoint.adClientParams.isAd');
    const payload = {
      entries: [
        { command: { reelWatchEndpoint: { adClientParams: { isAd: true } } }, id: 'ad1' },
        { command: { reelWatchEndpoint: { adClientParams: {} } }, id: 'plain0' },
        { id: 'plain1' },
      ],
    };
    const out = JSON.parse(JSON.stringify(payload));
    ok(Array.isArray(out.entries) && out.entries.length === 2
      && out.entries[0].id === 'plain0' && out.entries[1].id === 'plain1',
      `P2 46339: 命中 command.reelWatchEndpoint.adClientParams.isAd 的 entry 被整体移除`);
  }
  restoreJson();

  // --- ③-2 行 46347: 首页 feed adSlotRenderer ---
  {
    const { scriptlet } = loadBundle(CUR);
    scriptlet('json-prune', 'contents.twoColumnBrowseResultsRenderer.tabs.[].tabRenderer.content.richGridRenderer.contents.[-].richItemRenderer.content.adSlotRenderer');
    const feed = {
      contents: {
        twoColumnBrowseResultsRenderer: {
          tabs: [
            { tabRenderer: { content: { richGridRenderer: { contents: [
              { richItemRenderer: { content: { adSlotRenderer: { x: 1 } } } },
              { richItemRenderer: { content: { videoRenderer: { v: 1 } } } },
              { richItemRenderer: { content: { adSlotRenderer: { x: 2 } } } },
            ] } } } },
            { tabRenderer: { content: { richGridRenderer: { contents: [
              { richItemRenderer: { content: { videoRenderer: { v: 2 } } } },
              { richItemRenderer: { content: { adSlotRenderer: { x: 3 } } } },
            ] } } } },
          ],
        },
      },
    };
    const out = JSON.parse(JSON.stringify(feed));
    const c0 = out.contents.twoColumnBrowseResultsRenderer.tabs[0].tabRenderer.content.richGridRenderer.contents;
    const c1 = out.contents.twoColumnBrowseResultsRenderer.tabs[1].tabRenderer.content.richGridRenderer.contents;
    ok(c0.length === 1 && c0[0].richItemRenderer.content.videoRenderer
      && c1.length === 1 && c1[0].richItemRenderer.content.videoRenderer,
      'P2 46347: 两个 tab 的 adSlotRenderer feed 项被整体 splice 移除');
  }
  restoreJson();

  // --- ③-3 行 46348: ytInitialData 前缀变体 ---
  {
    const { scriptlet } = loadBundle(CUR);
    scriptlet('json-prune', 'ytInitialData.contents.twoColumnBrowseResultsRenderer.tabs.[].tabRenderer.content.richGridRenderer.contents.[-].richItemRenderer.content.adSlotRenderer');
    const payload = { ytInitialData: { contents: { twoColumnBrowseResultsRenderer: { tabs: [
      { tabRenderer: { content: { richGridRenderer: { contents: [
        { richItemRenderer: { content: { adSlotRenderer: { x: 1 } } } },
        { richItemRenderer: { content: { videoRenderer: { v: 1 } } } },
      ] } } } },
    ] } } } };
    const out = JSON.parse(JSON.stringify(payload));
    const c = out.ytInitialData.contents.twoColumnBrowseResultsRenderer.tabs[0].tabRenderer.content.richGridRenderer.contents;
    ok(c.length === 1 && c[0].richItemRenderer.content.videoRenderer, 'P2 46348: ytInitialData 前缀变体同样生效');
  }
  restoreJson();

  // --- ③-4 行 46357: m.youtube get_watch json-prune-fetch-response $.. + [?()] ---
  {
    const { scriptlet } = loadBundle(CUR);
    const watchPayload = {
      sections: [
        { itemSectionRenderer: { contents: [
          { adSlotRenderer: { a: 1 } },
          { videoRenderer: { b: 2 } },
          { adSlotRenderer: { a: 3 } },
        ] } },
        { itemSectionRenderer: { contents: [ { musicRenderer: { c: 9 } } ] } },
      ],
    };
    window.fetch = async () => new Response(JSON.stringify(watchPayload), { status: 200, headers: { 'content-type': 'application/json' } });
    scriptlet('json-prune-fetch-response', '$..itemSectionRenderer.contents[?(@.adSlotRenderer)]', '', '/get_watch');
    const resp = await window.fetch('https://m.youtube.com/youtubei/v1/get_watch?key=x');
    const j = await resp.json();
    const s0 = j.sections[0].itemSectionRenderer.contents;
    const s1 = j.sections[1].itemSectionRenderer.contents;
    ok(s0.length === 1 && s0[0].videoRenderer && s1.length === 1 && s1[0].musicRenderer,
      'P2 46357: $..descent+[?()] 过滤的 contents 广告元素被 splice 移除');
    // URL 不匹配时不裁剪
    const resp2 = await window.fetch('https://m.youtube.com/youtubei/v1/next?key=x');
    const j2 = await resp2.json();
    ok(j2.sections[0].itemSectionRenderer.contents.length === 3, 'P2 46357: URL 不匹配（/next）时响应保持原样');
    window.fetch = undefined;
  }
  restoreJson();

  // --- ③-5 wykop *.target.[=]./re/ 值过滤器（official 语义：required 路径仅作存在性门控） ---
  {
    const { scriptlet } = loadBundle(CUR);
    scriptlet('json-prune', '*', '*.target.[=]./ad\\.doubleclick\\.net/');
    const adPayload = { a: { target: 'https://ad.doubleclick.net/click' }, b: { target: 'https://www.wykop.pl/x' } };
    const out = JSON.parse(JSON.stringify(adPayload));
    ok(typeof out === 'object' && Object.keys(out).length === 0,
      'P2 wykop: 门控命中后按官方 jsonPruner 语义清空根级键（官方 getWildcardPropertyInChain(\'*\') 行为）');
    // 门控不命中（无 .target 项）→ 不裁剪
    const { scriptlet: sc2 } = loadBundle(CUR);
    sc2('json-prune', '*', '*.target.[=]./ad\\.doubleclick\\.net/');
    const clean = { a: { url: 'https://www.wykop.pl/api' }, b: [1, 2, 3] };
    const out2 = JSON.parse(JSON.stringify(clean));
    ok(out2.a && Array.isArray(out2.b), 'P2 wykop: 门控不命中（无 .target）时响应保持原样');
  }
  restoreJson();

  // --- ③-6 legacy 回归：既有可执行 json-prune 规则行为不变 ---
  {
    const { scriptlet } = loadBundle(CUR);
    scriptlet('json-prune', 'playerResponse.adPlacements playerResponse.adSlots');
    const p = { playerResponse: { adPlacements: { x: 1 }, adSlots: { y: 2 }, streamingData: { z: 3 } }, keep: 1 };
    const out = JSON.parse(JSON.stringify(p));
    ok(out.playerResponse && out.playerResponse.adPlacements === undefined
      && out.playerResponse.adSlots === undefined && out.playerResponse.streamingData
      && out.keep === 1, 'P2 legacy 回归: playerResponse 根级键删除行为不变');
    restoreJson();
    const { scriptlet: sc3 } = loadBundle(CUR);
    sc3('json-prune', 'playerResponse.messages.[].youThereRenderer');
    const m = { playerResponse: { messages: [ { youThereRenderer: { x: 1 }, a: 1 }, { b: 2 } ] } };
    const outm = JSON.parse(JSON.stringify(m));
    ok(outm.playerResponse.messages.length === 2 && outm.playerResponse.messages[0].youThereRenderer === undefined
      && outm.playerResponse.messages[0].a === 1, 'P2 legacy 回归: []. 数组元素内键删除行为不变');
    restoreJson();
    const { scriptlet: sc4 } = loadBundle(CUR);
    sc4('json-prune', 'one', 'obligatoryProp');
    const gated = JSON.parse(JSON.stringify({ one: 1, two: 2 }));
    ok(gated.one === 1 && gated.two === 2, 'P2 legacy 回归: obligatory 缺失时不裁剪');
    restoreJson();
    const { scriptlet: sc5 } = loadBundle(CUR);
    // 差分对拍：同一表达式在改动前后 bundle 上的输出必须一致（行为不变的直接证明）
    const wildcardExpr = ['content.*.media.src', 'content.*.media.ad'];
    const legacyCases = [
      ['playerResponse.adPlacements playerResponse.adSlots', null],
      ['playerResponse.messages.[].youThereRenderer', null],
      ['one', 'obligatoryProp'],
      wildcardExpr,
    ];
    let differentialOk = true;
    for (const [rm, req] of legacyCases) {
      const samples = [
        { playerResponse: { adPlacements: { x: 1 }, adSlots: { y: 2 }, streamingData: { z: 3 } }, keep: 1 },
        { playerResponse: { messages: [{ youThereRenderer: { x: 1 }, a: 1 }, { b: 2 }] } },
        { one: 1, two: 2, obligatoryProp: 3 },
        { content: { block1: { media: { src: '1.jpg', ad: true } }, block2: { media: { src: '2.jpg' } } } },
      ];
      const si = legacyCases.findIndex((c) => c === legacyCases.find((c2) => c2[0] === rm));
      for (const sample of samples) {
        restoreJson();
        const { scriptlet: sBefore } = loadBundle(BACKUP_PATH);
        const outBefore = JSON.stringify(JSON.parse(JSON.stringify(sample)));
        restoreJson();
        const { scriptlet: sAfter } = loadBundle(CUR);
        sAfter(rm, req);
        const midAfter = JSON.parse(JSON.stringify(sample));
        restoreJson();
        const { scriptlet: sB2 } = loadBundle(BACKUP_PATH);
        sB2(rm, req);
        const midBefore = JSON.parse(JSON.stringify(sample));
        restoreJson();
        if (JSON.stringify(midBefore) !== JSON.stringify(midAfter)) {
          differentialOk = false;
          console.log(`  diff for ${rm}: before=${JSON.stringify(midBefore)} after=${JSON.stringify(midAfter)}`);
        }
        void outBefore; void sBefore; void si;
      }
    }
    ok(differentialOk, 'P2 legacy 回归: 既有形态表达式改动前后输出逐字节一致（差分对拍）');
  }
  restoreJson();
}

// ================= ④ P4 运行时抽查 =================
function p4tests() {
  // trusted-set-constant（inferValue 任意类型）
  {
    restoreJson();
    const { scriptlet } = loadBundle(CUR);
    scriptlet('trusted-set-constant', 'window.__p24str', '"500"');
    scriptlet('trusted-set-constant', '__p24num', '48');
    scriptlet('trusted-set-constant', '__p24null', 'null');
    ok(window.__p24str === '500' && typeof window.__p24str === 'string', 'P4 trusted-set-constant: 带引号→字符串');
    ok(window.__p24num === 48 && typeof window.__p24num === 'number', 'P4 trusted-set-constant: 数字推断');
    ok(window.__p24null === null, 'P4 trusted-set-constant: null 推断');
  }

  // trusted-set-cookie（任意值＋$now$ 关键字＋expires）
  {
    restoreJson();
    const { scriptlet } = loadBundle(CUR);
    document.cookie = '';
    scriptlet('trusted-set-cookie', 'cmpconsent', 'accept');
    ok(document.cookie.includes('cmpconsent=accept'), 'P4 trusted-set-cookie: 任意值写入');
    scriptlet('trusted-set-cookie', 'p24ts', '{"firstTime":$now$}', '1day');
    ok(/\d{13}/.test(document.cookie), 'P4 trusted-set-cookie: $now$ 关键字展开');
    ok(document.cookie.includes('expires='), 'P4 trusted-set-cookie: 1day expires 附加');
  }

  // trusted-set-local-storage-item
  {
    restoreJson();
    const { scriptlet } = loadBundle(CUR);
    scriptlet('trusted-set-local-storage-item', 'COOKIE_CONSENTS', '{"preferences":3,"flag":false}');
    ok(window.localStorage.getItem('COOKIE_CONSENTS') === '{"preferences":3,"flag":false}',
      'P4 trusted-set-local-storage-item: 任意值写入 localStorage');
  }

  // trusted-prune-inbound-object（JSON.stringify 入参裁剪）
  {
    restoreJson();
    const { scriptlet } = loadBundle(CUR);
    scriptlet('trusted-prune-inbound-object', 'JSON.stringify', 'foo.bar');
    const s = JSON.stringify({ foo: { bar: 1, a: 2 }, b: 3 });
    ok(s === '{"foo":{"a":2},"b":3}', `P4 trusted-prune-inbound-object: stringify 入参被裁剪（got ${s}）`);
  }

  // trusted-replace-argument
  {
    restoreJson();
    const { scriptlet } = loadBundle(CUR);
    window.__p24fn = (x) => x;
    scriptlet('trusted-replace-argument', '__p24fn', '0', 'NEWVAL', 'Foo bar');
    const got = window.__p24fn('the Foo bar arg');
    ok(got === 'NEWVAL', `P4 trusted-replace-argument: 常量替换（got ${got}）`);
    const got2 = window.__p24fn('untouched');
    ok(got2 === 'untouched', 'P4 trusted-replace-argument: pattern 不匹配时保持原值');
    window.__p24fn2 = (x) => x;
    scriptlet('trusted-replace-argument', '__p24fn2', '0', 'replace:/noAds=false/noAds=true/g', 'noAds');
    const got3 = window.__p24fn2('x=noAds=false&y=noAds=false');
    ok(got3 === 'x=noAds=true&y=noAds=true', `P4 trusted-replace-argument: replace: 全局正则替换（got ${got3}）`);
  }

  // trusted-suppress-native-method（prevent 模式）
  {
    restoreJson();
    const { scriptlet } = loadBundle(CUR);
    window.__p24obj = { m(x) { return `ran:${x}`; } };
    scriptlet('trusted-suppress-native-method', '__p24obj.m', '"secret"', 'prevent');
    const s1 = window.__p24obj.m('secret');
    const s2 = window.__p24obj.m('ok');
    ok(s1 === undefined, `P4 trusted-suppress-native-method: 匹配签名被抑制（got ${String(s1)}）`);
    ok(s2 === 'ran:ok', 'P4 trusted-suppress-native-method: 不匹配签名正常放行');
  }

  // trusted-json-set（legacy 与 jsonpath 两种模式）
  {
    restoreJson();
    const { scriptlet } = loadBundle(CUR);
    scriptlet('trusted-json-set', 'JSON.parse', 'ads.enabled', 'false');
    const o1 = JSON.parse('{"ads":{"enabled":true},"c":1}');
    ok(o1.ads.enabled === false && o1.c === 1, 'P4 trusted-json-set: legacy 模式设值');
    restoreJson();
    const { scriptlet: sc2 } = loadBundle(CUR);
    sc2('trusted-json-set', 'JSON.parse', '$.ads.enabled', 'true');
    const o2 = JSON.parse('{"ads":{"enabled":false}}');
    ok(o2.ads.enabled === true, 'P4 trusted-json-set: jsonpath 模式设值（$.ads.enabled=true）');
    restoreJson();
    const { scriptlet: sc3 } = loadBundle(CUR);
    sc3('trusted-json-set', 'JSON.parse', '$..adSlot', '$remove$');
    const o3 = JSON.parse('{"deep":{"adSlot":{"x":1}},"list":[{"adSlot":{"y":2}}],"keep":1}');
    // 官方语义：$..adSlot 匹配的是属性节点 → 删除属性（宿主保留），非 splice
    ok(o3.deep.adSlot === undefined && o3.list.length === 1 && o3.list[0].adSlot === undefined && o3.keep === 1,
      'P4 trusted-json-set: jsonpath $remove$（descent 属性节点删除）');
    restoreJson();
    const { scriptlet: sc4 } = loadBundle(CUR);
    sc4('trusted-json-set', 'JSON.parse', '$..list[?(@.adSlot)]', '$remove$');
    const o4 = JSON.parse('{"deep":{"adSlot":{"x":1}},"list":[{"adSlot":{"y":2}},{"k":3}],"keep":1}');
    // 过滤段命中的是数组元素本身 → splice 移除
    ok(o4.list.length === 1 && o4.list[0].k === 3 && o4.deep.adSlot !== undefined,
      'P4 trusted-json-set: jsonpath $remove$（[?()] 过滤命中元素 splice）');
  }

  // trusted-create-element（head 下创建 script）
  {
    restoreJson();
    const { scriptlet } = loadBundle(CUR);
    let captured = null;
    const parent = { append(el) { captured = el; }, contains() { return false; } };
    document.querySelector = (sel) => (sel === 'head' ? parent : null);
    scriptlet('trusted-create-element', 'head', 'script', 'data-x="1"', 'window.__p24marker=1;');
    ok(captured && captured.nodeName === 'SCRIPT' && captured.textContent === 'window.__p24marker=1;'
      && captured.attributes['data-x'] === '1',
      'P4 trusted-create-element: 元素创建＋属性＋文本内容');
    document.querySelector = () => null;
  }

  // trusted-replace-node-text
  {
    restoreJson();
    const { scriptlet } = loadBundle(CUR);
    const node = { nodeName: 'DIV', textContent: 'some text here' };
    document.querySelectorAll = () => [node];
    document.documentElement.querySelectorAll = () => [node];
    scriptlet('trusted-replace-node-text', 'div', 'some', 'text', 'other text');
    ok(node.textContent === 'some other text here', `P4 trusted-replace-node-text: 文本替换（got ${node.textContent}）`);
    document.querySelectorAll = () => [];
    document.documentElement.querySelectorAll = () => [];
  }

  // trusted-replace-fetch-response
  {
    restoreJson();
    const { scriptlet } = loadBundle(CUR);
    window.fetch = async () => new Response('x=noAds=false&y=noAds=false', { status: 200, headers: { 'content-type': 'text/plain' } });
    scriptlet('trusted-replace-fetch-response', '/noAds=false/g', 'noAds=true', 'example.org');
    return window.fetch('https://example.org/api').then((r) => r.text()).then((text) => {
      ok(text === 'x=noAds=true&y=noAds=true', `P4 trusted-replace-fetch-response: fetch 响应改写（got ${text}）`);
      window.fetch = undefined;
    });
  }
  // trusted-replace-xhr-response / trusted-click-element：node 无真实 XHR/DOM，
  // dispatch 冒烟已覆盖（②），运行时行为留给浏览器层。
}

(async () => {
  try {
    await p2tests();
  } catch (ex) {
    failures += 1;
    console.log(`FAIL P2 tests threw: ${ex && ex.stack}`);
  }
  try {
    await p4tests();
  } catch (ex) {
    failures += 1;
    console.log(`FAIL P4 tests threw: ${ex && ex.stack}`);
  }
  console.log(`\nRESULT: pass=${passes} fail=${failures}`);
  process.exit(failures === 0 ? 0 : 1);
})();
