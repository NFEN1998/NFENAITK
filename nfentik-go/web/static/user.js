(function () {
  "use strict";

  var page = document.body.getAttribute("data-page") || "overview";
  var meta = document.querySelector('meta[name="app-site-name"]');
  var siteName = (meta && meta.content) || "NfenTik";

  var state = {
    token: "",
    authMode: "token",
    account: null,
    user: null,
    status: "",
    plans: [],
    planMap: {},
    csrf: "",
    logs: [],
    logPage: 1,
    logPageSize: 20,
    logTotal: 0,
  };

  function qs(id) { return document.getElementById(id); }
  function on(id, event, fn) { var el = qs(id); if (el) el.addEventListener(event, fn); }
  function setText(id, value) {
    var el = qs(id);
    if (el) el.textContent = value == null ? "" : String(value);
  }
  function setValueOrText(id, value) {
    var el = qs(id);
    if (!el) return;
    if (el.tagName === "INPUT" || el.tagName === "TEXTAREA") el.value = value == null ? "" : String(value);
    else el.textContent = value == null ? "" : String(value);
  }
  function setFill(id, percent) {
    var el = qs(id);
    if (el) el.style.width = Math.max(0, Math.min(100, percent)) + "%";
  }
  function esc(text) {
    return String(text == null ? "" : text)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
  }

  // ---------------------------------------------------------------- toast/modal
  var toastTimer = null;
  function toast(message) {
    if (!message) return;
    var el = document.querySelector("[data-toast-host]");
    if (!el) {
      el = document.createElement("div");
      el.className = "toast";
      el.setAttribute("data-toast-host", "");
      document.body.appendChild(el);
    }
    el.textContent = message;
    el.classList.add("show");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { el.classList.remove("show"); }, 1800);
  }

  function confirmDialog(message, opts) {
    opts = opts || {};
    return new Promise(function (resolve) {
      var wrap = document.createElement("div");
      wrap.className = "modal-overlay open";
      wrap.innerHTML =
        '<div class="modal-card">' +
        "<h2>" + esc(opts.title || "确认操作") + "</h2>" +
        '<p class="muted small">' + esc(message) + "</p>" +
        '<div class="modal-actions">' +
        '<button type="button" class="btn ghost" data-act="cancel">' + esc(opts.cancelText || "取消") + "</button>" +
        '<button type="button" class="btn ' + (opts.danger ? "danger" : "primary") + '" data-act="ok">' + esc(opts.okText || "确定") + "</button>" +
        "</div></div>";
      document.body.appendChild(wrap);
      function done(value) { wrap.remove(); resolve(value); }
      wrap.addEventListener("click", function (e) {
        var btn = e.target.closest("button[data-act]");
        if (btn) { done(btn.getAttribute("data-act") === "ok"); return; }
        if (e.target === wrap) done(false);
      });
    });
  }

  // ---------------------------------------------------------------- network
  function readCookie(name) {
    var parts = document.cookie ? document.cookie.split(";") : [];
    for (var i = 0; i < parts.length; i++) {
      var pair = parts[i].trim().split("=");
      if (pair[0] === name) return decodeURIComponent(pair.slice(1).join("="));
    }
    return "";
  }

  function headers() {
    var h = { "Content-Type": "application/json" };
    if (state.authMode === "token" && state.token) h["Authorization"] = "Bearer " + state.token;
    if (state.authMode === "account" && state.csrf) h["X-CSRF-Token"] = state.csrf;
    return h;
  }

  function clearAuth() {
    state.token = "";
    state.user = null;
    state.status = "";
    state.account = null;
    state.csrf = "";
    state.authMode = "token";
    try { sessionStorage.removeItem("nfentik_usertoken"); } catch (e) {}
  }

  function api(path, options) {
    options = options || {};
    options.headers = Object.assign(headers(), options.headers || {});
    return fetch("/api/user" + path, options).then(function (res) {
      if (res.status === 401) {
        clearAuth();
        location.replace("/user/login");
        throw new Error("未授权");
      }
      return res.json().catch(function () { return { success: false, message: "响应解析失败" }; });
    });
  }

  function copyText(text, message) {
    function done() { toast(message || "已复制"); }
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done).catch(function () { fallbackCopy(text, done); });
      return;
    }
    fallbackCopy(text, done);
  }

  function fallbackCopy(text, done) {
    var ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand("copy"); } catch (e) {}
    document.body.removeChild(ta);
    done();
  }

  // ---------------------------------------------------------------- chrome
  var accents = ["", "blue", "amber"];
  document.querySelectorAll("[data-accent-toggle]").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var current = document.documentElement.getAttribute("data-accent") || "";
      var next = accents[(accents.indexOf(current) + 1) % accents.length];
      if (next) document.documentElement.setAttribute("data-accent", next);
      else document.documentElement.removeAttribute("data-accent");
      try { localStorage.setItem("nfentik-accent", next); } catch (e) {}
    });
  });

  var sidebar = qs("sidebar");
  var backdrop = qs("backdrop");
  var menuBtn = qs("menu-btn");
  function setMenu(open) {
    if (!sidebar) return;
    sidebar.classList.toggle("open", open);
    if (backdrop) backdrop.classList.toggle("show", open);
    if (menuBtn) menuBtn.setAttribute("aria-expanded", open ? "true" : "false");
  }
  if (menuBtn) menuBtn.addEventListener("click", function () { setMenu(!sidebar.classList.contains("open")); });
  if (backdrop) backdrop.addEventListener("click", function () { setMenu(false); });
  document.addEventListener("keydown", function (e) { if (e.key === "Escape") setMenu(false); });

  document.addEventListener("click", function (e) {
    var target = e.target.closest("[data-copy-target]");
    if (target) {
      var selector = target.getAttribute("data-copy-target");
      var el = document.querySelector(selector);
      if (el) {
        var value = (el.tagName === "INPUT" || el.tagName === "TEXTAREA") ? el.value : el.textContent;
        copyText(value);
      }
      return;
    }
    var tokenCopy = e.target.closest("[data-copy-token]");
    if (tokenCopy) { copyText(state.token, "令牌已复制"); return; }
    var plain = e.target.closest("[data-toast]");
    if (plain) toast(plain.getAttribute("data-toast"));
  });

  // ---------------------------------------------------------------- login
  function getStoredToken() {
    try { return sessionStorage.getItem("nfentik_usertoken") || ""; } catch (e) { return ""; }
  }
  function storeToken(token) {
    try { sessionStorage.setItem("nfentik_usertoken", token); } catch (e) {}
  }

  function switchLoginTab(tab) {
    document.querySelectorAll("[data-login-tab]").forEach(function (btn) {
      btn.classList.toggle("active", btn.getAttribute("data-login-tab") === tab);
    });
    document.querySelectorAll("[data-login-pane]").forEach(function (pane) {
      pane.classList.toggle("active", pane.getAttribute("data-login-pane") === tab);
    });
  }

  function bindPasswordToggles() {
    document.querySelectorAll("[data-pw-toggle]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var input = qs(btn.getAttribute("data-pw-toggle"));
        if (!input) return;
        var show = input.type === "password";
        input.type = show ? "text" : "password";
        btn.textContent = show ? "隐藏" : "显示";
      });
    });
  }

  function bindLogin() {
    document.querySelectorAll("[data-login-tab]").forEach(function (btn) {
      btn.addEventListener("click", function () { switchLoginTab(btn.getAttribute("data-login-tab")); });
    });
    bindPasswordToggles();

    on("user-login-btn", "click", function () {
      var token = (qs("user-token") && qs("user-token").value.trim()) || "";
      if (!token) { setText("user-login-error", "请输入用户令牌"); return; }
      fetch("/api/user/login", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ token: token }),
      }).then(function (res) { return res.json(); }).then(function (data) {
        if (!data.success) { setText("user-login-error", data.message || "登录失败"); return; }
        storeToken(token);
        location.replace("/user");
      }).catch(function () { setText("user-login-error", "网络错误，请重试"); });
    });

    on("account-login-btn", "click", function () {
      var username = (qs("account-username") && qs("account-username").value.trim()) || "";
      var password = (qs("account-password") && qs("account-password").value) || "";
      if (!username || !password) { setText("account-login-error", "请输入用户名和密码"); return; }
      fetch("/api/user/account/login", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username: username, password: password }),
      }).then(function (res) { return res.json(); }).then(function (data) {
        if (!data.success) { setText("account-login-error", data.message || "登录失败"); return; }
        try { sessionStorage.removeItem("nfentik_usertoken"); } catch (e) {}
        location.replace("/user");
      }).catch(function () { setText("account-login-error", "网络错误，请重试"); });
    });

    on("register-btn", "click", function () {
      var username = (qs("register-username") && qs("register-username").value.trim()) || "";
      var password = (qs("register-password") && qs("register-password").value) || "";
      var confirm = (qs("register-password2") && qs("register-password2").value) || "";
      var token = (qs("register-token") && qs("register-token").value.trim()) || "";
      if (!username || !password) { setText("register-error", "请填写用户名和密码"); return; }
      if (password !== confirm) { setText("register-error", "两次输入的密码不一致"); return; }
      fetch("/api/user/register", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username: username, password: password, token: token }),
      }).then(function (res) { return res.json(); }).then(function (data) {
        if (!data.success) { setText("register-error", data.message || "注册失败"); return; }
        try { sessionStorage.removeItem("nfentik_usertoken"); } catch (e) {}
        location.replace("/user");
      }).catch(function () { setText("register-error", "网络错误，请重试"); });
    });

    on("user-token", "keydown", function (e) { if (e.key === "Enter") qs("user-login-btn").click(); });
    on("account-password", "keydown", function (e) { if (e.key === "Enter") qs("account-login-btn").click(); });
    on("register-password2", "keydown", function (e) { if (e.key === "Enter") qs("register-btn").click(); });
  }

  function tryAccountRedirect() {
    fetch("/api/user/account/me", { headers: { "Content-Type": "application/json" } })
      .then(function (res) { return res.ok ? res.json() : null; })
      .then(function (data) { if (data && data.success) location.replace("/user"); })
      .catch(function () {});
  }

  function bootLogin() {
    bindLogin();
    var saved = getStoredToken();
    if (saved) {
      fetch("/api/user/me", { headers: { "Content-Type": "application/json", "Authorization": "Bearer " + saved } })
        .then(function (res) { return res.ok ? res.json() : null; })
        .then(function (data) {
          if (data && data.success) { location.replace("/user"); return; }
          try { sessionStorage.removeItem("nfentik_usertoken"); } catch (e) {}
          tryAccountRedirect();
        }).catch(function () {});
      return;
    }
    tryAccountRedirect();
  }

  // ---------------------------------------------------------------- data helpers
  function planLabel(code) {
    var p = state.planMap[code];
    return p ? p.Label : (code || "");
  }
  function userPlanLabel() {
    var u = state.user || {};
    return u.plan_label || planLabel(u.plan_code) || "未开通";
  }
  function entitlementOf(kind) {
    var list = (state.user && state.user.entitlements) || [];
    for (var i = 0; i < list.length; i++) if (list[i].kind === kind) return list[i];
    return null;
  }
  function quotaPercent() {
    var u = state.user || {};
    var count = entitlementOf("count");
    var remain = count && count.remain_count != null ? count.remain_count : u.remain_count;
    var total = count && count.total_count != null ? count.total_count : u.total_count;
    if (remain != null && total != null && total > 0) {
      return Math.max(0, Math.min(100, Math.round((remain / total) * 100)));
    }
    if (u.plan_type === "duration") return 100;
    return 0;
  }
  function quotaText() {
    var u = state.user || {};
    var count = entitlementOf("count");
    var remain = count && count.remain_count != null ? count.remain_count : u.remain_count;
    var total = count && count.total_count != null ? count.total_count : u.total_count;
    if (remain != null) return remain + " / " + (total == null ? "-" : total);
    if (u.plan_type === "duration") return "不限";
    if (u.plan_type === "count") return "0 / " + (total == null ? "-" : total);
    return "未开通";
  }
  function quotaUsedText() {
    var u = state.user || {};
    if (u.plan_type === "duration") return "不限次数";
    if (u.plan_type !== "count" && u.remain_count == null && entitlementOf("count") == null) return "未开通";
    return "已用 " + (u.used_count || 0);
  }
  function shortDate(value) {
    if (!value) return "";
    return String(value).slice(0, 10);
  }
  function remainDays(value) {
    if (!value) return -1;
    var d = new Date(String(value).replace(" ", "T"));
    if (isNaN(d.getTime())) return -1;
    return Math.ceil((d.getTime() - Date.now()) / 86400000);
  }
  function statusInfo() {
    var u = state.user || {};
    var s = u.enabled ? state.status : "disabled";
    var map = {
      active: ["使用中", "ok"],
      expiring: ["即将到期", "warn"],
      expired: ["已过期", "danger"],
      exhausted: ["次数用完", "danger"],
      disabled: ["已停用", "danger"],
    };
    return map[s] || [s || "-", ""];
  }
  function sourceLabel(source) {
    return { bank: "题库", cache: "缓存", ai: "AI" }[source] || source || "-";
  }
  function logStatus(status) {
    var map = {
      ok: ["成功", "ok"],
      pending: ["待修正", "warn"],
      denied: ["拒绝", "danger"],
      error: ["失败", "danger"],
    };
    return map[status] || [status || "-", ""];
  }
  function maskToken(token) {
    if (!token) return "-";
    if (token.length <= 10) return token;
    return token.slice(0, 6) + "···" + token.slice(-4);
  }

  // ---------------------------------------------------------------- rendering
  function renderAlert() {
    var el = qs("user-alert");
    if (!el) return;
    var u = state.user || {};
    var text = "";
    var kind = "warn";
    if (!u.enabled) { text = "账号已停用，请联系管理员。"; kind = "danger"; }
    else if (state.status === "expired") { text = "套餐已过期，请兑换时长卡密或联系管理员续费。"; kind = "danger"; }
    else if (state.status === "exhausted") { text = "次数已用完，请兑换次数卡密或联系管理员。"; kind = "danger"; }
    else if (state.status === "expiring") { text = "套餐即将到期，请及时续费。"; }
    if (!text) { el.className = "notice warn hidden"; el.textContent = ""; return; }
    el.className = "notice " + kind;
    el.textContent = text;
    el.style.marginBottom = "16px";
  }

  function renderShell() {
    var u = state.user || {};
    var label = userPlanLabel();
    var percent = quotaPercent();
    setText("shell-plan-name", label);
    setText("shell-plan-percent", percent + "%");
    setFill("shell-plan-fill", percent);
    var quota = quotaText();
    setText("shell-quota-left", quota === "不限" ? "不限次数" : quota === "未开通" ? "未开通" : quota + " 剩余");
    setText("shell-quota-expire", u.expire_at ? shortDate(u.expire_at) : (u.plan_type ? "不限" : "未开通"));
    var accountName = state.account && state.account.username ? state.account.username : "用户";
    setText("shell-user-name", accountName);
    setText("shell-user-sub", label);
    setText("shell-user-avatar", accountName.charAt(0).toUpperCase());
    setText("ov-name", accountName);
    setText("ov-account-username", accountName);
    setText("ov-account-token", state.token || "-");
    setText("int-quota", quotaText());
  }

  function renderEntitlements() {
    var host = qs("user-entitlements");
    if (!host) return;
    var list = (state.user && state.user.entitlements) || [];
    if (!list.length) {
      host.innerHTML =
        '<div class="ent"><div class="k">时长权益</div><div class="v">未开通</div><div class="d">兑换时长卡后生效</div></div>' +
        '<div class="ent"><div class="k">次数权益</div><div class="v">未开通</div><div class="d">兑换次数卡后生效</div></div>';
      return;
    }
    host.innerHTML = list.map(function (e) {
      var label = e.kind === "duration" ? "时长权益" : "次数权益";
      var detail;
      if (e.kind === "duration") detail = e.unlimited ? "无限时长" : (e.expire_at ? e.expire_at + " 到期" : "不限期");
      else detail = "剩余 " + (e.remain_count == null ? "不限" : e.remain_count + " / " + (e.total_count == null ? "-" : e.total_count)) + (e.expire_at ? " · " + e.expire_at + " 到期" : "");
      return '<div class="ent"><div class="k">' + esc(label) + '</div><div class="v">' +
        esc(e.plan_label || e.plan_code || "-") + '</div><div class="d">' + esc(detail) + "</div></div>";
    }).join("");
  }

  function renderPlanData() {
    var u = state.user || {};
    var label = userPlanLabel();
    var st = statusInfo();
    var pill = qs("user-status-pill");
    if (pill) { pill.className = "pill " + st[1]; pill.textContent = st[0]; }
    setText("ov-plan-name", label);
    setText("ov-plan-sub", u.expire_at
      ? "开通于 " + shortDate(u.start_at) + " · " + shortDate(u.expire_at) + " 到期"
      : (u.plan_type ? "长期有效" : "尚未开通套餐"));
    setText("user-quota-text", quotaText());
    setFill("user-quota-fill", quotaPercent());
    setText("ov-used", quotaUsedText());
    setText("ov-expire", u.expire_at ? shortDate(u.expire_at) : (u.plan_type ? "不限" : "未开通"));
    setText("plan-kind", u.plan_type === "count" ? "次数套餐" : u.plan_type === "duration" ? "时长套餐" : "未开通");
    setText("user-used", u.used_count || 0);
    var days = remainDays(u.expire_at);
    setText("user-days", !u.expire_at ? "不限" : days < 0 ? "已到期" : days + " 天");
    setText("plan-quota-cap", quotaPercent() + "% 可用");
    setText("plan-label", u.plan_label || label);
    setText("plan-code", u.plan_code || "-");
    setText("plan-status", st[0]);
    setText("user-start", u.start_at || "-");
    setText("user-expire", u.expire_at || "不限");
    setValueOrText("user-token", state.token || "-");
    var note = qs("user-note-input");
    if (note) note.value = u.note || "";
    renderEntitlements();
    var notice = qs("plan-expire-notice");
    if (notice) {
      var div = notice.querySelector("div");
      if (div) {
        if (!u.expire_at) div.textContent = "当前套餐无到期时间，可长期使用。";
        else if (days < 0) div.textContent = "当前套餐已到期，请兑换时长卡密或联系管理员续费。";
        else if (days <= 7) div.textContent = "当前套餐将在 " + days + " 天后到期，请提前兑换时长卡密。";
        else div.textContent = "当前套餐将在 " + days + " 天后到期。如需继续使用，请提前兑换时长卡密。";
      }
    }
  }

  function renderAccount() {
    var acc = state.account;
    var u = state.user || {};
    var name = acc ? acc.username : "未绑定账号";
    setText("account-name", name);
    setText("account-username-view", acc ? acc.username : "-");
    setText("account-id", acc && acc.user_id != null ? acc.user_id : (u.id != null ? u.id : "-"));
    setText("account-token-view", maskToken(state.token));
    setText("account-auth", acc ? "账号密码 + 用户令牌" : "用户令牌");
    setText("security-last-login", acc ? "当前会话" : "-");
    setText("security-password-strength", acc ? "良好" : "-");
    setText("token-updated", acc ? "已绑定账号" : "已登录");
    var avatar = qs("account-avatar");
    if (avatar) avatar.textContent = (acc && acc.username ? acc.username.charAt(0) : "U").toUpperCase();

    var pill = qs("account-status-pill");
    if (pill) {
      if (!acc) { pill.className = "pill"; pill.textContent = "未绑定"; }
      else if (acc.status === "disabled") { pill.className = "pill danger"; pill.textContent = "已停用"; }
      else { pill.className = "pill ok"; pill.textContent = "已绑定"; }
    }
    setText("account-hint", acc ? "已绑定账号，可使用用户名密码登录用户中心。" : "当前为令牌登录，绑定账号后可使用用户名密码登录。");
    var bindWrap = qs("account-bind-wrap");
    if (bindWrap) bindWrap.classList.toggle("hidden", !!acc);
    var actions = qs("account-actions");
    if (actions) actions.classList.toggle("hidden", !acc);
    var passwordCard = qs("password-card");
    if (passwordCard) passwordCard.classList.toggle("hidden", !acc);
    var token = qs("user-token");
    if (token) token.value = state.token || "";
  }

  function renderOCS() {
    var host = qs("user-ocs-json");
    if (!host) return;
    var base = window.location.origin.replace(/\/+$/, "");
    var config = [{
      name: siteName,
      homepage: base,
      url: base + "/query",
      method: "get",
      type: "GM_xmlhttpRequest",
      contentType: "json",
      data: { title: "${title}", options: "${options}", type: "${type}", token: state.token },
      handler: "return (res)=>res.code === 0 ? [res.message, undefined] : [res.data.question,res.data.answer,{ai: res.data.is_ai}]",
    }];
    host.textContent = JSON.stringify(config, null, 2);
  }

  function renderUrls() {
    var base = window.location.origin.replace(/\/+$/, "");
    var token = encodeURIComponent(state.token || "");
    setValueOrText("user-url-mogo", base + "/nfentk?key=" + token + "&q=");
    setValueOrText("user-url-mogo-debug", base + "/nfentk?key=" + token + "&q=" + encodeURIComponent("示例题目") + "&info=" + encodeURIComponent("单选题****[选项1###选项2]") + "&kcinfo=" + encodeURIComponent("课程名|课程ID|账号"));
    setValueOrText("user-url-title", base + "/query?token=" + token + "&title=");
    setValueOrText("user-url-q", base + "/query?token=" + token + "&q=");
  }

  function renderRedeem() {
    setText("rd-quota-text", quotaText());
    setFill("rd-quota-fill", quotaPercent());
    setText("rd-used", quotaUsedText());
    setText("rd-expire", state.user && state.user.expire_at ? shortDate(state.user.expire_at) : "不限");
  }

  // ---------------------------------------------------------------- overview
  function renderOverviewStats(items) {
    var today = new Date();
    var prefix = today.getFullYear() + "-" + String(today.getMonth() + 1).padStart(2, "0") + "-" + String(today.getDate()).padStart(2, "0");
    var todays = items.filter(function (l) { return String(l.timestamp || "").slice(0, 10) === prefix; });
    var pool = todays.length ? todays : items;
    var calls = todays.length;
    var latency = pool.length ? Math.round(pool.reduce(function (sum, l) { return sum + (l.response_time || 0); }, 0) / pool.length) : 0;
    var hits = pool.filter(function (l) { return l.source === "bank" || l.source === "cache"; }).length;
    var hitRate = pool.length ? Math.round((hits / pool.length) * 1000) / 10 : 0;
    setText("ov-stat-calls", calls);
    setText("ov-stat-latency", latency + " ms");
    setText("ov-stat-hit", hitRate + "%");
  }

  function renderRecent(items) {
    var host = qs("ov-recent");
    if (!host) return;
    var recent = items.slice(0, 5);
    if (!recent.length) { host.innerHTML = '<div class="empty">暂无调用记录</div>'; return; }
    host.innerHTML = recent.map(function (l) {
      var s = logStatus(l.status);
      var time = String(l.timestamp || "").slice(11, 16) || "-";
      return '<div class="row"><span class="time">' + esc(time) + '</span><span class="q">' +
        esc(l.question || "") + '</span><span class="pill ' + s[1] + '">' + esc(s[0]) + "</span></div>";
    }).join("");
  }

  function loadOverviewLogs() {
    api("/logs?page=1&page_size=100").then(function (data) {
      if (!data.success) return;
      var items = data.items || [];
      renderOverviewStats(items);
      renderRecent(items);
    }).catch(function () {});
  }

  // ---------------------------------------------------------------- logs page
  function filteredLogs() {
    var search = (qs("log-search") && qs("log-search").value.trim().toLowerCase()) || "";
    var source = (qs("log-source") && qs("log-source").value) || "";
    var status = (qs("log-status") && qs("log-status").value) || "";
    return state.logs.filter(function (l) {
      if (source && l.source !== source) return false;
      if (status && l.status !== status) return false;
      if (search) {
        var text = (l.question || "") + " " + (l.answer || "");
        if (text.toLowerCase().indexOf(search) < 0) return false;
      }
      return true;
    });
  }

  function renderLogs() {
    var body = qs("user-logs");
    if (!body) return;
    var items = filteredLogs();
    if (!items.length) {
      body.innerHTML = '<tr><td colspan="6" class="empty">暂无调用记录</td></tr>';
    } else {
      body.innerHTML = items.map(function (l) {
        var s = logStatus(l.status);
        return "<tr>" +
          '<td class="muted" style="white-space:nowrap">' + esc(l.timestamp || "-") + "</td>" +
          '<td class="clamp">' + esc(l.question || "") + "</td>" +
          '<td class="clamp">' + esc(l.answer || "") + "</td>" +
          "<td>" + esc(sourceLabel(l.source)) + "</td>" +
          '<td><span class="pill ' + s[1] + '">' + esc(s[0]) + "</span></td>" +
          '<td class="muted">' + (l.response_time != null ? l.response_time + " ms" : "-") + "</td>" +
        "</tr>";
      }).join("");
    }
    var pages = Math.max(1, Math.ceil(state.logTotal / state.logPageSize));
    setText("user-logs-pageinfo", "第 " + state.logPage + " 页 / 共 " + pages + " 页");
    var prev = qs("user-logs-prev");
    var next = qs("user-logs-next");
    if (prev) prev.disabled = state.logPage <= 1;
    if (next) next.disabled = state.logPage * state.logPageSize >= state.logTotal;
  }

  function loadLogs() {
    api("/logs?page=" + state.logPage + "&page_size=" + state.logPageSize).then(function (data) {
      if (!data.success) { toast(data.message || "加载失败"); return; }
      state.logs = data.items || [];
      state.logTotal = data.total || 0;
      renderLogs();
    }).catch(function () {});
  }

  // ---------------------------------------------------------------- account page
  function loadAccount() {
    api("/account/me").then(function (data) {
      if (!data || !data.success) return;
      state.account = data.account || null;
      if (data.user) {
        state.user = data.user;
        state.token = data.user.token || state.token;
      }
      renderAccount();
      renderShell();
      renderPlanData();
    }).catch(function () {});
  }

  // ---------------------------------------------------------------- redeem history
  function readRedeemHistory() {
    try {
      var raw = localStorage.getItem("nfentik_redeem_history");
      var list = raw ? JSON.parse(raw) : [];
      return Array.isArray(list) ? list : [];
    } catch (e) { return []; }
  }
  function saveRedeemHistory(code) {
    var list = readRedeemHistory();
    var masked = code.length > 8 ? code.slice(0, 4) + "····" + code.slice(-4) : code;
    list.unshift({ time: new Date().toISOString(), code: masked });
    try { localStorage.setItem("nfentik_redeem_history", JSON.stringify(list.slice(0, 8))); } catch (e) {}
  }
  function renderRedeemHistory() {
    var host = qs("rd-history");
    if (!host) return;
    var list = readRedeemHistory();
    if (!list.length) { host.innerHTML = '<div class="empty">暂无兑换记录</div>'; return; }
    host.innerHTML = list.map(function (item) {
      var d = new Date(item.time);
      var label = isNaN(d.getTime()) ? "" : (String(d.getMonth() + 1).padStart(2, "0") + "-" + String(d.getDate()).padStart(2, "0"));
      return '<div class="row"><span class="time">' + esc(label) + '</span><span class="q">' +
        esc(item.code || "卡密") + '</span><span class="pill ok">成功</span></div>';
    }).join("");
  }

  // ---------------------------------------------------------------- actions
  function doLogout() {
    var done = function () { clearAuth(); location.replace("/user/login"); };
    if (state.authMode !== "account") { done(); return; }
    fetch("/api/user/account/logout", { method: "POST", headers: headers() })
      .catch(function () {}).then(done);
  }

  function bindCodeTabs() {
    var host = document.querySelector("[data-tabs]");
    if (!host) return;
    host.addEventListener("click", function (e) {
      var btn = e.target.closest("[data-tab]");
      if (!btn) return;
      host.querySelectorAll("[data-tab]").forEach(function (b) { b.classList.toggle("active", b === btn); });
      document.querySelectorAll("[data-pane]").forEach(function (pane) {
        pane.classList.toggle("active", pane.getAttribute("data-pane") === btn.getAttribute("data-tab"));
      });
    });
  }

  function bindActions() {
    bindCodeTabs();
    on("user-logout", "click", doLogout);
    on("account-logout-btn", "click", doLogout);

    on("user-note-save", "click", function () {
      var input = qs("user-note-input");
      var note = input ? input.value : "";
      api("/profile", { method: "PUT", body: JSON.stringify({ note: note }) }).then(function (data) {
        if (!data.success) { toast(data.message || "保存失败"); return; }
        state.user = data.user || state.user;
        renderPlanData();
        renderShell();
        toast("备注已保存");
      }).catch(function () {});
    });

    on("bind-btn", "click", function () {
      var username = (qs("bind-username") && qs("bind-username").value.trim()) || "";
      var password = (qs("bind-password") && qs("bind-password").value) || "";
      if (!username || !password) { setText("bind-error", "请填写用户名和密码"); return; }
      fetch("/api/user/account/bind", {
        method: "POST", headers: headers(),
        body: JSON.stringify({ username: username, password: password }),
      }).then(function (res) { return res.json(); }).then(function (data) {
        if (!data.success) { setText("bind-error", data.message || "绑定失败"); return; }
        setText("bind-error", "");
        state.authMode = "account";
        state.account = data.account || null;
        state.csrf = data.csrf_token || "";
        state.user = data.user || state.user;
        state.token = (data.user && data.user.token) || state.token;
        try { sessionStorage.removeItem("nfentik_usertoken"); } catch (e) {}
        toast("账号绑定成功");
        renderAccount();
        renderShell();
        renderPlanData();
      }).catch(function () { setText("bind-error", "网络错误，请重试"); });
    });

    on("password-save", "click", function () {
      var oldPw = (qs("old-password") && qs("old-password").value) || "";
      var newPw = (qs("new-password") && qs("new-password").value) || "";
      var confirm = (qs("new-password2") && qs("new-password2").value) || "";
      if (!oldPw || !newPw) { setText("password-error", "请填写原密码和新密码"); return; }
      if (newPw !== confirm) { setText("password-error", "两次输入的新密码不一致"); return; }
      api("/account/password", { method: "POST", body: JSON.stringify({ old_password: oldPw, new_password: newPw }) })
        .then(function (data) {
          if (!data.success) { setText("password-error", data.message || "修改失败"); return; }
          setText("password-error", "");
          state.csrf = data.csrf_token || state.csrf;
          if (qs("old-password")) qs("old-password").value = "";
          if (qs("new-password")) qs("new-password").value = "";
          if (qs("new-password2")) qs("new-password2").value = "";
          toast("密码已更新");
        }).catch(function () { setText("password-error", "网络错误，请重试"); });
    });

    on("redeem-btn", "click", function () {
      var code = (qs("redeem-code") && qs("redeem-code").value.trim()) || "";
      if (!code) { setText("redeem-error", "请输入卡密"); return; }
      api("/redeem", { method: "POST", body: JSON.stringify({ code: code }) }).then(function (data) {
        if (!data.success) { setText("redeem-error", data.message || "兑换失败"); return; }
        setText("redeem-error", "");
        if (qs("redeem-code")) qs("redeem-code").value = "";
        state.user = data.user || state.user;
        saveRedeemHistory(code);
        renderRedeemHistory();
        renderPlanData();
        renderShell();
        renderRedeem();
        renderOCS();
        renderUrls();
        toast("卡密兑换成功");
      }).catch(function () { setText("redeem-error", "网络错误，请重试"); });
    });

    on("user-logs-refresh", "click", function () { state.logPage = 1; loadLogs(); });
    on("log-filter-btn", "click", function () { renderLogs(); });
    on("log-search", "keydown", function (e) { if (e.key === "Enter") renderLogs(); });
    on("user-logs-prev", "click", function () {
      if (state.logPage <= 1) return;
      state.logPage--; loadLogs();
    });
    on("user-logs-next", "click", function () {
      if (state.logPage * state.logPageSize >= state.logTotal) return;
      state.logPage++; loadLogs();
    });

    on("user-reset-token", "click", async function () {
      if (!(await confirmDialog("重置后旧令牌立即失效，需要重新登录并更新 OCS 配置。确定继续？", { danger: true, okText: "重置" }))) return;
      api("/reset-token", { method: "POST" }).then(function (data) {
        if (!data.success) { toast(data.message || "重置失败"); return; }
        state.token = data.token;
        if (state.user) state.user.token = data.token;
        if (state.authMode === "token") storeToken(data.token);
        renderAccount();
        renderShell();
        renderPlanData();
        renderOCS();
        renderUrls();
        showTokenModal(data.token);
      }).catch(function () {});
    });

    on("token-modal-copy", "click", function () {
      var el = qs("token-modal-value");
      copyText(el ? el.textContent : state.token, "新令牌已复制");
    });
    on("token-modal-close", "click", hideTokenModal);
  }

  function showTokenModal(token) {
    var modal = qs("token-modal");
    setText("token-modal-value", token);
    if (modal) modal.classList.add("open");
  }
  function hideTokenModal() {
    var modal = qs("token-modal");
    if (modal) modal.classList.remove("open");
  }

  // ---------------------------------------------------------------- boot
  function loadMe() {
    api("/me").then(function (data) {
      if (!data.success) { toast(data.message || "加载失败"); return; }
      state.user = data.user;
      state.status = data.status;
      state.plans = data.plans || [];
      state.planMap = {};
      state.plans.forEach(function (p) { state.planMap[p.Code] = p; });
      if (!state.token && data.user) state.token = data.user.token || "";
      renderShell();
      renderAlert();
      if (page === "overview" || page === "plan") renderPlanData();
      if (page === "account") renderAccount();
      if (page === "redeem") renderRedeem();
      if (page === "integrate") { renderOCS(); renderUrls(); }
    }).catch(function () {});
  }

  function startPage() {
    bindActions();
    loadMe();
    if (page === "overview") loadOverviewLogs();
    if (page === "logs") loadLogs();
    if (page === "account") loadAccount();
    if (page === "redeem") renderRedeemHistory();
  }

  function bootMain() {
    var saved = getStoredToken();
    if (saved) {
      state.authMode = "token";
      state.token = saved;
      startPage();
      return;
    }
    fetch("/api/user/account/me", { headers: { "Content-Type": "application/json" } })
      .then(function (res) { return res.ok ? res.json() : null; })
      .then(function (data) {
        if (!data || !data.success) { location.replace("/user/login"); return; }
        state.authMode = "account";
        state.account = data.account || null;
        state.user = data.user || null;
        state.token = (data.user && data.user.token) || "";
        state.csrf = readCookie("nfentik_csrf");
        startPage();
      })
      .catch(function () { location.replace("/user/login"); });
  }

  if (page === "login") bootLogin();
  else bootMain();
})();
