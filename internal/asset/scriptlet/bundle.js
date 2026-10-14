var scriptlet=function(){"use strict";function e(e){return null!==e&&("function"==typeof e||"object"==typeof e)}const t="Zen";function n(e){return{log(n){for(var r=arguments.length,o=new Array(r>1?r-1:0),s=1;s<r;s++)o[s-1]=arguments[s];console.log(`${t} (${e}): ${n}`,...o)},debug(n){for(var r=arguments.length,o=new Array(r>1?r-1:0),s=1;s<r;s++)o[s-1]=arguments[s];console.debug(`${t} (${e}): ${n}`,...o)},info(n){for(var r=arguments.length,o=new Array(r>1?r-1:0),s=1;s<r;s++)o[s-1]=arguments[s];console.info(`${t} (${e}): ${n}`,...o)},warn(n){for(var r=arguments.length,o=new Array(r>1?r-1:0),s=1;s<r;s++)o[s-1]=arguments[s];console.warn(`${t} (${e}): ${n}`,...o)},error(n){for(var r=arguments.length,o=new Array(r>1?r-1:0),s=1;s<r;s++)o[s-1]=arguments[s];console.error(`${t} (${e}): ${n}`,...o)}}}const r=n("defineProxyChain");function o(t,n,o){const s=n.split(".");let i=t;for(let t=0;t<s.length;t++){const a=s[t];if(t===s.length-1){let e,t=i;for(;null!==t&&void 0===(e=Object.getOwnPropertyDescriptor(t,a));)t=Object.getPrototypeOf(t);const n=e?.get,r=e?.set;let s=e?.value;Object.defineProperty(i,a,{configurable:!0,enumerable:!0,get(){return o.onGet&&o.onGet(),n?n.call(this):s},set(e){o.onSet&&o.onSet(),r?r.call(this,e):s=e}})}else{if(!(a in i)){let n;const r=(t,n)=>new Proxy(t,{get(t,s){const i=Reflect.get(t,s,t);return 1===n.length&&s===n[0]?(o.onGet&&o.onGet(),i):s===n[0]&&e(i)?r(i,n.slice(1)):i},set:(e,t,r)=>(1===n.length&&t===n[0]&&o.onSet&&o.onSet(),Reflect.set(e,t,r))});return void Object.defineProperty(i,a,{configurable:!0,enumerable:!0,get:()=>e(n)?r(n,s.slice(t+1)):n,set(e){n=e}})}if(i=i[a],!e(i))return void r.warn(`Giving up on "${n}": "${a}" is not an object`)}}}const s=/^\/((?:[^/\\\r\n]|\\.)+)\/([gimsuy]*)$/;function i(e){const t=e.match(s);if(null===t)return null;try{return new RegExp(t[1],t[2])}catch{return null}}function a(e,t){try{return new RegExp(function(e){return e.replace(/[.*+?^${}()|[\]\\]/g,"\\$&")}(e),t)}catch{return null}}function l(){return Math.random().toString(36).substring(2,15)}const p=n("abort-current-inline-script");const c=n("abort-on-property-read");function u(e){if("string"!=typeof e||0===e.length)return void c.warn("property should be a non-empty string");const t=l();o(window,e,{onGet:()=>{throw c.info(`Blocked ${e} read`),new ReferenceError(`Aborted script with ID: ${t}`)}}),window.addEventListener("error",e=>{e.error instanceof ReferenceError&&e.error.message.includes(t)&&e.preventDefault()})}const f=n("abort-on-property-write");function d(e){if("string"!=typeof e||0===e.length)return void f.warn("property should be a non-empty string");const t=l();o(window,e,{onSet:()=>{throw f.info(`Blocked ${e} write`),new ReferenceError(`Aborted script with ID: ${t}`)}}),window.addEventListener("error",e=>{e.error instanceof ReferenceError&&e.error.message.includes(t)&&e.preventDefault()})}function y(e){const{stack:t}=new Error;return void 0!==t&&t.split("\n").slice(2).map(e=>e.trim()).some(t=>e.test(t))}const w=n("abort-on-stack-trace");function g(e,t){if("string"!=typeof e||0===e.length)return void w.warn("property should be a non-empty string");if("string"!=typeof t||0===t.length)return void w.warn("stack should be a non-empty string");const n=i(t)||a(t),r=l(),s=()=>{if(null!==n&&y(n))throw w.info(`Blocked script on '${t}' stack`),new ReferenceError(`Aborted script with ID: ${r}`)};o(window,e,{onGet:s,onSet:s}),window.addEventListener("error",e=>{e.error instanceof ReferenceError&&e.error.message.includes(r)&&e.preventDefault()})}function v(e,t,n){const r=m(e),o=m(t);let s=null;return"string"==typeof n&&(s=i(n)||a(n)),function(e){if("object"==typeof e&&(null===s||y(s))){if(o.length>0){let t=!1;for(const n of o)if(b(e,n)){t=!0;break}if(!t)return}for(const t of r)h(e,t)}}}function h(e,t){if(0===t.length||null==e)return;const[n,...r]=t;if("*"===n){const n=Object.getOwnPropertyNames(e).filter(t=>"object"==typeof e[t]||Array.isArray(e[t]));for(const o of n)h(e[o],t),h(e[o],r);return}if("[]"!==n)Object.hasOwn(e,n)&&(0===r.length?delete e[n]:h(e[n],r));else{if(!Array.isArray(e))return;for(let t=0;t<e.length;t++)h(e[t],r)}}function b(e,t){if(null==e)return!1;if(0===t.length)return!0;if("*"===t[0]){const n=Object.getOwnPropertyNames(e).filter(t=>"object"==typeof e[t]||Array.isArray(e[t]));for(const r of n)if(b(e[r],t)||b(e[r],t.slice(1)))return!0;return!1}if("[]"===t[0]){if(!Array.isArray(e))return!1;for(let n=0;n<e.length;n++)if(b(e[n],t.slice(1)))return!0;return!1}return!!Object.hasOwn(e,t[0])&&b(e[t[0]],t.slice(1))}function m(e){return"string"!=typeof e?[]:e.split(/\s+/).filter(Boolean).map(e=>e.split("."))}/* ===== [P2 2026-10-08] json-prune extended path engine (readable layer) =====
   Ported from AdguardTeam/Scriptlets master:
   - src/helpers/prune-utils.ts (getPrunePath / isPruningNeeded / jsonPruner / jsonSetter)
   - src/helpers/get-wildcard-property-in-chain.ts (isKeyInObject / getWildcardPropertyInChain)
   - src/helpers/json-path-utils.ts (resolveJsonSyntaxMode + JSONPath mutation semantics)
   Extended segments supported: `[-]`/`{-}` (array/object entry filter),
   `.[=].value` (value filter: true/false/number//regex/), `$` root,
   `$..key` recursive descent, `[?(...)]` predicates, `=v`/`+=v`/`$remove$`
   mutation suffixes. Only the json-prune family routes through
   p24PrunerFactory; plain legacy expressions keep using the original
   engine v() so existing behavior stays unchanged. */
const p24NativeParse = JSON.parse;
const p24NativeStringify = JSON.stringify;
const p24VALUE_MARKER = ".[=].";
function p24IsKeyInObject(baseObj, path, valueToCheck) {
    const parts = path.split(".");
    const check = (targetObject, pathSegments) => {
        if (targetObject === undefined || targetObject === null) return false;
        if (pathSegments.length === 0) {
            if (valueToCheck !== undefined) {
                if (typeof targetObject === "string" && valueToCheck instanceof RegExp) {
                    return valueToCheck.test(targetObject);
                }
                return targetObject === valueToCheck;
            }
            return true;
        }
        const current = pathSegments[0];
        const rest = pathSegments.slice(1);
        if (current === "*" || current === "[]") {
            if (Array.isArray(targetObject)) {
                return targetObject.some((item) => check(item, rest));
            }
            if (typeof targetObject === "object" && targetObject !== null) {
                return Object.keys(targetObject).some((key) => check(targetObject[key], rest));
            }
        }
        if (Object.prototype.hasOwnProperty.call(targetObject, current)) {
            return check(targetObject[current], rest);
        }
        return false;
    };
    return check(baseObj, parts);
}
function p24GetWildcardPropertyInChain(base, chain, lookThrough, output, valueToCheck) {
    if (typeof lookThrough === "undefined") lookThrough = false;
    if (typeof output === "undefined" || output === null) output = [];
    const pos = chain.indexOf(".");
    if (pos === -1) {
        if (chain === "*" || chain === "[]") {
            for (const key in base) {
                if (Object.prototype.hasOwnProperty.call(base, key)) {
                    if (valueToCheck !== undefined) {
                        const objectValue = base[key];
                        if (typeof objectValue === "string" && valueToCheck instanceof RegExp) {
                            if (valueToCheck.test(objectValue)) output.push({ base, prop: key });
                        } else if (objectValue === valueToCheck) {
                            output.push({ base, prop: key });
                        }
                    } else {
                        output.push({ base, prop: key });
                    }
                }
            }
        } else if (valueToCheck !== undefined) {
            const objectValue = base[chain];
            if (typeof objectValue === "string" && valueToCheck instanceof RegExp) {
                if (valueToCheck.test(objectValue)) output.push({ base, prop: chain });
            } else if (base[chain] === valueToCheck) {
                output.push({ base, prop: chain });
            }
        } else {
            output.push({ base, prop: chain });
        }
        return output;
    }
    const prop = chain.slice(0, pos);
    const shouldLookThrough = (prop === "[]" && Array.isArray(base))
        || (prop === "*" && base instanceof Object)
        || (prop === "[-]" && Array.isArray(base))
        || (prop === "{-}" && base instanceof Object);
    if (shouldLookThrough) {
        const nextProp = chain.slice(pos + 1);
        const baseKeys = Object.keys(base);
        if (prop === "{-}" || prop === "[-]") {
            const kind = Array.isArray(base) ? "array" : "object";
            const shouldRemove = !!(prop === "{-}" && kind === "object") || !!(prop === "[-]" && kind === "array");
            if (!shouldRemove) return output;
            baseKeys.forEach((key) => {
                const item = base[key];
                if (p24IsKeyInObject(item, nextProp, valueToCheck)) {
                    output.push({ base, prop: key });
                }
            });
            return output;
        }
        baseKeys.forEach((key) => {
            const item = base[key];
            p24GetWildcardPropertyInChain(item, nextProp, lookThrough, output, valueToCheck);
        });
    }
    if (Array.isArray(base)) {
        base.forEach((item) => {
            if (item !== undefined) {
                p24GetWildcardPropertyInChain(item, chain, lookThrough, output, valueToCheck);
            }
        });
    }
    const nextBase = base[prop];
    const restChain = chain.slice(pos + 1);
    if (nextBase !== undefined) {
        p24GetWildcardPropertyInChain(nextBase, restChain, lookThrough, output, valueToCheck);
    }
    return output;
}
function p24GetPrunePath(props) {
    if (typeof props !== "string" || props.length === 0) return [];
    const splitProps = (str) => {
        const parts = [];
        let current = "";
        let i = 0;
        let insideRegex = false;
        let escapeActive = false;
        while (i < str.length) {
            const ch = str[i];
            if (!insideRegex) {
                if (ch === " " || ch === "\n" || ch === "\t" || ch === "\r" || ch === "\f" || ch === "\v") {
                    while (i < str.length && /\s/.test(str[i])) i += 1;
                    if (current !== "") { parts.push(current); current = ""; }
                    continue;
                }
                if (str.startsWith(p24VALUE_MARKER, i)) {
                    current += p24VALUE_MARKER;
                    i += p24VALUE_MARKER.length;
                    if (str[i] === "/") {
                        insideRegex = true;
                        escapeActive = false;
                        current += "/";
                        i += 1;
                        continue;
                    }
                    continue;
                }
                current += ch;
                i += 1;
                continue;
            }
            current += ch;
            if (ch === "\\") {
                escapeActive = !escapeActive;
            } else if (ch === "/" && !escapeActive) {
                insideRegex = false;
                escapeActive = false;
            } else {
                escapeActive = false;
            }
            i += 1;
        }
        if (current !== "") parts.push(current);
        return parts;
    };
    const rawParts = splitProps(props);
    return rawParts.map((part) => {
        const splitPart = part.split(p24VALUE_MARKER);
        const path = splitPart[0];
        let value = splitPart[1];
        if (value !== undefined) {
            if (value === "true") value = true;
            else if (value === "false") value = false;
            else if (value.startsWith("/")) value = i(value) || a(value);
            else if (/^\d+$/.test(value)) value = parseFloat(value);
            return { path, value };
        }
        return { path };
    });
}
function p24IsPruningNeeded(root, prunePaths, requiredPaths, stackRe, lg) {
    if (!root) return false;
    let shouldProcess;
    const prunePathsToCheck = prunePaths.map((obj) => obj.path);
    const requiredPathsToCheck = requiredPaths.map((obj) => obj.path);
    if (prunePathsToCheck.length === 0 && requiredPathsToCheck.length > 0) {
        try {
            lg.info(`${window.location.hostname}\n${p24NativeStringify(root, null, 2)}\nStack trace:\n${new Error().stack}`);
        } catch (ex) {}
        return false;
    }
    if (stackRe && !y(stackRe)) return false;
    const wildcardSymbols = [".*.", "*.", ".*", ".[].", "[].", ".[]"];
    for (let i = 0; i < requiredPathsToCheck.length; i += 1) {
        const requiredPath = requiredPathsToCheck[i];
        const lastNestedPropName = requiredPath.split(".").pop();
        const hasWildcard = wildcardSymbols.some((symbol) => requiredPath.includes(symbol));
        const details = p24GetWildcardPropertyInChain(root, requiredPath, hasWildcard);
        if (!details.length) return false;
        shouldProcess = !hasWildcard;
        for (let j = 0; j < details.length; j += 1) {
            const hasRequiredProp = typeof lastNestedPropName === "string"
                && details[j].base[lastNestedPropName] !== undefined;
            if (hasWildcard) {
                shouldProcess = hasRequiredProp || shouldProcess;
            } else {
                shouldProcess = hasRequiredProp && shouldProcess;
            }
        }
    }
    return shouldProcess;
}
function p24JsonPruner(root, prunePaths, requiredPaths, stackRe, lg) {
    if (prunePaths.length === 0 && requiredPaths.length === 0) {
        try {
            lg.info(`${window.location.hostname}\n${p24NativeStringify(root, null, 2)}\nStack trace:\n${new Error().stack}`);
        } catch (ex) {}
        return root;
    }
    try {
        if (p24IsPruningNeeded(root, prunePaths, requiredPaths, stackRe, lg) === false) {
            return root;
        }
        prunePaths.forEach((path) => {
            const ownerObjArr = p24GetWildcardPropertyInChain(root, path.path, true, [], path.value);
            for (let i = ownerObjArr.length - 1; i >= 0; i -= 1) {
                const ownerObj = ownerObjArr[i];
                if (ownerObj === undefined || !ownerObj.base) continue;
                if (!Array.isArray(ownerObj.base)) {
                    delete ownerObj.base[ownerObj.prop];
                    continue;
                }
                try {
                    const index = Number(ownerObj.prop);
                    if (Number.isNaN(index)) continue;
                    ownerObj.base.splice(index, 1);
                } catch (error) {
                    lg.warn(`Error while deleting array element: ${error}`);
                }
            }
        });
    } catch (e) {
        lg.warn(`json-prune error: ${e}`);
    }
    return root;
}
function p24JsonSetter(root, setPath, valueFilter, getValue, requiredPaths, stackRe, lg, onMutation) {
    if (!setPath) {
        try {
            lg.info(`${window.location.hostname}\n${p24NativeStringify(root, null, 2)}\nStack trace:\n${new Error().stack}`);
        } catch (ex) {}
        return root;
    }
    try {
        if (p24IsPruningNeeded(root, [{ path: setPath }], requiredPaths, stackRe, lg) === false) {
            return root;
        }
        const wildcardSymbols = [".*.", "*.", ".*", ".[].", "[].", ".[]"];
        const hasWildcard = wildcardSymbols.some((symbol) => setPath.includes(symbol));
        const matchedNodes = p24GetWildcardPropertyInChain(root, setPath, hasWildcard, [], valueFilter);
        if (matchedNodes.length > 0) {
            for (let i = 0; i < matchedNodes.length; i += 1) {
                const node = matchedNodes[i];
                if (node && node.base) {
                    node.base[node.prop] = getValue(node.base[node.prop]);
                    onMutation();
                }
            }
        } else if (!hasWildcard && valueFilter === undefined) {
            const pathParts = setPath.split(".");
            let current = root;
            for (let i = 0; i < pathParts.length - 1; i += 1) {
                const part = pathParts[i];
                if (current[part] === undefined || current[part] === null || typeof current[part] !== "object") {
                    current[part] = {};
                }
                current = current[part];
            }
            const lastPart = pathParts[pathParts.length - 1];
            current[lastPart] = getValue(current[lastPart]);
            onMutation();
        }
    } catch (e) {
        lg.warn(`json-set error: ${e}`);
    }
    return root;
}
function p24JPParseLiteral(tok) {
    if (tok === "true") return true;
    if (tok === "false") return false;
    if (tok === "null") return null;
    if (tok !== "" && !Number.isNaN(Number(tok))) return Number(tok);
    if ((tok.length >= 2) && ((tok.startsWith("'") && tok.endsWith("'")) || (tok.startsWith('"') && tok.endsWith('"')))) {
        return tok.slice(1, -1);
    }
    return tok;
}
function p24JPCompilePredicate(src) {
    const s = src.trim();
    const ops = ["==", "!=", ">=", "<=", ">", "<"];
    let rel = null;
    let pathPart = s;
    let litStr = null;
    for (let oi = 0; oi < ops.length; oi += 1) {
        const idx = s.indexOf(ops[oi]);
        if (idx !== -1) {
            rel = ops[oi];
            pathPart = s.slice(0, idx).trim();
            litStr = s.slice(idx + ops[oi].length).trim();
            break;
        }
    }
    if (!pathPart.startsWith("@")) return null;
    let propPath = pathPart.slice(1);
    if (propPath.startsWith(".")) propPath = propPath.slice(1);
    const propSegs = propPath === "" ? [] : propPath.split(".");
    const lit = rel ? p24JPParseLiteral(litStr) : undefined;
    const resolve = (node) => {
        let cur = node;
        for (let k = 0; k < propSegs.length; k += 1) {
            if (cur === null || cur === undefined || typeof cur !== "object") return undefined;
            if (!Object.prototype.hasOwnProperty.call(cur, propSegs[k])) return undefined;
            cur = cur[propSegs[k]];
        }
        return cur;
    };
    return (node) => {
        const val = resolve(node);
        if (!rel) return val !== undefined;
        if (val === undefined) return false;
        switch (rel) {
            case "==": return val === lit;
            case "!=": return val !== lit;
            case ">": return val > lit;
            case ">=": return val >= lit;
            case "<": return val < lit;
            case "<=": return val <= lit;
            default: return false;
        }
    };
}
function p24JPMatchBracket(s, openIdx) {
    let depth = 0;
    let quote = null;
    for (let i = openIdx; i < s.length; i += 1) {
        const c = s[i];
        if (quote) {
            if (c === "\\") { i += 2; continue; }
            if (c === quote) quote = null;
            continue;
        }
        if (c === "'" || c === '"') { quote = c; continue; }
        if (c === "[") depth += 1;
        else if (c === "]") { depth -= 1; if (depth === 0) return i; }
    }
    return -1;
}
function p24JPParsePath(expr) {
    let s = expr.trim();
    let guard = null;
    if (s.startsWith("[?")) {
        const close = p24JPMatchBracket(s, 0);
        if (close === -1) return null;
        let inner = s.slice(1, close).trim();
        inner = inner.slice(1);
        if (inner.startsWith("(") && inner.endsWith(")")) inner = inner.slice(1, -1);
        guard = p24JPCompilePredicate(inner);
        if (!guard) return null;
        s = s.slice(close + 1).trim();
        if (s.startsWith("$")) s = s.slice(1);
        if (s === "") return null;
    } else if (s.startsWith("$")) {
        s = s.slice(1);
    }
    const segs = [];
    let i = 0;
    while (i < s.length) {
        const ch = s[i];
        if (ch === ".") {
            if (s[i + 1] === ".") {
                i += 2;
                let key = "";
                while (i < s.length && s[i] !== "." && s[i] !== "[") { key += s[i]; i += 1; }
                if (key === "") return null;
                segs.push({ t: "descent", key });
            } else {
                i += 1;
                let key = "";
                while (i < s.length && s[i] !== "." && s[i] !== "[") { key += s[i]; i += 1; }
                if (key === "*") segs.push({ t: "wild" });
                else if (key !== "") segs.push({ t: "key", key });
            }
        } else if (ch === "[") {
            const close = p24JPMatchBracket(s, i);
            if (close === -1) return null;
            const inner = s.slice(i + 1, close).trim();
            i = close + 1;
            if (inner.startsWith("?")) {
                let psrc = inner.slice(1);
                if (psrc.startsWith("(") && psrc.endsWith(")")) psrc = psrc.slice(1, -1);
                const pred = p24JPCompilePredicate(psrc);
                if (!pred) return null;
                segs.push({ t: "filter", pred });
            } else if (inner === "*" || inner === "") {
                segs.push({ t: "wild" });
            } else if (/^-?\d+$/.test(inner)) {
                segs.push({ t: "index", index: Number(inner) });
            } else if ((inner.startsWith("'") && inner.endsWith("'")) || (inner.startsWith('"') && inner.endsWith('"'))) {
                segs.push({ t: "key", key: inner.slice(1, -1) });
            } else {
                return null;
            }
        } else {
            let key = "";
            while (i < s.length && s[i] !== "." && s[i] !== "[") { key += s[i]; i += 1; }
            if (key === "*") segs.push({ t: "wild" });
            else if (key !== "") segs.push({ t: "key", key });
        }
    }
    return { guard, segs };
}
function p24JPSplitMutation(s) {
    let depthSq = 0;
    let depthPar = 0;
    let quote = null;
    let i = 0;
    while (i < s.length) {
        const c = s[i];
        if (quote) {
            if (c === "\\") { i += 2; continue; }
            if (c === quote) quote = null;
            i += 1;
            continue;
        }
        if (c === "'" || c === '"') { quote = c; i += 1; continue; }
        if (c === "[") { depthSq += 1; i += 1; continue; }
        if (c === "]") { depthSq -= 1; i += 1; continue; }
        if (c === "(") { depthPar += 1; i += 1; continue; }
        if (c === ")") { depthPar -= 1; i += 1; continue; }
        if (depthSq === 0 && depthPar === 0) {
            if (c === "+" && s[i + 1] === "=") {
                return { path: s.slice(0, i), op: "append", value: s.slice(i + 2) };
            }
            if (c === "=") {
                if (s[i + 1] === "=") { i += 2; continue; }
                return { path: s.slice(0, i), op: "set", value: s.slice(i + 1) };
            }
        }
        i += 1;
    }
    return { path: s, op: "remove", value: "" };
}
function p24JsonPathApply(root, expr, opts) {
    const split = p24JPSplitMutation(expr);
    const parsed = p24JPParsePath(split.path);
    if (!parsed) return false;
    if (parsed.guard && !parsed.guard(root)) return false;
    if (parsed.segs.length === 0) return false;
    const owners = [];
    const collect = (node, idx) => {
        if (node === null || node === undefined || typeof node !== "object") return;
        const seg = parsed.segs[idx];
        const last = idx === parsed.segs.length - 1;
        const visit = (child, ownerBase, ownerProp) => {
            if (last) owners.push({ base: ownerBase, prop: ownerProp });
            else collect(child, idx + 1);
        };
        if (seg.t === "key") {
            if (Object.prototype.hasOwnProperty.call(node, seg.key)) {
                visit(node[seg.key], node, seg.key);
            }
        } else if (seg.t === "index") {
            if (Array.isArray(node) && seg.index >= 0 && seg.index < node.length
                && Object.prototype.hasOwnProperty.call(node, seg.index)) {
                visit(node[seg.index], node, seg.index);
            }
        } else if (seg.t === "wild") {
            const keys = Object.keys(node);
            for (let k = 0; k < keys.length; k += 1) visit(node[keys[k]], node, keys[k]);
        } else if (seg.t === "descent") {
            const stack = [node];
            while (stack.length) {
                const cur = stack.pop();
                if (cur === null || cur === undefined || typeof cur !== "object") continue;
                if (Object.prototype.hasOwnProperty.call(cur, seg.key)) {
                    visit(cur[seg.key], cur, seg.key);
                }
                if (Array.isArray(cur)) {
                    for (let k = 0; k < cur.length; k += 1) stack.push(cur[k]);
                } else {
                    const keys = Object.keys(cur);
                    for (let k = 0; k < keys.length; k += 1) stack.push(cur[keys[k]]);
                }
            }
        } else if (seg.t === "filter") {
            if (Array.isArray(node)) {
                for (let k = 0; k < node.length; k += 1) {
                    if (seg.pred(node[k])) visit(node[k], node, k);
                }
            } else {
                const keys = Object.keys(node);
                for (let k = 0; k < keys.length; k += 1) {
                    if (seg.pred(node[keys[k]])) visit(node[keys[k]], node, keys[k]);
                }
            }
        }
    };
    collect(root, 0);
    if (owners.length === 0) return false;
    if (split.op === "remove") {
        const arrayGroups = new Map();
        for (let i = 0; i < owners.length; i += 1) {
            const o = owners[i];
            if (Array.isArray(o.base)) {
                const ix = Number(o.prop);
                if (Number.isNaN(ix)) continue;
                if (!arrayGroups.has(o.base)) arrayGroups.set(o.base, new Set());
                arrayGroups.get(o.base).add(ix);
            } else {
                delete o.base[o.prop];
            }
        }
        arrayGroups.forEach((idxSet, arr) => {
            const idxs = Array.from(idxSet).sort((p, q) => q - p);
            for (let i = 0; i < idxs.length; i += 1) arr.splice(idxs[i], 1);
        });
        return true;
    }
    for (let i = 0; i < owners.length; i += 1) {
        const o = owners[i];
        if (split.op === "append") {
            const cur = o.base[o.prop];
            const add = p24JPParseLiteral(split.value);
            o.base[o.prop] = (typeof cur === "string" || typeof add === "string")
                ? String(cur) + String(add)
                : ((typeof cur === "number" && typeof add === "number") ? cur + add : add);
        } else {
            o.base[o.prop] = p24JPParseLiteral(split.value);
        }
    }
    return true;
}
function p24ResolveSyntaxMode(expr) {
    if (typeof expr !== "string") return "legacy";
    const s = expr.trim();
    if (s.startsWith("$") || s.startsWith("[?") || s.startsWith(".")) return "jsonpath";
    return "legacy";
}
const p24IsExtendedExpr = (s) => typeof s === "string"
    && (s.trim().startsWith("$") || s.includes("[?") || s.includes("[-]") || s.includes("{-}") || s.includes("[=]"));
function p24PrunerFactory(propsToRemove, requiredProps, stack) {
    if (!p24IsExtendedExpr(propsToRemove) && !p24IsExtendedExpr(requiredProps)) {
        return v(propsToRemove, requiredProps, stack);
    }
    const st = typeof stack === "string" && stack.length > 0 ? (i(stack) || a(stack)) : null;
    if (p24ResolveSyntaxMode(propsToRemove) === "jsonpath") {
        return (root) => {
            if (typeof root !== "object" || root === null) return;
            if (st && !y(st)) return;
            p24JsonPathApply(root, propsToRemove, { op: "remove" });
        };
    }
    const prunePaths = p24GetPrunePath(propsToRemove);
    const requiredPaths = p24GetPrunePath(requiredProps);
    const lg = R;
    return (root) => {
        if (typeof root !== "object" || root === null) return;
        p24JsonPruner(root, prunePaths, requiredPaths, st, lg);
    };
}
const R=n("json-prune");const P=["url","method","credentials","cache","redirect","referrer","referrerPolicy","integrity","mode"];function x(e){if(""===e||"*"===e)return{};const t={},n=e.split(" ");for(const e of n){if(!e.includes(":")){t.url=i(e)||a(e)||e;continue}const[n,r]=e.split(":");if(""===n||void 0===r||""===r)throw new Error(`Invalid segment: "${e}"`);if(!P.includes(n))throw new Error(`Invalid segment key: "${n}"`);t[n]=i(r)||r}return t}function E(e,t){let n;n=t[0]instanceof Request?t[0]:void 0!==t[1]?{...t[1],url:t[0].toString()}:{url:t[0].toString()};for(const t of Object.keys(e))if(void 0===n[t]||!j(e[t],n[t]))return!1;return!0}function S(e){for(var t=arguments.length,n=new Array(t>1?t-1:0),r=1;r<t;r++)n[r-1]=arguments[r];const o={method:n[0],url:n[1].toString()};for(const t of Object.keys(e))if(void 0===o[t]||!j(e[t],o[t]))return!1;return!0}function j(e,t){return"string"==typeof e?e===t:e.test(t)}function k(e){const t=parseInt(e,10);if(isNaN(t))throw new Error("input is NaN");if(!Number.isFinite(t))throw new Error("input is Infinite");return t}const O=/length:(\d+)-(\d+)/,$="ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*()_+=~";function T(e){if("false"===e)return"";let t,n;const r=e.match(O);if("true"===e)t=10,n=10;else{if(!r)throw new Error("Invalid pattern");if(t=k(r[1]),n=k(r[2]),n>5e5)throw new Error("maxLength exceeds limit");if(t>n)throw new Error("minLength exceeds maxLength")}var o,s;return function(e){let t="";for(let n=0;n<e;n++)t+=$.charAt(Math.floor(76*Math.random()));return t}((o=t,s=n,o=Math.ceil(o),s=Math.floor(s),Math.floor(Math.random()*(s-o+1)+o)))}const L=n("json-prune-fetch-response");const A=n("json-prune-xhr-response"),H=Symbol("requestHeaders"),N=Symbol("shouldPrune"),M=Symbol("openArgs"),I=Symbol("responseHeaders");const D=n("no-protected-audience");const q=n("no-topics");const C=n("nowebrtc");const X=n("prevent-addEventListener");const G=n("prevent-fetch");function F(e){let t,n,r=arguments.length>1&&void 0!==arguments[1]?arguments[1]:"emptyObj",o=arguments.length>2?arguments[2]:void 0;if("undefined"!=typeof fetch&&"undefined"!=typeof Proxy&&"undefined"!=typeof Response){switch(r){case"":case"emptyObj":t="{}";break;case"emptyArr":t="[]";break;case"emptyStr":t="";break;default:return void G.warn(`Invalid responseBody: ${r}`)}if("string"!=typeof o||"basic"===o||"cors"===o||"opaque"===o){try{n=x(e)}catch(e){G.warn("Error parsing props",e)}fetch=new Proxy(fetch,{apply:async(e,r,s)=>{if(!E(n,s))return Reflect.apply(e,r,s);const i=new Response(t,{status:200,statusText:"OK",headers:new Headers({"Content-Length":t.length.toString(),"Content-Type":"application/json",Date:(new Date).toUTCString()})});if("opaque"===o)Object.defineProperties(i,{url:{value:""},status:{value:0},statusText:{value:""},body:{value:null},type:{value:"opaque"},headers:{value:new Headers}});else{let e;e=s[0]instanceof URL?s[0].toString():s[0]instanceof Request?s[0].url:s[0],Object.defineProperties(i,{url:{value:e},type:{value:o||"basic"}})}return i}})}else G.warn(`Invalid responseType: ${o}`)}else G.warn("Either fetch, Proxy, or Response is not supported in this environment")}function U(e){let{callback:t,delay:n,matchCallback:r,matchDelay:o}=e;if("function"!=typeof t&&"string"!=typeof t)return!1;if("string"!=typeof r||o&&!z(o))return!1;const{isInverted:s,regexp:i}=J(r),{isInverted:a,match:l}=W(o),p=K(n),c=String(t),u=i?.test(c)!==s,f=null!==l&&p===l!==a;return null===l?u:r?u&&f:f}const B=e=>{let t=!1;return e.startsWith("!")&&(e=e.slice(1),t=!0),{isReverse:t,value:e}},J=e=>{const{isReverse:t,value:n}=B(e);return{isInverted:t,regexp:i(n)||a(n)}},W=e=>{const{isReverse:t,value:n}=B(e),r=parseInt(n,10);return{isInverted:t,match:Number.isNaN(r)?null:r}},z=e=>{if("string"!=typeof e)return!1;const t=e=>"number"==typeof e&&!Number.isNaN(e)&&Number.isFinite(e);return e.startsWith("!")?t(+e.slice(1)):t(+e)},K=e=>{const t=Math.floor(parseInt(e,10));return"number"!=typeof t||Number.isNaN(t)?e:t},V=n("prevent-set-interval");const Z=n("prevent-set-timeout");const Q=n("prevent-window-open");function Y(e,t,n){let r;try{r="1"===e||"0"===e?function(e,t,n){let r,o=!1;"0"===e&&(o=!0);if("string"==typeof t&&t.length>0&&(r=(i(t)||a(t))??void 0,void 0===r))throw new Error("Could not parse search");let s=()=>{};if("trueFunc"===n)s=()=>!0;else if("string"==typeof n&&n.length>0){if(!n.startsWith("{")||!n.endsWith("}"))throw new Error(`Invalid replacement ${n}`);const e=n.slice(1,-1).split("=");if(2!==e.length||0===e[0].length||"noopFunc"!==e[1])throw new Error(`Invalid replacement ${n}`);s={[e[0]]:()=>{}}}return(e,t,n)=>{let i;if(0===n.length||null==n[0])i="";else if("string"==typeof n[0])i=n[0];else{if(!(n[0]instanceof URL))return Reflect.apply(e,t,n);i=n[0].toString()}if(void 0!==r){let s=r.test(i);if(o&&(s=!s),!s)return Reflect.apply(e,t,n)}return Q.info("Preventing window.open",{args:n}),s}}(e,t,n):function(e,t,n){let r,o,s=!1;if("string"==typeof e&&e.length>0&&(s="!"===e[0],s&&(e=e.slice(1)),r=(i(e)||a(e))??void 0,void 0===r))throw new Error("Could not parse match");"string"==typeof t&&t.length>0&&(o=k(t));if("string"==typeof n&&"obj"!==n&&"blank"!==n)throw new Error(`Replacement type ${n} not supported`);return(e,t,i)=>{let a,l,p;if(0===i.length||null==i[0])a="";else if("string"==typeof i[0])a=i[0];else{if(!(i[0]instanceof URL))return Reflect.apply(e,t,i);a=i[0].toString()}if(void 0!==r){let n=r.test(a);if(s&&(n=!n),!n)return Reflect.apply(e,t,i)}if(Q.info("Preventing window.open",{args:i}),"blank"===n)return Reflect.apply(e,t,["about:blank",...i.slice(1)]);if("obj"===n)l=document.createElement("object"),l.data=a;else l=document.createElement("iframe"),l.src=a;if(l.style.setProperty("height","1px","important"),l.style.setProperty("width","1px","important"),l.style.setProperty("position","absolute","important"),l.style.setProperty("top","-9999px","important"),document.body.appendChild(l),void 0!==o&&setTimeout(()=>{l.remove()},1e3*o),"obj"===n){if(p=l.contentWindow,null===p||"object"!=typeof p)return null;Object.defineProperties(p,{closed:{value:!1},opener:{value:window},frameElement:{value:null}})}else p=new Proxy(window,{get:(e,t,n)=>{if("closed"===t)return!1;const r=Reflect.get(e,t,n);return"function"==typeof r?()=>{}:r},set:()=>!0});return p}}(e,t,n)}catch(e){return void Q.warn("Error while making handler",{ex:e})}window.open=new Proxy(window.open,{apply:r})}const _=n("prevent-xhr"),ee=Symbol("prevent"),te=Symbol("url"),ne=Symbol("responseHeaders");function re(e,t){if("undefined"==typeof Proxy)return void _.warn("Proxy is not supported in this environment");if("string"!=typeof e)return void _.warn("propsToMatch is required");let n;try{n=x(e)}catch(e){return void _.warn("error parsing props",e)}XMLHttpRequest.prototype.open=new Proxy(XMLHttpRequest.prototype.open,{apply:(e,t,r)=>t[ee]||S(n,...r)?(_.debug("Preventing XHR request",r),t[ee]=!0,t[te]=r[1].toString(),Reflect.apply(e,t,r)):(t[ee]=!1,Reflect.apply(e,t,r))}),XMLHttpRequest.prototype.send=new Proxy(XMLHttpRequest.prototype.send,{apply:(e,n,r)=>{if(!n[ee])return Reflect.apply(e,n,r);setTimeout(()=>{const e={readyState:{value:n.DONE,writable:!1},statusText:{value:"OK",writable:!1},response:{value:"",writable:!1},responseText:{value:"",writable:!1},responseURL:{value:n[te],writable:!1},responseXML:{value:null,writable:!1},status:{value:200,writable:!1}};switch(n[ne]={date:(new Date).toUTCString(),"content-length":"0"},n.responseType){case"arraybuffer":e.response.value=new ArrayBuffer(0),n[ne]["content-type"]="application/octet-stream";break;case"blob":e.response.value=new Blob([]),n[ne]["content-type"]="application/octet-stream";break;case"document":{const t=(new DOMParser).parseFromString("","text/html");e.response.value=t,e.responseXML.value=t,n[ne]["content-type"]="text/html",n[ne]["content-length"]=t.documentElement.outerHTML.length.toString();break}case"json":e.response.value={},e.responseText.value="{}",n[ne]["content-type"]="application/json",n[ne]["content-length"]="2";break;default:if(n[ne]["content-type"]="text/plain","string"!=typeof t||""===t)break;try{const r=T(t);e.response.value=r,e.responseText.value=r,n[ne]["content-length"]=r.length.toString()}catch(e){_.error("Generating random response text",e)}}Object.defineProperties(n,e),n.dispatchEvent(new Event("readystatechange")),n.dispatchEvent(new Event("load")),n.dispatchEvent(new Event("loadend"))},1)}}),XMLHttpRequest.prototype.getResponseHeader=new Proxy(XMLHttpRequest.prototype.getResponseHeader,{apply:(e,t,n)=>t[ee]?t.readyState!==t.DONE?null:t[ne][n[0].toLowerCase()]??null:Reflect.apply(e,t,n)}),XMLHttpRequest.prototype.getAllResponseHeaders=new Proxy(XMLHttpRequest.prototype.getAllResponseHeaders,{apply:(e,t,n)=>{if(!t[ee])return Reflect.apply(e,t,n);if(t.readyState!==t.DONE)return null;let r="";for(const[e,n]of Object.entries(t[ne]))r+=`${e}: ${n}\r\n`;return r}})}const oe=n("sanitize-clipboard");function se(e,t){try{const n=new URL(e);let r=!1;for(const e of t)if("string"==typeof e)n.searchParams.has(e)&&(n.searchParams.delete(e),r=!0);else for(const t of Array.from(n.searchParams.keys()))e.test(t)&&(n.searchParams.delete(t),r=!0);return r?n.toString():e}catch{return e}}const ie=n("set-constant");const ae=new Set(["undefined","false","true","null","yes","no","on","off","accept","accepted","reject","rejected","allowed","denied","forbidden","forever",""]);function le(e){if(ae.has(e.toLowerCase()))return e;if("emptyArr"===e)return"[]";if("emptyObj"===e)return"{}";if("$remove$"===e)return"$remove$";const t=parseInt(e);if(!isNaN(t)&&t>=0&&t<=32767)return e;throw new Error("Invalid value")}function pe(e,t){const n=i(t);if(null!==n){const t=Object.keys(e);for(const r of t)n.test(r)&&e.removeItem(r)}else e.removeItem(t)}/* ===== [P4 2026-10-08] trusted-* scriptlet family: shared helpers =====
   Each helper is ported from the corresponding file in AdguardTeam/Scriptlets
   master (see per-section comments). Trusted Types policy creation mirrors
   trusted-types-utils.ts with the identity AGPolicy transforms. */
const p24NoopFunc = () => {};
const p24NoopCallbackFunc = () => p24NoopFunc;
const p24TrueFunc = () => true;
const p24FalseFunc = () => false;
const p24ThrowFunc = () => { throw new Error(); };
const p24NoopArray = () => [];
const p24NoopObject = () => ({});
const p24NoopPromiseReject = () => Promise.reject();
const p24NoopPromiseResolve = (responseBody, responseUrl, responseType) => {
    if (typeof Response === "undefined") return undefined;
    const body = responseBody === undefined ? "{}" : responseBody;
    const url = responseUrl === undefined ? "" : responseUrl;
    const type = responseType === undefined ? "basic" : responseType;
    const response = new Response(body, { headers: { "Content-Length": `${body.length}` }, status: 200, statusText: "OK" });
    if (type === "opaque") {
        Object.defineProperties(response, { body: { value: null }, status: { value: 0 }, ok: { value: false }, statusText: { value: "" }, url: { value: "" }, type: { value: type } });
    } else {
        Object.defineProperties(response, { url: { value: url }, type: { value: type } });
    }
    return Promise.resolve(response);
};
/* string-utils.ts inferValue */
function p24InferValue(value) {
    if (value === "undefined") return undefined;
    if (value === "false") return false;
    if (value === "true") return true;
    if (value === "null") return null;
    if (value === "NaN") return NaN;
    if (typeof value === "string" && value.startsWith("/") && value.endsWith("/")) {
        return i(value) || a(value);
    }
    const numVal = Number(value);
    if (!Number.isNaN(numVal)) {
        if (Math.abs(numVal) > 32767) throw new Error("number values bigger than 32767 are not allowed");
        return numVal;
    }
    try {
        const parsableVal = p24NativeParse(value);
        if (parsableVal instanceof Object || typeof parsableVal === "string") return parsableVal;
    } catch (e) {
        return value;
    }
    return value;
}
/* parse-keyword-value.ts parseKeywordValue */
function p24ParseKeywordValue(rawValue) {
    if (typeof rawValue !== "string") return rawValue;
    const KEYWORDS_REGEXP = /\$(?:currentISODate|currentDate|now)\$/g;
    if (rawValue.search(KEYWORDS_REGEXP) === -1) return rawValue;
    const date = new Date();
    return rawValue.replace(KEYWORDS_REGEXP, (keyword) => {
        if (keyword === "$now$") return date.getTime().toString();
        if (keyword === "$currentDate$") return date.toString();
        if (keyword === "$currentISODate$") return date.toISOString();
        return keyword;
    });
}
/* cookie-utils.ts isValidCookiePath / getCookiePath / serializeCookie / getTrustedCookieOffsetMs */
function p24IsValidCookiePath(rawPath) { return rawPath === "/" || rawPath === "none"; }
function p24GetCookiePath(rawPath) { return rawPath === "/" ? "path=/" : ""; }
function p24SerializeCookie(name, rawValue, rawPath, domainValue, shouldEncodeValue) {
    const COOKIE_BREAKER = ";";
    if ((!shouldEncodeValue && `${rawValue}`.includes(COOKIE_BREAKER)) || name.includes(COOKIE_BREAKER)) return null;
    const value = shouldEncodeValue ? encodeURIComponent(rawValue) : rawValue;
    let resultCookie = `${name}=${value}`;
    if (name.startsWith("__Host-")) {
        resultCookie += "; path=/; secure";
        return resultCookie;
    }
    const path = p24GetCookiePath(rawPath);
    if (path) resultCookie += `; ${path}`;
    if (name.startsWith("__Secure-")) resultCookie += "; secure";
    if (domainValue) resultCookie += `; domain=${domainValue}`;
    return resultCookie;
}
function p24GetTrustedCookieOffsetMs(offsetExpiresSec) {
    const MS_IN_SEC = 1000;
    let parsedSec;
    if (offsetExpiresSec === "1year") parsedSec = 365 * 24 * 60 * 60;
    else if (offsetExpiresSec === "1day") parsedSec = 24 * 60 * 60;
    else {
        parsedSec = Number.parseInt(offsetExpiresSec, 10);
        if (Number.isNaN(parsedSec)) return null;
    }
    return parsedSec * MS_IN_SEC;
}
/* storage-utils.ts setStorageItem */
function p24SetStorageItem(storage, key, value, lg) {
    try {
        storage.setItem(key, value);
    } catch (e) {
        lg.warn(`Unable to set storage item due to: ${e && e.message}`);
    }
}
/* get-property-in-chain.ts getPropertyInChain */
function p24GetPropertyInChain(base, chain) {
    const pos = chain.indexOf(".");
    if (pos === -1) return { base, prop: chain };
    const prop = chain.slice(0, pos);
    if (base === null) return { base, prop, chain };
    const nextBase = base[prop];
    const restChain = chain.slice(pos + 1);
    const baseIsEmpty = (base instanceof Object || typeof base === "object")
        && base !== null && Object.keys(base).length === 0 && !base.prototype;
    if (baseIsEmpty) return { base, prop, chain };
    if (nextBase === null) return { base, prop, chain };
    if (nextBase !== undefined) return p24GetPropertyInChain(nextBase, restChain);
    Object.defineProperty(base, prop, { configurable: true });
    return { base, prop, chain };
}
/* string-utils.ts extractRegexAndReplacement */
function p24ExtractRegexAndReplacement(str) {
    if (!str) return undefined;
    let regexWithReplacement = str.slice("replace:".length);
    let regexFlags = "";
    if (regexWithReplacement.endsWith("/g")) {
        regexWithReplacement = regexWithReplacement.slice(0, -1);
        regexFlags = "g";
    }
    if (!regexWithReplacement.startsWith("/") || !regexWithReplacement.endsWith("/")) return undefined;
    const content = regexWithReplacement.slice(1, -1);
    let delimiterIndex = -1;
    for (let i = 0; i < content.length; i += 1) {
        if (content[i] === "/") {
            let slashIsEscaped = false;
            let backslashIndex = i - 1;
            while (backslashIndex >= 0 && content[backslashIndex] === "\\") {
                slashIsEscaped = !slashIsEscaped;
                backslashIndex -= 1;
            }
            if (!slashIsEscaped) { delimiterIndex = i; break; }
        }
    }
    if (delimiterIndex === -1) return undefined;
    const regex = `/${content.slice(0, delimiterIndex)}/${regexFlags}`;
    const replacement = content.slice(delimiterIndex + 1);
    if (!regex || regex === "//") return undefined;
    const replaceRegexValue = i(regex) || a(regex);
    if (!replaceRegexValue) return undefined;
    return { regexPart: replaceRegexValue, replacementPart: replacement };
}
/* string-utils.ts splitByPipeRespectingRegex */
function p24SplitByPipeRespectingRegex(str) {
    const PIPE = "|";
    const SLASH = "/";
    const BACKSLASH = "\\";
    const result = [];
    let current = "";
    let insideRegex = false;
    let i = 0;
    while (i < str.length) {
        const char = str[i];
        if (!insideRegex) {
            if (char === SLASH && current === "") {
                insideRegex = true;
                current += char;
                i += 1;
                continue;
            }
            if (char === PIPE) {
                result.push(current);
                current = "";
                i += 1;
                continue;
            }
        } else if (char === SLASH) {
            let backslashCount = 0;
            let j = current.length - 1;
            while (j >= 0 && current[j] === BACKSLASH) {
                backslashCount += 1;
                j -= 1;
            }
            if (backslashCount % 2 === 0) {
                current += char;
                i += 1;
                while (i < str.length && /[gimsuy]/.test(str[i])) {
                    current += str[i];
                    i += 1;
                }
                insideRegex = false;
                continue;
            }
        }
        current += char;
        i += 1;
    }
    result.push(current);
    return result;
}
/* value-matchers.ts isValueMatched and sub-matchers */
function p24IsArbitraryObject(value) {
    return value !== null && typeof value === "object" && !Array.isArray(value) && !(value instanceof RegExp);
}
function p24IsStringMatched(str, matcher) {
    if (typeof matcher === "string") {
        if (matcher === "") return str === matcher;
        return str.includes(matcher);
    }
    if (matcher instanceof RegExp) return matcher.test(str);
    return false;
}
function p24IsObjectMatched(obj, matcher) {
    const matcherKeys = Object.keys(matcher);
    for (let i = 0; i < matcherKeys.length; i += 1) {
        if (!p24IsValueMatched(obj[matcherKeys[i]], matcher[matcherKeys[i]])) return false;
    }
    return true;
}
function p24IsArrayMatched(array, matcher) {
    if (array.length === 0) return matcher.length === 0;
    if (matcher.length === 0) return false;
    for (let i = 0; i < matcher.length; i += 1) {
        const isMatching = array.some((arrItem) => p24IsValueMatched(arrItem, matcher[i]));
        if (!isMatching) return false;
    }
    return true;
}
function p24IsValueMatched(value, matcher) {
    if (typeof value === "function") return false;
    if (Number.isNaN(value)) return Number.isNaN(matcher);
    if (value === null || typeof value === "undefined" || typeof value === "number" || typeof value === "boolean") {
        return value === matcher;
    }
    if (typeof value === "string") {
        if (typeof matcher === "string" || matcher instanceof RegExp) return p24IsStringMatched(value, matcher);
        return false;
    }
    if (Array.isArray(value) && Array.isArray(matcher)) return p24IsArrayMatched(value, matcher);
    if (p24IsArbitraryObject(value) && p24IsArbitraryObject(matcher)) return p24IsObjectMatched(value, matcher);
    return false;
}
/* abort mechanism matching the bundle's abort-on-stack-trace pattern
   (prevent-utils.ts getAbortFunc semantics) */
function p24GetAbortFunc() {
    const id = l();
    const abort = () => {
        throw new ReferenceError(`Aborted script with ID: ${id}`);
    };
    window.addEventListener("error", (ev) => {
        if (ev.error instanceof ReferenceError && ev.error.message.includes(id)) ev.preventDefault();
    });
    return abort;
}
/* observer.ts observeDocumentWithTimeout */
function p24ObserveDocumentWithTimeout(callback, options, timeout) {
    const opts = options || { subtree: true, childList: true };
    const to = typeof timeout === "number" ? timeout : 10000;
    const documentObserver = new MutationObserver((mutations, observer) => {
        observer.disconnect();
        callback(mutations, observer);
        observer.observe(document.documentElement, opts);
    });
    documentObserver.observe(document.documentElement, opts);
    setTimeout(() => documentObserver.disconnect(), to);
}
/* trusted-types-utils.ts getTrustedTypesApi essence: identity AGPolicy */
let p24TTPolicy = null;
try {
    if (typeof trustedTypes !== "undefined" && trustedTypes && typeof trustedTypes.createPolicy === "function") {
        p24TTPolicy = trustedTypes.createPolicy("AGPolicy", {
            createHTML: (input) => input,
            createScript: (input) => input,
            createScriptURL: (input) => input,
        });
    }
} catch (ex) {
    p24TTPolicy = null;
}
function p24TTCreateScript(input) {
    if (p24TTPolicy && typeof p24TTPolicy.createScript === "function") {
        try { return p24TTPolicy.createScript(input); } catch (ex) { return input; }
    }
    return input;
}
/* node-text-utils.ts parseNodeTextParams / isTargetNode / replaceNodeText /
   handleExistingNodes / handleMutations */
function p24ParseNodeTextParams(nodeName, textMatch, pattern) {
    const isStringNameMatch = !(nodeName.startsWith("/") && nodeName.endsWith("/"));
    const selector = isStringNameMatch ? nodeName : "*";
    const nodeNameMatch = isStringNameMatch ? nodeName : (i(nodeName) || a(nodeName));
    const textContentMatch = !textMatch.startsWith("/") ? textMatch : (i(textMatch) || a(textMatch));
    let patternMatch;
    if (pattern) {
        patternMatch = !pattern.startsWith("/") ? pattern : (i(pattern) || a(pattern));
    }
    return { selector, nodeNameMatch, textContentMatch, patternMatch };
}
function p24IsTargetNode(node, nodeNameMatch, textContentMatch) {
    const { nodeName, textContent } = node;
    const nodeNameLowerCase = nodeName.toLowerCase();
    return textContent !== null && textContent !== ""
        && (nodeNameMatch instanceof RegExp ? nodeNameMatch.test(nodeNameLowerCase) : nodeNameMatch === nodeNameLowerCase)
        && (textContentMatch instanceof RegExp ? textContentMatch.test(textContent) : textContent.includes(textContentMatch));
}
function p24ReplaceNodeText(node, pattern, replacement, lg) {
    const { textContent } = node;
    if (textContent) {
        let modifiedText = textContent.replace(pattern, replacement);
        if (node.nodeName === "SCRIPT") modifiedText = p24TTCreateScript(modifiedText);
        node.textContent = modifiedText;
        lg.info(`Replaced text content of ${node.nodeName}`);
    }
}
function p24HandleExistingNodes(selector, handler) {
    const processNodes = (parent) => {
        if (selector === "#text") {
            handler([].slice.call(parent.childNodes).filter((node) => node.nodeType === 3));
        } else {
            try { handler([].slice.call(parent.querySelectorAll(selector))); } catch (ex) {}
        }
    };
    processNodes(document);
}
function p24HandleMutations(mutations, handler) {
    const added = [];
    for (let i = 0; i < mutations.length; i += 1) {
        const addedNodes = mutations[i].addedNodes;
        for (let j = 0; j < addedNodes.length; j += 1) added.push(addedNodes[j]);
    }
    handler(added);
}
/* attribute-utils.ts parseAttributePairs */
function p24ParseAttributePairs(input) {
    if (!input) return [];
    const NAME_VALUE_SEPARATOR = "=";
    const PAIRS_SEPARATOR = " ";
    const SINGLE_QUOTE = "'";
    const DOUBLE_QUOTE = '"';
    const BACKSLASH = "\\";
    const pairs = [];
    for (let i = 0; i < input.length; i += 1) {
        let name = "";
        let value = "";
        while (i < input.length && input[i] !== NAME_VALUE_SEPARATOR && input[i] !== PAIRS_SEPARATOR) {
            name += input[i];
            i += 1;
        }
        if (i < input.length && input[i] === NAME_VALUE_SEPARATOR) {
            i += 1;
            let quote = null;
            if (input[i] === SINGLE_QUOTE || input[i] === DOUBLE_QUOTE) {
                quote = input[i];
                i += 1;
                for (; i < input.length; i += 1) {
                    if (input[i] === quote) {
                        if (input[i - 1] === BACKSLASH) {
                            value = `${value.slice(0, -1)}${quote}`;
                        } else {
                            i += 1;
                            quote = null;
                            break;
                        }
                    } else {
                        value += input[i];
                    }
                }
                if (quote !== null) throw new Error(`Unbalanced quote for attribute value: '${input}'`);
            } else {
                throw new Error(`Attribute value should be quoted: "${input.slice(i)}"`);
            }
        }
        name = name.trim();
        value = value.trim();
        if (!name) {
            if (!value) continue;
            throw new Error(`Attribute name before '=' should be specified: '${input}'`);
        }
        pairs.push({ name, value });
        if (input[i] && input[i] !== PAIRS_SEPARATOR) {
            throw new Error(`No space before attribute: '${input.slice(i)}'`);
        }
    }
    return pairs;
}
/* set-constant-utils.ts createSetChainPropAccessor; stack matching reuses
   the bundle's y() helper with a pre-compiled RegExp */
function p24CreateSetChainPropAccessor(config) {
    const { stackRe, mustCancel, trapProp, getConstantValue, setConstantValue, lg } = config;
    const setChainPropAccess = (owner, property) => {
        const chainInfo = p24GetPropertyInChain(owner, property);
        const { base, prop, chain } = chainInfo;
        const inChainPropHandler = {
            factValue: undefined,
            init(v) { this.factValue = v; return true; },
            get() { return this.factValue; },
            set(v) {
                if (this.factValue === v) return;
                this.factValue = v;
                if (v instanceof Object) setChainPropAccess(v, chain);
            },
        };
        let suspending = false;
        const endPropHandler = {
            factValue: undefined,
            init(v) {
                if (mustCancel(v)) return false;
                this.factValue = v;
                return true;
            },
            get() {
                if (!stackRe) return getConstantValue();
                if (!suspending) {
                    suspending = true;
                    let stackMatches = false;
                    try {
                        stackMatches = y(stackRe);
                    } catch (e) {
                        suspending = false;
                        return this.factValue;
                    }
                    suspending = false;
                    if (stackMatches) return getConstantValue();
                }
                return this.factValue;
            },
            set(v) {
                if (mustCancel(v)) {
                    setConstantValue(v);
                    return;
                }
                this.factValue = v;
            },
        };
        if (!chain) {
            trapProp(base, prop, false, endPropHandler);
            return;
        }
        if (base !== undefined && base[prop] === null) {
            trapProp(base, prop, true, inChainPropHandler);
            return;
        }
        if ((base instanceof Object || typeof base === "object") && base !== null
            && Object.keys(base).length === 0 && !base.prototype) {
            trapProp(base, prop, true, inChainPropHandler);
        }
        const propValue = owner[prop];
        if (propValue instanceof Object || (typeof propValue === "object" && propValue !== null)) {
            setChainPropAccess(propValue, chain);
        }
        trapProp(base, prop, true, inChainPropHandler);
    };
    return setChainPropAccess;
}
/* cookie-utils.ts parseCookieString; string-utils.ts parseMatchArg (simplified) */
function p24ParseCookieString(cookieString) {
    const cookieChunks = cookieString.split(";");
    const cookieData = {};
    cookieChunks.forEach((singleCookie) => {
        let cookieKey;
        let cookieValue = "";
        const delimiterIndex = singleCookie.indexOf("=");
        if (delimiterIndex === -1) {
            cookieKey = singleCookie.trim();
        } else {
            cookieKey = singleCookie.slice(0, delimiterIndex).trim();
            cookieValue = singleCookie.slice(delimiterIndex + 1);
        }
        cookieData[cookieKey] = cookieValue || null;
    });
    return cookieData;
}
function p24ParseMatchArg(match) {
    const isInvertedMatch = match ? match.startsWith("!") : false;
    const matchValue = isInvertedMatch ? match.slice(1) : match;
    return { isInvertedMatch, matchValue };
}
/* throttle.ts throttle (trailing-call pattern) */
function p24Throttle(fn, ms) {
    let isThrottled = false;
    let savedArgs = null;
    const wrapper = (...args) => {
        if (isThrottled) {
            savedArgs = args;
            return;
        }
        fn(...args);
        isThrottled = true;
        setTimeout(() => {
            isThrottled = false;
            if (savedArgs) {
                const nextArgs = savedArgs;
                savedArgs = null;
                wrapper(...nextArgs);
            }
        }, ms);
    };
    return wrapper;
}
/* click-utils.ts spoofClickEventsIsTrusted */
function p24SpoofClickIsTrusted() {
    const PATCHED_FLAG = "__p24ClickIsTrustedPatched";
    if (EventTarget.prototype[PATCHED_FLAG]) return;
    const SPOOFED_EVENTS = new Set([
        "click", "mousedown", "mouseup", "mouseover", "mouseenter",
        "pointerdown", "pointerup", "pointerover", "pointerenter",
    ]);
    const nativeAddEventListener = EventTarget.prototype.addEventListener;
    const nativeRemoveEventListener = EventTarget.prototype.removeEventListener;
    const wrappedListeners = new WeakMap();
    const normalizeCapture = (options) => {
        if (typeof options === "boolean") return options;
        return options && options.capture ? options.capture : false;
    };
    const getMapKey = (type, options) => `${type}\0${normalizeCapture(options)}`;
    EventTarget.prototype.addEventListener = function addEventListenerWrapper(type, listener, options) {
        if (!listener || !SPOOFED_EVENTS.has(type)) {
            return nativeAddEventListener.call(this, type, listener, options);
        }
        const isFn = typeof listener === "function";
        const key = getMapKey(type, options);
        const wrapped = function wrappedListener(event) {
            const proxied = new Proxy(event, {
                get(target, prop) {
                    if (prop === "isTrusted") return true;
                    const val = Reflect.get(target, prop);
                    if (typeof val === "function") return val.bind(target);
                    return val;
                },
                set(target, prop, value) {
                    return Reflect.set(target, prop, value);
                },
            });
            if (isFn) return listener.call(this, proxied);
            return listener.handleEvent.call(listener, proxied);
        };
        const listenerRef = listener;
        let map = wrappedListeners.get(listenerRef);
        if (!map) {
            map = new Map();
            wrappedListeners.set(listenerRef, map);
        }
        const existing = map.get(key);
        if (!existing) map.set(key, wrapped);
        return nativeAddEventListener.call(this, type, existing || wrapped, options);
    };
    EventTarget.prototype.removeEventListener = function removeEventListenerWrapper(type, listener, options) {
        if (!listener || !SPOOFED_EVENTS.has(type)) {
            return nativeRemoveEventListener.call(this, type, listener, options);
        }
        const listenerRef = listener;
        const key = getMapKey(type, options);
        const map = wrappedListeners.get(listenerRef);
        if (map && map.has(key)) {
            const wrapped = map.get(key);
            map.delete(key);
            return nativeRemoveEventListener.call(this, type, wrapped, options);
        }
        return nativeRemoveEventListener.call(this, type, listener, options);
    };
    EventTarget.prototype[PATCHED_FLAG] = true;
}
/* open-shadow-dom-utils.ts queryShadowSelector / findElementWithText */
function p24QueryShadowSelector(selector, context, textContent, shadowRootsMap) {
    const SHADOW_COMBINATOR = " >>> ";
    const pos = selector.indexOf(SHADOW_COMBINATOR);
    if (pos === -1) {
        if (textContent) {
            const elements = context.querySelectorAll(selector);
            for (let i = 0; i < elements.length; i += 1) {
                const el = elements[i];
                const t = el.textContent;
                if (t && textContent.test(t)) return el;
            }
            return null;
        }
        return context.querySelector(selector);
    }
    const shadowHostSelector = selector.slice(0, pos).trim();
    const elem = context.querySelector(shadowHostSelector);
    if (!elem) return null;
    const root = elem.shadowRoot || (shadowRootsMap && shadowRootsMap.get(elem));
    if (!root) return null;
    const shadowRootSelector = selector.slice(pos + SHADOW_COMBINATOR.length).trim();
    return p24QueryShadowSelector(shadowRootSelector, root, textContent, shadowRootsMap);
}
/* click-utils.ts clickElement: React internal handlers first, native
   pointer/mouse sequence otherwise */
function p24ClickElement(element, clickType) {
    const REACT_PROPS_KEY_PREFIX = "__reactProps$";
    const NATIVE_CLICK_TYPE = "native";
    const rect = element.getBoundingClientRect();
    const x = rect.left + rect.width / 2;
    const y = rect.top + rect.height / 2;
    const commonOpts = {
        bubbles: true, cancelable: true, composed: true, view: window,
        clientX: x, clientY: y, screenX: x + window.screenX, screenY: y + window.screenY,
        button: 0, buttons: 1,
    };
    const noBubbleOpts = Object.assign({}, commonOpts, { bubbles: false });
    const releaseOpts = Object.assign({}, commonOpts, { buttons: 0 });
    const createEventProxy = (nativeEvent, eventType, isReactEvent) => {
        let defaultPrevented = nativeEvent.defaultPrevented;
        let propagationStopped = false;
        return new Proxy(nativeEvent, {
            get(target, prop) {
                if (prop === "isTrusted") return true;
                if (!isReactEvent) {
                    const value = Reflect.get(target, prop);
                    if (typeof value === "function") return value.bind(target);
                    return value;
                }
                if (prop === "nativeEvent") return target;
                if (prop === "target" || prop === "srcElement" || prop === "currentTarget") return element;
                if (prop === "type") return eventType;
                if (prop === "defaultPrevented") return defaultPrevented;
                if (prop === "persist") return () => {};
                if (prop === "isDefaultPrevented") return () => defaultPrevented;
                if (prop === "isPropagationStopped") return () => propagationStopped;
                if (prop === "preventDefault") {
                    return () => {
                        defaultPrevented = true;
                        target.preventDefault();
                    };
                }
                if (prop === "stopPropagation") {
                    return () => {
                        propagationStopped = true;
                        target.stopPropagation();
                    };
                }
                if (prop === "stopImmediatePropagation") {
                    return () => {
                        propagationStopped = true;
                        if (typeof target.stopImmediatePropagation === "function") target.stopImmediatePropagation();
                    };
                }
                const value = Reflect.get(target, prop);
                if (typeof value === "function") return value.bind(target);
                return value;
            },
        });
    };
    const wrapInlineHandlers = (target, eventTypes) => {
        const originalHandlers = new Map();
        eventTypes.forEach((eventType) => {
            const propertyName = `on${eventType}`;
            const handler = target[propertyName];
            if (typeof handler !== "function" || originalHandlers.has(propertyName)) return;
            originalHandlers.set(propertyName, handler);
            target[propertyName] = function wrappedInlineHandler(event) {
                const onEventProxy = createEventProxy(event, eventType);
                return handler.call(this, onEventProxy);
            };
        });
        return () => {
            originalHandlers.forEach((handler, propertyName) => {
                target[propertyName] = handler;
            });
        };
    };
    const dispatchNativeClick = () => {
        const hasPointerEvent = typeof PointerEvent === "function";
        const spoofedEventTypes = new Set([
            "click", "mousedown", "mouseup", "mouseover", "mouseenter",
            "pointerdown", "pointerup", "pointerover", "pointerenter",
        ]);
        const restoreInlineHandlers = wrapInlineHandlers(element, spoofedEventTypes);
        try {
            if (hasPointerEvent) {
                element.dispatchEvent(new PointerEvent("pointerover", commonOpts));
                element.dispatchEvent(new PointerEvent("pointerenter", noBubbleOpts));
            }
            element.dispatchEvent(new MouseEvent("mouseover", commonOpts));
            element.dispatchEvent(new MouseEvent("mouseenter", noBubbleOpts));
            if (hasPointerEvent) element.dispatchEvent(new PointerEvent("pointerdown", commonOpts));
            element.dispatchEvent(new MouseEvent("mousedown", commonOpts));
            element.focus();
            if (hasPointerEvent) element.dispatchEvent(new PointerEvent("pointerup", releaseOpts));
            element.dispatchEvent(new MouseEvent("mouseup", releaseOpts));
            element.dispatchEvent(new MouseEvent("click", releaseOpts));
        } finally {
            restoreInlineHandlers();
        }
    };
    const reactPropsKey = Object.keys(element).find((key) => typeof key === "string" && key.startsWith(REACT_PROPS_KEY_PREFIX));
    if (reactPropsKey && clickType !== NATIVE_CLICK_TYPE) {
        const reactProps = element[reactPropsKey];
        if (reactProps && typeof reactProps.onClick === "function") {
            if (typeof reactProps.onFocus === "function") {
                const focusEvent = typeof FocusEvent === "function"
                    ? new FocusEvent("focus", { bubbles: false, cancelable: false, composed: true, relatedTarget: null })
                    : new Event("focus", { bubbles: false, cancelable: false, composed: true });
                reactProps.onFocus.call(element, createEventProxy(focusEvent, "focus", true));
            }
            const clickEvent = new MouseEvent("click", releaseOpts);
            reactProps.onClick.call(element, createEventProxy(clickEvent, "click", true));
            return;
        }
    }
    dispatchNativeClick();
}
/* json-path-utils.ts buildJsonPathExpression */
function p24BuildJsonPathExpression(selectorPath, argumentValue) {
    const REMOVE_VALUE_MARKER = "$remove$";
    const normalizedSelectorPath = selectorPath.trim();
    if (normalizedSelectorPath === "") return normalizedSelectorPath;
    let bracketDepth = 0;
    let braceDepth = 0;
    let parenthesisDepth = 0;
    let quote = null;
    for (let i = 0; i < normalizedSelectorPath.length; i += 1) {
        const currentChar = normalizedSelectorPath[i];
        if (quote) {
            if (currentChar === quote) {
                let backslashCount = 0;
                let k = i - 1;
                while (k >= 0 && normalizedSelectorPath[k] === "\\") {
                    backslashCount += 1;
                    k -= 1;
                }
                if (backslashCount % 2 === 0) quote = null;
            }
            continue;
        }
        if (currentChar === "'" || currentChar === '"') { quote = currentChar; continue; }
        if (currentChar === "[") { bracketDepth += 1; continue; }
        if (currentChar === "]") { bracketDepth -= 1; continue; }
        if (currentChar === "{") { braceDepth += 1; continue; }
        if (currentChar === "}") { braceDepth -= 1; continue; }
        if (currentChar === "(") { parenthesisDepth += 1; continue; }
        if (currentChar === ")") { parenthesisDepth -= 1; continue; }
        if (bracketDepth === 0 && braceDepth === 0 && parenthesisDepth === 0) {
            if (normalizedSelectorPath.startsWith("+=", i) || currentChar === "=") {
                return normalizedSelectorPath;
            }
        }
    }
    if (argumentValue === REMOVE_VALUE_MARKER) return normalizedSelectorPath;
    if (argumentValue === undefined) return "";
    return `${normalizedSelectorPath}=${String(argumentValue)}`;
}
/* json-set-utils.ts parseJsonSetArgumentValue / getJsonSetValue */
function p24ParseJsonSetArgumentValue(argumentValue) {
    let constantValue;
    let replaceRegexValue = "";
    let shouldReplaceArgument = false;
    let shouldMergeJsonValue = false;
    let hasTimeKeywords = false;
    if (argumentValue.startsWith("replace:")) {
        const pair = p24ExtractRegexAndReplacement(argumentValue);
        if (!pair) return null;
        replaceRegexValue = pair.regexPart;
        constantValue = p24ParseKeywordValue(pair.replacementPart);
        hasTimeKeywords = constantValue !== pair.replacementPart;
        shouldReplaceArgument = true;
    } else if (argumentValue.startsWith("json:")) {
        try {
            const rawJsonValue = argumentValue.slice("json:".length);
            const parsedJsonValue = p24ParseKeywordValue(rawJsonValue);
            hasTimeKeywords = parsedJsonValue !== rawJsonValue;
            constantValue = p24NativeParse(parsedJsonValue);
            shouldMergeJsonValue = true;
        } catch (ex) {
            return null;
        }
    } else {
        const parsedArgument = p24ParseKeywordValue(argumentValue);
        hasTimeKeywords = parsedArgument !== argumentValue;
        if (parsedArgument === "undefined") constantValue = undefined;
        else if (parsedArgument === "false") constantValue = false;
        else if (parsedArgument === "true") constantValue = true;
        else if (parsedArgument === "null") constantValue = null;
        else if (parsedArgument === "NaN") constantValue = NaN;
        else if (parsedArgument === "emptyArr" || parsedArgument === "[]") constantValue = p24NoopArray();
        else if (parsedArgument === "emptyObj" || parsedArgument === "{}") constantValue = p24NoopObject();
        else if (parsedArgument === "noopFunc") constantValue = p24NoopFunc;
        else if (parsedArgument === "noopCallbackFunc") constantValue = p24NoopCallbackFunc;
        else if (parsedArgument === "trueFunc") constantValue = p24TrueFunc;
        else if (parsedArgument === "falseFunc") constantValue = p24FalseFunc;
        else if (parsedArgument === "throwFunc") constantValue = p24ThrowFunc;
        else if (parsedArgument === "noopPromiseResolve") constantValue = p24NoopPromiseResolve;
        else if (parsedArgument === "noopPromiseReject") constantValue = p24NoopPromiseReject;
        else if (/^-?\d+$/.test(parsedArgument)) {
            constantValue = parseFloat(parsedArgument);
            if (Number.isNaN(constantValue)) return null;
        } else constantValue = parsedArgument;
    }
    return { constantValue, replaceRegexValue, shouldReplaceArgument, shouldMergeJsonValue, hasTimeKeywords };
}
function p24GetJsonSetValue(currentValue, parsedArgumentValue) {
    if (parsedArgumentValue.shouldReplaceArgument) {
        if (typeof currentValue === "string") {
            return currentValue.replace(parsedArgumentValue.replaceRegexValue, parsedArgumentValue.constantValue);
        }
        return currentValue;
    }
    const shouldMergeObjects = parsedArgumentValue.shouldMergeJsonValue
        && currentValue !== null && typeof currentValue === "object" && !Array.isArray(currentValue)
        && parsedArgumentValue.constantValue !== null && typeof parsedArgumentValue.constantValue === "object"
        && !Array.isArray(parsedArgumentValue.constantValue);
    if (shouldMergeObjects) {
        return Object.assign({}, currentValue, parsedArgumentValue.constantValue);
    }
    return parsedArgumentValue.constantValue;
}
/* prune-utils.ts jsonLineEdit (line-delimited JSON support) */
function p24JsonLineEdit(callback, text) {
    const lineSeparator = text.includes("\r\n") ? "\r\n" : "\n";
    const linesBefore = text.split(/\r?\n/);
    const linesAfter = [];
    let hasJsonLines = false;
    for (let i = 0; i < linesBefore.length; i += 1) {
        const lineBefore = linesBefore[i];
        let obj;
        try {
            obj = p24NativeParse(lineBefore);
        } catch (ex) {
            obj = undefined;
        }
        if (typeof obj !== "object" || obj === null) {
            linesAfter.push(lineBefore);
            continue;
        }
        hasJsonLines = true;
        const lineBeforeNormalized = p24NativeStringify(obj);
        const objAfter = callback(obj);
        if (objAfter === undefined) {
            linesAfter.push(lineBefore);
            continue;
        }
        const lineAfter = p24NativeStringify(objAfter);
        if (lineAfter === lineBeforeNormalized) {
            linesAfter.push(lineBefore);
            continue;
        }
        linesAfter.push(lineAfter);
    }
    return { hasJsonLines, text: linesAfter.join(lineSeparator) };
}
const ce=n("index"),ue=new Map([["abort-current-inline-script",function(e,t){if("string"!=typeof e||0===e.length)return void p.warn("property should be a non-empty string");let n;"string"==typeof t&&t.length>0&&(n=i(t)||a(t));const r=l(),s=document.currentScript,c=()=>{const t=document.currentScript;if(t instanceof HTMLScriptElement&&t!==s&&(!n||n.test(t.textContent||"")))throw p.info(`Blocked ${e} in currentScript`),new ReferenceError(`Aborted script with ID: ${r}`)};o(window,e,{onGet:c,onSet:c}),window.addEventListener("error",e=>{e.error instanceof ReferenceError&&e.error.message.includes(r)&&e.preventDefault()})}],["abort-on-property-read",u],["aopr",u],["abort-on-property-write",d],["aopw",d],["abort-on-stack-trace",g],["aost",g],["json-prune",function(e,t,n){if("undefined"==typeof Proxy)return void R.warn("Proxy not available in this environment");if("string"!=typeof e||0===e.length)return void R.warn("propsToRemove should be a non-empty string");const r=p24PrunerFactory(e,t,n);JSON.parse=new Proxy(JSON.parse,{apply:(e,t,n)=>{const o=Reflect.apply(e,t,n);return r(o),o}}),"undefined"!=typeof Response&&(Response.prototype.json=new Proxy(Response.prototype.json,{apply:async(e,t,n)=>{const o=await Reflect.apply(e,t,n);return r(o),o}}))}],["nowebrtc",function(){if(!window.RTCPeerConnection)return;const e=e=>{C.log(`document tried to create an RTCPeerConnection with config: ${e}`)},t=()=>{};e.prototype={close:t,createDataChannel:t,createOffer:t,setRemoteDescription:t,toString:()=>"[object RTCPeerConnection]"};const n=window.RTCPeerConnection;window.RTCPeerConnection=e,n.prototype&&(n.prototype.createDataChannel=()=>({close:t,send:t}))}],["prevent-fetch",F],["no-fetch-if",F],["prevent-xhr",re],["no-xhr-if",re],["set-local-storage-item",function(e,t){if("string"!=typeof e)throw new Error(`key should be string, is ${e}`);if("string"!=typeof t)throw new Error(`value should be string, is ${t}`);const n=le(t);"$remove$"===n?pe(localStorage,e):localStorage.setItem(e,n)}],["set-session-storage-item",function(e,t){if("string"!=typeof e)throw new Error(`key should be string, is ${e}`);if("string"!=typeof t)throw new Error(`value should be string, is ${t}`);const n=le(t);"$remove$"===n?pe(sessionStorage,e):sessionStorage.setItem(e,n)}],["set-constant",function(t,n,r,o,s){let l;switch(void 0!==s&&ie.warn("setProxyTrap will be ignored"),n){case"undefined":l=void 0;break;case"false":l=!1;break;case"true":l=!0;break;case"null":l=null;break;case"emptyObj":l={};break;case"emptyArr":l=[];break;case"noopFunc":l=()=>{};break;case"noopCallbackFunc":l=()=>()=>{};break;case"trueFunc":l=()=>!0;break;case"falseFunc":l=()=>!1;break;case"throwFunc":l=()=>{throw new Error};break;case"noopPromiseResolve":l=()=>Promise.resolve(new Response("",{status:200,statusText:"OK"}));break;case"noopPromiseReject":l=()=>Promise.reject();break;case"":l="";break;case"-1":l=-1;break;case"yes":l="yes";break;case"no":l="no";break;default:{const e=parseInt(n,10);if(!isNaN(e)&&e>=0&&e<=32767){l=n;break}throw new Error("Invalid value")}}const p=l;switch(o){case"asFunction":l=()=>p;break;case"asCallback":l=()=>()=>p;break;case"asResolved":l=()=>Promise.resolve(p);break;case"asRejected":l=()=>Promise.reject(p)}let c;void 0!==r&&""!==r&&(c=i(r)||a(r)),c??=null;const u=()=>{ie.debug(`Returning fake value for property window.${t}`,{value:n})};if(!t.includes(".")){let e=window[t];const n=Object.getOwnPropertyDescriptor(window,t);return void Object.defineProperty(window,t,{configurable:!0,get:()=>null===c||y(c)?(u(),l):"function"==typeof n?.get?n.get.apply(window):e,set:"function"==typeof n?.set?n?.set.bind(window):t=>{e=t}})}const f=Object,d=Function,w=t=>{let n,r;return(o,s)=>{if(1===t.length&&t[0]===s)return u(),l;let i=Reflect.get(o,s,o);const a=f.getOwnPropertyDescriptor(o,s);if(a&&"value"in a&&!a.configurable&&!a.writable)return i;if("function"==typeof i&&/\{\s*\[native code\]/.test(d.prototype.toString.call(i))&&(void 0!==r&&r[s]?i=r[s]:(i=i.bind(o),void 0===r&&(r={}),r[s]=i)),t[0]!==s||!e(i)||null!==c&&!y(c))return i;if(n?.link===i)return n.proxy;const p=new Proxy(i,{get:w(t.slice(1))});return n={link:i,proxy:p},p}},g=t.split("."),v=g[0];let h=window,b=window[v];for(let t=1;t<g.length;t++){const n=g[t];if(null!=b&&t===g.length-1){const e=Object.getOwnPropertyDescriptor(b,n);let t=b[n];return void Object.defineProperty(b,n,{configurable:!0,get:()=>null===c||y(c)?(u(),l):"function"==typeof e?.get?e.get.apply(b):t,set:"function"==typeof e?.set?e?.set.bind(b):e=>{t=e}})}if(null==b||null==b[n]){const n=Object.getOwnPropertyDescriptor(h,g[t-1]);let r,o=b;return void Object.defineProperty(h,g[t-1],{configurable:!0,get:()=>{const s=n?.get?n.get.apply(h):o;if(!e(s)||null!==c&&!y(c))return s;if(r?.capturedValue===s)return r.proxy;const i=new Proxy(s,{get:w(g.slice(t))});return r={capturedValue:s,proxy:i},i},set:"function"==typeof n?.set?n?.set.bind(h):e=>{o=e}})}h=b,b=b[n]}throw ie.warn("Hit an invariant in setConstant",{property:t,value:n,stack:r}),new Error("Invariant hit")}],["json-prune-fetch-response",function(e,t,n,r){if("undefined"==typeof Proxy||"undefined"==typeof fetch||"undefined"==typeof Response)return void L.warn("Either Proxy, fetch, or Response is not supported in this environment");if("string"!=typeof e||0===e.length)return void L.warn("propsToRemove cannot be empty");let o;if("string"==typeof n)try{o=x(n)}catch(e){return void L.warn("error parsing propsToMatch",e)}const s=p24PrunerFactory(e,t,r);window.fetch=new Proxy(window.fetch,{apply:async(e,t,n)=>{if(o&&!E(o,n))return Reflect.apply(e,t,n);const r=await Reflect.apply(e,t,n),i=r.clone();let a;try{a=await r.json()}catch{return i}s(a);const l=new Response(JSON.stringify(a),{status:r.status,statusText:r.statusText,headers:r.headers});return Object.defineProperties(l,{url:{value:r.url},type:{value:r.type},ok:{value:r.ok},redirected:{value:r.redirected}}),l}})}],["json-prune-xhr-response",function(e,t,n,r){if("undefined"==typeof Proxy)return void A.warn("Proxy is not supported in this environment");if("string"!=typeof e||0===e.length)return void A.warn("propsToMatch cannot be empty");let o;if("string"==typeof n)try{o=x(n)}catch(e){return void A.warn("error parsing propsToMatch",e)}const s=p24PrunerFactory(e,t,r),{open:i,send:a}=window.XMLHttpRequest.prototype;XMLHttpRequest.prototype.open=new Proxy(XMLHttpRequest.prototype.open,{apply:(e,t,n)=>void 0===o||S(o,...n)?(t[N]=!0,t[H]=[],t[M]=n,t[I]=[],Reflect.apply(e,t,n)):Reflect.apply(e,t,n)}),XMLHttpRequest.prototype.setRequestHeader=new Proxy(XMLHttpRequest.prototype.setRequestHeader,{apply:(e,t,n)=>t[N]?(t[H].push(n),Reflect.apply(e,t,n)):Reflect.apply(e,t,n)}),XMLHttpRequest.prototype.send=new Proxy(XMLHttpRequest.prototype.send,{apply:(e,t,n)=>{if(!t[N])return Reflect.apply(e,t,n);const r=new XMLHttpRequest;r.addEventListener("readystatechange",async()=>{if(r.readyState!==XMLHttpRequest.DONE)return;const e={readyState:{value:r.readyState,writable:!1},responseURL:{value:r.responseURL,writable:!1},status:{value:r.status,writable:!1},statusText:{value:r.statusText,writable:!1},response:{value:r.response,writable:!1}};try{e.responseXML={value:r.responseXML,writable:!1}}catch{}try{e.responseText={value:r.responseText,writable:!1}}catch{}try{if(""===r.responseType||"text"===r.responseType){const t=JSON.parse(r.responseText);s(t);const n=JSON.stringify(t);e.response={value:n,writable:!1},e.responseText={value:n,writable:!1}}else if("arraybuffer"===r.responseType){const t=(new TextDecoder).decode(r.response),n=JSON.parse(t);s(n);const o=(new TextEncoder).encode(JSON.stringify(n));e.response={value:o,writable:!1}}else if("blob"===r.responseType){const t=await r.response.text(),n=JSON.parse(t);s(n);const o=new Blob([JSON.stringify(n)]);e.response={value:o,writable:!1}}else{if("json"!==r.responseType)throw new Error(`Unsupported type: ${r.responseType}`);s(r.response),e.response={value:r.response,writable:!1}}}catch(e){A.error("Error parsing/pruning response",e)}Object.defineProperties(t,e);const n=r.getAllResponseHeaders();for(const e of n.trim().split(/[\r\n]+/)){const[n,r]=e.split(": ");t[I].push([n,r])}setTimeout(()=>{t.dispatchEvent(new Event("readystatechange")),t.dispatchEvent(new Event("load")),t.dispatchEvent(new Event("loadend"))},1)}),i.apply(r,t[M]);for(const[e,n]of t[H])r.setRequestHeader(e,n);try{a.apply(r,n)}catch(r){return A.error("Error sending substitute request",r),Reflect.apply(e,t,n)}}}),XMLHttpRequest.prototype.getResponseHeader=new Proxy(XMLHttpRequest.prototype.getResponseHeader,{apply:(e,t,n)=>{if(!t[N])return Reflect.apply(e,t,n);let r=null;for(const[e,o]of t[I])if(e===n[0]){r=o;break}return r}}),XMLHttpRequest.prototype.getAllResponseHeaders=new Proxy(XMLHttpRequest.prototype.getAllResponseHeaders,{apply:(e,t,n)=>t[N]?t[I].map(e=>{let[t,n]=e;return`${t}: ${n}`}).join("\r\n"):Reflect.apply(e,t,n)})}],["prevent-window-open",Y],["nowoif",Y],["prevent-setTimeout",function(){let e=arguments.length>0&&void 0!==arguments[0]?arguments[0]:"",t=arguments.length>1&&void 0!==arguments[1]?arguments[1]:"";"undefined"!=typeof Proxy?window.setTimeout=new Proxy(window.setTimeout,{apply:(n,r,o)=>{const[s,i]=o;return U({callback:s,delay:i,matchCallback:e,matchDelay:t})?(Z.info(`Prevented setTimeout(${String(s)}, ${i})"`),0):Reflect.apply(n,r,o)}}):Z.warn("Proxy not available in this environment")}],["prevent-setInterval",function(){let e=arguments.length>0&&void 0!==arguments[0]?arguments[0]:"",t=arguments.length>1&&void 0!==arguments[1]?arguments[1]:"";"undefined"!=typeof Proxy?window.setInterval=new Proxy(window.setInterval,{apply:(n,r,o)=>{const[s,i]=o;return U({callback:s,delay:i,matchCallback:e,matchDelay:t})?(V.info(`Prevented setInterval(${String(s)}, ${i})"`),0):Reflect.apply(n,r,o)}}):V.warn("Proxy not available in this environment")}],["prevent-addEventListener",function(){let e=arguments.length>0&&void 0!==arguments[0]?arguments[0]:"",t=arguments.length>1&&void 0!==arguments[1]?arguments[1]:"";if(!e&&!t)return;const n=i(e)||a(e),r=i(t)||a(t);if(!n&&!r)return;const o={apply(e,t,o){const[s,i]=o,a=(e=>{try{return"object"==typeof e&&"handleEvent"in e&&"function"==typeof e.handleEvent?e.handleEvent.toString():e.toString()}catch{return""}})(i);let l=!1;if(n&&!r?l=n.test(s):r&&!n?l=r.test(s):n&&r&&(l=n.test(s)&&r.test(a)),!l)return Reflect.apply(e,t,o);X.info(`Blocked addEventListener("${s}", ${a})`)}};window.addEventListener=new Proxy(window.addEventListener,o),document.addEventListener=new Proxy(document.addEventListener,o),Element.prototype.addEventListener=new Proxy(window.Element.prototype.addEventListener,o),EventTarget.prototype.addEventListener=new Proxy(window.EventTarget.prototype.addEventListener,o)}],["no-topics",function(){const e="browsingTopics";if("function"!=typeof Document||"object"!=typeof Document.prototype)return;const t=Object.getOwnPropertyDescriptor(Document.prototype,e);if(!t||!t.configurable||"function"!=typeof t.value)return;const n=t.value,r=new Proxy(n,{apply:()=>(q.info("Preventing Topics API usage"),Promise.resolve(new Response("",{status:200,statusText:"OK"})))});Object.defineProperty(Document.prototype,e,{configurable:!0,get:()=>r,set:()=>{}})}],["no-protected-audience",function(){if("function"!=typeof Navigator||"object"!=typeof Navigator.prototype)return;const e={joinAdInterestGroup:()=>Promise.resolve(),runAdAuction:()=>Promise.resolve(null),leaveAdInterestGroup:()=>Promise.resolve(),clearOriginJoinedAdInterestGroups:()=>Promise.resolve(),createAuctionNonce:()=>"",updateAdInterestGroups:()=>{}};for(const t of Object.keys(e)){const n=Object.getOwnPropertyDescriptor(Navigator.prototype,t);if(!n||!n.configurable||"function"!=typeof n.value)continue;const r=n.value,o=new Proxy(r,{apply:()=>(D.info(`Preventing usage of Protected Audience API: ${t}`),e[t]())});Object.defineProperty(Navigator.prototype,t,{configurable:!0,get:()=>o,set:()=>{}})}}],["sanitize-clipboard",function(e){if("string"!=typeof e||0===e.length)return void oe.warn("params should be a non-empty string");const t=e.split(" ").map(e=>i(e)||e);if(navigator.clipboard){const e={async apply(e,n,r){const[o]=r,s=await Promise.resolve(o),i=se(String(s),t);return i===s?Reflect.apply(e,n,r):(oe.info(`Sanitized clipboard for '${String(s)}'`),Reflect.apply(e,n,[i]))}};navigator.clipboard.writeText=new Proxy(navigator.clipboard.writeText,e)}document.addEventListener("copy",e=>{const n=e;let r=window.getSelection()?.toString()??"";if(!r){const e=document.activeElement;!e||"INPUT"!==e.tagName&&"TEXTAREA"!==e.tagName||null===e.selectionStart||null===e.selectionEnd||e.selectionStart===e.selectionEnd||(r=e.value.slice(e.selectionStart,e.selectionEnd))}if(!r)return;const o=se(r,t);o!==r&&n?.clipboardData&&(n.clipboardData.setData("text/plain",o),n.preventDefault(),oe.info(`Sanitized clipboard for '${r}'`))},!0)}],["prevent-element-src-loading", function(nm, mt) {
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
    }],
    /* ===== [P4 2026-10-08] trusted-* family (12 keys), ported from
       AdguardTeam/Scriptlets master src/scriptlets/trusted-*.ts|.js ===== */
    ["trusted-replace-node-text", function(nodeName, textMatch, pattern, replacement, ...extraArgs) {
        const lg = n("trusted-replace-node-text");
        const fixQuotes = (str) => (typeof str === "string" ? str.replace(/\\'/g, "'").replace(/\\"/g, '"') : str);
        const fixedPattern = fixQuotes(pattern);
        const fixedReplacement = fixQuotes(replacement);
        const { selector, nodeNameMatch, textContentMatch, patternMatch } = p24ParseNodeTextParams(nodeName, textMatch, fixedPattern);
        const shouldLog = extraArgs.includes("verbose");
        const handleNodes = (nodes) => nodes.forEach((node) => {
            if (p24IsTargetNode(node, nodeNameMatch, textContentMatch)) {
                if (shouldLog) lg.info(`Original text content: ${node.textContent}`);
                p24ReplaceNodeText(node, patternMatch, fixedReplacement, lg);
                if (shouldLog) lg.info(`Modified text content: ${node.textContent}`);
            }
        });
        if (document.documentElement) p24HandleExistingNodes(selector, handleNodes);
        p24ObserveDocumentWithTimeout((mutations) => p24HandleMutations(mutations, handleNodes));
    }],
    ["trusted-create-element", function(parentSelector, tagName, attributePairs, textContent, cleanupDelayMs) {
        const lg = n("trusted-create-element");
        if (!parentSelector || !tagName) return;
        const IFRAME_WINDOW_NAME = "trusted-create-element-window";
        if (window.name === IFRAME_WINDOW_NAME) return;
        const cleanupDelay = cleanupDelayMs === undefined ? NaN : Number(cleanupDelayMs);
        let element;
        try {
            element = document.createElement(tagName);
            if (String(tagName).toLowerCase() === "script" && textContent) {
                element.textContent = p24TTCreateScript(textContent);
            } else {
                element.textContent = textContent === undefined ? "" : textContent;
            }
        } catch (e) {
            return void lg.warn(`Cannot create element with tag name '${tagName}' due to ${e && e.message}`);
        }
        let attributes = [];
        try {
            attributes = p24ParseAttributePairs(attributePairs || "");
        } catch (e) {
            return void lg.warn(`Cannot parse attributePairs param: '${attributePairs}' due to ${e && e.message}`);
        }
        attributes.forEach((attr) => {
            try {
                element.setAttribute(attr.name, attr.value);
            } catch (e) {
                lg.warn(`Cannot set attribute '${attr.name}' with value '${attr.value}' due to ${e && e.message}`);
            }
        });
        let elementCreated = false;
        let elementRemoved = false;
        const findParentAndAppendEl = (parentElSelector, el, removeElDelayMs) => {
            let parentEl;
            try {
                parentEl = document.querySelector(parentElSelector);
            } catch (e) {
                lg.warn(`Cannot find parent element by selector '${parentElSelector}' due to ${e && e.message}`);
                return false;
            }
            if (!parentEl) {
                lg.warn(`No parent element found by selector: '${parentElSelector}'`);
                return false;
            }
            try {
                if (!parentEl.contains(el)) parentEl.append(el);
                if (el instanceof HTMLIFrameElement && el.contentWindow) el.contentWindow.name = IFRAME_WINDOW_NAME;
                elementCreated = true;
                lg.info(`Created element ${tagName} in parent ${parentElSelector}`);
            } catch (e) {
                lg.warn(`Cannot append child to parent by selector '${parentElSelector}' due to ${e && e.message}`);
                return false;
            }
            if (!Number.isNaN(removeElDelayMs)) {
                setTimeout(() => {
                    el.remove();
                    elementRemoved = true;
                }, removeElDelayMs);
            }
            return true;
        };
        if (!findParentAndAppendEl(parentSelector, element, cleanupDelay)) {
            p24ObserveDocumentWithTimeout((mutations, observer) => {
                if (elementRemoved || elementCreated || findParentAndAppendEl(parentSelector, element, cleanupDelay)) {
                    observer.disconnect();
                }
            });
        }
    }],
    ["trusted-replace-argument", function(methodPath, argumentIndex, argumentValue, pattern, stack, verbose) {
        const lg = n("trusted-replace-argument");
        const vb = verbose === undefined ? "false" : String(verbose);
        if (((!methodPath || !argumentIndex || !argumentValue) && vb === "false") || (!methodPath && vb === "true")) return;
        const SHOULD_LOG_ONLY = vb === "true" && !argumentIndex && !argumentValue && !pattern && !stack;
        let constantValue;
        let replaceRegexValue = "";
        let shouldReplaceArgument = false;
        if (typeof argumentValue === "string" && argumentValue.startsWith("replace:")) {
            const pair = p24ExtractRegexAndReplacement(argumentValue);
            if (!pair) return void lg.warn(`Invalid argument value format: ${argumentValue}`);
            replaceRegexValue = pair.regexPart;
            constantValue = pair.replacementPart;
            shouldReplaceArgument = true;
        } else if (typeof argumentValue === "string" && argumentValue.startsWith("json:")) {
            try {
                constantValue = p24NativeParse(argumentValue.slice("json:".length));
            } catch (error) {
                return void lg.warn(`Invalid JSON argument value: ${argumentValue}`);
            }
        } else if (argumentValue === "undefined") constantValue = undefined;
        else if (argumentValue === "false") constantValue = false;
        else if (argumentValue === "true") constantValue = true;
        else if (argumentValue === "null") constantValue = null;
        else if (argumentValue === "emptyArr") constantValue = p24NoopArray();
        else if (argumentValue === "emptyObj") constantValue = p24NoopObject();
        else if (argumentValue === "noopFunc") constantValue = p24NoopFunc;
        else if (argumentValue === "noopCallbackFunc") constantValue = p24NoopCallbackFunc;
        else if (argumentValue === "trueFunc") constantValue = p24TrueFunc;
        else if (argumentValue === "falseFunc") constantValue = p24FalseFunc;
        else if (argumentValue === "throwFunc") constantValue = p24ThrowFunc;
        else if (argumentValue === "noopPromiseResolve") constantValue = p24NoopPromiseResolve;
        else if (argumentValue === "noopPromiseReject") constantValue = p24NoopPromiseReject;
        else if (typeof argumentValue === "string" && /^-?\d+$/.test(argumentValue)) {
            constantValue = parseFloat(argumentValue);
            if (Number.isNaN(constantValue)) return;
        } else constantValue = argumentValue;
        const parts = p24GetPropertyInChain(window, methodPath);
        if (typeof parts.chain !== "undefined") {
            return void lg.warn(`Could not reach the end of the prop chain: ${methodPath}`);
        }
        const nativeMethod = parts.base[parts.prop];
        if (!nativeMethod || typeof nativeMethod !== "function") {
            return void lg.warn(`Could not retrieve the method: ${methodPath}`);
        }
        const st = typeof stack === "string" && stack.length > 0 ? (i(stack) || a(stack)) : null;
        const stringifyObject = (obj) => p24NativeStringify(obj, (key, value2) => (typeof value2 === "function" ? value2.toString() : value2));
        const checkArgument = (arg) => {
            if (st && !y(st)) return false;
            if (pattern) {
                if (typeof arg === "object" && arg !== null) {
                    try {
                        const argString = stringifyObject(arg);
                        return !!argString && (i(pattern) || a(pattern)).test(argString);
                    } catch (error) {
                        lg.warn(`Failed to stringify argument: ${arg}\nError: ${error}`);
                    }
                }
                const argumentContent = String(arg);
                return !!argumentContent && (i(pattern) || a(pattern)).test(argumentContent);
            }
            return true;
        };
        const replaceTargetArgument = (argumentsList) => {
            const argumentToReplace = argumentsList[Number(argumentIndex)];
            if (shouldReplaceArgument) {
                const argumentString = String(argumentToReplace);
                const replacedArgument = argumentString.replace(replaceRegexValue, constantValue);
                if (replacedArgument !== argumentString) argumentsList[Number(argumentIndex)] = replacedArgument;
            } else {
                argumentsList[Number(argumentIndex)] = constantValue;
            }
        };
        const processArgs = (argumentsList) => {
            if (SHOULD_LOG_ONLY) return false;
            const argumentToReplace = argumentsList[Number(argumentIndex)];
            if (!checkArgument(argumentToReplace)) return false;
            replaceTargetArgument(argumentsList);
            lg.info(`Replaced argument ${argumentIndex} of ${methodPath}`);
            return true;
        };
        let isMatchingSuspended = false;
        const wrapApply = (target, thisArg, argumentsList) => {
            try {
                if (isMatchingSuspended) return Reflect.apply(target, thisArg, argumentsList);
                isMatchingSuspended = true;
                processArgs(argumentsList);
                isMatchingSuspended = false;
                return Reflect.apply(target, thisArg, argumentsList);
            } catch (error) {
                isMatchingSuspended = false;
                lg.warn(`Unexpected error during argument replacement: ${error && error.message}`);
                return Reflect.apply(target, thisArg, argumentsList);
            }
        };
        const wrapConstruct = (target, argumentsList, newTarget) => {
            try {
                if (isMatchingSuspended) return Reflect.construct(target, argumentsList, newTarget);
                isMatchingSuspended = true;
                processArgs(argumentsList);
                isMatchingSuspended = false;
                return Reflect.construct(target, argumentsList, newTarget);
            } catch (error) {
                isMatchingSuspended = false;
                lg.warn(`Unexpected error during argument replacement: ${error && error.message}`);
                return Reflect.construct(target, argumentsList, newTarget);
            }
        };
        parts.base[parts.prop] = new Proxy(nativeMethod, {
            apply: wrapApply,
            construct: wrapConstruct,
            get(target, propName, receiver) {
                if (propName === "toString") return target.toString.bind(target);
                return Reflect.get(target, propName, receiver);
            },
        });
    }],
    ["trusted-click-element", function(selectors, extraMatch, delay, reload, observerTimeoutSec) {
        const lg = n("trusted-click-element");
        if (!selectors) return;
        const SHADOW_COMBINATOR = " >>> ";
        const DEFAULT_OBSERVER_TIMEOUT_SEC = 10;
        const THROTTLE_DELAY_MS = 20;
        const STATIC_CLICK_DELAY_MS = 150;
        const STATIC_RELOAD_DELAY_MS = 500;
        const COOKIE_MATCH_MARKER = "cookie:";
        const LOCAL_STORAGE_MATCH_MARKER = "localStorage:";
        const TEXT_MATCH_MARKER = "containsText:";
        const CLICK_TYPE_MATCH_MARKER = "clickType:";
        const CLICK_TYPE_NATIVE = "native";
        const RELOAD_ON_FINAL_CLICK_MARKER = "reloadAfterClick";
        const EXTRA_MATCH_DELIMITER = /(,\s*){1}(?=!?cookie:|!?localStorage:|containsText:|clickType:)/;
        const sleepMs = (delayMs) => new Promise((resolve) => { setTimeout(resolve, delayMs); });
        p24SpoofClickIsTrusted();
        const closedShadowRoots = new WeakMap();
        const bridgeObservers = new Set();
        let triggerMainObserver = () => {};
        if (selectors.includes(SHADOW_COMBINATOR)) {
            const attachShadowWrapper = (target, thisArg, argumentsList) => {
                const shadowRoot = Reflect.apply(target, thisArg, argumentsList);
                const mode = argumentsList[0] && argumentsList[0].mode;
                if (mode === "closed") closedShadowRoots.set(thisArg, shadowRoot);
                const bridgeObserver = new MutationObserver(() => {
                    triggerMainObserver();
                });
                try {
                    bridgeObserver.observe(shadowRoot, { childList: true, subtree: true });
                    bridgeObservers.add(bridgeObserver);
                } catch (ex) {}
                return shadowRoot;
            };
            window.Element.prototype.attachShadow = new Proxy(window.Element.prototype.attachShadow, { apply: attachShadowWrapper });
        }
        const disconnectBridgeObservers = () => {
            bridgeObservers.forEach((obs) => obs.disconnect());
            bridgeObservers.clear();
        };
        let observerTimeoutMs = DEFAULT_OBSERVER_TIMEOUT_SEC * 1000;
        if (observerTimeoutSec) {
            const parsedTimeout = Number(observerTimeoutSec);
            if (!Number.isInteger(parsedTimeout) || parsedTimeout <= 0) {
                return void lg.warn(`Passed observer timeout '${observerTimeoutSec}' is invalid`);
            }
            observerTimeoutMs = parsedTimeout * 1000;
        }
        let parsedDelayMs;
        if (delay) {
            parsedDelayMs = Number(delay);
            if (!Number.isInteger(parsedDelayMs) || parsedDelayMs < 0) {
                return void lg.warn(`Passed delay '${delay}' is invalid`);
            }
            if (parsedDelayMs >= observerTimeoutMs) {
                return void lg.warn(`Passed delay '${delay}' is bigger than ${observerTimeoutMs} ms`);
            }
        }
        let canClick = !parsedDelayMs;
        const cookieMatches = [];
        const localStorageMatches = [];
        let textMatches = "";
        let clickType = "";
        let isInvertedMatchCookie = false;
        let isInvertedMatchLocalStorage = false;
        if (extraMatch) {
            const parsedExtraMatch = extraMatch.split(EXTRA_MATCH_DELIMITER).map((matchStr) => matchStr.trim());
            parsedExtraMatch.forEach((matchStr) => {
                if (matchStr.includes(COOKIE_MATCH_MARKER)) {
                    const parsed = p24ParseMatchArg(matchStr);
                    isInvertedMatchCookie = parsed.isInvertedMatch;
                    cookieMatches.push(parsed.matchValue.replace(COOKIE_MATCH_MARKER, ""));
                }
                if (matchStr.includes(LOCAL_STORAGE_MATCH_MARKER)) {
                    const parsed = p24ParseMatchArg(matchStr);
                    isInvertedMatchLocalStorage = parsed.isInvertedMatch;
                    localStorageMatches.push(parsed.matchValue.replace(LOCAL_STORAGE_MATCH_MARKER, ""));
                }
                if (matchStr.includes(TEXT_MATCH_MARKER)) {
                    const parsed = p24ParseMatchArg(matchStr);
                    textMatches = parsed.matchValue.replace(TEXT_MATCH_MARKER, "");
                }
                if (matchStr.includes(CLICK_TYPE_MATCH_MARKER)) {
                    const parsed = p24ParseMatchArg(matchStr);
                    if (parsed.isInvertedMatch) {
                        lg.warn(`Passed click type '${matchStr}' is invalid`);
                        return;
                    }
                    const passedClickType = parsed.matchValue.replace(CLICK_TYPE_MATCH_MARKER, "");
                    if (passedClickType !== CLICK_TYPE_NATIVE) {
                        lg.warn(`Passed click type '${passedClickType}' is invalid`);
                        return;
                    }
                    clickType = passedClickType;
                }
            });
        }
        if (cookieMatches.length > 0) {
            const parsedCookieMatches = p24ParseCookieString(cookieMatches.join(";"));
            const parsedCookies = p24ParseCookieString(document.cookie);
            const cookieKeys = Object.keys(parsedCookies);
            if (cookieKeys.length === 0) return;
            const cookiesMatched = Object.keys(parsedCookieMatches).every((key) => {
                const valueMatch = parsedCookieMatches[key] ? (i(parsedCookieMatches[key]) || a(parsedCookieMatches[key])) : null;
                const keyMatch = i(key) || a(key);
                return cookieKeys.some((cookieKey) => {
                    if (!keyMatch.test(cookieKey)) return false;
                    if (!valueMatch) return true;
                    const parsedCookieValue = parsedCookies[cookieKey];
                    if (!parsedCookieValue) return false;
                    return valueMatch.test(parsedCookieValue);
                });
            });
            if (cookiesMatched !== isInvertedMatchCookie) return;
        }
        if (localStorageMatches.length > 0) {
            const localStorageMatched = localStorageMatches.every((str) => {
                const itemValue = window.localStorage.getItem(str);
                return itemValue || itemValue === "";
            });
            if (localStorageMatched !== isInvertedMatchLocalStorage) return;
        }
        const textMatchRegexp = textMatches ? (i(textMatches) || a(textMatches)) : null;
        let selectorsSequence = selectors.split(",").map((selector) => selector.trim());
        const elementsSequence = Array(selectorsSequence.length).fill({ element: null, clicked: false, selectorText: null });
        const findAndClickElement = (elementObj) => {
            try {
                if (!elementObj.selectorText) return;
                const element = p24QueryShadowSelector(elementObj.selectorText, document.documentElement, null, closedShadowRoots);
                if (!element) {
                    lg.warn(`Could not find element: '${elementObj.selectorText}'`);
                    return;
                }
                p24ClickElement(element, clickType);
                elementObj.clicked = true;
            } catch (error) {
                lg.warn(`Could not click element: '${elementObj.selectorText}'`);
            }
        };
        let shouldReloadAfterClick = false;
        let reloadDelayMs = STATIC_RELOAD_DELAY_MS;
        if (reload) {
            const reloadSplit = reload.split(":");
            const reloadMarker = reloadSplit[0];
            const reloadValue = reloadSplit[1];
            if (reloadMarker !== RELOAD_ON_FINAL_CLICK_MARKER) {
                return void lg.warn(`Passed reload option '${reload}' is invalid`);
            }
            if (reloadValue) {
                const passedReload = Number(reloadValue);
                if (Number.isNaN(passedReload)) {
                    return void lg.warn(`Passed reload delay value '${passedReload}' is invalid`);
                }
                if (passedReload > observerTimeoutMs) {
                    return void lg.warn(`Passed reload delay value '${passedReload}' is bigger than maximum ${observerTimeoutMs} ms`);
                }
                reloadDelayMs = passedReload;
            }
            shouldReloadAfterClick = true;
        }
        let canReload = true;
        const clickElementsBySequence = async () => {
            for (let i = 0; i < elementsSequence.length; i += 1) {
                const elementObj = elementsSequence[i];
                if (i >= 1) await sleepMs(STATIC_CLICK_DELAY_MS);
                if (!elementObj.element) break;
                if (!elementObj.clicked) {
                    if (elementObj.element.isConnected) {
                        p24ClickElement(elementObj.element, clickType);
                        elementObj.clicked = true;
                    } else {
                        findAndClickElement(elementObj);
                    }
                }
            }
            const allElementsClicked = elementsSequence.every((elementObj) => elementObj.clicked === true);
            if (allElementsClicked) {
                if (shouldReloadAfterClick && canReload) {
                    canReload = false;
                    setTimeout(() => {
                        window.location.reload();
                    }, reloadDelayMs);
                }
                lg.info("All elements clicked");
            }
        };
        const handleElement = (element, i, selector) => {
            elementsSequence[i] = { element: element || null, clicked: false, selectorText: selector || null };
            if (canClick) clickElementsBySequence();
        };
        const fulfillAndHandleSelectors = () => {
            const fulfilledSelectors = [];
            selectorsSequence.forEach((selector, i) => {
                if (!selector) return;
                const element = p24QueryShadowSelector(selector, document.documentElement, textMatchRegexp, closedShadowRoots);
                if (!element) return;
                handleElement(element, i, selector);
                fulfilledSelectors.push(selector);
            });
            selectorsSequence = selectorsSequence.map((selector) => (selector && fulfilledSelectors.includes(selector) ? null : selector));
            return selectorsSequence;
        };
        const findElements = (mutations, observer) => {
            selectorsSequence = fulfillAndHandleSelectors();
            const allSelectorsFulfilled = selectorsSequence.every((selector) => selector === null);
            if (allSelectorsFulfilled) {
                observer.disconnect();
                disconnectBridgeObservers();
            }
        };
        const initializeMutationObserver = () => {
            const observer = new MutationObserver(p24Throttle(findElements, THROTTLE_DELAY_MS));
            observer.observe(document.documentElement, { attributes: true, childList: true, subtree: true });
            setTimeout(() => {
                observer.disconnect();
                disconnectBridgeObservers();
            }, observerTimeoutMs);
        };
        const checkInitialElements = () => {
            const foundElements = selectorsSequence.every((selector) => {
                if (!selector) return false;
                const element = p24QueryShadowSelector(selector, document.documentElement, textMatchRegexp, closedShadowRoots);
                return !!element;
            });
            if (foundElements) {
                fulfillAndHandleSelectors();
                disconnectBridgeObservers();
            } else {
                initializeMutationObserver();
            }
        };
        triggerMainObserver = () => {
            const randomName = `adg-${l()}`;
            const el = document.documentElement;
            el.setAttribute(randomName, "");
            el.removeAttribute(randomName);
        };
        checkInitialElements();
        if (parsedDelayMs) {
            setTimeout(() => {
                clickElementsBySequence();
                canClick = true;
            }, parsedDelayMs);
        }
    }],
    ["trusted-suppress-native-method", function(methodPath, signatureStr, how, stack) {
        const lg = n("trusted-suppress-native-method");
        if (!methodPath || !signatureStr) return;
        const IGNORE_ARG_SYMBOL = " ";
        const suppress = how === "abort" ? p24GetAbortFunc() : () => {};
        let signatureMatcher;
        try {
            signatureMatcher = p24SplitByPipeRespectingRegex(signatureStr).map((value) => (value === IGNORE_ARG_SYMBOL ? value : p24InferValue(value)));
        } catch (e) {
            return void lg.warn(`Could not parse the signature matcher: ${e && e.message}`);
        }
        const parts = p24GetPropertyInChain(window, methodPath);
        if (typeof parts.chain !== "undefined") {
            return void lg.warn(`Could not reach the end of the prop chain: ${methodPath}`);
        }
        const nativeMethod = parts.base[parts.prop];
        if (!nativeMethod || typeof nativeMethod !== "function") {
            return void lg.warn(`Could not retrieve the method: ${methodPath}`);
        }
        const st = typeof stack === "string" && stack.length > 0 ? (i(stack) || a(stack)) : null;
        const matchMethodCall = (nativeArguments, matchArguments) => matchArguments.every((matcher, idx) => {
            if (matcher === IGNORE_ARG_SYMBOL) return true;
            return p24IsValueMatched(nativeArguments[idx], matcher);
        });
        let isMatchingSuspended = false;
        parts.base[parts.prop] = new Proxy(nativeMethod, {
            apply(target, thisArg, argumentsList) {
                if (isMatchingSuspended) return Reflect.apply(target, thisArg, argumentsList);
                isMatchingSuspended = true;
                if (st && !y(st)) {
                    isMatchingSuspended = false;
                    return Reflect.apply(target, thisArg, argumentsList);
                }
                const isMatching = matchMethodCall(argumentsList, signatureMatcher);
                isMatchingSuspended = false;
                if (isMatching) {
                    lg.info(`Suppressed ${methodPath} call`);
                    return suppress();
                }
                return Reflect.apply(target, thisArg, argumentsList);
            },
        });
    }],
    ["trusted-set-local-storage-item", function(key, value) {
        const lg = n("trusted-set-local-storage-item");
        if (typeof key === "undefined") return void lg.warn("Item key should be specified");
        if (typeof value === "undefined") return void lg.warn("Item value should be specified");
        const parsedValue = p24ParseKeywordValue(value);
        p24SetStorageItem(window.localStorage, key, parsedValue, lg);
        lg.info(`Set local storage item ${key}`);
    }],
    ["trusted-set-constant", function(property, value, stack) {
        const lg = n("trusted-set-constant");
        if (!property) return;
        let constantValue;
        try {
            constantValue = p24InferValue(value);
        } catch (e) {
            return void lg.warn(`${e && e.message}`);
        }
        let canceled = false;
        const mustCancel = (incoming) => {
            if (canceled) return canceled;
            canceled = incoming !== undefined && constantValue !== undefined
                && typeof incoming !== typeof constantValue && incoming !== null;
            return canceled;
        };
        const trapProp = (base, prop, configurable, handler) => {
            if (!handler.init(base[prop])) return false;
            const origDescriptor = Object.getOwnPropertyDescriptor(base, prop);
            let prevSetter;
            if (origDescriptor instanceof Object) {
                if (!origDescriptor.configurable) {
                    lg.warn(`Property '${prop}' is not configurable`);
                    return false;
                }
                base[prop] = constantValue;
                if (origDescriptor.set instanceof Function) prevSetter = origDescriptor.set;
            }
            Object.defineProperty(base, prop, {
                configurable,
                get() { return handler.get(); },
                set(v) {
                    if (prevSetter !== undefined) prevSetter(v);
                    handler.set(v);
                },
            });
            return true;
        };
        const stackRe = typeof stack === "string" && stack.length > 0 ? (i(stack) || a(stack)) : null;
        const setChainPropAccess = p24CreateSetChainPropAccessor({
            stackRe,
            mustCancel,
            trapProp,
            getConstantValue: () => constantValue,
            setConstantValue: (v) => { constantValue = v; },
            lg,
        });
        setChainPropAccess(window, property);
    }],
    ["trusted-set-cookie", function(name, value, offsetExpiresSec, path, domain) {
        const lg = n("trusted-set-cookie");
        if (typeof name === "undefined") return void lg.warn("Cookie name should be specified");
        if (typeof value === "undefined") return void lg.warn("Cookie value should be specified");
        const parsedValue = p24ParseKeywordValue(value);
        const rawPath = path === undefined || path === null ? "/" : path;
        if (!p24IsValidCookiePath(rawPath)) return void lg.warn(`Invalid cookie path: '${rawPath}'`);
        const dm = domain === undefined ? "" : domain;
        if (!document.location.origin.includes(dm)) return void lg.warn(`Cookie domain not matched by origin: '${dm}'`);
        let cookieToSet = p24SerializeCookie(name, parsedValue, rawPath, dm, false);
        if (!cookieToSet) return void lg.warn("Invalid cookie name or value");
        if (offsetExpiresSec) {
            const parsedOffsetMs = p24GetTrustedCookieOffsetMs(offsetExpiresSec);
            if (!parsedOffsetMs) return void lg.warn(`Invalid offsetExpiresSec value: ${offsetExpiresSec}`);
            const expires = Date.now() + parsedOffsetMs;
            cookieToSet += `; expires=${new Date(expires).toUTCString()}`;
        }
        document.cookie = cookieToSet;
        lg.info(`Set cookie ${name}`);
    }],
    ["trusted-prune-inbound-object", function(functionName, propsToRemove, requiredInitialProps, stack) {
        const lg = n("trusted-prune-inbound-object");
        if (!functionName) return;
        const parts = p24GetPropertyInChain(window, functionName);
        if (!parts.base || !parts.prop || typeof parts.base[parts.prop] !== "function") {
            return void lg.warn(`${functionName} is not a function`);
        }
        const prunePaths = p24GetPrunePath(propsToRemove);
        const requiredPaths = p24GetPrunePath(requiredInitialProps);
        const st = typeof stack === "string" && stack.length > 0 ? (i(stack) || a(stack)) : null;
        parts.base[parts.prop] = new Proxy(parts.base[parts.prop], {
            apply(target, thisArg, args) {
                let data = args[0];
                if (typeof data === "object" && data !== null) {
                    data = p24JsonPruner(data, prunePaths, requiredPaths, st, lg);
                    args[0] = data;
                }
                return Reflect.apply(target, thisArg, args);
            },
        });
    }],
    ["trusted-replace-xhr-response", function(pattern, replacement, propsToMatch, verbose) {
        const lg = n("trusted-replace-xhr-response");
        if (typeof Proxy === "undefined") return;
        if (pattern === "" && replacement !== "") {
            return void lg.warn("Pattern argument should not be empty string.");
        }
        const shouldLog = pattern === "" && replacement === "";
        const shouldLogContent = verbose === "true";
        let parsedProps;
        if (typeof propsToMatch === "string" && propsToMatch.length > 0) {
            try {
                parsedProps = x(propsToMatch);
            } catch (e) {
                return void lg.warn(`error parsing propsToMatch: ${e}`);
            }
        }
        const matchedXhrRequests = new Set();
        const xhrRequestHeaders = new Map();
        const forgedRequests = new WeakMap();
        let xhrData;
        const openWrapper = (target, thisArg, args) => {
            xhrData = { method: args[0], url: args[1] };
            if (shouldLog) {
                lg.info(`xhr( method: ${String(args[0])} url: ${String(args[1])} )`);
                return Reflect.apply(target, thisArg, args);
            }
            if (parsedProps && S(parsedProps, args[0], args[1])) {
                matchedXhrRequests.add(thisArg);
            }
            if (matchedXhrRequests.has(thisArg) && !xhrRequestHeaders.has(thisArg)) {
                xhrRequestHeaders.set(thisArg, []);
                const setRequestHeaderHandler = {
                    apply(setTarget, setThisArg, setArgs) {
                        const headers = xhrRequestHeaders.get(setThisArg);
                        if (headers) headers.push(setArgs);
                        return Reflect.apply(setTarget, setThisArg, setArgs);
                    },
                };
                thisArg.setRequestHeader = new Proxy(thisArg.setRequestHeader, setRequestHeaderHandler);
                const responseHeaderWrapper = (headerTarget, headerThisArg, headerArgs) => {
                    const forgedRequest = forgedRequests.get(headerThisArg);
                    if (forgedRequest) return Reflect.apply(headerTarget, forgedRequest, headerArgs);
                    return Reflect.apply(headerTarget, headerThisArg, headerArgs);
                };
                thisArg.getResponseHeader = new Proxy(thisArg.getResponseHeader, { apply: responseHeaderWrapper });
                thisArg.getAllResponseHeaders = new Proxy(thisArg.getAllResponseHeaders, { apply: responseHeaderWrapper });
            }
            return Reflect.apply(target, thisArg, args);
        };
        const sendWrapper = (target, thisArg, args) => {
            if (!matchedXhrRequests.has(thisArg)) {
                return Reflect.apply(target, thisArg, args);
            }
            const forgedRequest = new XMLHttpRequest();
            forgedRequest.withCredentials = thisArg.withCredentials;
            forgedRequest.addEventListener("readystatechange", () => {
                if (forgedRequest.readyState !== 4) return;
                const { readyState, response, responseText, responseURL, responseXML, status, statusText } = forgedRequest;
                const content = responseText || response;
                if (typeof content !== "string") return;
                const patternRegexp = pattern === "*" ? /(\n|.)*/ : (i(pattern) || a(pattern));
                const isPatternFound = pattern === "*" || patternRegexp.test(content);
                let responseContent = content;
                if (isPatternFound) {
                    if (shouldLogContent) lg.info(`Original text content: ${content}`);
                    responseContent = content.replace(patternRegexp, replacement === undefined ? "" : replacement);
                    if (shouldLogContent) lg.info(`Modified text content: ${responseContent}`);
                }
                Object.defineProperties(thisArg, {
                    readyState: { value: readyState, writable: false },
                    responseURL: { value: responseURL, writable: false },
                    responseXML: { value: responseXML, writable: false },
                    status: { value: status, writable: false },
                    statusText: { value: statusText, writable: false },
                    response: { value: responseContent, writable: false },
                    responseText: { value: responseContent, writable: false },
                });
                forgedRequests.set(thisArg, forgedRequest);
                setTimeout(() => {
                    thisArg.dispatchEvent(new Event("readystatechange"));
                    thisArg.dispatchEvent(new ProgressEvent("load"));
                    thisArg.dispatchEvent(new ProgressEvent("loadend"));
                }, 1);
                if (isPatternFound) lg.info(`Replaced xhr response for ${xhrData && xhrData.url}`);
            });
            const openArgs = [xhrData.method, xhrData.url];
            XMLHttpRequest.prototype.open.apply(forgedRequest, openArgs);
            const collectedHeaders = xhrRequestHeaders.get(thisArg) || [];
            collectedHeaders.forEach((header) => {
                forgedRequest.setRequestHeader(header[0], header[1]);
            });
            xhrRequestHeaders.delete(thisArg);
            matchedXhrRequests.delete(thisArg);
            try {
                Reflect.apply(XMLHttpRequest.prototype.send, forgedRequest, args);
            } catch (ex) {
                return Reflect.apply(target, thisArg, args);
            }
            return undefined;
        };
        XMLHttpRequest.prototype.open = new Proxy(XMLHttpRequest.prototype.open, { apply: openWrapper });
        XMLHttpRequest.prototype.send = new Proxy(XMLHttpRequest.prototype.send, { apply: sendWrapper });
    }],
    ["trusted-replace-fetch-response", function(pattern, replacement, propsToMatch, verbose) {
        const lg = n("trusted-replace-fetch-response");
        if (typeof fetch === "undefined" || typeof Proxy === "undefined" || typeof Response === "undefined") return;
        if (pattern === "" && replacement !== "") {
            return void lg.warn("Pattern argument should not be empty string");
        }
        const shouldLog = pattern === "" && replacement === "";
        const shouldLogContent = verbose === "true";
        let parsedProps;
        if (typeof propsToMatch === "string" && propsToMatch.length > 0) {
            try {
                parsedProps = x(propsToMatch);
            } catch (e) {
                return void lg.warn(`error parsing propsToMatch: ${e}`);
            }
        }
        const nativeFetch = window.fetch;
        const patternRegexp = pattern === "*" ? /(\n|.)*/ : (i(pattern) || a(pattern));
        window.fetch = new Proxy(window.fetch, {
            apply(target, thisArg, args) {
                if (shouldLog) {
                    lg.info(`fetch( ${String(args[0])} )`);
                    return Reflect.apply(target, thisArg, args);
                }
                if (parsedProps && !E(parsedProps, args)) {
                    return Reflect.apply(target, thisArg, args);
                }
                return nativeFetch.apply(null, args)
                    .then((response) => response.clone().text()
                        .then((bodyText) => {
                            const isPatternFound = pattern === "*" || patternRegexp.test(bodyText);
                            if (!isPatternFound) return response;
                            if (shouldLogContent) lg.info(`Original text content: ${bodyText}`);
                            const modifiedTextContent = bodyText.replace(patternRegexp, replacement === undefined ? "" : replacement);
                            if (shouldLogContent) lg.info(`Modified text content: ${modifiedTextContent}`);
                            const forgedResponse = new Response(modifiedTextContent, { status: response.status, statusText: response.statusText, headers: response.headers });
                            Object.defineProperties(forgedResponse, {
                                url: { value: response.url },
                                type: { value: response.type },
                                ok: { value: response.ok },
                                redirected: { value: response.redirected },
                            });
                            lg.info("Replaced fetch response");
                            return forgedResponse;
                        })
                        .catch(() => {
                            lg.warn("Response body can't be converted to text");
                            return response;
                        }))
                    .catch(() => Reflect.apply(target, thisArg, args));
            },
        });
    }],
    ["trusted-json-set", function(methodPath, propsPath, argumentValue, requiredInitialProps, jsonSource, stack, mode, verbose) {
        const lg = n("trusted-json-set");
        const isLogOnlyMode = !propsPath;
        const shouldLogVerboseContent = verbose === "true" && !isLogOnlyMode;
        let syntaxMode;
        if (typeof mode === "string" && (mode.trim().toLowerCase() === "legacy" || mode.trim().toLowerCase() === "jsonpath")) {
            syntaxMode = mode.trim().toLowerCase();
        } else {
            syntaxMode = p24ResolveSyntaxMode(propsPath);
        }
        const jsonPathExpression = (!isLogOnlyMode && syntaxMode === "jsonpath")
            ? p24BuildJsonPathExpression(propsPath, argumentValue)
            : "";
        if (!methodPath) return;
        if (!isLogOnlyMode && syntaxMode === "legacy" && argumentValue === undefined) return;
        if (!isLogOnlyMode && syntaxMode === "jsonpath" && jsonPathExpression === "") {
            return void lg.warn("JSONPath mode requires argumentValue unless propsPath already contains an inline mutation");
        }
        let parsedArgumentValue;
        if (!isLogOnlyMode && syntaxMode === "legacy") {
            parsedArgumentValue = p24ParseJsonSetArgumentValue(argumentValue);
            if (!parsedArgumentValue) return;
        }
        const parts = p24GetPropertyInChain(window, methodPath);
        if (typeof parts.chain !== "undefined") {
            return void lg.warn(`Could not reach the end of the prop chain: ${methodPath}`);
        }
        const nativeMethod = parts.base[parts.prop];
        if (!nativeMethod || typeof nativeMethod !== "function") {
            return void lg.warn(`Could not retrieve the method: ${methodPath}`);
        }
        const JSON_SOURCES = { ARG: "arg", ARGS: "args", THIS: "this", RESULT: "result", ALL: "all" };
        const normalizeJsonSource = () => {
            if (jsonSource === "argument") return JSON_SOURCES.ARG;
            if (jsonSource === "arguments") return JSON_SOURCES.ARGS;
            if (jsonSource === "thisArg") return JSON_SOURCES.THIS;
            if (typeof jsonSource === "string" && /^arg:(\d+\|)*\d+$/.test(jsonSource.trim())) return jsonSource.trim();
            if (jsonSource !== JSON_SOURCES.ARG && jsonSource !== JSON_SOURCES.ARGS
                && jsonSource !== JSON_SOURCES.THIS && jsonSource !== JSON_SOURCES.RESULT
                && jsonSource !== JSON_SOURCES.ALL) {
                return JSON_SOURCES.RESULT;
            }
            return jsonSource;
        };
        const parsedSetPaths = syntaxMode === "legacy" ? p24GetPrunePath(propsPath) : [];
        const setPathObj = parsedSetPaths[0];
        const requiredPaths = !isLogOnlyMode && syntaxMode === "legacy" ? p24GetPrunePath(requiredInitialProps) : [];
        const normalizedJsonSource = normalizeJsonSource();
        const st = typeof stack === "string" && stack.length > 0 ? (i(stack) || a(stack)) : null;
        const getSelectedArgumentIndexes = (argsLength) => {
            if (normalizedJsonSource === JSON_SOURCES.ARG) return argsLength > 0 ? [0] : [];
            if (typeof normalizedJsonSource === "string" && normalizedJsonSource.startsWith("arg:")) {
                const rawIndexes = normalizedJsonSource.slice(4).split("|");
                const indexes = [];
                for (let i = 0; i < rawIndexes.length; i += 1) {
                    const index = parseFloat(rawIndexes[i]);
                    if (Number.isNaN(index)) continue;
                    if (index < 0 || index >= argsLength || indexes.includes(index)) continue;
                    indexes.push(index);
                }
                return indexes;
            }
            return [];
        };
        const getValueToSet = (currentValue) => {
            if (parsedArgumentValue === undefined) return currentValue;
            return p24GetJsonSetValue(currentValue, parsedArgumentValue);
        };
        const applyJsonMutation = (jsonValue) => {
            let changed = false;
            if (syntaxMode === "jsonpath") {
                const changedByPath = p24JsonPathApply(jsonValue, jsonPathExpression, { op: "mutate" });
                if (changedByPath) changed = true;
                return { changed, value: jsonValue };
            }
            if (parsedArgumentValue && parsedArgumentValue.hasTimeKeywords) {
                parsedArgumentValue = p24ParseJsonSetArgumentValue(argumentValue) || parsedArgumentValue;
            }
            const value = p24JsonSetter(jsonValue, setPathObj ? setPathObj.path : "", setPathObj ? setPathObj.value : undefined, getValueToSet, requiredPaths, st, lg, () => {
                changed = true;
            });
            return { changed, value };
        };
        const modifyJsonValue = (jsonValue, errorMessage) => {
            const currentStackTrace = new Error().stack || "";
            if (isLogOnlyMode) {
                if (!st || y(st)) {
                    try {
                        if (jsonValue !== null && typeof jsonValue === "object") {
                            lg.info(`${window.location.hostname}\n${p24NativeStringify(jsonValue, null, 2)}\nStack trace:\n${currentStackTrace}`);
                        } else if (typeof jsonValue === "string") {
                            lg.info(`${window.location.hostname}\n${jsonValue}\nStack trace:\n${currentStackTrace}`);
                        }
                    } catch (error) {
                        lg.warn(`${errorMessage}: ${error && error.message}`);
                    }
                }
                return jsonValue;
            }
            if (jsonValue !== null && typeof jsonValue === "object") {
                try {
                    return applyJsonMutation(jsonValue).value;
                } catch (error) {
                    lg.warn(`${errorMessage}: ${error && error.message}`);
                    return jsonValue;
                }
            }
            if (typeof jsonValue === "string") {
                let messageError = "";
                try {
                    const parsedValue = p24NativeParse(jsonValue);
                    if (parsedValue !== null && typeof parsedValue === "object") {
                        return p24NativeStringify(applyJsonMutation(parsedValue).value);
                    }
                } catch (error) {
                    messageError = error instanceof Error ? error.message : String(error);
                }
                try {
                    let changed = false;
                    const lineEditResult = p24JsonLineEdit((parsedLine) => {
                        const mutationResult = applyJsonMutation(parsedLine);
                        if (mutationResult.changed) changed = true;
                        return mutationResult.value;
                    }, jsonValue);
                    if (lineEditResult.hasJsonLines) return lineEditResult.text;
                    lg.warn(`Error parsing JSON string: ${messageError}`);
                    return jsonValue;
                } catch (error) {
                    lg.warn(`${errorMessage}: ${error && error.message}`);
                    return jsonValue;
                }
            }
            return jsonValue;
        };
        let isMatchingSuspended = false;
        const objectWrapper = (target, thisArg, args) => {
            try {
                if (isMatchingSuspended) return Reflect.apply(target, thisArg, args);
                isMatchingSuspended = true;
                const selectedArgumentIndexes = getSelectedArgumentIndexes(args.length);
                for (let i = 0; i < selectedArgumentIndexes.length; i += 1) {
                    const index = selectedArgumentIndexes[i];
                    args[index] = modifyJsonValue(args[index], `Error during setting the argument at index ${index}`);
                }
                if (normalizedJsonSource === JSON_SOURCES.ARGS || normalizedJsonSource === JSON_SOURCES.ALL) {
                    for (let i = 0; i < args.length; i += 1) {
                        args[i] = modifyJsonValue(args[i], `Error during setting the argument at index ${i}`);
                    }
                }
                let modifiedThisArg = thisArg;
                if (normalizedJsonSource === JSON_SOURCES.THIS || normalizedJsonSource === JSON_SOURCES.ALL) {
                    modifiedThisArg = modifyJsonValue(thisArg, "Error during setting the thisArg value");
                }
                let result = Reflect.apply(target, modifiedThisArg, args);
                if (normalizedJsonSource === JSON_SOURCES.RESULT || normalizedJsonSource === JSON_SOURCES.ALL) {
                    result = modifyJsonValue(result, "Error during setting the result value");
                }
                isMatchingSuspended = false;
                return result;
            } catch (error) {
                isMatchingSuspended = false;
                lg.warn(`Unexpected error during JSON modification: ${error && error.message}`);
                return Reflect.apply(target, thisArg, args);
            }
        };
        parts.base[parts.prop] = new Proxy(nativeMethod, {
            apply: objectWrapper,
            get(target, propName, receiver) {
                if (propName === "toString") return target.toString.bind(target);
                return Reflect.get(target, propName, receiver);
            },
        });
    }]
]);return function(e){if(ue.has(e)){for(var t=arguments.length,n=new Array(t>1?t-1:0),r=1;r<t;r++)n[r-1]=arguments[r];ue.get(e)(...n)}else ce.debug(`Scriptlet ${e} does not exist or is not yet implemented.`)}}();
