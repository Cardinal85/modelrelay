/* ModelRelay WebUI 前端（原生 JS，无构建步骤） */
"use strict";

const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => Array.from(document.querySelectorAll(sel));

const state = { me: null, page: "overview", nodes: [], timer: null };
let turnstileSiteKey = "";
let turnstileWidgetId = null;

async function api(path, opts = {}) {
  const res = await fetch("/api" + path, {
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    ...opts,
  });
  if (res.status === 401) {
    showLogin();
    throw new Error("unauthorized");
  }
  if (!res.ok) {
    let msg = "HTTP " + res.status;
    try { msg = (await res.json()).error || msg; } catch (e) { /* ignore */ }
    throw new Error(msg);
  }
  return res.json();
}

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}

function badge(s) {
  const cls = ["online", "offline", "suspect", "degraded", "draining", "revoked"].includes(s) ? s : "";
  return `<span class="badge ${esc(cls)}">${esc(s)}</span>`;
}

function fmtTime(t) {
  if (!t) return "-";
  const d = new Date(t);
  return isNaN(d) ? esc(t) : d.toLocaleString("zh-CN", { hour12: false });
}

function fmtMs(ms) { return ms ? ms + " ms" : "-"; }

/* ---------- 登录 ---------- */
function showLogin() {
  $("#main-view").classList.add("hidden");
  $("#login-view").classList.remove("hidden");
}
function showMain() {
  $("#login-view").classList.add("hidden");
  $("#main-view").classList.remove("hidden");
}

function loadTurnstileScript() {
  return new Promise((resolve, reject) => {
    if (window.turnstile) {
      resolve();
      return;
    }
    const s = document.createElement("script");
    s.src = "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";
    s.async = true;
    s.onload = () => resolve();
    s.onerror = () => reject(new Error("failed to load Turnstile"));
    document.head.appendChild(s);
  });
}

async function setupTurnstile() {
  try {
    const res = await fetch("/api/login-config", { cache: "no-store", credentials: "same-origin" });
    if (!res.ok) return;
    const cfg = await res.json();
    turnstileSiteKey = cfg.turnstile_site_key || "";
    if (!turnstileSiteKey) return;
    $("#turnstile-wrap").classList.remove("hidden");
    await loadTurnstileScript();
    if (window.turnstile && turnstileWidgetId == null) {
      turnstileWidgetId = window.turnstile.render("#turnstile-wrap", { sitekey: turnstileSiteKey });
    }
  } catch (e) {
    const el = $("#login-error");
    el.textContent = "人机验证加载失败，请刷新页面后重试";
    el.classList.remove("hidden");
  }
}

function turnstileToken() {
  if (!turnstileSiteKey || !window.turnstile) return "";
  try { return window.turnstile.getResponse(turnstileWidgetId) || ""; } catch (e) { return ""; }
}

function resetTurnstile() {
  if (!turnstileSiteKey || !window.turnstile || turnstileWidgetId == null) return;
  try { window.turnstile.reset(turnstileWidgetId); } catch (e) { /* ignore */ }
}

async function doLogin(e) {
  e.preventDefault();
  $("#login-error").classList.add("hidden");
  if (turnstileSiteKey && !turnstileToken()) {
    $("#login-error").textContent = "请先完成人机验证";
    $("#login-error").classList.remove("hidden");
    return;
  }
  try {
    const r = await api("/login", {
      method: "POST",
      body: JSON.stringify({
        username: $("#login-user").value,
        password: $("#login-pass").value,
        turnstile_token: turnstileToken(),
      }),
    });
    state.me = r.user;
    showMain();
    route();
  } catch (err) {
    resetTurnstile();
    $("#login-error").textContent = err.message;
    $("#login-error").classList.remove("hidden");
  }
}

async function logout() {
  try { await api("/logout", { method: "POST" }); } catch (e) { /* ignore */ }
  showLogin();
}

/* ---------- 路由 ---------- */
function route() {
  const hash = location.hash || "#/overview";
  const page = hash.replace("#/", "").split("/")[0] || "overview";
  if (!["overview", "nodes", "capabilities", "certs", "requests", "settings"].includes(page)) {
    location.hash = "#/overview";
    return;
  }
  state.page = page;
  $$(".sidebar nav a").forEach((a) =>
    a.classList.toggle("active", a.dataset.page === page));
  $$(".page").forEach((p) => p.classList.add("hidden"));
  $("#page-" + page).classList.remove("hidden");
  loadPage(page);
}

async function loadPage(page) {
  try {
    if (page === "overview") return renderOverview(await api("/overview"));
    if (page === "nodes") return renderNodes(await api("/nodes"));
    if (page === "capabilities") return renderCapabilities(await api("/capabilities"));
    if (page === "certs") return renderCerts(await api("/certs"));
    if (page === "requests") return renderRequests(await api("/requests"));
    if (page === "settings") return renderSettings(await api("/settings"));
  } catch (e) { if (e.message !== "unauthorized") showToast(e.message, true); }
}

function refresh() { if (state.me) loadPage(state.page); }
function startAuto() {
  if (state.timer) clearInterval(state.timer);
  state.timer = setInterval(refresh, 5000);
}

/* ---------- 总览 ---------- */
function renderOverview(d) {
  const s = d.stats || {};
  $("#page-overview").innerHTML = `
    <div class="page-head"><h2>总览</h2><span class="muted">Relay ${esc(d.relay_id)} · ${esc(d.version)} · 协议 v${esc(d.protocol_version)}</span></div>
    <div class="stat-grid">
      <div class="stat-card ok"><div class="num">${esc(d.nodes.online)}</div><div class="label">在线节点</div></div>
      <div class="stat-card err"><div class="num">${esc(d.nodes.offline)}</div><div class="label">离线节点</div></div>
      <div class="stat-card warn"><div class="num">${esc(d.nodes.degraded + d.nodes.suspect)}</div><div class="label">降级/疑似节点</div></div>
      <div class="stat-card"><div class="num">${esc(d.active_requests)}</div><div class="label">活动请求</div></div>
      <div class="stat-card"><div class="num">${esc(d.queued)}</div><div class="label">排队请求</div></div>
      <div class="stat-card warn"><div class="num">${esc(d.certs_expiring)}</div><div class="label">临期证书(30d)</div></div>
      <div class="stat-card ok"><div class="num">${esc(s.requests_success)}</div><div class="label">成功请求</div></div>
      <div class="stat-card err"><div class="num">${esc(s.requests_failed)}</div><div class="label">失败请求</div></div>
    </div>
    <div class="card">
      <h3 style="margin-bottom:10px">模型分布</h3>
      <table><thead><tr><th>模型</th><th>节点</th></tr></thead><tbody>
      ${(d.models || []).map((m) => `<tr><td>${esc(m.id)}</td><td>${(m.nodes || []).map(esc).join(", ") || "-"}</td></tr>`).join("") || '<tr><td colspan="2" class="muted">暂无模型</td></tr>'}
      </tbody></table>
    </div>
    <div class="card" style="margin-top:12px">
      <h3 style="margin-bottom:10px">最近错误</h3>
      ${(d.recent_errors || []).map((e) => `<div class="muted" style="margin:3px 0">[${fmtTime(e.time)}] ${esc(e.error_code)} · ${esc(e.model)} · ${esc(e.node)}</div>`).join("") || '<span class="muted">无</span>'}
    </div>`;
}

/* ---------- 节点 ---------- */
function renderNodes(d) {
  const nodes = d.nodes || [];
  $("#page-nodes").innerHTML = `
    <div class="page-head"><h2>节点</h2><button class="btn small" onclick="refresh()">刷新</button></div>
    <div class="card"><table><thead><tr>
      <th>名称</th><th>平台</th><th>状态</th><th>模型</th><th>并发</th><th>心跳</th><th>证书</th><th>操作</th>
    </tr></thead><tbody>
    ${nodes.map((n) => `<tr>
      <td>${esc(n.id)}</td>
      <td class="muted">${esc(n.os)}/${esc(n.arch)}</td>
      <td>${badge(n.state)}</td>
      <td>${esc(n.model_count)}</td>
      <td>${esc(n.active)}/${esc(n.max_concurrent)}</td>
      <td class="muted">${fmtTime(n.last_heartbeat)}</td>
      <td class="muted">${esc((n.cert_hash || "").slice(0, 12))}</td>
      <td>
        <button class="btn small" onclick="nodeDetail('${esc(n.id)}')">详情</button>
        <button class="btn small" onclick="nodeProbe('${esc(n.id)}')">探测</button>
        <button class="btn small" onclick="nodeDrain('${esc(n.id)}')">Drain</button>
        <button class="btn small" onclick="nodeKick('${esc(n.id)}')">踢出</button>
        <button class="btn small" onclick="nodeConcurrency('${esc(n.id)}', ${esc(n.max_concurrent)})">并发</button>
      </td>
    </tr>`).join("") || '<tr><td colspan="8" class="muted">暂无在线节点</td></tr>'}
    </tbody></table></div>`;
}

async function nodeDetail(id) {
  try {
    const d = await api("/nodes/" + encodeURIComponent(id));
    const n = d.node;
    $("#drawer").innerHTML = `
      <h3>节点 ${esc(n.id)}</h3>
      <table><tbody>
        <tr><td>状态</td><td>${badge(n.state)}</td></tr>
        <tr><td>平台</td><td>${esc(n.os)} / ${esc(n.arch)}</td></tr>
        <tr><td>Agent 版本</td><td>${esc(n.agent_version)}</td></tr>
        <tr><td>并发</td><td>${esc(n.active)} / ${esc(n.max_concurrent)}</td></tr>
        <tr><td>连接时间</td><td>${fmtTime(n.connected_at)}</td></tr>
        <tr><td>最后心跳</td><td>${fmtTime(n.last_heartbeat)}</td></tr>
        <tr><td>证书指纹</td><td class="muted">${esc(n.cert_hash)}</td></tr>
        <tr><td>最近错误</td><td class="muted">${esc(n.last_error) || "-"}</td></tr>
      </tbody></table>
      <h3 style="margin:14px 0 8px">模型与能力</h3>
      ${(d.models || []).map((m) => `<div style="margin:8px 0"><b>${esc(m.id)}</b><br>${
        (m.capabilities || []).map((c) => `<span class="chip">${esc(c)}</span>`).join("") || '<span class="muted">未探测</span>'
      }</div>`).join("") || '<span class="muted">无模型</span>'}
      <div style="margin-top:16px"><button class="btn" onclick="closeDrawer()">关闭</button></div>`;
    $("#drawer").classList.remove("hidden");
  } catch (e) { showToast(e.message, true); }
}
function closeDrawer() { $("#drawer").classList.add("hidden"); }

async function nodeProbe(id) {
  try {
    await api("/nodes/" + encodeURIComponent(id) + "/probe", { method: "POST" });
    showToast("已触发探测");
  } catch (e) { showToast(e.message, true); }
}

function nodeDrain(id) {
  confirmModal("Drain 节点 " + id, "节点将停止接收新请求，等待已有请求完成。", async () => {
    await api("/nodes/" + encodeURIComponent(id) + "/drain", { method: "POST", body: JSON.stringify({ grace_seconds: 300 }) });
    showToast("Drain 已下发");
  });
}

function nodeKick(id) {
  confirmModal("踢出节点 " + id, "将强制断开该节点连接并移除注册。", async () => {
    await api("/nodes/" + encodeURIComponent(id) + "/kick", { method: "POST" });
    showToast("已踢出");
  });
}

function nodeConcurrency(id, cur) {
  const body = `<label>最大并发</label><input id="cc-input" type="number" min="1" value="${esc(cur)}">`;
  confirmModal("修改节点并发 " + id, body, async () => {
    const v = parseInt($("#cc-input").value, 10);
    await api("/nodes/" + encodeURIComponent(id) + "/concurrency", { method: "POST", body: JSON.stringify({ max_concurrency: v }) });
    showToast("并发已更新");
  });
}

/* ---------- 接口能力 ---------- */
function renderCapabilities(d) {
  const nodes = d.nodes || [];
  $("#page-capabilities").innerHTML = `
    <div class="page-head"><h2>接口能力</h2></div>
    ${nodes.map((n) => `
      <div class="card" style="margin-bottom:12px">
        <h3 style="margin-bottom:8px">${esc(n.id)} <span class="muted">(${esc(n.model_count)} 模型)</span></h3>
        ${(n.models || []).map((m) => `
          <div style="margin:6px 0"><b>${esc(m.id)}</b> — ${
            (m.capabilities || []).map((c) => `<span class="chip">${esc(c)}</span>`).join("") || '<span class="muted">未探测</span>'
          }</div>`).join("") || '<span class="muted">无模型</span>'}
      </div>`).join("") || '<div class="card muted">暂无节点</div>'}`;
}

/* ---------- 证书 ---------- */
function renderCerts(d) {
  const certs = d.certs || [];
  $("#page-certs").innerHTML = `
    <div class="page-head"><h2>证书</h2><span class="muted">临期(30d): ${esc(d.expiring)}</span></div>
    <div class="card"><table><thead><tr>
      <th>节点</th><th>序列号</th><th>主题</th><th>签发时间</th><th>到期时间</th><th>状态</th><th>操作</th>
    </tr></thead><tbody>
    ${certs.map((c) => `<tr>
      <td>${esc(c.node_id)}</td>
      <td class="muted">${esc(c.serial.slice(0, 16))}</td>
      <td class="muted">${esc(c.subject)}</td>
      <td>${fmtTime(c.not_before)}</td>
      <td>${fmtTime(c.not_after)}</td>
      <td>${badge(c.status)}</td>
      <td>${c.status === "active" ? `<button class="btn small danger" onclick="revokeCert('${esc(c.serial)}')">吊销</button>` : "-"}</td>
    </tr>`).join("") || '<tr><td colspan="7" class="muted">暂无证书记录</td></tr>'}
    </tbody></table></div>`;
}

function revokeCert(serial) {
  confirmModal("吊销证书", "吊销后该节点将无法建立新连接。", async () => {
    await api("/certs/revoke", { method: "POST", body: JSON.stringify({ serial }) });
    showToast("已吊销");
  });
}

/* ---------- 请求追踪 ---------- */
function renderRequests(d) {
  const reqs = d.requests || [];
  $("#page-requests").innerHTML = `
    <div class="page-head"><h2>请求追踪</h2><span class="muted">仅摘要，不含正文</span></div>
    <div class="card"><table><thead><tr>
      <th>request_id</th><th>时间</th><th>路径</th><th>模型</th><th>节点</th><th>状态</th><th>TTFT</th><th>总耗时</th><th>错误码</th>
    </tr></thead><tbody>
    ${reqs.map((r) => `<tr>
      <td class="muted">${esc(r.request_id.slice(0, 8))}…</td>
      <td>${fmtTime(r.time)}</td>
      <td>${esc(r.path)}</td>
      <td>${esc(r.model) || "-"}</td>
      <td>${esc(r.node) || "-"}</td>
      <td>${badge(r.status >= 200 && r.status < 400 ? "online" : "offline")} ${esc(r.status)}</td>
      <td>${fmtMs(r.ttft_ms)}</td>
      <td>${fmtMs(r.duration_ms)}</td>
      <td class="muted">${esc(r.error_code) || "-"}</td>
    </tr>`).join("") || '<tr><td colspan="9" class="muted">暂无请求记录</td></tr>'}
    </tbody></table></div>`;
}

/* ---------- 系统设置 ---------- */
function renderSettings(d) {
  const r = d.relay || {};
  const users = d.users || [];
  $("#page-settings").innerHTML = `
    <div class="page-head"><h2>系统设置</h2></div>
    <div class="card">
      <h3 style="margin-bottom:8px">Relay 信息</h3>
      <table><tbody>
        <tr><td>relay_id</td><td>${esc(r.relay_id)}</td></tr>
        <tr><td>HTTP 监听</td><td>${esc(r.http_listen)}</td></tr>
        <tr><td>WSS 监听</td><td>${esc(r.wss_listen)}</td></tr>
        <tr><td>版本</td><td>${esc(r.version)}</td></tr>
        <tr><td>协议版本</td><td>${esc(r.protocol_version)}</td></tr>
      </tbody></table>
    </div>
    <div class="card" style="margin-top:12px">
      <h3 style="margin-bottom:8px">数据保留</h3>
      <label><input type="checkbox" id="keep-prompt" ${r.retention && r.retention.keep_prompt_response ? "checked" : ""}> 保存完整 Prompt/Response（默认关闭）</label>
      <div style="margin-top:8px"><label>保留天数</label><input id="retention-days" type="number" min="1" value="${esc(r.retention ? r.retention.retention_days : 30)}"></div>
      <div style="margin-top:12px"><button class="btn primary" onclick="saveRetention()">保存</button></div>
      <div id="settings-result" class="error"></div>
    </div>
    <details>
      <summary>管理员账号</summary>
      <table><thead><tr><th>用户名</th><th>角色</th></tr></thead><tbody>
        ${users.map((u) => `<tr><td>${esc(u.username)}</td><td>${esc(u.role)}</td></tr>`).join("")}
      </tbody></table>
    </details>`;
}

async function saveRetention() {
  try {
    const r = await api("/settings/retention", {
      method: "POST",
      body: JSON.stringify({
        keep_prompt_response: $("#keep-prompt").checked,
        retention_days: parseInt($("#retention-days").value, 10) || 30,
      }),
    });
    $("#settings-result").textContent = "已保存" + (r.restart_required ? "（部分配置需重启生效）" : "");
  } catch (e) { $("#settings-result").textContent = e.message; }
}

/* ---------- 弹窗/提示 ---------- */
let modalOkFn = null;
function confirmModal(title, bodyHtml, okFn) {
  $("#modal-title").textContent = title;
  $("#modal-body").innerHTML = typeof bodyHtml === "string" ? bodyHtml : bodyHtml();
  modalOkFn = okFn;
  $("#modal").classList.remove("hidden");
}
$("#modal-cancel").addEventListener("click", () => $("#modal").classList.add("hidden"));
$("#modal-ok").addEventListener("click", async () => {
  $("#modal").classList.add("hidden");
  try { if (modalOkFn) await modalOkFn(); } catch (e) { showToast(e.message, true); }
});

function showToast(msg, isErr) {
  let t = $("#toast");
  if (!t) {
    t = document.createElement("div");
    t.id = "toast";
    t.style.cssText = "position:fixed;top:16px;right:16px;z-index:99;padding:10px 16px;border-radius:8px;background:#1f2329;color:#fff;font-size:13px;";
    document.body.appendChild(t);
  }
  t.textContent = msg;
  t.style.background = isErr ? "#dc2626" : "#1f2329";
  clearTimeout(t._timer);
  t._timer = setTimeout(() => t.remove(), 3000);
}

/* ---------- 启动 ---------- */
async function boot() {
  $("#login-form").addEventListener("submit", doLogin);
  $("#logout-link").addEventListener("click", (e) => { e.preventDefault(); logout(); });
  window.addEventListener("hashchange", () => { if (state.me) route(); });
  try {
    state.me = await api("/me");
    showMain();
    route();
    startAuto();
  } catch (e) {
    showLogin();
    await setupTurnstile();
  }
}
boot();
