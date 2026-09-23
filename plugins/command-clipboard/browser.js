(() => {
  "use strict";

  const ROOT_ID = "komari-command-clipboard-root";
  const COMPACT_MODE_KEY = "komari-command-clipboard.compact-mode";
  function getInitialCompactMode() {
    try {
      return window.localStorage.getItem(COMPACT_MODE_KEY) !== "false";
    } catch {
      return true;
    }
  }

  const state = {
    commands: [],
    sessions: [],
    busy: false,
    compactMode: getInitialCompactMode(),
    expansionTimers: new Set(),
    statusTimer: 0,
    refreshTimer: 0,
    terminalBody: null,
    sessionObserver: null,
  };

  const isZh = () => /^(zh|zh[_-])/i.test(document.documentElement.lang || navigator.language || "");
  const strings = () => isZh()
    ? {
        title: "命令剪贴板", add: "新建片段", execute: "执行", edit: "编辑", remove: "删除",
        name: "名称", content: "命令内容", remark: "备注", weight: "排序权重", cancel: "取消",
        save: "保存", create: "创建", currentTerminal: "当前终端", noActive: "当前页面没有活动终端",
        compact: "更少显示", compactOn: "更少显示已开启；悬停名称两秒展开", compactOff: "更少显示已关闭",
        waiting: "正在连接当前终端…", loading: "正在加载…", empty: "还没有命令片段",
        loadError: "读取失败", saveError: "保存失败", deleteConfirm: "确定删除这个命令片段吗？",
        sent: "已发送到当前终端", sendError: "发送失败", sessionLost: "当前终端会话尚未连接",
        omitted: "其余内容已省略", close: "关闭", open: "命令片段", networkError: "请求失败",
      }
    : {
        title: "Command Clipboard", add: "New snippet", execute: "Run", edit: "Edit", remove: "Delete",
        name: "Name", content: "Command", remark: "Note", weight: "Sort weight", cancel: "Cancel",
        save: "Save", create: "Create", currentTerminal: "Current terminal", noActive: "No active terminal on this page",
        compact: "Compact view", compactOn: "Compact view on; hover over a name for two seconds to expand", compactOff: "Compact view off",
        waiting: "Connecting to the current terminal…", loading: "Loading…", empty: "No command snippets yet",
        loadError: "Could not load snippets", saveError: "Could not save snippet", deleteConfirm: "Delete this command snippet?",
        sent: "Sent to the current terminal", sendError: "Could not send command", sessionLost: "Current terminal session is not connected",
        omitted: "more characters omitted", close: "Close", open: "Command snippets", networkError: "Request failed",
      };

  const onTerminalPage = () => window.location.pathname.replace(/\/$/, "") === "/terminal";

  function sessionNodeForRequest(requestID) {
    if (!requestID) return null;
    return Array.from(document.querySelectorAll(".km-terminal-session"))
      .find((node) => node.dataset.kccRequestId === requestID) || null;
  }

  function activeSessionNode() {
    return document.querySelector(".km-terminal-session.is-active");
  }

  function nextUnboundSessionNode(uuid) {
    const nodes = Array.from(document.querySelectorAll(".km-terminal-session"));
    return nodes.find((node) => !node.dataset.kccRequestId && !node.dataset.kccSocketToken &&
      (!node.dataset.kccUuid || node.dataset.kccUuid === uuid)) || null;
  }

  function notifyCurrentSessionChange() {
    window.dispatchEvent(new CustomEvent("kcc:current-session-changed"));
  }

  function clearExpansionTimers() {
    for (const timer of state.expansionTimers) window.clearTimeout(timer);
    state.expansionTimers.clear();
  }

  // The terminal page renders one .km-terminal-session node per tab in tab
  // order; its WebSocket is opened by that component after the nodes mount.
  // Match initial sockets to those nodes in order, then use request_id for all
  // reconnects. The active tab is exposed by the page's is-active class.
  function installTerminalSessionTracker() {
    if (window.__komariCommandClipboardTrackerInstalled) return;
    window.__komariCommandClipboardTrackerInstalled = true;

    let nextSocketToken = 0;
    const NativeWebSocket = window.WebSocket;
    window.WebSocket = new Proxy(NativeWebSocket, {
      construct(target, args, newTarget) {
        const socket = Reflect.construct(target, args, newTarget);
        if (!onTerminalPage()) return socket;

        let url;
        try {
          url = new URL(String(args[0]), window.location.href);
        } catch {
          return socket;
        }
        const match = url.pathname.match(/\/api\/admin\/client\/([^/]+)\/terminal\/?$/);
        if (!match) return socket;

        let uuid;
        try {
          uuid = decodeURIComponent(match[1]);
        } catch {
          return socket;
        }
        const existingRequestID = url.searchParams.get("request_id") || "";
        const socketToken = `kcc-${++nextSocketToken}`;
        let terminalNode = sessionNodeForRequest(existingRequestID) || nextUnboundSessionNode(uuid);
        if (terminalNode) {
          terminalNode.dataset.kccUuid = uuid;
          terminalNode.dataset.kccSocketToken = socketToken;
          if (existingRequestID) terminalNode.dataset.kccRequestId = existingRequestID;
        }

        socket.addEventListener("message", (event) => {
          if (typeof event.data !== "string") return;
          let message;
          try {
            message = JSON.parse(event.data);
          } catch {
            return;
          }
          if (typeof message?.request_id !== "string" || !message.request_id) return;

          let targetNode = sessionNodeForRequest(message.request_id) || terminalNode;
          if (!targetNode?.isConnected && document.querySelectorAll(".km-terminal-session").length === 1) {
            targetNode = activeSessionNode();
          }
          if (!targetNode?.isConnected) return;

          targetNode.dataset.kccRequestId = message.request_id;
          targetNode.dataset.kccUuid = uuid;
          if (targetNode.dataset.kccSocketToken === socketToken) {
            delete targetNode.dataset.kccSocketToken;
          }
          terminalNode = targetNode;
          notifyCurrentSessionChange();
        });

        socket.addEventListener("close", () => {
          if (terminalNode?.dataset.kccSocketToken === socketToken && !terminalNode.dataset.kccRequestId) {
            delete terminalNode.dataset.kccSocketToken;
          }
          notifyCurrentSessionChange();
        });
        return socket;
      },
    });
  }

  function observeTerminalSessions(body) {
    if (body === state.terminalBody) return;
    state.sessionObserver?.disconnect();
    state.terminalBody = body;
    if (!body) return;

    state.sessionObserver ||= new MutationObserver((records) => {
      if (records.some((record) => record.type === "childList" || record.attributeName === "class" || record.attributeName === "data-kcc-request-id")) {
        notifyCurrentSessionChange();
      }
    });
    state.sessionObserver.observe(body, {
      attributes: true,
      childList: true,
      subtree: true,
      attributeFilter: ["class", "data-kcc-request-id"],
    });
  }

  async function requestJSON(url, options = {}) {
    const response = await fetch(url, {
      credentials: "same-origin",
      cache: "no-store",
      ...options,
    });
    const payload = await response.json().catch(() => null);
    if (!response.ok || payload?.status === "error") {
      throw new Error(payload?.message || `${strings().networkError} (${response.status})`);
    }
    return payload;
  }

  function mountUI() {
    const root = document.getElementById(ROOT_ID);
    if (!root || root.dataset.kccMounted === "true") return;
    root.dataset.kccMounted = "true";
    root.innerHTML = `
      <button class="kcc-toggle" type="button" aria-expanded="false">
        <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
          <rect x="5" y="4" width="14" height="17" rx="2"></rect>
          <path d="M9 4V3h6v1M9 11l-2 2 2 2M15 11l2 2-2 2M10 18h4"></path>
        </svg>
      </button>
      <aside class="kcc-panel" data-open="false" aria-label="Command Clipboard">
        <div class="kcc-header"><span data-kcc-title></span><button class="kcc-button" type="button" data-kcc-close></button></div>
        <div class="kcc-toolbar"><button class="kcc-button kcc-button-primary" type="button" data-kcc-add></button><button class="kcc-button" type="button" data-kcc-compact aria-pressed="true"></button><button class="kcc-button" type="button" data-kcc-refresh>↻</button></div>
        <div class="kcc-current-session" data-kcc-current-session aria-live="polite"></div>
        <div class="kcc-items" data-kcc-items></div>
        <div class="kcc-footer" data-kcc-status role="status" aria-live="polite"></div>
      </aside>
      <dialog class="kcc-dialog" data-kcc-dialog>
        <form class="kcc-form" data-kcc-form>
          <h2 data-kcc-form-title></h2>
          <label for="kcc-name" data-kcc-name-label></label><input id="kcc-name" name="name" maxlength="255" required>
          <label for="kcc-text" data-kcc-text-label></label><textarea id="kcc-text" name="text" required></textarea>
          <label for="kcc-remark" data-kcc-remark-label></label><textarea id="kcc-remark" name="remark" rows="2"></textarea>
          <label for="kcc-weight" data-kcc-weight-label></label><input class="kcc-weight" id="kcc-weight" name="weight" type="number" value="0">
          <div class="kcc-form-actions"><button class="kcc-button" type="button" data-kcc-cancel></button><button class="kcc-button kcc-button-primary" type="submit" data-kcc-save></button></div>
        </form>
      </dialog>`;

    const toggle = root.querySelector(".kcc-toggle");
    const panel = root.querySelector(".kcc-panel");
    const dialog = root.querySelector("[data-kcc-dialog]");
    const form = root.querySelector("[data-kcc-form]");
    const items = root.querySelector("[data-kcc-items]");
    const status = root.querySelector("[data-kcc-status]");
    const currentSessionLabel = root.querySelector("[data-kcc-current-session]");
    const compactButton = root.querySelector("[data-kcc-compact]");
    let editingID = null;

    function updateCompactMode() {
      const t = strings();
      panel.dataset.compact = state.compactMode ? "true" : "false";
      compactButton.textContent = state.compactMode ? `✓ ${t.compact}` : t.compact;
      compactButton.setAttribute("aria-pressed", state.compactMode ? "true" : "false");
      compactButton.title = state.compactMode ? t.compactOn : t.compactOff;
    }

    function setStatus(message, error = false) {
      status.textContent = message;
      status.style.color = error ? "#ffb4b1" : "";
      if (state.statusTimer) window.clearTimeout(state.statusTimer);
      if (message) {
        state.statusTimer = window.setTimeout(() => {
          status.textContent = "";
          status.style.color = "";
        }, 5000);
      }
    }

    function selectedSession() {
      const requestID = activeSessionNode()?.dataset.kccRequestId || "";
      return state.sessions.find((session) => session.request_id === requestID) || null;
    }

    function updateCurrentStatus() {
      const activeNode = activeSessionNode();
      const session = selectedSession();
      root.querySelectorAll("[data-kcc-execute]").forEach((button) => {
        button.disabled = !session || state.busy;
      });
      if (!activeNode) {
        currentSessionLabel.textContent = strings().noActive;
        setStatus(strings().noActive);
      } else if (!activeNode.dataset.kccRequestId) {
        currentSessionLabel.textContent = strings().waiting;
        setStatus(strings().waiting);
      } else if (!session) {
        currentSessionLabel.textContent = strings().sessionLost;
        setStatus(strings().sessionLost);
      } else {
        currentSessionLabel.textContent = `${strings().currentTerminal}: ${session.client_name || session.uuid}`;
        if ([strings().noActive, strings().waiting, strings().sessionLost].includes(status.textContent)) {
          setStatus("");
        }
      }
      if (!session && state.busy) {
        setStatus(strings().noActive);
      }
    }

    async function refreshSessions() {
      try {
        const payload = await requestJSON("/api/admin/terminal/sessions");
        state.sessions = Array.isArray(payload?.data) ? payload.data : [];
        updateCurrentStatus();
      } catch (error) {
        state.sessions = [];
        updateCurrentStatus();
        setStatus(error instanceof Error ? error.message : strings().loadError, true);
      }
    }

    async function refreshCommands() {
      items.textContent = strings().loading;
      try {
        const payload = await requestJSON("/api/admin/clipboard");
        state.commands = Array.isArray(payload?.data) ? payload.data : [];
        state.commands.sort((left, right) => Number(right.weight || 0) - Number(left.weight || 0));
        renderCommands();
      } catch (error) {
        items.textContent = "";
        const message = document.createElement("div");
        message.className = "kcc-empty";
        message.textContent = error instanceof Error ? error.message : strings().loadError;
        items.append(message);
      }
    }

    function renderCommands() {
      clearExpansionTimers();
      items.replaceChildren();
      if (!state.commands.length) {
        const empty = document.createElement("div");
        empty.className = "kcc-empty";
        empty.textContent = strings().empty;
        items.append(empty);
        return;
      }

      const t = strings();
      for (const command of state.commands) {
        const card = document.createElement("article");
        card.className = "kcc-card";
        const head = document.createElement("div");
        head.className = "kcc-card-head";
        const title = document.createElement("div");
        title.className = "kcc-card-title";
        title.textContent = command.name || "";
        let expansionTimer = 0;
        const cancelExpansion = () => {
          if (!expansionTimer) return;
          window.clearTimeout(expansionTimer);
          state.expansionTimers.delete(expansionTimer);
          expansionTimer = 0;
        };
        title.addEventListener("mouseenter", () => {
          if (!state.compactMode || card.dataset.kccExpanded === "true") return;
          cancelExpansion();
          expansionTimer = window.setTimeout(() => {
            state.expansionTimers.delete(expansionTimer);
            expansionTimer = 0;
            if (state.compactMode && title.matches(":hover")) {
              card.dataset.kccExpanded = "true";
            }
          }, 2000);
          state.expansionTimers.add(expansionTimer);
        });
        title.addEventListener("mouseleave", cancelExpansion);
        card.addEventListener("mouseleave", () => {
          cancelExpansion();
          if (state.compactMode) delete card.dataset.kccExpanded;
        });
        const run = document.createElement("button");
        run.className = "kcc-button kcc-button-primary";
        run.type = "button";
        run.textContent = t.execute;
        run.dataset.kccExecute = "true";
        run.addEventListener("click", () => executeCommand(command, run));
        head.append(title, run);

        const preview = document.createElement("pre");
        preview.className = "kcc-code";
        const text = String(command.text || "");
        preview.textContent = text.length > 300
          ? `${text.slice(0, 300)}\n… ${text.length - 300} ${t.omitted}`
          : text;

        const remark = document.createElement("div");
        remark.className = "kcc-remark";
        remark.textContent = command.remark || "";

        const actions = document.createElement("div");
        actions.className = "kcc-actions";
        const edit = document.createElement("button");
        edit.className = "kcc-button";
        edit.type = "button";
        edit.textContent = t.edit;
        edit.addEventListener("click", () => openEditor(command));
        const remove = document.createElement("button");
        remove.className = "kcc-button kcc-button-danger";
        remove.type = "button";
        remove.textContent = t.remove;
        remove.addEventListener("click", () => deleteCommand(command));
        actions.append(edit, remove);
        card.append(head, preview, remark, actions);
        items.append(card);
      }
      updateCurrentStatus();
    }

    async function executeCommand(command, button) {
      const session = selectedSession();
      if (!session) {
        setStatus(strings().noActive, true);
        return;
      }
      state.busy = true;
      button.disabled = true;
      const allRunButtons = root.querySelectorAll("[data-kcc-execute]");
      allRunButtons.forEach((item) => { item.disabled = true; });
      try {
        await requestJSON(`/api/admin/terminal/sessions/${encodeURIComponent(session.request_id)}/input`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ data: `${String(command.text || "")}\r` }),
        });
        setStatus(`${strings().sent}: ${session.client_name || session.uuid}`);
      } catch (error) {
        const message = error instanceof Error ? error.message : strings().sendError;
        setStatus(/not connected|disconnected/i.test(message) ? strings().sessionLost : message, true);
        await refreshSessions();
      } finally {
        state.busy = false;
        updateCurrentStatus();
      }
    }

    function openEditor(command = null) {
      editingID = command?.id ?? null;
      const t = strings();
      root.querySelector("[data-kcc-form-title]").textContent = editingID === null ? t.add : t.edit;
      root.querySelector("[data-kcc-name-label]").textContent = t.name;
      root.querySelector("[data-kcc-text-label]").textContent = t.content;
      root.querySelector("[data-kcc-remark-label]").textContent = t.remark;
      root.querySelector("[data-kcc-weight-label]").textContent = t.weight;
      root.querySelector("[data-kcc-cancel]").textContent = t.cancel;
      root.querySelector("[data-kcc-save]").textContent = editingID === null ? t.create : t.save;
      form.elements.name.value = command?.name || "";
      form.elements.text.value = command?.text || "";
      form.elements.remark.value = command?.remark || "";
      form.elements.weight.value = Number(command?.weight || 0);
      dialog.showModal();
      form.elements.name.focus();
    }

    async function saveCommand(event) {
      event.preventDefault();
      const submit = root.querySelector("[data-kcc-save]");
      submit.disabled = true;
      const body = {
        name: form.elements.name.value.trim(),
        text: form.elements.text.value,
        remark: form.elements.remark.value,
        weight: Number.parseInt(form.elements.weight.value || "0", 10) || 0,
      };
      try {
        const url = editingID === null
          ? "/api/admin/clipboard"
          : `/api/admin/clipboard/${encodeURIComponent(editingID)}`;
        await requestJSON(url, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        });
        dialog.close();
        setStatus(strings().save);
        await refreshCommands();
      } catch (error) {
        setStatus(error instanceof Error ? error.message : strings().saveError, true);
      } finally {
        submit.disabled = false;
      }
    }

    async function deleteCommand(command) {
      if (!window.confirm(strings().deleteConfirm)) return;
      try {
        await requestJSON(`/api/admin/clipboard/${encodeURIComponent(command.id)}/remove`, { method: "POST" });
        await refreshCommands();
      } catch (error) {
        setStatus(error instanceof Error ? error.message : strings().networkError, true);
      }
    }

    function setOpen(open) {
      panel.dataset.open = open ? "true" : "false";
      toggle.setAttribute("aria-expanded", open ? "true" : "false");
      toggle.setAttribute("aria-label", open ? strings().close : strings().open);
      toggle.title = open ? strings().close : strings().open;
      toggle.hidden = open;
      if (open) {
        void refreshCommands();
        void refreshSessions();
        if (!state.refreshTimer) {
          state.refreshTimer = window.setInterval(() => void refreshSessions(), 5000);
        }
      } else if (state.refreshTimer) {
        window.clearInterval(state.refreshTimer);
        state.refreshTimer = 0;
      }
    }

    function localize() {
      const t = strings();
      root.querySelector("[data-kcc-title]").textContent = t.title;
      root.querySelector("[data-kcc-add]").textContent = `+ ${t.add}`;
      root.querySelector("[data-kcc-close]").textContent = t.close;
      root.querySelector("[data-kcc-name-label]").textContent = t.name;
      root.querySelector("[data-kcc-text-label]").textContent = t.content;
      root.querySelector("[data-kcc-remark-label]").textContent = t.remark;
      root.querySelector("[data-kcc-weight-label]").textContent = t.weight;
      root.querySelector("[data-kcc-cancel]").textContent = t.cancel;
      root.querySelector("[data-kcc-save]").textContent = t.save;
      toggle.setAttribute("aria-label", t.open);
      toggle.title = t.open;
      root.querySelector("[data-kcc-refresh]").title = isZh() ? "刷新" : "Refresh";
      updateCompactMode();
    }

    toggle.addEventListener("click", () => setOpen(panel.dataset.open !== "true"));
    root.querySelector("[data-kcc-close]").addEventListener("click", () => setOpen(false));
    root.querySelector("[data-kcc-add]").addEventListener("click", () => openEditor());
    root.querySelector("[data-kcc-refresh]").addEventListener("click", () => {
      void refreshCommands();
      void refreshSessions();
    });
    compactButton.addEventListener("click", () => {
      state.compactMode = !state.compactMode;
      clearExpansionTimers();
      if (state.compactMode) {
        root.querySelectorAll(".kcc-card[data-kcc-expanded]").forEach((card) => {
          delete card.dataset.kccExpanded;
        });
      }
      try {
        window.localStorage.setItem(COMPACT_MODE_KEY, String(state.compactMode));
      } catch {
        // Keep the in-memory preference when storage is unavailable.
      }
      updateCompactMode();
    });
    root.querySelector("[data-kcc-cancel]").addEventListener("click", () => dialog.close());
    form.addEventListener("submit", saveCommand);
    window.addEventListener("kcc:current-session-changed", () => {
      if (panel.dataset.open === "true") void refreshSessions();
      else updateCurrentStatus();
    });
    dialog.addEventListener("click", (event) => {
      if (event.target === dialog) dialog.close();
    });
    localize();
    updateCurrentStatus();
  }

  function updatePageVisibility() {
    if (onTerminalPage()) mountUI();
    const root = document.getElementById(ROOT_ID);
    if (root) root.hidden = !onTerminalPage();
    observeTerminalSessions(onTerminalPage() ? document.querySelector(".km-terminal-body") : null);
  }

  installTerminalSessionTracker();
  const schedulePageUpdate = () => {
    window.requestAnimationFrame(() => window.requestAnimationFrame(updatePageVisibility));
  };
  for (const method of ["pushState", "replaceState"]) {
    const original = window.history[method];
    window.history[method] = function (...args) {
      const result = original.apply(this, args);
      schedulePageUpdate();
      return result;
    };
  }
  document.addEventListener("DOMContentLoaded", updatePageVisibility, { once: true });
  window.addEventListener("popstate", schedulePageUpdate);
  schedulePageUpdate();
})();
