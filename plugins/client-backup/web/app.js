"use strict";

const $ = id => document.getElementById(id);
const API = "/api/rpc2";
let requestID = 0;
let selectedClients = null;
let previewCounts = null;

function escapeHTML(value) {
  return String(value == null ? "" : value).replace(/[&<>"']/g, character => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;"
  })[character]);
}

function notice(message, kind) {
  const el = $("notice");
  el.textContent = message || "";
  el.className = "notice " + (kind || "info");
  el.hidden = !message;
}

async function rpc(method, params) {
  const response = await fetch(API, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ jsonrpc: "2.0", method, params: params || {}, id: ++requestID })
  });
  let payload;
  try { payload = await response.json(); }
  catch (_) { throw new Error("服务端返回了无效响应"); }
  if (!response.ok) throw new Error(payload.error && payload.error.message || ("请求失败（HTTP " + response.status + "）"));
  if (payload.error) throw new Error(payload.error.message || "请求失败");
  return payload.result;
}

function ipv4Key(value) {
  const parts = String(value || "").trim().split(".");
  if (parts.length !== 4) return "";
  const numbers = parts.map(part => /^\d{1,3}$/.test(part) ? Number(part) : NaN);
  if (numbers.some(number => !Number.isInteger(number) || number < 0 || number > 255)) return "";
  return numbers.join(".");
}

async function exportBackup() {
  const button = $("export");
  button.disabled = true;
  notice("正在读取节点列表…", "info");
  try {
    const clients = await rpc("admin:listClients", {});
    if (!Array.isArray(clients)) throw new Error("节点列表格式异常");
    const document = {
      format: "komari-client-backup",
      version: 1,
      created_at: new Date().toISOString(),
      clients
    };
    const blob = new Blob([JSON.stringify(document, null, 2) + "\n"], { type: "application/json;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const link = documentElement("a");
    link.href = url;
    link.download = "komari-nodes-" + new Date().toISOString().slice(0, 10) + ".json";
    link.click();
    URL.revokeObjectURL(url);
    notice("已导出 " + clients.length + " 个节点。备份文件包含 RPC Token，请妥善保管。", "success");
  } catch (error) {
    notice(error.message || "导出失败", "error");
  } finally {
    button.disabled = false;
  }
}

function documentElement(tag) {
  return window.document.createElement(tag);
}

function validateBackup(value) {
  if (!value || value.format !== "komari-client-backup" || value.version !== 1 || !Array.isArray(value.clients)) {
    throw new Error("文件不是受支持的 Komari 节点备份（需要 format=komari-client-backup、version=1）");
  }
  if (value.clients.length === 0) throw new Error("备份文件中没有节点");
  for (let index = 0; index < value.clients.length; index++) {
    const client = value.clients[index];
    if (!client || typeof client.uuid !== "string" || !client.uuid || typeof client.token !== "string" || !client.token) {
      throw new Error("第 " + (index + 1) + " 个节点缺少 UUID 或 RPC Token");
    }
  }
  return value.clients;
}

async function inspectFile(file) {
  selectedClients = null;
  previewCounts = null;
  $("restore").disabled = true;
  $("preview").hidden = true;
  $("file-name").textContent = file ? file.name : "尚未选择文件";
  if (!file) return;
  if (file.size > 32 * 1024 * 1024) {
    notice("备份文件超过 32 MiB，未加载。", "error");
    return;
  }
  try {
    const parsed = JSON.parse(await file.text());
    selectedClients = validateBackup(parsed);
    notice("备份文件包含 " + selectedClients.length + " 个节点，正在计算匹配预览…", "info");
    const current = await rpc("admin:listClients", {});
    if (!Array.isArray(current)) throw new Error("无法读取当前节点列表");
    previewCounts = calculatePreview(selectedClients, current);
    renderPreview(previewCounts);
    $("restore").disabled = false;
    notice("备份已载入。恢复执行时服务端会重新检查匹配与冲突。", "success");
  } catch (error) {
    selectedClients = null;
    previewCounts = null;
    notice(error.message || "无法读取备份文件", "error");
  }
}

function calculatePreview(clients, current) {
  const index = new Map();
  for (const client of current) {
    const ip = ipv4Key(client.ipv4);
    if (!ip) continue;
    index.set(ip, (index.get(ip) || 0) + 1);
  }
  const savedIPs = new Map();
  for (const client of clients) {
    const ip = ipv4Key(client.ipv4);
    if (ip) savedIPs.set(ip, (savedIPs.get(ip) || 0) + 1);
  }
  const counts = { total: clients.length, matched: 0, added: 0, ambiguous: 0 };
  for (const client of clients) {
    const ip = ipv4Key(client.ipv4);
    if (ip && (savedIPs.get(ip) > 1 || index.get(ip) > 1)) counts.ambiguous++;
    else if (ip && index.get(ip) === 1) counts.matched++;
    else counts.added++;
  }
  return counts;
}

function renderPreview(counts) {
  const preview = $("preview");
  preview.innerHTML = "<strong>恢复预览</strong><span>共 " + counts.total + " 个节点</span><span>按 IPv4 更新 " + counts.matched + " 个</span><span>计划新增 " + counts.added + " 个</span>" +
    (counts.ambiguous ? "<span class=\"warning\">IPv4 有歧义 " + counts.ambiguous + " 个，执行时会跳过</span>" : "");
  preview.hidden = false;
}

function renderResults(results) {
  const counts = { added: 0, updated: 0, skipped: 0 };
  for (const result of results) counts[result.action] = (counts[result.action] || 0) + 1;
  $("result-summary").textContent = "新增 " + counts.added + " · 更新 " + counts.updated + " · 跳过 " + counts.skipped;
  $("result-rows").innerHTML = results.map(result => {
    const labels = { added: "已新增", updated: "已更新", skipped: "已跳过" };
    return "<tr><td><span class=\"badge " + escapeHTML(result.action) + "\">" + escapeHTML(labels[result.action] || result.action) +
      "</span></td><td>" + escapeHTML(result.name || result.target_uuid || result.uuid) +
      "<small>UUID " + escapeHTML(result.target_uuid || result.uuid) + "</small></td><td>" + escapeHTML(result.ipv4 || "—") +
      "</td><td>" + escapeHTML(result.reason || (result.action === "updated" ? "保留现有 UUID / RPC Token" : result.action === "added" ? "已保留备份 UUID / RPC Token" : "—")) + "</td></tr>";
  }).join("");
  $("results-panel").hidden = false;
}

async function restoreBackup() {
  if (!selectedClients || !previewCounts) return;
  const summary = "将检查 " + previewCounts.total + " 个节点：预计更新 " + previewCounts.matched + " 个，新增 " + previewCounts.added + " 个。\n\n" +
    "新增节点会写入备份中的 RPC Token。请确认这是可信的备份文件。继续恢复？";
  if (!window.confirm(summary)) return;
  const button = $("restore");
  button.disabled = true;
  notice("正在恢复节点…", "info");
  try {
    const response = await rpc("admin:restoreClients", { clients: selectedClients });
    if (!response || !Array.isArray(response.results)) throw new Error("服务端恢复结果格式异常");
    renderResults(response.results);
    notice("恢复完成。请查看下方逐节点结果；跳过的节点需要处理冲突后再恢复。", "success");
  } catch (error) {
    notice(error.message || "恢复失败", "error");
  } finally {
    button.disabled = false;
  }
}

$("export").addEventListener("click", exportBackup);
$("backup-file").addEventListener("change", event => inspectFile(event.target.files && event.target.files[0]));
$("restore").addEventListener("click", restoreBackup);
