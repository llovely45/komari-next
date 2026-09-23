"use strict";

const $ = id => document.getElementById(id);
const API = "/api/rpc2";
const DAY_NAMES = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
let state = null;
let nodes = [];
let nodesError = "";
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

async function request(method, params) {
  const response = await fetch(API, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ jsonrpc: "2.0", method, params: params || {}, id: ++requestID })
  });
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
  $("run-state").className = "state-pill " + (state.running ? "is-running" : "");
  $("run-state").innerHTML = "<i></i>" + (state.running ? "同步中" : "运行正常");
  const nodeNames = new Map(nodes.map(node => [node.uuid, node.name]));
  $("rules").innerHTML = rules.map(rule => {
    const run = (state.history || {})[rule.id];
    const outcome = outcomeLabel(run);
    const serverNames = rule.servers.map(uuid => nodeNames.get(uuid) || uuid);
    const resultLines = run && Array.isArray(run.results) ? run.results.map(result => {
      const name = nodeNames.get(result.uuid) || result.uuid || "";
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
          ' · ' + escapeHTML(rule.type) + (rule.proxied ? ' · 橙云' : '') + '</p></div></div>' +
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
  $("save-hint").textContent = state.storageError || "密钥和规则只在点击保存后写入服务端。";
  $("save-hint").className = state.storageError ? "save-error" : "";
}

async function refresh() {
  const values = await Promise.allSettled([
    request("plugin:cloudflare-ddns:state"),
    request("plugin:cloudflare-ddns:clients")
  ]);
  if (values[0].status === "rejected") throw values[0].reason;
  state = values[0].value;
  if (values[1].status === "rejected") {
    nodes = [];
    nodesError = values[1].reason && values[1].reason.message || "未知错误";
  } else {
    nodes = values[1].value || [];
    nodesError = "";
  }
  if (!refresh.draftLoaded) {
    rules = (state.config.rules || []).map(rule => JSON.parse(JSON.stringify(rule)));
    refresh.draftLoaded = true;
  }
  render();
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
  const chosen = new Set(selected || []);
  if (nodesError) {
    $("server-list").innerHTML = '<div class="inline-empty">读取节点失败：' + escapeHTML(nodesError) + '</div>';
    return;
  }
  if (!nodes.length) {
    $("server-list").innerHTML = '<div class="inline-empty">暂时没有可选节点</div>';
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
  $("rule-enabled").checked = rule ? rule.enabled : true;
  $("rule-proxied").checked = rule ? rule.proxied : false;
  $("rule-adopt").checked = rule ? rule.adoptExisting : false;
  const schedule = rule && rule.schedule || {};
  $("schedule-enabled").checked = schedule.enabled === true;
  $("schedule-start").value = schedule.start || "00:00";
  $("schedule-end").value = schedule.end || "23:59";
  $("schedule-offset").value = String(schedule.utcOffsetMinutes == null ? 480 : schedule.utcOffsetMinutes);
  drawWeekdays(schedule.days || [0, 1, 2, 3, 4, 5, 6]);
  drawServers(rule ? rule.servers : []);
  $("delete-rule").hidden = !rule;
  $("editor").hidden = false;
  $("editor").scrollIntoView({ behavior: "smooth", block: "start" });
  updateEditorFields();
}

function updateEditorFields() {
  const cloudflare = $("rule-provider").value === "cloudflare";
  $("proxy-option").hidden = !cloudflare;
  if (!cloudflare) $("rule-proxied").checked = false;
  const enabled = $("schedule-enabled").checked;
  $("schedule-fields").classList.toggle("disabled", !enabled);
  $("schedule-fields").querySelectorAll("input").forEach(input => { input.disabled = !enabled; });
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
  const servers = selectedValues($("server-list"));
  const days = selectedValues($("weekdays")).map(Number);
  if (!domain) return showNotice("请填写完整域名。", "error");
  if (!servers.length) return showNotice("至少选择一个来源节点。", "error");
  if ($("schedule-enabled").checked && !days.length) return showNotice("时段限制至少要选择一个星期。", "error");
  const existing = editingId ? rules.find(rule => rule.id === editingId) : null;
  const rule = {
    id: existing ? existing.id : newID(),
    provider: $("rule-provider").value,
    domain,
    type: $("rule-type").value,
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
$("schedule-enabled").addEventListener("change", updateEditorFields);
$("hw-region").addEventListener("input", () => { $("hw-region").dataset.dirty = "1"; });
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
