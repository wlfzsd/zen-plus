var scriptlet=function(){"use strict";function e(e){return null!==e&&("function"==typeof e||"object"==typeof e)}const t="Zen";function n(e){return{log(n){for(var r=arguments.length,o=new Array(r>1?r-1:0),s=1;s<r;s++)o[s-1]=arguments[s];console.log(`${t} (${e}): ${n}`,...o)},debug(n){for(var r=arguments.length,o=new Array(r>1?r-1:0),s=1;s<r;s++)o[s-1]=arguments[s];console.debug(`${t} (${e}): ${n}`,...o)},info(n){for(var r=arguments.length,o=new Array(r>1?r-1:0),s=1;s<r;s++)o[s-1]=arguments[s];console.info(`${t} (${e}): ${n}`,...o)},warn(n){for(var r=arguments.length,o=new Array(r>1?r-1:0),s=1;s<r;s++)o[s-1]=arguments[s];console.warn(`${t} (${e}): ${n}`,...o)},error(n){for(var r=arguments.length,o=new Array(r>1?r-1:0),s=1;s<r;s++)o[s-1]=arguments[s];console.error(`${t} (${e}): ${n}`,...o)}}}const r=n("defineProxyChain");function o(t,n,o){const s=n.split(".");let i=t;for(let t=0;t<s.length;t++){const a=s[t];if(t===s.length-1){let e,t=i;for(;null!==t&&void 0===(e=Object.getOwnPropertyDescriptor(t,a));)t=Object.getPrototypeOf(t);const n=e?.get,r=e?.set;let s=e?.value;Object.defineProperty(i,a,{configurable:!0,enumerable:!0,get(){return o.onGet&&o.onGet(),n?n.call(this):s},set(e){o.onSet&&o.onSet(),r?r.call(this,e):s=e}})}else{if(!(a in i)){let n;const r=(t,n)=>new Proxy(t,{get(t,s){const i=Reflect.get(t,s,t);return 1===n.length&&s===n[0]?(o.onGet&&o.onGet(),i):s===n[0]&&e(i)?r(i,n.slice(1)):i},set:(e,t,r)=>(1===n.length&&t===n[0]&&o.onSet&&o.onSet(),Reflect.set(e,t,r))});return void Object.defineProperty(i,a,{configurable:!0,enumerable:!0,get:()=>e(n)?r(n,s.slice(t+1)):n,set(e){n=e}})}if(i=i[a],!e(i))return void r.warn(`Giving up on "${n}": "${a}" is not an object`)}}}const s=/^\/((?:[^/\\\r\n]|\\.)+)\/([gimsuy]*)$/;function i(e){const t=e.match(s);if(null===t)return null;try{return new RegExp(t[1],t[2])}catch{return null}}function a(e,t){try{return new RegExp(function(e){return e.replace(/[.*+?^${}()|[\]\\]/g,"\\$&")}(e),t)}catch{return null}}function l(){return Math.random().toString(36).substring(2,15)}const p=n("abort-current-inline-script");const c=n("abort-on-property-read");function u(e){if("string"!=typeof e||0===e.length)return void c.warn("property should be a non-empty string");const t=l();o(window,e,{onGet:()=>{throw c.info(`Blocked ${e} read`),new ReferenceError(`Aborted script with ID: ${t}`)}}),window.addEventListener("error",e=>{e.error instanceof ReferenceError&&e.error.message.includes(t)&&e.preventDefault()})}const f=n("abort-on-property-write");function d(e){if("string"!=typeof e||0===e.length)return void f.warn("property should be a non-empty string");const t=l();o(window,e,{onSet:()=>{throw f.info(`Blocked ${e} write`),new ReferenceError(`Aborted script with ID: ${t}`)}}),window.addEventListener("error",e=>{e.error instanceof ReferenceError&&e.error.message.includes(t)&&e.preventDefault()})}function y(e){const{stack:t}=new Error;return void 0!==t&&t.split("\n").slice(2).map(e=>e.trim()).some(t=>e.test(t))}const w=n("abort-on-stack-trace");function g(e,t){if("string"!=typeof e||0===e.length)return void w.warn("property should be a non-empty string");if("string"!=typeof t||0===t.length)return void w.warn("stack should be a non-empty string");const n=i(t)||a(t),r=l(),s=()=>{if(null!==n&&y(n))throw w.info(`Blocked script on '${t}' stack`),new ReferenceError(`Aborted script with ID: ${r}`)};o(window,e,{onGet:s,onSet:s}),window.addEventListener("error",e=>{e.error instanceof ReferenceError&&e.error.message.includes(r)&&e.preventDefault()})}function v(e,t,n){const r=m(e),o=m(t);let s=null;return"string"==typeof n&&(s=i(n)||a(n)),function(e){if("object"==typeof e&&(null===s||y(s))){if(o.length>0){let t=!1;for(const n of o)if(b(e,n)){t=!0;break}if(!t)return}for(const t of r)h(e,t)}}}function h(e,t){if(0===t.length||null==e)return;const[n,...r]=t;if("*"===n){const n=Object.getOwnPropertyNames(e).filter(t=>"object"==typeof e[t]||Array.isArray(e[t]));for(const o of n)h(e[o],t),h(e[o],r);return}if("[]"!==n)Object.hasOwn(e,n)&&(0===r.length?delete e[n]:h(e[n],r));else{if(!Array.isArray(e))return;for(let t=0;t<e.length;t++)h(e[t],r)}}function b(e,t){if(null==e)return!1;if(0===t.length)return!0;if("*"===t[0]){const n=Object.getOwnPropertyNames(e).filter(t=>"object"==typeof e[t]||Array.isArray(e[t]));for(const r of n)if(b(e[r],t)||b(e[r],t.slice(1)))return!0;return!1}if("[]"===t[0]){if(!Array.isArray(e))return!1;for(let n=0;n<e.length;n++)if(b(e[n],t.slice(1)))return!0;return!1}return!!Object.hasOwn(e,t[0])&&b(e[t[0]],t.slice(1))}function m(e){return"string"!=typeof e?[]:e.split(/\s+/).filter(Boolean).map(e=>e.split("."))}const R=n("json-prune");const P=["url","method","credentials","cache","redirect","referrer","referrerPolicy","integrity","mode"];function x(e){if(""===e||"*"===e)return{};const t={},n=e.split(" ");for(const e of n){if(!e.includes(":")){t.url=i(e)||a(e)||e;continue}const[n,r]=e.split(":");if(""===n||void 0===r||""===r)throw new Error(`Invalid segment: "${e}"`);if(!P.includes(n))throw new Error(`Invalid segment key: "${n}"`);t[n]=i(r)||r}return t}function E(e,t){let n;n=t[0]instanceof Request?t[0]:void 0!==t[1]?{...t[1],url:t[0].toString()}:{url:t[0].toString()};for(const t of Object.keys(e))if(void 0===n[t]||!j(e[t],n[t]))return!1;return!0}function S(e){for(var t=arguments.length,n=new Array(t>1?t-1:0),r=1;r<t;r++)n[r-1]=arguments[r];const o={method:n[0],url:n[1].toString()};for(const t of Object.keys(e))if(void 0===o[t]||!j(e[t],o[t]))return!1;return!0}function j(e,t){return"string"==typeof e?e===t:e.test(t)}function k(e){const t=parseInt(e,10);if(isNaN(t))throw new Error("input is NaN");if(!Number.isFinite(t))throw new Error("input is Infinite");return t}const O=/length:(\d+)-(\d+)/,$="ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*()_+=~";function T(e){if("false"===e)return"";let t,n;const r=e.match(O);if("true"===e)t=10,n=10;else{if(!r)throw new Error("Invalid pattern");if(t=k(r[1]),n=k(r[2]),n>5e5)throw new Error("maxLength exceeds limit");if(t>n)throw new Error("minLength exceeds maxLength")}var o,s;return function(e){let t="";for(let n=0;n<e;n++)t+=$.charAt(Math.floor(76*Math.random()));return t}((o=t,s=n,o=Math.ceil(o),s=Math.floor(s),Math.floor(Math.random()*(s-o+1)+o)))}const L=n("json-prune-fetch-response");const A=n("json-prune-xhr-response"),H=Symbol("requestHeaders"),N=Symbol("shouldPrune"),M=Symbol("openArgs"),I=Symbol("responseHeaders");const D=n("no-protected-audience");const q=n("no-topics");const C=n("nowebrtc");const X=n("prevent-addEventListener");const G=n("prevent-fetch");function F(e){let t,n,r=arguments.length>1&&void 0!==arguments[1]?arguments[1]:"emptyObj",o=arguments.length>2?arguments[2]:void 0;if("undefined"!=typeof fetch&&"undefined"!=typeof Proxy&&"undefined"!=typeof Response){switch(r){case"":case"emptyObj":t="{}";break;case"emptyArr":t="[]";break;case"emptyStr":t="";break;default:return void G.warn(`Invalid responseBody: ${r}`)}if("string"!=typeof o||"basic"===o||"cors"===o||"opaque"===o){try{n=x(e)}catch(e){G.warn("Error parsing props",e)}fetch=new Proxy(fetch,{apply:async(e,r,s)=>{if(!E(n,s))return Reflect.apply(e,r,s);const i=new Response(t,{status:200,statusText:"OK",headers:new Headers({"Content-Length":t.length.toString(),"Content-Type":"application/json",Date:(new Date).toUTCString()})});if("opaque"===o)Object.defineProperties(i,{url:{value:""},status:{value:0},statusText:{value:""},body:{value:null},type:{value:"opaque"},headers:{value:new Headers}});else{let e;e=s[0]instanceof URL?s[0].toString():s[0]instanceof Request?s[0].url:s[0],Object.defineProperties(i,{url:{value:e},type:{value:o||"basic"}})}return i}})}else G.warn(`Invalid responseType: ${o}`)}else G.warn("Either fetch, Proxy, or Response is not supported in this environment")}function U(e){let{callback:t,delay:n,matchCallback:r,matchDelay:o}=e;if("function"!=typeof t&&"string"!=typeof t)return!1;if("string"!=typeof r||o&&!z(o))return!1;const{isInverted:s,regexp:i}=J(r),{isInverted:a,match:l}=W(o),p=K(n),c=String(t),u=i?.test(c)!==s,f=null!==l&&p===l!==a;return null===l?u:r?u&&f:f}const B=e=>{let t=!1;return e.startsWith("!")&&(e=e.slice(1),t=!0),{isReverse:t,value:e}},J=e=>{const{isReverse:t,value:n}=B(e);return{isInverted:t,regexp:i(n)||a(n)}},W=e=>{const{isReverse:t,value:n}=B(e),r=parseInt(n,10);return{isInverted:t,match:Number.isNaN(r)?null:r}},z=e=>{if("string"!=typeof e)return!1;const t=e=>"number"==typeof e&&!Number.isNaN(e)&&Number.isFinite(e);return e.startsWith("!")?t(+e.slice(1)):t(+e)},K=e=>{const t=Math.floor(parseInt(e,10));return"number"!=typeof t||Number.isNaN(t)?e:t},V=n("prevent-set-interval");const Z=n("prevent-set-timeout");const Q=n("prevent-window-open");function Y(e,t,n){let r;try{r="1"===e||"0"===e?function(e,t,n){let r,o=!1;"0"===e&&(o=!0);if("string"==typeof t&&t.length>0&&(r=(i(t)||a(t))??void 0,void 0===r))throw new Error("Could not parse search");let s=()=>{};if("trueFunc"===n)s=()=>!0;else if("string"==typeof n&&n.length>0){if(!n.startsWith("{")||!n.endsWith("}"))throw new Error(`Invalid replacement ${n}`);const e=n.slice(1,-1).split("=");if(2!==e.length||0===e[0].length||"noopFunc"!==e[1])throw new Error(`Invalid replacement ${n}`);s={[e[0]]:()=>{}}}return(e,t,n)=>{let i;if(0===n.length||null==n[0])i="";else if("string"==typeof n[0])i=n[0];else{if(!(n[0]instanceof URL))return Reflect.apply(e,t,n);i=n[0].toString()}if(void 0!==r){let s=r.test(i);if(o&&(s=!s),!s)return Reflect.apply(e,t,n)}return Q.info("Preventing window.open",{args:n}),s}}(e,t,n):function(e,t,n){let r,o,s=!1;if("string"==typeof e&&e.length>0&&(s="!"===e[0],s&&(e=e.slice(1)),r=(i(e)||a(e))??void 0,void 0===r))throw new Error("Could not parse match");"string"==typeof t&&t.length>0&&(o=k(t));if("string"==typeof n&&"obj"!==n&&"blank"!==n)throw new Error(`Replacement type ${n} not supported`);return(e,t,i)=>{let a,l,p;if(0===i.length||null==i[0])a="";else if("string"==typeof i[0])a=i[0];else{if(!(i[0]instanceof URL))return Reflect.apply(e,t,i);a=i[0].toString()}if(void 0!==r){let n=r.test(a);if(s&&(n=!n),!n)return Reflect.apply(e,t,i)}if(Q.info("Preventing window.open",{args:i}),"blank"===n)return Reflect.apply(e,t,["about:blank",...i.slice(1)]);if("obj"===n)l=document.createElement("object"),l.data=a;else l=document.createElement("iframe"),l.src=a;if(l.style.setProperty("height","1px","important"),l.style.setProperty("width","1px","important"),l.style.setProperty("position","absolute","important"),l.style.setProperty("top","-9999px","important"),document.body.appendChild(l),void 0!==o&&setTimeout(()=>{l.remove()},1e3*o),"obj"===n){if(p=l.contentWindow,null===p||"object"!=typeof p)return null;Object.defineProperties(p,{closed:{value:!1},opener:{value:window},frameElement:{value:null}})}else p=new Proxy(window,{get:(e,t,n)=>{if("closed"===t)return!1;const r=Reflect.get(e,t,n);return"function"==typeof r?()=>{}:r},set:()=>!0});return p}}(e,t,n)}catch(e){return void Q.warn("Error while making handler",{ex:e})}window.open=new Proxy(window.open,{apply:r})}const _=n("prevent-xhr"),ee=Symbol("prevent"),te=Symbol("url"),ne=Symbol("responseHeaders");function re(e,t){if("undefined"==typeof Proxy)return void _.warn("Proxy is not supported in this environment");if("string"!=typeof e)return void _.warn("propsToMatch is required");let n;try{n=x(e)}catch(e){return void _.warn("error parsing props",e)}XMLHttpRequest.prototype.open=new Proxy(XMLHttpRequest.prototype.open,{apply:(e,t,r)=>t[ee]||S(n,...r)?(_.debug("Preventing XHR request",r),t[ee]=!0,t[te]=r[1].toString(),Reflect.apply(e,t,r)):(t[ee]=!1,Reflect.apply(e,t,r))}),XMLHttpRequest.prototype.send=new Proxy(XMLHttpRequest.prototype.send,{apply:(e,n,r)=>{if(!n[ee])return Reflect.apply(e,n,r);setTimeout(()=>{const e={readyState:{value:n.DONE,writable:!1},statusText:{value:"OK",writable:!1},response:{value:"",writable:!1},responseText:{value:"",writable:!1},responseURL:{value:n[te],writable:!1},responseXML:{value:null,writable:!1},status:{value:200,writable:!1}};switch(n[ne]={date:(new Date).toUTCString(),"content-length":"0"},n.responseType){case"arraybuffer":e.response.value=new ArrayBuffer(0),n[ne]["content-type"]="application/octet-stream";break;case"blob":e.response.value=new Blob([]),n[ne]["content-type"]="application/octet-stream";break;case"document":{const t=(new DOMParser).parseFromString("","text/html");e.response.value=t,e.responseXML.value=t,n[ne]["content-type"]="text/html",n[ne]["content-length"]=t.documentElement.outerHTML.length.toString();break}case"json":e.response.value={},e.responseText.value="{}",n[ne]["content-type"]="application/json",n[ne]["content-length"]="2";break;default:if(n[ne]["content-type"]="text/plain","string"!=typeof t||""===t)break;try{const r=T(t);e.response.value=r,e.responseText.value=r,n[ne]["content-length"]=r.length.toString()}catch(e){_.error("Generating random response text",e)}}Object.defineProperties(n,e),n.dispatchEvent(new Event("readystatechange")),n.dispatchEvent(new Event("load")),n.dispatchEvent(new Event("loadend"))},1)}}),XMLHttpRequest.prototype.getResponseHeader=new Proxy(XMLHttpRequest.prototype.getResponseHeader,{apply:(e,t,n)=>t[ee]?t.readyState!==t.DONE?null:t[ne][n[0].toLowerCase()]??null:Reflect.apply(e,t,n)}),XMLHttpRequest.prototype.getAllResponseHeaders=new Proxy(XMLHttpRequest.prototype.getAllResponseHeaders,{apply:(e,t,n)=>{if(!t[ee])return Reflect.apply(e,t,n);if(t.readyState!==t.DONE)return null;let r="";for(const[e,n]of Object.entries(t[ne]))r+=`${e}: ${n}\r\n`;return r}})}const oe=n("sanitize-clipboard");function se(e,t){try{const n=new URL(e);let r=!1;for(const e of t)if("string"==typeof e)n.searchParams.has(e)&&(n.searchParams.delete(e),r=!0);else for(const t of Array.from(n.searchParams.keys()))e.test(t)&&(n.searchParams.delete(t),r=!0);return r?n.toString():e}catch{return e}}const ie=n("set-constant");const ae=new Set(["undefined","false","true","null","yes","no","on","off","accept","accepted","reject","rejected","allowed","denied","forbidden","forever",""]);function le(e){if(ae.has(e.toLowerCase()))return e;if("emptyArr"===e)return"[]";if("emptyObj"===e)return"{}";if("$remove$"===e)return"$remove$";const t=parseInt(e);if(!isNaN(t)&&t>=0&&t<=32767)return e;throw new Error("Invalid value")}function pe(e,t){const n=i(t);if(null!==n){const t=Object.keys(e);for(const r of t)n.test(r)&&e.removeItem(r)}else e.removeItem(t)}const ce=n("index"),ue=new Map([["abort-current-inline-script",function(e,t){if("string"!=typeof e||0===e.length)return void p.warn("property should be a non-empty string");let n;"string"==typeof t&&t.length>0&&(n=i(t)||a(t));const r=l(),s=document.currentScript,c=()=>{const t=document.currentScript;if(t instanceof HTMLScriptElement&&t!==s&&(!n||n.test(t.textContent||"")))throw p.info(`Blocked ${e} in currentScript`),new ReferenceError(`Aborted script with ID: ${r}`)};o(window,e,{onGet:c,onSet:c}),window.addEventListener("error",e=>{e.error instanceof ReferenceError&&e.error.message.includes(r)&&e.preventDefault()})}],["abort-on-property-read",u],["aopr",u],["abort-on-property-write",d],["aopw",d],["abort-on-stack-trace",g],["aost",g],["json-prune",function(e,t,n){if("undefined"==typeof Proxy)return void R.warn("Proxy not available in this environment");if("string"!=typeof e||0===e.length)return void R.warn("propsToRemove should be a non-empty string");const r=v(e,t,n);JSON.parse=new Proxy(JSON.parse,{apply:(e,t,n)=>{const o=Reflect.apply(e,t,n);return r(o),o}}),"undefined"!=typeof Response&&(Response.prototype.json=new Proxy(Response.prototype.json,{apply:async(e,t,n)=>{const o=await Reflect.apply(e,t,n);return r(o),o}}))}],["nowebrtc",function(){if(!window.RTCPeerConnection)return;const e=e=>{C.log(`document tried to create an RTCPeerConnection with config: ${e}`)},t=()=>{};e.prototype={close:t,createDataChannel:t,createOffer:t,setRemoteDescription:t,toString:()=>"[object RTCPeerConnection]"};const n=window.RTCPeerConnection;window.RTCPeerConnection=e,n.prototype&&(n.prototype.createDataChannel=()=>({close:t,send:t}))}],["prevent-fetch",F],["no-fetch-if",F],["prevent-xhr",re],["no-xhr-if",re],["set-local-storage-item",function(e,t){if("string"!=typeof e)throw new Error(`key should be string, is ${e}`);if("string"!=typeof t)throw new Error(`value should be string, is ${t}`);const n=le(t);"$remove$"===n?pe(localStorage,e):localStorage.setItem(e,n)}],["set-session-storage-item",function(e,t){if("string"!=typeof e)throw new Error(`key should be string, is ${e}`);if("string"!=typeof t)throw new Error(`value should be string, is ${t}`);const n=le(t);"$remove$"===n?pe(sessionStorage,e):sessionStorage.setItem(e,n)}],["set-constant",function(t,n,r,o,s){let l;switch(void 0!==s&&ie.warn("setProxyTrap will be ignored"),n){case"undefined":l=void 0;break;case"false":l=!1;break;case"true":l=!0;break;case"null":l=null;break;case"emptyObj":l={};break;case"emptyArr":l=[];break;case"noopFunc":l=()=>{};break;case"noopCallbackFunc":l=()=>()=>{};break;case"trueFunc":l=()=>!0;break;case"falseFunc":l=()=>!1;break;case"throwFunc":l=()=>{throw new Error};break;case"noopPromiseResolve":l=()=>Promise.resolve(new Response("",{status:200,statusText:"OK"}));break;case"noopPromiseReject":l=()=>Promise.reject();break;case"":l="";break;case"-1":l=-1;break;case"yes":l="yes";break;case"no":l="no";break;default:{const e=parseInt(n,10);if(!isNaN(e)&&e>=0&&e<=32767){l=n;break}throw new Error("Invalid value")}}const p=l;switch(o){case"asFunction":l=()=>p;break;case"asCallback":l=()=>()=>p;break;case"asResolved":l=()=>Promise.resolve(p);break;case"asRejected":l=()=>Promise.reject(p)}let c;void 0!==r&&""!==r&&(c=i(r)||a(r)),c??=null;const u=()=>{ie.debug(`Returning fake value for property window.${t}`,{value:n})};if(!t.includes(".")){let e=window[t];const n=Object.getOwnPropertyDescriptor(window,t);return void Object.defineProperty(window,t,{configurable:!0,get:()=>null===c||y(c)?(u(),l):"function"==typeof n?.get?n.get.apply(window):e,set:"function"==typeof n?.set?n?.set.bind(window):t=>{e=t}})}const f=Object,d=Function,w=t=>{let n,r;return(o,s)=>{if(1===t.length&&t[0]===s)return u(),l;let i=Reflect.get(o,s,o);const a=f.getOwnPropertyDescriptor(o,s);if(a&&"value"in a&&!a.configurable&&!a.writable)return i;if("function"==typeof i&&/\{\s*\[native code\]/.test(d.prototype.toString.call(i))&&(void 0!==r&&r[s]?i=r[s]:(i=i.bind(o),void 0===r&&(r={}),r[s]=i)),t[0]!==s||!e(i)||null!==c&&!y(c))return i;if(n?.link===i)return n.proxy;const p=new Proxy(i,{get:w(t.slice(1))});return n={link:i,proxy:p},p}},g=t.split("."),v=g[0];let h=window,b=window[v];for(let t=1;t<g.length;t++){const n=g[t];if(null!=b&&t===g.length-1){const e=Object.getOwnPropertyDescriptor(b,n);let t=b[n];return void Object.defineProperty(b,n,{configurable:!0,get:()=>null===c||y(c)?(u(),l):"function"==typeof e?.get?e.get.apply(b):t,set:"function"==typeof e?.set?e?.set.bind(b):e=>{t=e}})}if(null==b||null==b[n]){const n=Object.getOwnPropertyDescriptor(h,g[t-1]);let r,o=b;return void Object.defineProperty(h,g[t-1],{configurable:!0,get:()=>{const s=n?.get?n.get.apply(h):o;if(!e(s)||null!==c&&!y(c))return s;if(r?.capturedValue===s)return r.proxy;const i=new Proxy(s,{get:w(g.slice(t))});return r={capturedValue:s,proxy:i},i},set:"function"==typeof n?.set?n?.set.bind(h):e=>{o=e}})}h=b,b=b[n]}throw ie.warn("Hit an invariant in setConstant",{property:t,value:n,stack:r}),new Error("Invariant hit")}],["json-prune-fetch-response",function(e,t,n,r){if("undefined"==typeof Proxy||"undefined"==typeof fetch||"undefined"==typeof Response)return void L.warn("Either Proxy, fetch, or Response is not supported in this environment");if("string"!=typeof e||0===e.length)return void L.warn("propsToRemove cannot be empty");let o;if("string"==typeof n)try{o=x(n)}catch(e){return void L.warn("error parsing propsToMatch",e)}const s=v(e,t,r);window.fetch=new Proxy(window.fetch,{apply:async(e,t,n)=>{if(o&&!E(o,n))return Reflect.apply(e,t,n);const r=await Reflect.apply(e,t,n),i=r.clone();let a;try{a=await r.json()}catch{return i}s(a);const l=new Response(JSON.stringify(a),{status:r.status,statusText:r.statusText,headers:r.headers});return Object.defineProperties(l,{url:{value:r.url},type:{value:r.type},ok:{value:r.ok},redirected:{value:r.redirected}}),l}})}],["json-prune-xhr-response",function(e,t,n,r){if("undefined"==typeof Proxy)return void A.warn("Proxy is not supported in this environment");if("string"!=typeof e||0===e.length)return void A.warn("propsToMatch cannot be empty");let o;if("string"==typeof n)try{o=x(n)}catch(e){return void A.warn("error parsing propsToMatch",e)}const s=v(e,t,r),{open:i,send:a}=window.XMLHttpRequest.prototype;XMLHttpRequest.prototype.open=new Proxy(XMLHttpRequest.prototype.open,{apply:(e,t,n)=>void 0===o||S(o,...n)?(t[N]=!0,t[H]=[],t[M]=n,t[I]=[],Reflect.apply(e,t,n)):Reflect.apply(e,t,n)}),XMLHttpRequest.prototype.setRequestHeader=new Proxy(XMLHttpRequest.prototype.setRequestHeader,{apply:(e,t,n)=>t[N]?(t[H].push(n),Reflect.apply(e,t,n)):Reflect.apply(e,t,n)}),XMLHttpRequest.prototype.send=new Proxy(XMLHttpRequest.prototype.send,{apply:(e,t,n)=>{if(!t[N])return Reflect.apply(e,t,n);const r=new XMLHttpRequest;r.addEventListener("readystatechange",async()=>{if(r.readyState!==XMLHttpRequest.DONE)return;const e={readyState:{value:r.readyState,writable:!1},responseURL:{value:r.responseURL,writable:!1},status:{value:r.status,writable:!1},statusText:{value:r.statusText,writable:!1},response:{value:r.response,writable:!1}};try{e.responseXML={value:r.responseXML,writable:!1}}catch{}try{e.responseText={value:r.responseText,writable:!1}}catch{}try{if(""===r.responseType||"text"===r.responseType){const t=JSON.parse(r.responseText);s(t);const n=JSON.stringify(t);e.response={value:n,writable:!1},e.responseText={value:n,writable:!1}}else if("arraybuffer"===r.responseType){const t=(new TextDecoder).decode(r.response),n=JSON.parse(t);s(n);const o=(new TextEncoder).encode(JSON.stringify(n));e.response={value:o,writable:!1}}else if("blob"===r.responseType){const t=await r.response.text(),n=JSON.parse(t);s(n);const o=new Blob([JSON.stringify(n)]);e.response={value:o,writable:!1}}else{if("json"!==r.responseType)throw new Error(`Unsupported type: ${r.responseType}`);s(r.response),e.response={value:r.response,writable:!1}}}catch(e){A.error("Error parsing/pruning response",e)}Object.defineProperties(t,e);const n=r.getAllResponseHeaders();for(const e of n.trim().split(/[\r\n]+/)){const[n,r]=e.split(": ");t[I].push([n,r])}setTimeout(()=>{t.dispatchEvent(new Event("readystatechange")),t.dispatchEvent(new Event("load")),t.dispatchEvent(new Event("loadend"))},1)}),i.apply(r,t[M]);for(const[e,n]of t[H])r.setRequestHeader(e,n);try{a.apply(r,n)}catch(r){return A.error("Error sending substitute request",r),Reflect.apply(e,t,n)}}}),XMLHttpRequest.prototype.getResponseHeader=new Proxy(XMLHttpRequest.prototype.getResponseHeader,{apply:(e,t,n)=>{if(!t[N])return Reflect.apply(e,t,n);let r=null;for(const[e,o]of t[I])if(e===n[0]){r=o;break}return r}}),XMLHttpRequest.prototype.getAllResponseHeaders=new Proxy(XMLHttpRequest.prototype.getAllResponseHeaders,{apply:(e,t,n)=>t[N]?t[I].map(e=>{let[t,n]=e;return`${t}: ${n}`}).join("\r\n"):Reflect.apply(e,t,n)})}],["prevent-window-open",Y],["nowoif",Y],["prevent-setTimeout",function(){let e=arguments.length>0&&void 0!==arguments[0]?arguments[0]:"",t=arguments.length>1&&void 0!==arguments[1]?arguments[1]:"";"undefined"!=typeof Proxy?window.setTimeout=new Proxy(window.setTimeout,{apply:(n,r,o)=>{const[s,i]=o;return U({callback:s,delay:i,matchCallback:e,matchDelay:t})?(Z.info(`Prevented setTimeout(${String(s)}, ${i})"`),0):Reflect.apply(n,r,o)}}):Z.warn("Proxy not available in this environment")}],["prevent-setInterval",function(){let e=arguments.length>0&&void 0!==arguments[0]?arguments[0]:"",t=arguments.length>1&&void 0!==arguments[1]?arguments[1]:"";"undefined"!=typeof Proxy?window.setInterval=new Proxy(window.setInterval,{apply:(n,r,o)=>{const[s,i]=o;return U({callback:s,delay:i,matchCallback:e,matchDelay:t})?(V.info(`Prevented setInterval(${String(s)}, ${i})"`),0):Reflect.apply(n,r,o)}}):V.warn("Proxy not available in this environment")}],["prevent-addEventListener",function(){let e=arguments.length>0&&void 0!==arguments[0]?arguments[0]:"",t=arguments.length>1&&void 0!==arguments[1]?arguments[1]:"";if(!e&&!t)return;const n=i(e)||a(e),r=i(t)||a(t);if(!n&&!r)return;const o={apply(e,t,o){const[s,i]=o,a=(e=>{try{return"object"==typeof e&&"handleEvent"in e&&"function"==typeof e.handleEvent?e.handleEvent.toString():e.toString()}catch{return""}})(i);let l=!1;if(n&&!r?l=n.test(s):r&&!n?l=r.test(s):n&&r&&(l=n.test(s)&&r.test(a)),!l)return Reflect.apply(e,t,o);X.info(`Blocked addEventListener("${s}", ${a})`)}};window.addEventListener=new Proxy(window.addEventListener,o),document.addEventListener=new Proxy(document.addEventListener,o),Element.prototype.addEventListener=new Proxy(window.Element.prototype.addEventListener,o),EventTarget.prototype.addEventListener=new Proxy(window.EventTarget.prototype.addEventListener,o)}],["no-topics",function(){const e="browsingTopics";if("function"!=typeof Document||"object"!=typeof Document.prototype)return;const t=Object.getOwnPropertyDescriptor(Document.prototype,e);if(!t||!t.configurable||"function"!=typeof t.value)return;const n=t.value,r=new Proxy(n,{apply:()=>(q.info("Preventing Topics API usage"),Promise.resolve(new Response("",{status:200,statusText:"OK"})))});Object.defineProperty(Document.prototype,e,{configurable:!0,get:()=>r,set:()=>{}})}],["no-protected-audience",function(){if("function"!=typeof Navigator||"object"!=typeof Navigator.prototype)return;const e={joinAdInterestGroup:()=>Promise.resolve(),runAdAuction:()=>Promise.resolve(null),leaveAdInterestGroup:()=>Promise.resolve(),clearOriginJoinedAdInterestGroups:()=>Promise.resolve(),createAuctionNonce:()=>"",updateAdInterestGroups:()=>{}};for(const t of Object.keys(e)){const n=Object.getOwnPropertyDescriptor(Navigator.prototype,t);if(!n||!n.configurable||"function"!=typeof n.value)continue;const r=n.value,o=new Proxy(r,{apply:()=>(D.info(`Preventing usage of Protected Audience API: ${t}`),e[t]())});Object.defineProperty(Navigator.prototype,t,{configurable:!0,get:()=>o,set:()=>{}})}}],["sanitize-clipboard",function(e){if("string"!=typeof e||0===e.length)return void oe.warn("params should be a non-empty string");const t=e.split(" ").map(e=>i(e)||e);if(navigator.clipboard){const e={async apply(e,n,r){const[o]=r,s=await Promise.resolve(o),i=se(String(s),t);return i===s?Reflect.apply(e,n,r):(oe.info(`Sanitized clipboard for '${String(s)}'`),Reflect.apply(e,n,[i]))}};navigator.clipboard.writeText=new Proxy(navigator.clipboard.writeText,e)}document.addEventListener("copy",e=>{const n=e;let r=window.getSelection()?.toString()??"";if(!r){const e=document.activeElement;!e||"INPUT"!==e.tagName&&"TEXTAREA"!==e.tagName||null===e.selectionStart||null===e.selectionEnd||e.selectionStart===e.selectionEnd||(r=e.value.slice(e.selectionStart,e.selectionEnd))}if(!r)return;const o=se(r,t);o!==r&&n?.clipboardData&&(n.clipboardData.setData("text/plain",o),n.preventDefault(),oe.info(`Sanitized clipboard for '${r}'`))},!0)}],["prevent-element-src-loading", function(nm, mt) {
        const lg = n("prevent-element-src-loading");
        if (typeof Proxy === "undefined" || typeof Reflect === "undefined") return;
        if (typeof nm !== "string" || 0 === nm.length) return void lg.warn("tagName should be a non-empty string");
        const tag = nm.toLowerCase();
        const ctor = { script: HTMLScriptElement, img: HTMLImageElement, iframe: HTMLIFrameElement, link: HTMLLinkElement }[tag];
        if (!ctor) return;
        const prop = "link" === tag ? "href" : "src";
        const re = mt === void 0 ? null : (i(mt) || a(mt));
        const test = v => (re ? re.test(String(v)) : true);
        const MARK = "prevent-element-src-loading";
        const mocks = {
            script: "data:text/javascript;base64,KCk9Pnt9",
            img: "data:image/gif;base64,R0lGODlhAQABAAAAACH5BAEKAAEALAAAAAABAAEAAAICTAEAOw==",
            iframe: "data:text/html;base64, PGRpdj48L2Rpdj4=",
            link: "data:text/plain;base64="
        };
        const isMatchedEl = el => "string" === typeof el.nodeName && el.nodeName.toLowerCase() === tag && !!mocks[tag];
        ctor.prototype.setAttribute = new Proxy(Element.prototype.setAttribute, {
            apply(target, thisArg, as) {
                if (!as[0] || !as[1] || !thisArg) return Reflect.apply(target, thisArg, as);
                const attrName = String(as[0]).toLowerCase();
                if (attrName === prop && isMatchedEl(thisArg) && test(as[1])) {
                    lg.info(`Prevented ${tag} source loading`);
                    thisArg.setAttribute(MARK, "matched");
                    return Reflect.apply(target, thisArg, [attrName, mocks[tag]]);
                }
                return Reflect.apply(target, thisArg, as);
            }
        });
        const desc = Object.getOwnPropertyDescriptor(ctor.prototype, prop);
        if (desc) {
            Object.defineProperty(ctor.prototype, prop, {
                enumerable: true,
                configurable: true,
                get() { return desc.get.call(this); },
                set(v) {
                    if (isMatchedEl(this) && test(v)) {
                        lg.info(`Prevented ${tag} source loading`);
                        this.setAttribute(MARK, "matched");
                        desc.set.call(this, mocks[tag]);
                        return;
                    }
                    desc.set.call(this, v);
                }
            });
        }
        const onerr = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "onerror");
        if (onerr) {
            Object.defineProperty(HTMLElement.prototype, "onerror", {
                enumerable: true,
                configurable: true,
                get() { return onerr.get.call(this); },
                set(cb) {
                    if (this.getAttribute && this.getAttribute(MARK) === "matched") {
                        onerr.set.call(this, () => {});
                        return;
                    }
                    onerr.set.call(this, cb);
                }
            });
        }
        const nativeAEL = EventTarget.prototype.addEventListener;
        EventTarget.prototype.addEventListener = new Proxy(nativeAEL, {
            apply(target, thisArg, as) {
                if (!as[0] || !as[1] || !thisArg) return Reflect.apply(target, thisArg, as);
                if ("error" === as[0] && "function" === typeof thisArg.getAttribute && thisArg.getAttribute(MARK) === "matched") {
                    return Reflect.apply(target, thisArg, ["error", () => {}]);
                }
                return Reflect.apply(target, thisArg, as);
            }
        });
        window.addEventListener("error", ev => {
            const el = ev && ev.target;
            if (!el || !el.nodeName || el.nodeName.toLowerCase() !== tag) return;
            const u = el.src;
            if (!u || !test(u)) return;
            el.onerror = "function" === typeof el.onload ? el.onload : () => {};
        }, true);
    }],
    ["remove-node-text", function(nm, tm, ps) {
        const lg = n("remove-node-text");
        if (typeof tm !== "string" || 0 === tm.length) return void lg.warn("textMatch should be a non-empty string");
        if (typeof nm !== "string" || 0 === nm.length) return void lg.warn("nodeName should be a non-empty string");
        const isStrName = !(nm.startsWith("/") && nm.endsWith("/"));
        const nameMatch = isStrName ? null : (i(nm) || a(nm));
        const textMatch = tm.startsWith("/") ? (i(tm) || a(tm)) : tm;
        const selector = isStrName ? nm : "*";
        const handle = nodes => {
            for (const node of nodes) {
                const text = node.textContent;
                if (!text) continue;
                const nameOk = isStrName
                    ? "string" === typeof node.nodeName && node.nodeName.toLowerCase() === nm
                    : null !== nameMatch && nameMatch.test(String(node.nodeName).toLowerCase());
                const textOk = "string" === typeof textMatch
                    ? text.includes(textMatch)
                    : null !== textMatch && textMatch.test(text);
                if (nameOk && textOk) {
                    try {
                        node.textContent = "";
                        lg.info(`Removed text of ${node.nodeName}`);
                    } catch (ex) {}
                }
            }
        };
        const existing = () => {
            let parents = [document];
            if (ps) {
                try { parents = [].slice.call(document.querySelectorAll(ps)); } catch (ex) { return; }
            }
            for (const parent of parents) {
                if ("#text" === selector) {
                    handle([].slice.call(parent.childNodes).filter(x => 3 === x.nodeType));
                } else {
                    try { handle([].slice.call(parent.querySelectorAll(selector))); } catch (ex) {}
                }
            }
        };
        existing();
        try {
            const mo = new MutationObserver(mutations => {
                mo.disconnect();
                try {
                    if (ps) existing();
                    else {
                        const added = [];
                        for (const m of mutations) for (const nd of m.addedNodes) added.push(nd);
                        handle(added);
                    }
                } finally {
                    mo.observe(document.documentElement, { subtree: true, childList: true });
                }
            });
            mo.observe(document.documentElement, { subtree: true, childList: true });
            setTimeout(() => mo.disconnect(), 10000);
        } catch (ex) {}
    }],
    ["remove-attr", function(at, sel, ap) {
        const lg = n("remove-attr");
        if (typeof at !== "string" || 0 === at.length) return void lg.warn("attrs should be a non-empty string");
        const attrs = at.split(/\s*\|\s*/);
        if (!sel) sel = attrs.map(x => `[${window.CSS && CSS.escape ? CSS.escape(x) : x}]`).join(",");
        try { document.createDocumentFragment().querySelector(sel); } catch (ex) { return void lg.warn(`Invalid selector arg: '${sel}'`); }
        const applying = ap === void 0 ? "asap stay" : String(ap);
        const flags = applying.trim().split(" ").filter(x => "asap" === x || "complete" === x || "stay" === x);
        const has = f => flags.indexOf(f) !== -1;
        const run = () => {
            let nodes;
            try { nodes = document.querySelectorAll(sel); } catch (ex) { return; }
            let removed = false;
            for (const node of nodes) {
                for (const attr of attrs) {
                    try {
                        if (node.hasAttribute(attr)) {
                            node.removeAttribute(attr);
                            removed = true;
                        }
                    } catch (ex) {}
                }
            }
            if (removed) lg.info(`Removed attributes ${at}`);
        };
        const observe = () => {
            try {
                const mo = new MutationObserver(() => {
                    mo.disconnect();
                    try { run(); } finally { mo.observe(document.documentElement, { childList: true, subtree: true, attributes: true }); }
                });
                mo.observe(document.documentElement, { childList: true, subtree: true, attributes: true });
            } catch (ex) {}
        };
        if (has("asap")) {
            if ("loading" === document.readyState) window.addEventListener("DOMContentLoaded", run, { once: true });
            else run();
        }
        if ("complete" !== document.readyState && has("complete")) {
            window.addEventListener("load", () => {
                run();
                if (has("stay")) observe();
            }, { once: true });
        } else if (has("stay")) {
            if (-1 === applying.indexOf(" ")) run();
            observe();
        }
    }],
    ["remove-class", function(cl, sel, ap) {
        const lg = n("remove-class");
        if (typeof cl !== "string" || 0 === cl.length) return void lg.warn("classes should be a non-empty string");
        const classes = cl.split(/\s*\|\s*/);
        const selectors = sel ? [sel] : classes.map(x => `.${window.CSS && CSS.escape ? CSS.escape(x) : x}`);
        for (const s of selectors) {
            try { document.createDocumentFragment().querySelector(s); } catch (ex) { return void lg.warn(`Invalid selector arg: '${s}'`); }
        }
        const applying = ap === void 0 ? "asap stay" : String(ap);
        const flags = applying.trim().split(" ").filter(x => "asap" === x || "complete" === x || "stay" === x);
        const has = f => flags.indexOf(f) !== -1;
        const run = () => {
            const nodes = new Set();
            for (const s of selectors) {
                let els;
                try { els = document.querySelectorAll(s); } catch (ex) { continue; }
                for (const el of els) nodes.add(el);
            }
            let removed = false;
            for (const node of nodes) {
                for (const className of classes) {
                    try {
                        if (node.classList.contains(className)) {
                            node.classList.remove(className);
                            removed = true;
                        }
                    } catch (ex) {}
                }
            }
            if (removed) lg.info(`Removed classes ${cl}`);
        };
        const observe = () => {
            try {
                const mo = new MutationObserver(() => {
                    mo.disconnect();
                    try { run(); } finally { mo.observe(document.documentElement, { childList: true, subtree: true, attributes: true, attributeFilter: ["class"] }); }
                });
                mo.observe(document.documentElement, { childList: true, subtree: true, attributes: true, attributeFilter: ["class"] });
            } catch (ex) {}
        };
        if (has("asap")) {
            if ("loading" === document.readyState) window.addEventListener("DOMContentLoaded", run, { once: true });
            else run();
        }
        if ("complete" !== document.readyState && has("complete")) {
            window.addEventListener("load", () => {
                run();
                if (has("stay")) observe();
            }, { once: true });
        } else if (has("stay")) {
            if (-1 === applying.indexOf(" ")) run();
            observe();
        }
    }],
    ["set-attr", function(sel, at, vl) {
        const lg = n("set-attr");
        if (typeof sel !== "string" || 0 === sel.length) return void lg.warn("selector should be a non-empty string");
        if (typeof at !== "string" || 0 === at.length) return void lg.warn("attr should be a non-empty string");
        const value = vl === void 0 ? "" : vl;
        const validAttrName = x => {
            try { document.createAttribute(x); return true; } catch (ex) { return false; }
        };
        const copyFrom = "string" === typeof value && value.startsWith("[") && value.endsWith("]") && validAttrName(value.slice(1, -1));
        const intVal = parseInt(value, 10);
        const isValidValue = 0 === String(value).length
            || (!Number.isNaN(intVal) && intVal >= 0 && intVal <= 32767)
            || ["true", "false"].indexOf(String(value).toLowerCase()) !== -1;
        if (!copyFrom && !isValidValue) return void lg.warn(`Invalid attribute value provided: '${value}'`);
        try { document.createDocumentFragment().querySelector(sel); } catch (ex) { return void lg.warn(`Invalid selector arg: '${sel}'`); }
        const missing = new WeakSet();
        const apply = () => {
            let els;
            try { els = document.querySelectorAll(sel); } catch (ex) { return; }
            for (const el of els) {
                try {
                    let target = value;
                    if (copyFrom) {
                        const src = el.getAttribute(value.slice(1, -1));
                        if (null === src) {
                            if (!missing.has(el)) {
                                missing.add(el);
                                lg.warn(`No element attribute found to copy value from: ${value}`);
                            }
                        } else {
                            missing.delete(el);
                        }
                        target = String(src);
                    }
                    if (el.getAttribute(at) !== target) el.setAttribute(at, target);
                } catch (ex) {}
            }
        };
        apply();
        try {
            const mo = new MutationObserver(() => {
                mo.disconnect();
                try { apply(); } finally { mo.observe(document.documentElement, { childList: true, subtree: true, attributes: true }); }
            });
            mo.observe(document.documentElement, { childList: true, subtree: true, attributes: true });
        } catch (ex) {}
    }],
    ["set-cookie", function(nm, vl, pt, dm) {
        const lg = n("set-cookie");
        if (typeof nm !== "string" || 0 === nm.length) return void lg.warn("name should be a non-empty string");
        const allowed = new Set(["true", "t", "false", "f", "yes", "y", "no", "n", "ok", "on", "off", "accept", "accepted", "notaccepted", "reject", "rejected", "allow", "allowed", "disallow", "deny", "denied", "enable", "enabled", "disable", "disabled", "necessary", "required", "hide", "hidden", "essential", "nonessential", "checked", "unchecked", "forbidden", "forever", "declined", "mandatory", "all"]);
        let valid = null;
        if ("string" === typeof vl && 0 !== vl.length) {
            if (allowed.has(vl.toLowerCase())) valid = vl;
            else if ("emptyArr" === vl) valid = "[]";
            else if ("emptyObj" === vl) valid = "{}";
            else if (/^\d+$/.test(vl)) {
                const num = parseFloat(vl);
                if (!Number.isNaN(num) && Math.abs(num) <= 32767) valid = num;
            }
        }
        if (null === valid) return void lg.warn(`Invalid cookie value: '${vl}'`);
        const path = pt === void 0 ? "/" : pt;
        if ("/" !== path && "none" !== path) return void lg.warn(`Invalid cookie path: '${path}'`);
        const domain = dm === void 0 ? "" : dm;
        if (!document.location.origin.includes(domain)) return void lg.warn(`Cookie domain not matched by origin: '${domain}'`);
        if (nm.includes(";") || String(valid).includes(";")) return void lg.warn("Invalid cookie name or value");
        let cookie = `${nm}=${valid}`;
        if (nm.startsWith("__Host-")) cookie += "; path=/; secure";
        else {
            if ("/" === path) cookie += "; path=/";
            if (nm.startsWith("__Secure-")) cookie += "; secure";
            if (domain) cookie += `; domain=${domain}`;
        }
        document.cookie = cookie;
        lg.info(`Set cookie ${nm}`);
    }],
    ["set-cookie-reload", function(nm, vl, pt, dm) {
        const lg = n("set-cookie-reload");
        if (typeof nm !== "string" || 0 === nm.length) return void lg.warn("name should be a non-empty string");
        const isSet = () => {
            const kw = /\$(?:currentISODate|currentDate|now)\$/g;
            const used = "string" === typeof vl ? vl.match(kw) : null;
            let mt = null;
            if (used) {
                const esc = s => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
                const kp = k => "$now$" === k
                    ? `(\\d{${String(Date.now()).length}})`
                    : ("$currentDate$" === k
                        ? "(\\w{3} \\w{3} \\d{2} \\d{4} \\d{2}:\\d{2}:\\d{2} GMT[+-]\\d{4}(?: \\([^)]*\\))?)"
                        : "(\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}\\.\\d{3}Z)");
                const chunks = vl.split(kw);
                mt = new RegExp(`^${chunks.map((c, ix) => esc(c) + (used[ix] ? kp(used[ix]) : "")).join("")}$`);
            }
            return document.cookie.split(";").some(part => {
                const pos = part.indexOf("=");
                if (-1 === pos) return false;
                const cn = part.slice(0, pos).trim();
                if (nm !== cn) return false;
                const cv = part.slice(pos + 1).trim();
                if (mt) {
                    const m = mt.exec(cv);
                    if (null === m) return false;
                    const now = Date.now();
                    return m.slice(1).every(tv => {
                        const t = /^\d+$/.test(tv) ? parseInt(tv, 10) : new Date(tv).getTime();
                        return t > now - 864e5;
                    });
                }
                return vl === cv;
            });
        };
        if (isSet()) return;
        const allowed = new Set(["true", "t", "false", "f", "yes", "y", "no", "n", "ok", "on", "off", "accept", "accepted", "notaccepted", "reject", "rejected", "allow", "allowed", "disallow", "deny", "denied", "enable", "enabled", "disable", "disabled", "necessary", "required", "hide", "hidden", "essential", "nonessential", "checked", "unchecked", "forbidden", "forever", "declined", "mandatory", "all"]);
        let valid = null;
        if ("string" === typeof vl && 0 !== vl.length) {
            if (allowed.has(vl.toLowerCase())) valid = vl;
            else if ("emptyArr" === vl) valid = "[]";
            else if ("emptyObj" === vl) valid = "{}";
            else if (/^\d+$/.test(vl)) {
                const num = parseFloat(vl);
                if (!Number.isNaN(num) && Math.abs(num) <= 32767) valid = num;
            }
        }
        if (null === valid) return void lg.warn(`Invalid cookie value: '${vl}'`);
        const path = pt === void 0 ? "/" : pt;
        if ("/" !== path && "none" !== path) return void lg.warn(`Invalid cookie path: '${path}'`);
        const domain = dm === void 0 ? "" : dm;
        if (!document.location.origin.includes(domain)) return void lg.warn(`Cookie domain not matched by origin: '${domain}'`);
        if (nm.includes(";") || String(valid).includes(";")) return void lg.warn("Invalid cookie name or value");
        let cookie = `${nm}=${valid}`;
        if (nm.startsWith("__Host-")) cookie += "; path=/; secure";
        else {
            if ("/" === path) cookie += "; path=/";
            if (nm.startsWith("__Secure-")) cookie += "; secure";
            if (domain) cookie += `; domain=${domain}`;
        }
        document.cookie = cookie;
        lg.info(`Set cookie ${nm}`);
        if (isSet()) window.location.reload();
    }],
    ["remove-cookie", function(mt) {
        const re = mt === void 0 ? null : (i(mt) || a(mt));
        const test = v => (re ? re.test(String(v)) : true);
        const kill = (name, host) => {
            const spec = `${name}=`;
            const exp = "; expires=Thu, 01 Jan 1970 00:00:00 GMT";
            const d1 = `; domain=${host}`;
            const d2 = `; domain=.${host}`;
            const p = "; path=/";
            document.cookie = spec + exp;
            document.cookie = spec + d1 + exp;
            document.cookie = spec + d2 + exp;
            document.cookie = spec + p + exp;
            document.cookie = spec + d1 + p + exp;
            document.cookie = spec + d2 + p + exp;
        };
        const rmAll = () => {
            const parts = document.cookie.split(";");
            const hosts = document.location.hostname.split(".");
            for (const part of parts) {
                const pos = part.indexOf("=");
                if (-1 === pos) continue;
                const name = part.slice(0, pos).trim();
                if (!test(name)) continue;
                for (let j = 0; j < hosts.length; j += 1) {
                    const host = hosts.slice(j).join(".");
                    if (host) kill(name, host);
                }
            }
        };
        rmAll();
        window.addEventListener("beforeunload", rmAll);
    }],
    ["prevent-eval-if", function(sr) {
        const lg = n("prevent-eval-if");
        const re = sr === void 0 ? null : (i(sr) || a(sr));
        const test = v => (re ? re.test(String(v)) : true);
        const native = window.eval;
        window.eval = function(payload) {
            if (!test(payload)) return native.call(window, payload);
            lg.info(`Prevented eval(${String(payload).slice(0, 200)})`);
            return void 0;
        }.bind(window);
        try { window.eval.toString = native.toString.bind(native); } catch (ex) {}
    }],
    ["prevent-refresh", function(ds) {
        const lg = n("prevent-refresh");
        const num = s => {
            const p = parseInt(s, 10);
            return Number.isNaN(p) ? null : p;
        };
        const metas = () => {
            try { return [].slice.call(document.querySelectorAll('meta[http-equiv="refresh" i][content]')); } catch (ex) {
                try { return [].slice.call(document.querySelectorAll('meta[http-equiv="refresh"][content]')); } catch (ex2) { return []; }
            }
        };
        const stop = () => {
            const ms = metas();
            if (0 === ms.length) return;
            let sec = num(ds);
            if (null === sec) {
                const delays = [];
                for (const m of ms) {
                    const c = m.getAttribute("content") || "";
                    if (0 === c.length) continue;
                    const li = c.indexOf(";");
                    const v = num(-1 !== li ? c.substring(0, li) : c);
                    if (null !== v) delays.push(v);
                }
                if (0 === delays.length) return;
                sec = Math.min.apply(null, delays);
            }
            setTimeout(() => {
                try { window.stop(); } catch (ex) {}
                lg.info("Prevented meta refresh");
            }, sec * 1000);
        };
        if ("loading" === document.readyState) document.addEventListener("DOMContentLoaded", stop, { once: true });
        else stop();
    }],
    ["adjust-setTimeout", function(mc, md, bo) {
        const lg = n("adjust-setTimeout");
        const native = window.setTimeout;
        const re = mc === void 0 ? null : (i(mc) || a(mc));
        const cbStr = cb => {
            if ("function" === typeof cb) return Function.prototype.toString.call(cb);
            try { return String(cb); } catch (ex) { return Object.prototype.toString.call(cb); }
        };
        const wantDelay = "*" === md ? null : (() => {
            const p = parseInt(md, 10);
            return Number.isNaN(p) ? 1000 : p;
        })();
        const factor = (() => {
            const f = parseFloat(bo);
            let v = Number.isNaN(f) || !Number.isFinite(f) ? 0.05 : f;
            if (v < 0.001) v = 0.001;
            if (v > 50) v = 50;
            return v;
        })();
        window.setTimeout = function(cb, dl) {
            if ("function" !== typeof cb && "string" !== typeof cb) {
                lg.warn(`Scriptlet can't be applied because of invalid callback: '${cbStr(cb)}'`);
            } else if ((re ? re.test(cbStr(cb)) : true) && (null === wantDelay || dl === wantDelay)) {
                dl *= factor;
                lg.info(`Adjusted setTimeout(${cbStr(cb)}, ${dl})`);
            }
            for (var len = arguments.length, rest = new Array(len > 2 ? len - 2 : 0), k = 2; k < len; k += 1) rest[k - 2] = arguments[k];
            return native.apply(window, [cb, dl].concat(rest));
        };
    }],
    ["adjust-setInterval", function(mc, md, bo) {
        const lg = n("adjust-setInterval");
        const native = window.setInterval;
        const re = mc === void 0 ? null : (i(mc) || a(mc));
        const cbStr = cb => {
            if ("function" === typeof cb) return Function.prototype.toString.call(cb);
            try { return String(cb); } catch (ex) { return Object.prototype.toString.call(cb); }
        };
        const wantDelay = "*" === md ? null : (() => {
            const p = parseInt(md, 10);
            return Number.isNaN(p) ? 1000 : p;
        })();
        const factor = (() => {
            const f = parseFloat(bo);
            let v = Number.isNaN(f) || !Number.isFinite(f) ? 0.05 : f;
            if (v < 0.001) v = 0.001;
            if (v > 50) v = 50;
            return v;
        })();
        window.setInterval = function(cb, dl) {
            if ("function" !== typeof cb && "string" !== typeof cb) {
                lg.warn(`Scriptlet can't be applied because of invalid callback: '${cbStr(cb)}'`);
            } else if ((re ? re.test(cbStr(cb)) : true) && (null === wantDelay || dl === wantDelay)) {
                dl *= factor;
                lg.info(`Adjusted setInterval(${cbStr(cb)}, ${dl})`);
            }
            for (var len = arguments.length, rest = new Array(len > 2 ? len - 2 : 0), k = 2; k < len; k += 1) rest[k - 2] = arguments[k];
            return native.apply(window, [cb, dl].concat(rest));
        };
    }],
    ["prevent-innerHTML", function(sel, pat, rp) {
        const lg = n("prevent-innerHTML");
        const s = sel === void 0 ? "" : sel;
        const p = pat === void 0 ? "" : pat;
        const nd = Object.getOwnPropertyDescriptor(Element.prototype, "innerHTML");
        if (!nd) return;
        const inv = "string" === typeof p && p.startsWith("!");
        const pv = inv ? p.slice(1) : p;
        const re = "" === pv ? new RegExp(".?") : (i(pv) || a(pv));
        const matched = v => {
            const m = re.test(String(v));
            return inv ? !m : m;
        };
        const chk = (el, v) => {
            if (s) {
                if (!el || "function" !== typeof el.matches) return false;
                try { if (!el.matches(s)) return false; } catch (ex) { return false; }
            }
            return matched(v);
        };
        Object.defineProperty(Element.prototype, "innerHTML", {
            configurable: true,
            enumerable: true,
            get() {
                const v = nd.get ? nd.get.call(this) : nd.value;
                if (rp !== void 0 && chk(this, v)) {
                    lg.info("Replaced innerHTML getter value");
                    return rp;
                }
                return v;
            },
            set(v) {
                if (chk(this, v)) {
                    lg.info("Prevented innerHTML assignment");
                    return;
                }
                if (nd.set) nd.set.call(this, v);
            }
        });
    }],
    ["prevent-navigation", function(up) {
        const lg = n("prevent-navigation");
        const nv = window.navigation;
        if (!nv) return;
        const logOnly = up === void 0;
        let pat = null;
        if ("location.href" === up) pat = window.location.href;
        else if ("string" === typeof up) pat = i(up) || a(up);
        nv.addEventListener("navigate", ev => {
            const u = ev && ev.destination && ev.destination.url;
            if (!u) return;
            if (logOnly) {
                lg.info(`Navigating to: ${u}`);
                return;
            }
            const blocked = pat ? ("string" === typeof pat ? u === pat : pat.test(u)) : false;
            if (blocked) {
                ev.preventDefault();
                lg.info(`Blocked navigation to: ${u}`);
            }
        });
    }],
    ["log", function() { console.log([].slice.call(arguments)); }],
    ["log-eval", function() {
        const lg = n("log-eval");
        const native = window.eval;
        window.eval = function(str) {
            lg.info(`eval("${str}")`);
            return native(str);
        };
        const nativeFunction = window.Function;
        const fnWrapper = function() {
            for (var len = arguments.length, as = new Array(len), k = 0; k < len; k += 1) as[k] = arguments[k];
            lg.info(`new Function(${as.join(", ")})`);
            return nativeFunction.apply(this, as);
        };
        fnWrapper.prototype = Object.create(nativeFunction.prototype);
        fnWrapper.prototype.constructor = fnWrapper;
        window.Function = fnWrapper;
    }],
    ["log-addEventListener", function(np) {
        const lg = n("log-addEventListener");
        const native = window.EventTarget.prototype.addEventListener;
        const ls = l => {
            try {
                if (l && "object" === typeof l && "handleEvent" in l && "function" === typeof l.handleEvent) return l.handleEvent.toString();
            } catch (ex) {}
            try { return l.toString(); } catch (ex2) { return ""; }
        };
        const w = function(tp, li) {
            if ("string" === typeof tp && null !== li && void 0 !== li) {
                lg.info(`addEventListener("${tp}", ${ls(li)})`);
            }
            let cx = this;
            if (this && this.constructor && "Window" === this.constructor.name && this !== window) cx = window;
            return native.apply(cx, arguments);
        };
        if ("true" === np) {
            window.EventTarget.prototype.addEventListener = w;
        } else {
            const d = { configurable: true, set() {}, get: () => w };
            Object.defineProperty(window.EventTarget.prototype, "addEventListener", d);
            Object.defineProperty(window, "addEventListener", d);
            Object.defineProperty(document, "addEventListener", d);
        }
    }],
    ["log-on-stack-trace", function(pp) {
        const lg = n("log-on-stack-trace");
        if (!pp) return;
        const table = () => {
            const info = {};
            String(new Error().stack).split("\n").slice(2).forEach(line => {
                const l = line.replace(/^ {4}at /, "");
                const m = /\(([^)]+)\)/.exec(l);
                if (m) info[l.split(" ").slice(0, -1).join(" ") || "?"] = m[1];
                else info["?"] = l;
            });
            return info;
        };
        const hook = (base, prop) => {
            let val;
            try { val = base[prop]; } catch (ex) { val = void 0; }
            Object.defineProperty(base, prop, {
                configurable: true,
                get() {
                    lg.info(`Get ${prop}`);
                    console.table(table());
                    return val;
                },
                set(v) {
                    lg.info(`Set ${prop}`);
                    console.table(table());
                    val = v;
                }
            });
        };
        const walk = (owner, property) => {
            const dot = property.indexOf(".");
            if (-1 === dot) {
                hook(owner, property);
                return;
            }
            const seg = property.slice(0, dot);
            const rest = property.slice(dot + 1);
            let next;
            try { next = owner[seg]; } catch (ex) { next = void 0; }
            if (null === next || void 0 === next) {
                Object.defineProperty(owner, seg, {
                    configurable: true,
                    get() { return next; },
                    set(v) {
                        next = v;
                        if (v) walk(v, rest);
                    }
                });
                return;
            }
            walk(next, rest);
        };
        walk(window, pp);
    }]]);return function(e){if(ue.has(e)){for(var t=arguments.length,n=new Array(t>1?t-1:0),r=1;r<t;r++)n[r-1]=arguments[r];ue.get(e)(...n)}else ce.debug(`Scriptlet ${e} does not exist or is not yet implemented.`)}}();
