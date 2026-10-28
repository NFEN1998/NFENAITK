(function () {
  "use strict";

  var app = document.getElementById("app");
  var state = {
    token: "", user: null, status: "", plans: [], planMap: {},
    logPage: 1, logPageSize: 20, logTotal: 0,
    authMode: "token", account: null, csrf: "",
  };

  // siteName comes from the system setting, injected by the page template, so
  // the generated OCS config carries the configured question bank name.
  function readSiteName() {
    var m = document.querySelector('meta[name="app-site-name"]');
    return m && m.content ? m.content : "nfentik";
  }

  var siteName = readSiteName();

  function qs(id) { return document.getElementById(id); }

  function esc(text) {
    return String(text == null ? "" : text)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
  }

  // toast shows a styled, auto-dismissing notification.
  function toast(message, type) {
    if (!message) return;
    var host = document.getElementById("toast-host");
    if (!host) {
      host = document.createElement("div");
      host.id = "toast-host";
      host.className = "toast-host";
      document.body.appendChild(host);
    }
    var el = document.createElement("div");
    el.className = "toast" + (type ? " " + type : "");
    el.setAttribute("role", type === "error" ? "alert" : "status");
    el.innerHTML = '<span class="toast-dot"></span><span class="toast-msg"></span><button type="button" class="toast-x" aria-label="关闭">×</button>';
    el.querySelector(".toast-msg").textContent = String(message);
    host.appendChild(el);
    var timer = setTimeout(remove, type === "error" ? 5600 : 3200);
    function remove() {
      clearTimeout(timer);
      if (!el.parentNode) return;
      el.classList.add("leaving");
      el.addEventListener("animationend", function () { el.remove(); }, { once: true });
      setTimeout(function () { if (el.parentNode) el.remove(); }, 400);
    }
    el.querySelector(".toast-x").addEventListener("click", remove);
  }

  // confirmDialog shows a styled confirmation modal and resolves to a boolean.
  function confirmDialog(message, opts) {
    opts = opts || {};
    return new Promise(function (resolve) {
      var wrap = document.createElement("div");
      wrap.className = "modal-overlay";
      wrap.innerHTML =
        '<div class="modal-card">' +
        "<h2>" + esc(opts.title || "确认操作") + "</h2>" +
        '<p class="muted">' + esc(message) + "</p>" +
        '<div class="modal-actions">' +
        '<button type="button" class="btn ghost" data-act="cancel">' + esc(opts.cancelText || "取消") + "</button>" +
        '<button type="button" class="btn ' + (opts.danger ? "danger" : "primary") + '" data-act="ok">' + esc(opts.okText || "确定") + "</button>" +
        "</div></div>";
      document.body.appendChild(wrap);
      function done(value) { wrap.remove(); resolve(value); }
      wrap.addEventListener("click", function (e) {
        var btn = e.target.closest("button[data-act]");
        if (btn) { done(btn.dataset.act === "ok"); return; }
        if (e.target === wrap) done(false);
      });
    });
  }

  function headers() {
    var h = { "Content-Type": "application/json" };
    if (state.authMode === "token" && state.token) h["Authorization"] = "Bearer " + state.token;
    if (state.authMode === "account" && state.csrf) h["X-CSRF-Token"] = state.csrf;
    return h;
  }

  function readCookie(name) {
    var parts = document.cookie ? document.cookie.split(";") : [];
    for (var i = 0; i < parts.length; i++) {
      var pair = parts[i].trim().split("=");
      if (pair[0] === name) return decodeURIComponent(pair.slice(1).join("="));
    }
    return "";
  }

  function clearState() {
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
      if (res.status === 401) { clearState(); showLogin(); throw new Error("未授权"); }
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

  function enterMain() {
    showMain();
    loadMe();
    loadLogs();
    loadAccount();
  }

  function switchLoginTab(tab) {
    document.querySelectorAll("[data-login-tab]").forEach(function (btn) {
      btn.classList.toggle("active", btn.getAttribute("data-login-tab") === tab);
    });
    ["token", "account", "register"].forEach(function (name) {
      qs("login-panel-" + name).classList.toggle("hidden", name !== tab);
    });
  }

  document.querySelectorAll("[data-login-tab]").forEach(function (btn) {
    btn.addEventListener("click", function () { switchLoginTab(btn.getAttribute("data-login-tab")); });
  });

  function applyAccountLogin(data) {
    state.authMode = "account";
    state.user = data.user || null;
    state.account = data.account || null;
    state.token = (data.user && data.user.token) || "";
    state.csrf = data.csrf_token || "";
    try { sessionStorage.removeItem("nfentik_usertoken"); } catch (e) {}
    enterMain();
  }

  qs("user-login-btn").addEventListener("click", function () {
    var token = qs("user-token").value.trim();
    if (!token) { qs("user-login-error").textContent = "请输入用户令牌"; return; }
    fetch("/api/user/login", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ token: token }),
    }).then(function (res) { return res.json(); }).then(function (data) {
      if (!data.success) { qs("user-login-error").textContent = data.message || "登录失败"; return; }
      state.authMode = "token";
      state.token = token;
      state.account = null;
      state.csrf = "";
      try { sessionStorage.setItem("nfentik_usertoken", token); } catch (e) {}
      enterMain();
    }).catch(function () { qs("user-login-error").textContent = "网络错误，请重试"; });
  });

  qs("account-login-btn").addEventListener("click", function () {
    var username = qs("account-username").value.trim();
    var password = qs("account-password").value;
    if (!username || !password) { qs("account-login-error").textContent = "请输入用户名和密码"; return; }
    fetch("/api/user/account/login", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username: username, password: password }),
    }).then(function (res) { return res.json(); }).then(function (data) {
      if (!data.success) { qs("account-login-error").textContent = data.message || "登录失败"; return; }
      qs("account-login-error").textContent = "";
      applyAccountLogin(data);
    }).catch(function () { qs("account-login-error").textContent = "网络错误，请重试"; });
  });

  qs("register-btn").addEventListener("click", function () {
    var username = qs("register-username").value.trim();
    var password = qs("register-password").value;
    var confirm = qs("register-password2").value;
    var token = qs("register-token").value.trim();
    if (!username || !password) { qs("register-error").textContent = "请填写用户名和密码"; return; }
    if (password !== confirm) { qs("register-error").textContent = "两次输入的密码不一致"; return; }
    fetch("/api/user/register", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username: username, password: password, token: token }),
    }).then(function (res) { return res.json(); }).then(function (data) {
      if (!data.success) { qs("register-error").textContent = data.message || "注册失败"; return; }
      qs("register-error").textContent = "";
      applyAccountLogin(data);
      toast("注册成功，请兑换卡密后使用", "success");
    }).catch(function () { qs("register-error").textContent = "网络错误，请重试"; });
  });

  qs("user-token").addEventListener("keydown", function (e) {
    if (e.key === "Enter") qs("user-login-btn").click();
  });
  qs("account-password").addEventListener("keydown", function (e) {
    if (e.key === "Enter") qs("account-login-btn").click();
  });
  qs("register-password2").addEventListener("keydown", function (e) {
    if (e.key === "Enter") qs("register-btn").click();
  });

  qs("user-token-toggle").addEventListener("click", function () {
    var input = qs("user-token");
    var show = input.type === "password";
    input.type = show ? "text" : "password";
    this.textContent = show ? "隐藏" : "显示";
  });

  function doLogout() {
    fetch("/api/user/account/logout", { method: "POST", headers: headers() })
      .catch(function () {})
      .then(function () {
        clearState();
        showLogin();
        switchLoginTab("token");
      });
  }

  qs("user-logout").addEventListener("click", doLogout);
  qs("account-logout-btn").addEventListener("click", doLogout);

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
      renderUrlExamples();
    }).catch(function () {});
  }

  function planLabel(code) {
    var p = state.planMap[code];
    return p ? p.Label : (code || "-");
  }

  function entitlementOf(u, kind) {
    var list = u.entitlements || [];
    for (var i = 0; i < list.length; i++) {
      if (list[i].kind === kind) return list[i];
    }
    return null;
  }

  function durationEntActive(e) {
    if (!e) return false;
    if (e.unlimited) return true;
    if (!e.expire_at) return true;
    return new Date(String(e.expire_at).replace(" ", "T")).getTime() > Date.now();
  }

  function renderEntitlements(u) {
    var list = u.entitlements || [];
    if (!list.length) {
      qs("user-entitlements").innerHTML = '<div class="plan-item"><span class="label">时长权益</span><strong>未开通</strong></div>' +
        '<div class="plan-item"><span class="label">次数权益</span><strong>未开通</strong></div>';
      return;
    }
    qs("user-entitlements").innerHTML = list.map(function (e) {
      var label = e.kind === "duration" ? "时长权益" : "次数权益";
      var detail;
      if (e.kind === "duration") {
        detail = e.unlimited ? "无限时长" : (e.expire_at ? (e.expire_at + " 到期") : "不限");
      } else {
        detail = "剩余 " + (e.remain_count == null ? "不限" : (e.remain_count + " / " + (e.total_count == null ? "-" : e.total_count))) +
          (e.expire_at ? " · " + e.expire_at + " 到期" : "");
      }
      return '<div class="plan-item"><span class="label">' + label + "</span><strong>" +
        esc(e.plan_label || e.plan_code || "-") + '</strong><span class="muted small">' + esc(detail) + "</span></div>";
    }).join("");
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

    renderEntitlements(u);

    var quota = qs("user-quota");
    var used = u.used_count || 0;
    var countEnt = entitlementOf(u, "count");
    var durationEnt = entitlementOf(u, "duration");
    if (countEnt && countEnt.remain_count != null) {
      quota.classList.remove("hidden");
      qs("user-quota-label").textContent = durationEnt && durationEntActive(durationEnt)
        ? "次数余额（时长到期后启用）" : "剩余次数";
      var total = countEnt.total_count || 0;
      var remain = countEnt.remain_count || 0;
      qs("user-quota-text").textContent = remain + " / " + total;
      var pct = total > 0 ? Math.max(0, Math.min(100, Math.round((remain / total) * 100))) : 0;
      qs("user-quota-fill").style.width = pct + "%";
    } else if (u.remain_count != null) {
      quota.classList.remove("hidden");
      qs("user-quota-label").textContent = "剩余次数";
      var legacyTotal = u.total_count || 0;
      var legacyRemain = u.remain_count || 0;
      qs("user-quota-text").textContent = legacyRemain + " / " + legacyTotal;
      var legacyPct = legacyTotal > 0 ? Math.max(0, Math.min(100, Math.round((legacyRemain / legacyTotal) * 100))) : 0;
      qs("user-quota-fill").style.width = legacyPct + "%";
    } else {
      quota.classList.remove("hidden");
      qs("user-quota-label").textContent = "已用次数";
      qs("user-quota-text").textContent = used + " / 无限";
      qs("user-quota-fill").style.width = "100%";
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
      el.textContent = "套餐已过期，请兑换时长卡密或联系管理员续费。";
      return;
    }
    if (state.status === "exhausted") {
      el.className = "user-alert danger";
      var zero = (u.total_count || 0) === 0;
      el.textContent = zero
        ? "尚未充值，请在下方兑换卡密后使用。"
        : "调用次数已用尽，请兑换卡密或联系管理员续费。";
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

  // ------------------------------------------------------------------ account

  function loadAccount() {
    api("/account/me").then(function (data) {
      if (!data || !data.success) return;
      state.account = data.account || null;
      if (data.user) {
        state.user = data.user;
        state.token = data.user.token || state.token;
      }
      renderAccount();
    }).catch(function () {});
  }

  function renderAccount() {
    var acc = state.account;
    qs("account-username-view").textContent = acc ? acc.username : "-";
    qs("account-token-view").textContent = state.token || "-";
    var pill = qs("account-status-pill");
    if (acc) {
      pill.className = acc.status === "disabled" ? "pill disabled" : "pill active";
      pill.textContent = acc.status === "disabled" ? "已停用" : "已绑定";
    } else {
      pill.className = "pill disabled";
      pill.textContent = "未绑定";
    }
    qs("account-bind-wrap").classList.toggle("hidden", !!acc);
    qs("account-actions").classList.toggle("hidden", !acc);
    qs("account-hint").textContent = acc
      ? "已绑定账号，可使用用户名密码登录用户中心。"
      : "当前为令牌登录，绑定账号后可使用用户名密码登录。";
  }

  qs("bind-btn").addEventListener("click", function () {
    var username = qs("bind-username").value.trim();
    var password = qs("bind-password").value;
    if (!username || !password) { qs("bind-error").textContent = "请填写用户名和密码"; return; }
    fetch("/api/user/account/bind", {
      method: "POST", headers: headers(),
      body: JSON.stringify({ username: username, password: password }),
    }).then(function (res) { return res.json(); }).then(function (data) {
      if (!data.success) { qs("bind-error").textContent = data.message || "绑定失败"; return; }
      qs("bind-error").textContent = "";
      state.authMode = "account";
      state.account = data.account || null;
      state.csrf = data.csrf_token || "";
      state.user = data.user || state.user;
      state.token = (data.user && data.user.token) || state.token;
      try { sessionStorage.removeItem("nfentik_usertoken"); } catch (e) {}
      toast("账号绑定成功", "success");
      renderAccount();
      renderProfile();
      renderOCS();
      renderUrlExamples();
    }).catch(function () { qs("bind-error").textContent = "网络错误，请重试"; });
  });

  qs("account-token-copy").addEventListener("click", function () {
    copyText(state.token, "令牌已复制");
  });

  qs("password-btn").addEventListener("click", function () {
    qs("password-wrap").classList.toggle("hidden");
  });

  qs("password-save").addEventListener("click", function () {
    var oldPassword = qs("old-password").value;
    var newPassword = qs("new-password").value;
    if (!oldPassword || !newPassword) { qs("password-error").textContent = "请填写原密码和新密码"; return; }
    api("/account/password", {
      method: "POST",
      body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }),
    }).then(function (data) {
      if (!data.success) { qs("password-error").textContent = data.message || "修改失败"; return; }
      qs("password-error").textContent = "";
      state.csrf = data.csrf_token || state.csrf;
      qs("old-password").value = "";
      qs("new-password").value = "";
      qs("password-wrap").classList.add("hidden");
      toast("密码已更新", "success");
    }).catch(function () { qs("password-error").textContent = "网络错误，请重试"; });
  });

  // ------------------------------------------------------------------ redeem

  qs("redeem-btn").addEventListener("click", function () {
    var code = qs("redeem-code").value.trim();
    if (!code) { qs("redeem-error").textContent = "请输入卡密"; return; }
    api("/redeem", { method: "POST", body: JSON.stringify({ code: code }) }).then(function (data) {
      if (!data.success) { qs("redeem-error").textContent = data.message || "兑换失败"; return; }
      qs("redeem-error").textContent = "";
      qs("redeem-code").value = "";
      state.user = data.user || state.user;
      renderProfile();
      renderOCS();
      renderUrlExamples();
      loadMe();
      toast("卡密兑换成功", "success");
    }).catch(function () { qs("redeem-error").textContent = "网络错误，请重试"; });
  });

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
      name: siteName,
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

  // urlExample builds a GET /query URL using the given question key.
  function urlExample(key) {
    var base = window.location.origin.replace(/\/+$/, "");
    return base + "/query?token=" + encodeURIComponent(state.token) + "&" + key + "=";
  }

  // mogoUrlExample is the short URL pasted into MOGO; MOGO appends q, info
  // and kcinfo itself. key and token are interchangeable.
  function mogoUrlExample() {
    var base = window.location.origin.replace(/\/+$/, "");
    return base + "/nfentk?key=" + encodeURIComponent(state.token) + "&q=";
  }

  function mogoDebugUrlExample() {
    var base = window.location.origin.replace(/\/+$/, "");
    return base + "/nfentk?key=" + encodeURIComponent(state.token) +
      "&q=" + encodeURIComponent("示例题目") +
      "&info=" + encodeURIComponent("单选题****[选项1###选项2]") +
      "&kcinfo=" + encodeURIComponent("课程名|课程ID|账号");
  }

  function renderUrlExamples() {
    qs("user-url-mogo").value = mogoUrlExample();
    qs("user-url-mogo-debug").value = mogoDebugUrlExample();
    qs("user-url-title").value = urlExample("title");
    qs("user-url-q").value = urlExample("q");
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

  qs("user-copy-url1").addEventListener("click", function () {
    copyText(qs("user-url-mogo").value, "已复制 URL 模式1接口");
  });

  qs("user-copy-url").addEventListener("click", function () {
    copyText(qs("user-url-title").value, "已复制 URL 模式2接口");
  });

  document.querySelectorAll("[data-copy]").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var input = qs(btn.getAttribute("data-copy"));
      if (input) copyText(input.value, "已复制");
    });
  });

  // ------------------------------------------------------------------ logs

  function loadLogs() {
    api("/logs?page=" + state.logPage + "&page_size=" + state.logPageSize).then(function (data) {
      if (!data.success) { toast(data.message); return; }
      state.logTotal = data.total || 0;
      renderLogs(data.items || []);
      qs("user-logs-pageinfo").textContent = "第 " + state.logPage + " 页 / 共 " +
        Math.max(1, Math.ceil(state.logTotal / state.logPageSize)) + " 页";
    }).catch(function () {});
  }

  var sourceLabels = { bank: "题库", cache: "缓存", ai: "AI" };
  var statusLabelsLog = { ok: "成功", denied: "拒绝", error: "失败" };

  function renderLogs(items) {
    var body = qs("user-logs").querySelector("tbody");
    if (!items.length) {
      body.innerHTML = '<tr><td colspan="7" class="muted">暂无调用记录</td></tr>';
      return;
    }
    body.innerHTML = items.map(function (l) {
      return "<tr>" +
        "<td class=\"small\">" + esc(l.timestamp) + "</td>" +
        '<td class="clamp">' + esc(l.question || "") + "</td>" +
        '<td class="clamp">' + esc(l.options || "") + "</td>" +
        '<td class="clamp">' + esc(l.answer || "") + "</td>" +
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

  qs("user-reset-token").addEventListener("click", async function () {
    if (!(await confirmDialog("重置后旧令牌立即失效，需要重新登录并更新 OCS 配置。确定继续？", { danger: true, okText: "重置" }))) return;
    api("/reset-token", { method: "POST" }).then(function (res) {
      if (!res.success) { toast(res.message, "error"); return; }
      state.token = res.token;
      if (state.user) state.user.token = res.token;
      if (state.authMode === "token") {
        try { sessionStorage.setItem("nfentik_usertoken", state.token); } catch (e) {}
      }
      renderAccount();
      renderProfile();
      renderOCS();
      renderUrlExamples();
      showTokenModal(res.token);
    });
  });

  function showTokenModal(token) {
    qs("token-modal-value").textContent = token;
    qs("token-modal").classList.remove("hidden");
  }

  function hideTokenModal() {
    qs("token-modal").classList.add("hidden");
  }

  qs("token-modal-copy").addEventListener("click", function () {
    copyText(qs("token-modal-value").textContent, "新令牌已复制");
  });

  qs("token-modal-close").addEventListener("click", hideTokenModal);

  // ------------------------------------------------------------------ bootstrap

  function boot() {
    var token = "";
    try { token = sessionStorage.getItem("nfentik_usertoken") || ""; } catch (e) {}
    if (token) {
      state.authMode = "token";
      state.token = token;
      enterMain();
      return;
    }
    // No stored token: try an existing account session cookie.
    fetch("/api/user/account/me", { headers: { "Content-Type": "application/json" } })
      .then(function (res) {
        if (!res.ok) return null;
        return res.json();
      })
      .then(function (data) {
        if (data && data.success && data.account) {
          state.authMode = "account";
          state.account = data.account;
          state.user = data.user || null;
          state.token = (data.user && data.user.token) || "";
          state.csrf = readCookie("nfentik_csrf");
          enterMain();
          return;
        }
        showLogin();
      })
      .catch(function () { showLogin(); });
  }

  boot();
})();
