"use strict";

const $ = id => document.getElementById(id);
const API = "/api/rpc2";
const DAY_NAMES = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
let state = null;
let nodes = [];
let nodesError = "";
let nodesLoading = false;
let nodesLoaded = false;
let nodesRequest = null;
let stateRequest = null;
let currentNodeSelection = [];
let huaweiLines = [{ line: "default", line_name: "默认线路" }];
let huaweiLinesDomain = "";
let loadingHuaweiLines = false;
let rules = [];
let editingId = "";
let busy = false;
let requestID = 0;

function escapeHTML(value) {
  return String(value == null ? "" : value).replace(/[&<>"']/g, character => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;"
  })[character]);
}

function showNotice(message, kind) {
  const notice = $("notice");
  notice.textContent = message || "";
  notice.className = "notice " + (kind || "info");
  notice.hidden = !message;
  if (message) window.scrollTo({ top: 0, behavior: "smooth" });
}

async function request(method, params, timeoutMs) {
  const controller = timeoutMs && typeof AbortController !== "undefined" ? new AbortController() : null;
  const timer = timeoutMs ? window.setTimeout(() => { if (controller) controller.abort(); }, timeoutMs) : 0;
  let response;
  try {
    response = await fetch(API, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ jsonrpc: "2.0", method, params: params || {}, id: ++requestID }),
      signal: controller ? controller.signal : undefined
    });
  } catch (error) {
    if (error && error.name === "AbortError") throw new Error("读取节点列表超时，可重试或改用手动 IP。");
    throw error;
  } finally {
    if (timer) window.clearTimeout(timer);
  }
  let envelope;
  try { envelope = await response.json(); }
  catch (_) { throw new Error("服务端返回了无效响应"); }
  if (!response.ok) throw new Error(envelope.error && envelope.error.message || ("请求失败（HTTP " + response.status + "）"));
  if (envelope.error) throw new Error(envelope.error.message || "请求失败");
  return envelope.result;
}

function providerLabel(provider) {
  return provider === "huaweicloud" ? "华为云国际站" : "Cloudflare";
}

function normalizeDomainKey(value) {
  return String(value || "").trim().toLowerCase().replace(/\.$/, "");
}

function huaweiLineName(id) {
  const line = huaweiLines.find(value => value.line === (id || "default"));
  return line ? line.line_name || line.line : (id === "default" || !id ? "全网默认" : id);
}

function isHuaweiLineType(line, kind) {
  const id = String(line.line || "").toLowerCase().replace(/[^a-z0-9]/g, "");
  const name = String(line.line_name || "").trim().toLowerCase();
  if (kind === "operator") return /^(isp|operator|operatorline|ispline)$/.test(id) || /^(运营商线路解析|运营商线路|运营商)$/.test(name);
  return /^(region|geography|geo|arealine|regionline)$/.test(id) || /^(地域解析|地区解析|地域线路解析)$/.test(name);
}

function drawHuaweiLines(selected, requestedType) {
  const byID = new Map(huaweiLines.map(line => [line.line, line]));
  if (!byID.has("default")) byID.set("default", { line: "default", line_name: "全网默认", available: true });
  const children = new Map();
  huaweiLines.forEach(line => {
    if (!line.father_id || line.father_id === line.line || !byID.has(line.father_id)) return;
    const list = children.get(line.father_id) || [];
    list.push(line);
    children.set(line.father_id, list);
  });
  const hasAvailable = (id, seen) => {
    if (seen.has(id)) return false;
    seen.add(id);
    const line = byID.get(id);
    if (line && line.available) return true;
    return (children.get(id) || []).some(child => hasAvailable(child.line, new Set(seen)));
  };
  const path = [];
  let cursor = selected && byID.get(selected);
  const visited = new Set();
  while (cursor && !visited.has(cursor.line)) {
    visited.add(cursor.line);
    path.unshift(cursor.line);
    cursor = byID.get(cursor.father_id);
  }
  const defaultLine = byID.get("default");
  const operator = huaweiLines.find(line => isHuaweiLineType(line, "operator"));
  const region = huaweiLines.find(line => isHuaweiLineType(line, "region"));
  let categories = [defaultLine];
  if (operator) categories.push(operator);
  if (region) categories.push(region);
  if (!operator && !region) {
    let roots = huaweiLines.filter(line => !line.father_id || !byID.has(line.father_id) || line.father_id === line.line);
    if (roots.length === 1 && roots[0].line === "default") roots = children.get("default") || [];
    categories = [defaultLine].concat(roots.filter(line => line.line !== "default" && hasAvailable(line.line, new Set())));
  }
  categories = categories.filter((line, index, list) => list.findIndex(value => value.line === line.line) === index);
  const pathCategory = categories.find(line => path.includes(line.line));
  const activeType = (requestedType && categories.some(line => line.line === requestedType) ? requestedType : "") ||
    (pathCategory && pathCategory.line) || (selected === "default" ? "default" : "default");
  $("huawei-line-type").innerHTML = categories.map(line => '<option value="' + escapeHTML(line.line) + '">' +
    escapeHTML(line.line === "default" ? "全网默认" : (line.line_name || line.line)) + "</option>").join("");
  $("huawei-line-type").value = activeType;

  const typeLine = byID.get(activeType) || defaultLine;
  let parentID = typeLine.line;
  let chosenLine = typeLine.available ? typeLine.line : "";
  let pathIndex = path.indexOf(typeLine.line) + 1;
  const levelIDs = ["huawei-line-level-1", "huawei-line-level-2", "huawei-line-level-3"];
  const placeholders = ["选择区域或运营商", "地区默认", "省市默认"];
  levelIDs.forEach((id, depth) => {
    const select = $(id);
    const options = (children.get(parentID) || []).filter(line => hasAvailable(line.line, new Set()));
    const wanted = pathIndex > 0 ? path[pathIndex] : "";
    if (!options.length) {
      select.innerHTML = '<option value="">' + placeholders[depth] + "</option>";
      select.disabled = true;
      return;
    }
    const optionsHTML = options.map(line => '<option value="' + escapeHTML(line.line) + '">' +
      escapeHTML(line.line_name || line.line) + "</option>").join("");
    select.innerHTML = '<option value="">' + placeholders[depth] + "</option>" + optionsHTML;
    select.disabled = false;
    let selectedID = options.some(line => line.line === wanted) ? wanted : "";
    if (!selectedID && !selected) {
      const defaultChild = options.find(line => /默认|default/i.test(line.line_name || ""));
      if (defaultChild) selectedID = defaultChild.line;
    }
    select.value = selectedID;
    if (!selectedID) return;
    const chosen = byID.get(selectedID);
    if (chosen && chosen.available) chosenLine = chosen.line;
    parentID = selectedID;
    pathIndex++;
  });
  if (selected && path.length && activeType === "default" && selected !== "default") chosenLine = "";
  $("huawei-line").value = chosenLine;
}

function formatTime(value) {
  if (!value) return "尚未同步";
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "—";
  return date.toLocaleString();
}

function offsetLabel(minutes) {
  const sign = minutes < 0 ? "−" : "+";
  const absolute = Math.abs(minutes);
  return "UTC" + sign + String(Math.floor(absolute / 60)).padStart(2, "0") +
    ":" + String(absolute % 60).padStart(2, "0");
}

function scheduleLabel(schedule) {
  if (!schedule || !schedule.enabled) return "全天";
  const days = (schedule.days || []).map(day => DAY_NAMES[day]).join(" ");
  return days + " · " + schedule.start + "–" + schedule.end + " · " +
    offsetLabel(Number(schedule.utcOffsetMinutes) || 0);
}

function outcomeLabel(run) {
  if (!run) return ["待运行", "pending"];
  if (run.outcome === "running") return ["同步中", "running"];
  if (run.outcome === "error") return ["有错误", "error"];
  return ["已同步", "success"];
}

function render() {
  if (!state) return;
  const config = state.config || {};
  const cf = config.cloudflare || {};
  const hw = config.huaweicloud || {};
  $("cf-credential-state").textContent = cf.hasToken ? "Token 已保存" :
    (cf.hasLegacyKey ? "Global Key 已保存" : "未配置");
  $("cf-credential-state").className = "badge " + (cf.hasToken || cf.hasLegacyKey ? "badge-ready" : "");
  if (!$("cf-mode").dataset.dirty) $("cf-mode").value = cf.mode || "token";
  updateCFFields();
  $("cf-token").placeholder = cf.hasToken ? "已保存；留空保持不变" : "Zone DNS Read / Edit";
  $("hw-credential-state").textContent = hw.hasCredentials ? "AK/SK 已保存" : "未配置";
  $("hw-credential-state").className = "badge " + (hw.hasCredentials ? "badge-ready" : "");
  $("hw-ak").placeholder = hw.hasCredentials ? "已保存；留空保持不变" : "华为云国际站 AK";
  $("hw-sk").placeholder = hw.hasCredentials ? "已保存；留空保持不变" : "华为云国际站 SK";
  if (!$("hw-region").dataset.dirty) $("hw-region").value = hw.region || "ap-southeast-1";
  $("rule-count").textContent = String(rules.length);
  $("empty").hidden = rules.length > 0;
  $("sync-all").disabled = busy || rules.filter(rule => rule.enabled).length === 0;
  $("save").disabled = busy;
  $("run-state").className = "state-pill " + (state.running ? "is-running" : (state.lastError ? "is-error" : ""));
  $("run-state").innerHTML = "<i></i>" + (state.running ? "同步中" : (state.lastError ? "存在错误" : "运行正常"));
  const nodeNames = new Map(nodes.map(node => [node.uuid, node.name]));
  $("rules").innerHTML = rules.map(rule => {
    const run = (state.history || {})[rule.id];
    const outcome = outcomeLabel(run);
    const serverNames = rule.source === "manual" ? ["指定 IP · " + rule.manualIP] :
      (rule.servers || []).map(uuid => nodeNames.get(uuid) || uuid);
    const resultLines = run && Array.isArray(run.results) ? run.results.map(result => {
      const name = result.uuid === "manual" ? "指定 IP" : (nodeNames.get(result.uuid) || result.uuid || "");
      const detail = result.message || result.ip || (result.ips || []).join(", ") || result.action || "";
      return '<li><span>' + escapeHTML(name || providerLabel(rule.provider)) +
        '</span><span class="result-action ' + escapeHTML(result.action || "error") + '">' +
        escapeHTML(result.action || "error") + '</span><span class="result-detail">' +
        escapeHTML(detail) + '</span></li>';
    }).join("") : "";
    const errorLine = run && run.error ? '<p class="rule-error">' + escapeHTML(run.error) + "</p>" : "";
    return '<article class="rule-card">' +
      '<div class="rule-main">' +
        '<div class="rule-title-wrap"><span class="provider-dot ' + (rule.provider === "huaweicloud" ? "hw" : "cf") + '"></span>' +
          '<div><h3>' + escapeHTML(rule.domain) + '</h3><p>' + escapeHTML(providerLabel(rule.provider)) +
          ' · ' + escapeHTML(rule.type) + (rule.provider === "huaweicloud" ? ' · ' + escapeHTML(huaweiLineName(rule.line)) : '') +
          (rule.proxied ? ' · 橙云' : '') + '</p></div></div>' +
        '<div class="rule-meta"><span class="rule-status ' + outcome[1] + '"><i></i>' + outcome[0] + '</span>' +
          '<span>' + escapeHTML(serverNames.join("、")) + '</span>' +
          '<span>每 ' + escapeHTML(rule.interval) + ' 分钟</span>' +
          '<span>' + escapeHTML(scheduleLabel(rule.schedule)) + '</span></div>' +
        '<div class="rule-last">最近运行：' + escapeHTML(formatTime(run && run.finishedAt || run && run.startedAt)) +
          (run && run.nextAt ? ' <span>· 下次检查 ' + escapeHTML(formatTime(run.nextAt)) + '</span>' : '') + '</div>' +
        errorLine +
        (resultLines ? '<ul class="result-list">' + resultLines + '</ul>' : '') +
      '</div>' +
      '<div class="rule-actions"><button class="button button-small button-muted" data-sync="' + escapeHTML(rule.id) + '" type="button">立即同步</button>' +
        '<button class="button button-small button-plain" data-edit="' + escapeHTML(rule.id) + '" type="button">编辑</button></div>' +
    '</article>';
  }).join("");
  $("save-hint").textContent = state.storageError || state.lastError || "密钥和规则只在点击保存后写入服务端。";
  $("save-hint").className = state.storageError || state.lastError ? "save-error" : "";
  $("load-huawei-lines").disabled = busy || loadingHuaweiLines;
}

function refresh() {
  if (stateRequest) return stateRequest;
  stateRequest = request("plugin:cloudflare-ddns:state").then(value => {
    state = value;
    if (!refresh.draftLoaded) {
      rules = (state.config.rules || []).map(rule => JSON.parse(JSON.stringify(rule)));
      refresh.draftLoaded = true;
    }
    render();
    if (!refresh.nodeNamesLoaded && rules.some(rule => rule.source !== "manual")) {
      refresh.nodeNamesLoaded = true;
      refreshNodes();
    }
  }).finally(() => {
    stateRequest = null;
  });
  return stateRequest;
}

function selectedNodeIDs() {
  return currentNodeSelection.slice();
}

async function refreshNodes(force) {
  if (nodesRequest) return nodesRequest;
  if (nodesLoaded && !force) return nodes;
  nodesLoading = true;
  nodesError = "";
  if (!$("editor").hidden && $("source-mode").value === "nodes") {
    drawServers(selectedNodeIDs());
  }
  nodesRequest = request("plugin:cloudflare-ddns:clients", {}, 50000).then(value => {
    nodes = value || [];
    nodesError = "";
    nodesLoaded = true;
    return nodes;
  }).catch(error => {
    nodes = [];
    nodesError = error && error.message || "未知错误";
    nodesLoaded = true;
    return [];
  }).finally(() => {
    nodesLoading = false;
    nodesRequest = null;
    if (!$("editor").hidden && $("source-mode").value === "nodes") {
      drawServers(selectedNodeIDs());
    }
    render();
  });
  return nodesRequest;
}

function newID() {
  if (window.crypto && typeof window.crypto.randomUUID === "function") return window.crypto.randomUUID().replace(/-/g, "");
  return Date.now().toString(16) + Math.random().toString(16).slice(2, 18);
}

function drawWeekdays(selected) {
  const value = new Set(selected || [0, 1, 2, 3, 4, 5, 6]);
  $("weekdays").innerHTML = DAY_NAMES.map((name, day) =>
    '<label class="weekday"><input type="checkbox" value="' + day + '"' +
    (value.has(day) ? " checked" : "") + '><span>' + name + "</span></label>").join("");
}

function drawServers(selected) {
  currentNodeSelection = (selected || []).slice();
  const chosen = new Set(selected || []);
  if (nodesLoading) {
    $("server-list").innerHTML = '<div class="inline-empty">正在读取 Komari 节点列表…</div>';
    return;
  }
  if (nodesError) {
    $("server-list").innerHTML = '<div class="inline-empty">读取节点失败：' + escapeHTML(nodesError) +
      ' <button class="button button-small button-muted" data-retry-nodes type="button">重试</button></div>';
    return;
  }
  if (!nodesLoaded) {
    $("server-list").innerHTML = '<div class="inline-empty">尚未读取节点列表。 <button class="button button-small button-muted" data-retry-nodes type="button">读取节点</button></div>';
    return;
  }
  if (!nodes.length) {
    $("server-list").innerHTML = '<div class="inline-empty">暂时没有可选节点。 <button class="button button-small button-muted" data-retry-nodes type="button">重试</button></div>';
    return;
  }
  $("server-list").innerHTML = nodes.map(node => {
    const addr = [node.ipv4, node.ipv6].filter(Boolean).join(" · ");
    return '<label class="server-option"><input type="checkbox" value="' + escapeHTML(node.uuid) +
      '"' + (chosen.has(node.uuid) ? " checked" : "") + '><span class="server-copy"><b>' +
      escapeHTML(node.name) + '</b><small>' + escapeHTML(addr || "尚未上报 IP") + '</small></span></label>';
  }).join("");
}

function setEditor(rule) {
  editingId = rule ? rule.id : "";
  $("editor-title").textContent = rule ? "编辑解析规则" : "添加解析规则";
  $("rule-provider").value = rule ? rule.provider : "cloudflare";
  $("rule-domain").value = rule ? rule.domain : "";
  $("rule-type").value = rule ? rule.type : "A";
  $("rule-interval").value = String(rule ? rule.interval : 5);
  $("rule-ttl").value = String(rule ? rule.ttl : 300);
  $("source-mode").value = rule && rule.source === "manual" ? "manual" : "nodes";
  $("manual-ip").value = rule && rule.manualIP || "";
  const domainKey = normalizeDomainKey(rule && rule.domain);
  if (domainKey && huaweiLinesDomain === domainKey) {
    drawHuaweiLines(rule && rule.line || "default");
  } else {
    huaweiLines = [{ line: "default", line_name: "默认线路" }];
    huaweiLinesDomain = "";
    drawHuaweiLines(rule && rule.line || "default");
  }
  $("huawei-line-hint").textContent = "填写域名后读取该 DNS Zone 的可用线路；旧规则使用默认线路。";
  $("rule-enabled").checked = rule ? rule.enabled : true;
  $("rule-proxied").checked = rule ? rule.proxied : false;
  $("rule-adopt").checked = rule ? rule.adoptExisting : false;
  const schedule = rule && rule.schedule || {};
  $("schedule-enabled").checked = schedule.enabled === true;
  $("schedule-start").value = schedule.start || "00:00";
  $("schedule-end").value = schedule.end || "23:59";
  $("schedule-offset").value = String(schedule.utcOffsetMinutes == null ? 480 : schedule.utcOffsetMinutes);
  drawWeekdays(schedule.days || [0, 1, 2, 3, 4, 5, 6]);
  currentNodeSelection = rule && rule.source !== "manual" ? (rule.servers || []).slice() : [];
  drawServers(rule ? rule.servers : []);
  $("delete-rule").hidden = !rule;
  $("editor").hidden = false;
  $("editor").scrollIntoView({ behavior: "smooth", block: "start" });
  updateEditorFields();
}

function updateEditorFields() {
  const cloudflare = $("rule-provider").value === "cloudflare";
  const manual = $("source-mode").value === "manual";
  $("proxy-option").hidden = !cloudflare;
  $("huawei-line-field").hidden = cloudflare;
  $("manual-ip-field").hidden = !manual;
  $("server-field").hidden = manual;
  if (!cloudflare) $("rule-proxied").checked = false;
  const enabled = $("schedule-enabled").checked;
  $("schedule-fields").classList.toggle("disabled", !enabled);
  $("schedule-fields").querySelectorAll("input").forEach(input => { input.disabled = !enabled; });
  if (!manual && !nodesLoaded) refreshNodes();
}

async function loadHuaweiLines() {
  if (loadingHuaweiLines || busy) return;
  const domain = $("rule-domain").value.trim();
  if (!domain) return showNotice("请先填写域名，再读取华为云解析线路。", "error");
  loadingHuaweiLines = true;
  render();
  try {
    const lines = await request("plugin:cloudflare-ddns:huaweiLines", {
      domain,
      credentials: {
        huaweiAccessKey: $("hw-ak").value.trim(),
        huaweiSecretKey: $("hw-sk").value.trim(),
        huaweiRegion: $("hw-region").value.trim()
      }
    });
    const selected = $("huawei-line").value || "default";
    huaweiLines = Array.isArray(lines) ? lines : [];
    if (!huaweiLines.some(line => line.line === "default")) huaweiLines.unshift({ line: "default", line_name: "默认线路" });
    huaweiLinesDomain = normalizeDomainKey(domain);
    const keep = huaweiLines.some(line => line.line === selected) ? selected : "default";
    drawHuaweiLines(keep);
    $("huawei-line-hint").textContent = "已读取该域名 Zone 的 " + huaweiLines.length + " 条可用线路。";
    if (keep !== selected) showNotice("当前保存的线路已不可用，已切换到默认线路，请确认后应用。", "info");
    else showNotice("已读取华为云 DNS 线路。", "success");
  } catch (error) {
    showNotice(error.message, "error");
  } finally {
    loadingHuaweiLines = false;
    render();
  }
}

function updateCFFields() {
  const globalKey = $("cf-mode").value === "global";
  $("cf-token-field").hidden = globalKey;
  $("cf-global-fields").hidden = !globalKey;
}

function closeEditor() {
  $("editor").hidden = true;
  editingId = "";
}

function selectedValues(container) {
  return Array.from(container.querySelectorAll('input[type="checkbox"]:checked')).map(input => input.value);
}

function applyRule() {
  const domain = $("rule-domain").value.trim();
  const source = $("source-mode").value;
  const servers = source === "nodes" ? selectedValues($("server-list")) : [];
  const manualIP = source === "manual" ? $("manual-ip").value.trim() : "";
  const days = selectedValues($("weekdays")).map(Number);
  if (!domain) return showNotice("请填写完整域名。", "error");
  if (source === "nodes" && !servers.length) return showNotice("至少选择一个来源节点。", "error");
  if (source === "manual" && !manualIP) return showNotice("请填写要写入 DNS 的 IP 地址。", "error");
  if ($("schedule-enabled").checked && !days.length) return showNotice("时段限制至少要选择一个星期。", "error");
  const existing = editingId ? rules.find(rule => rule.id === editingId) : null;
  const provider = $("rule-provider").value;
  const line = provider === "huaweicloud" ? ($("huawei-line").value || "default") : "";
  const normalizedDomain = normalizeDomainKey(domain);
  const duplicate = rules.some(item => item.id !== editingId && item.provider === provider &&
    normalizeDomainKey(item.domain) === normalizedDomain && item.type === $("rule-type").value &&
    (provider !== "huaweicloud" || (item.line || "default") === line));
  if (duplicate) return showNotice("相同服务商、域名、记录类型和解析线路只能配置一条规则。", "error");
  const rule = {
    id: existing ? existing.id : newID(),
    provider,
    domain,
    type: $("rule-type").value,
    source,
    manualIP,
    line,
    interval: Number($("rule-interval").value),
    servers,
    enabled: $("rule-enabled").checked,
    proxied: $("rule-proxied").checked,
    adoptExisting: $("rule-adopt").checked,
    ttl: Number($("rule-ttl").value) || 300,
    schedule: {
      enabled: $("schedule-enabled").checked,
      start: $("schedule-start").value || "00:00",
      end: $("schedule-end").value || "23:59",
      days: $("schedule-enabled").checked ? days : [0, 1, 2, 3, 4, 5, 6],
      utcOffsetMinutes: Number($("schedule-offset").value)
    }
  };
  if (existing) rules = rules.map(item => item.id === editingId ? rule : item);
  else rules = rules.concat([rule]);
  closeEditor();
  render();
  showNotice("规则已暂存；点击“保存设置”后才会在服务端生效。", "success");
}

function deleteRule() {
  if (!editingId) return;
  rules = rules.filter(rule => rule.id !== editingId);
  closeEditor();
  render();
  showNotice("规则已从待保存设置中移除。删除规则不会删除云端 DNS 记录。", "info");
}

async function save() {
  if (busy) return;
  busy = true;
  render();
  try {
    const response = await request("plugin:cloudflare-ddns:save", {
        credentials: {
          cloudflareMode: $("cf-mode").value,
          cloudflareToken: $("cf-token").value.trim(),
          cloudflareEmail: $("cf-email").value.trim(),
          cloudflareKey: $("cf-key").value.trim(),
          huaweiAccessKey: $("hw-ak").value.trim(),
          huaweiSecretKey: $("hw-sk").value.trim(),
          huaweiRegion: $("hw-region").value.trim(),
          clearCloudflare: $("cf-clear").checked,
          clearHuawei: $("hw-clear").checked
        },
        rules
    });
    $("cf-token").value = "";
    $("cf-email").value = "";
    $("cf-key").value = "";
    $("hw-ak").value = "";
    $("hw-sk").value = "";
    $("cf-clear").checked = false;
    $("hw-clear").checked = false;
    $("hw-region").dataset.dirty = "";
    state = response;
    rules = (state.config.rules || []).map(rule => JSON.parse(JSON.stringify(rule)));
    refresh.draftLoaded = true;
    showNotice("设置已保存，定时任务已按新配置运行。", "success");
  } catch (error) {
    showNotice(error.message, "error");
  } finally {
    busy = false;
    render();
  }
}

async function test(provider) {
  if (busy) return;
  busy = true;
  render();
  try {
    const data = await request("plugin:cloudflare-ddns:test", {
        provider,
        credentials: {
          cloudflareMode: $("cf-mode").value,
          cloudflareToken: $("cf-token").value.trim(),
          cloudflareEmail: $("cf-email").value.trim(),
          cloudflareKey: $("cf-key").value.trim(),
          huaweiAccessKey: $("hw-ak").value.trim(),
          huaweiSecretKey: $("hw-sk").value.trim(),
          huaweiRegion: $("hw-region").value.trim()
        }
    });
    showNotice(data.message, "success");
  } catch (error) {
    showNotice(error.message, "error");
  } finally {
    busy = false;
    render();
  }
}

async function sync(id) {
  if (busy) return;
  busy = true;
  render();
  try {
    const data = await request("plugin:cloudflare-ddns:sync", id ? { id } : {});
    showNotice(data.started ? "同步任务已启动。" : "当前没有可执行的规则。", data.started ? "success" : "info");
    window.setTimeout(() => refresh().catch(error => showNotice(error.message, "error")), 700);
  } catch (error) {
    showNotice(error.message, "error");
  } finally {
    busy = false;
    render();
  }
}

$("add-rule").addEventListener("click", () => setEditor(null));
$("add-first").addEventListener("click", () => setEditor(null));
$("close-editor").addEventListener("click", closeEditor);
$("cancel-editor").addEventListener("click", closeEditor);
$("apply-rule").addEventListener("click", applyRule);
$("delete-rule").addEventListener("click", deleteRule);
$("save").addEventListener("click", save);
$("sync-all").addEventListener("click", () => sync(""));
$("test-cf").addEventListener("click", () => test("cloudflare"));
$("test-hw").addEventListener("click", () => test("huaweicloud"));
$("cf-mode").addEventListener("change", () => {
  $("cf-mode").dataset.dirty = "1";
  updateCFFields();
});
$("rule-provider").addEventListener("change", updateEditorFields);
$("source-mode").addEventListener("change", updateEditorFields);
$("server-list").addEventListener("click", event => {
  if (event.target.closest("[data-retry-nodes]")) refreshNodes(true);
});
$("server-list").addEventListener("change", () => {
  currentNodeSelection = selectedValues($("server-list"));
});
$("load-huawei-lines").addEventListener("click", loadHuaweiLines);
$("huawei-line-type").addEventListener("change", () => drawHuaweiLines("", $("huawei-line-type").value));
["huawei-line-level-1", "huawei-line-level-2", "huawei-line-level-3"].forEach(id => {
  $(id).addEventListener("change", () => {
    const selected = $("huawei-line-level-3").value || $("huawei-line-level-2").value ||
      $("huawei-line-level-1").value || "";
    drawHuaweiLines(selected, $("huawei-line-type").value);
  });
});
$("rule-domain").addEventListener("input", () => {
  const key = normalizeDomainKey($("rule-domain").value);
  if (key === huaweiLinesDomain) return;
  huaweiLines = [{ line: "default", line_name: "默认线路" }];
  huaweiLinesDomain = "";
  drawHuaweiLines("default");
  $("huawei-line-hint").textContent = "域名已修改，请重新读取对应 Zone 的线路。";
});
$("schedule-enabled").addEventListener("change", updateEditorFields);
$("hw-region").addEventListener("input", () => {
  $("hw-region").dataset.dirty = "1";
  if (!huaweiLinesDomain) return;
  const selected = $("huawei-line").value || "default";
  huaweiLines = [{ line: "default", line_name: "默认线路" }];
  huaweiLinesDomain = "";
  drawHuaweiLines(selected);
  $("huawei-line-hint").textContent = "华为云区域已修改，请重新读取该 Zone 的线路。";
});
$("rules").addEventListener("click", event => {
  const edit = event.target.closest("[data-edit]");
  const syncButton = event.target.closest("[data-sync]");
  if (edit) setEditor(rules.find(rule => rule.id === edit.dataset.edit));
  if (syncButton) sync(syncButton.dataset.sync);
});

refresh().catch(error => showNotice(error.message, "error"));
window.setInterval(() => {
  if (!busy) refresh().catch(() => {});
}, 15000);
