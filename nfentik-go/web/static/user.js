(function () {
  "use strict";

  var app = document.getElementById("app");
  var state = { token: "", user: null, status: "", plans: [], planMap: {}, logPage: 1, logPageSize: 20, logTotal: 0 };

  function qs(id) { return document.getElementById(id); }

  function esc(text) {
    return String(text == null ? "" : text)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
  }

  function toast(message) { if (message) window.alert(message); }

  function headers() {
    var h = { "Content-Type": "application/json" };
    if (state.token) h["Authorization"] = "Bearer " + state.token;
    return h;
  }

  function api(path, options) {
    options = options || {};
    options.headers = Object.assign(headers(), options.headers || {});
    return fetch("/api/user" + path, options).then(function (res) {
      if (res.status === 401) { showLogin(); throw new Error("未授权"); }
      return res.json().catch(function () { return { success: false, message: "响应解析失败" }; });
    });
  }

  // ------------------------------------------------------------------ login

  function showLogin() {
    qs("user-login").classList.remove("hidden");
    qs("user-main").classList.add("hidden");
  }

  function showMain() {
    qs("user-login").classList.add("hidden");
    qs("user-main").classList.remove("hidden");
  }

  qs("user-login-btn").addEventListener("click", function () {
    var token = qs("user-token").value.trim();
    if (!token) { qs("user-login-error").textContent = "请输入用户令牌"; return; }
    fetch("/api/user/login", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ token: token }),
    }).then(function (res) { return res.json(); }).then(function (data) {
      if (!data.success) { qs("user-login-error").textContent = data.message || "登录失败"; return; }
      state.token = token;
      try { sessionStorage.setItem("nfentik_usertoken", token); } catch (e) {}
      showMain();
      loadMe();
      loadLogs();
    }).catch(function () { qs("user-login-error").textContent = "网络错误，请重试"; });
  });

  qs("user-token").addEventListener("keydown", function (e) {
    if (e.key === "Enter") qs("user-login-btn").click();
  });

  qs("user-token-toggle").addEventListener("click", function () {
    var input = qs("user-token");
    var show = input.type === "раs​s​wоr​d";
    input.type = show ? "text" : "раs​s​wоr​d";
    this.textContent = show ? "隐藏" : "显示";
  });

  qs("user-logout").addEventListener("click", function () {
    state.token = "";
    try { sessionStorage.removeItem("nfentik_usertoken"); } catch (e) {}
    showLogin();
  });

  // ------------------------------------------------------------------ profile

  var statusLabels = {
    active: "有效", expiring: "即将到期", expired: "已过期",
    exhausted: "次数用尽", disabled: "已停用",
  };

  function loadMe() {
    api("/me").then(function (data) {
      if (!data.success) { toast(data.message); return; }
      state.user = data.user;
      state.status = data.status;
      state.plans = data.plans || [];
      state.planMap = {};
      state.plans.forEach(function (p) { state.planMap[p.Code] = p; });
      renderProfile();
      renderOCS();
    });
  }

  function planLabel(code) {
    var p = state.planMap[code];
    return p ? p.Label : (code || "-");
  }

  function renderProfile() {
    var u = state.user || {};
    qs("user-name").textContent = u.token || "-";
    qs("user-plan-label").textContent = planLabel(u.plan_code);
    qs("user-start").textContent = u.start_at || "-";
    qs("user-expire").textContent = u.expire_at || "不限";
    qs("user-note").textContent = u.note ? u.note : "（暂无备注）";

    var pill = qs("user-status-pill");
    pill.className = "pill " + (u.enabled ? state.status : "disabled");
    pill.textContent = statusLabels[u.enabled ? state.status : "disabled"] || state.status;

    var quota = qs("user-quota");
    if (u.remain_count != null) {
      quota.classList.remove("hidden");
      var total = u.total_count || 0;
      var remain = u.remain_count || 0;
      qs("user-quota-text").textContent = remain + " / " + total;
      var pct = total > 0 ? Math.max(0, Math.min(100, Math.round((remain / total) * 100))) : 0;
      qs("user-quota-fill").style.width = pct + "%";
    } else {
      quota.classList.add("hidden");
    }

    renderAlert();
  }

  function renderAlert() {
    var el = qs("user-alert");
    var u = state.user || {};
    el.className = "user-alert hidden";
    el.textContent = "";
    if (!u.enabled) {
      el.className = "user-alert danger";
      el.textContent = "账号已停用，请联系管理员。";
      return;
    }
    if (state.status === "expired") {
      el.className = "user-alert danger";
      el.textContent = "套餐已过期，请及时续费。";
      return;
    }
    if (state.status === "exhausted") {
      el.className = "user-alert danger";
      el.textContent = "调用次数已用尽，请及时续费。";
      return;
    }
    if (state.status === "expiring" && u.expire_at) {
      var days = remainDays(u.expire_at);
      el.className = "user-alert warn";
      el.textContent = days >= 0
        ? "套餐将于 " + days + " 天后到期，请及时续费。"
        : "套餐即将到期，请及时续费。";
    }
  }

  function remainDays(expireAt) {
    var t = parseDate(expireAt);
    if (!t) return -1;
    return Math.ceil((t.getTime() - Date.now()) / 86400000);
  }

  function parseDate(value) {
    if (!value) return null;
    var v = String(value).replace(" ", "T");
    var d = new Date(v);
    return isNaN(d.getTime()) ? null : d;
  }

  // ------------------------------------------------------------------ note

  qs("user-note-edit").addEventListener("click", function () {
    qs("user-note-input").value = (state.user && state.user.note) || "";
    qs("user-note-edit-wrap").classList.remove("hidden");
    qs("user-note").classList.add("hidden");
    this.classList.add("hidden");
  });

  qs("user-note-cancel").addEventListener("click", resetNoteView);

  function resetNoteView() {
    qs("user-note-edit-wrap").classList.add("hidden");
    qs("user-note").classList.remove("hidden");
    qs("user-note-edit").classList.remove("hidden");
  }

  qs("user-note-save").addEventListener("click", function () {
    api("/profile", { method: "PUT", body: JSON.stringify({ note: qs("user-note-input").value }) }).then(function (res) {
      if (!res.success) { toast(res.message); return; }
      state.user = res.user;
      resetNoteView();
      renderProfile();
    });
  });

  // ------------------------------------------------------------------ ocs

  function ocsConfigObject() {
    var base = window.location.origin.replace(/\/+$/, "");
    return [{
      name: "NFENAI题库",
      homepage: base,
      url: base + "/query",
      method: "get",
      type: "GM_xmlhttpRequest",
      contentType: "json",
      data: { title: "${title}", options: "${options}", type: "${type}", token: state.token },
      handler: "return (res)=>res.code === 0 ? [res.message, undefined] : [res.data.question,res.data.answer,{ai: res.data.is_ai}]",
    }];
  }

  function renderOCS() {
    qs("user-ocs-json").value = JSON.stringify(ocsConfigObject(), null, 2);
  }

  function copyText(text, okMessage) {
    function done() { toast(okMessage || "已复制"); }
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done).catch(function () { fallbackCopy(text, done); });
      return;
    }
    fallbackCopy(text, done);
  }

  function fallbackCopy(text, done) {
    var ta = document.createElement("textarea");
    ta.value = text; ta.style.position = "fixed"; ta.style.opacity = "0";
    document.body.appendChild(ta); ta.select();
    try { document.execCommand("copy"); } catch (e) {}
    document.body.removeChild(ta); done();
  }

  qs("user-copy-ocs").addEventListener("click", function () {
    copyText(qs("user-ocs-json").value, "已复制 OCS 配置");
  });

  // ------------------------------------------------------------------ logs

  function loadLogs() {
    api("/logs?page=" + state.logPage + "&page_size=" + state.logPageSize).then(function (data) {
      if (!data.success) { toast(data.message); return; }
      state.logTotal = data.total || 0;
      renderLogs(data.items || []);
      qs("user-logs-pageinfo").textContent = "第 " + state.logPage + " 页 / 共 " +
        Math.max(1, Math.ceil(state.logTotal / state.logPageSize)) + " 页";
    });
  }

  var sourceLabels = { bank: "题库", cache: "缓存", ai: "AI" };
  var statusLabelsLog = { ok: "成功", denied: "拒绝", error: "失败" };

  function renderLogs(items) {
    var body = qs("user-logs").querySelector("tbody");
    if (!items.length) {
      body.innerHTML = '<tr><td colspan="5" class="muted">暂无调用记录</td></tr>';
      return;
    }
    body.innerHTML = items.map(function (l) {
      return "<tr>" +
        "<td class=\"small\">" + esc(l.timestamp) + "</td>" +
        '<td class="clamp">' + esc(l.question || "") + "</td>" +
        "<td>" + esc(sourceLabels[l.source] || l.source || "-") + "</td>" +
        "<td>" + esc(statusLabelsLog[l.status] || l.status || "-") + "</td>" +
        "<td>" + (l.response_time != null ? l.response_time + " ms" : "-") + "</td>" +
      "</tr>";
    }).join("");
  }

  qs("user-logs-refresh").addEventListener("click", function () { state.logPage = 1; loadLogs(); });
  qs("user-logs-prev").addEventListener("click", function () {
    if (state.logPage <= 1) return;
    state.logPage--; loadLogs();
  });
  qs("user-logs-next").addEventListener("click", function () {
    if (state.logPage * state.logPageSize >= state.logTotal) return;
    state.logPage++; loadLogs();
  });

  // ------------------------------------------------------------------ reset token

  qs("user-reset-token").addEventListener("click", function () {
    if (!window.confirm("重置后旧令牌立即失效，需要重新登录并更新 OCS 配置。确定继续？")) return;
    api("/reset-token", { method: "POST" }).then(function (res) {
      if (!res.success) { toast(res.message); return; }
      state.token = res.token;
      try { sessionStorage.setItem("nfentik_usertoken", state.token); } catch (e) {}
      renderOCS();
      toast("令牌已重置，OCS 配置已更新，请重新复制");
    });
  });

  // ------------------------------------------------------------------ bootstrap

  function boot() {
    var token = "";
    try { token = sessionStorage.getItem("nfentik_usertoken") || ""; } catch (e) {}
    if (!token) { showLogin(); return; }
    state.token = token;
    showMain();
    loadMe();
    loadLogs();
  }

  boot();
})();
