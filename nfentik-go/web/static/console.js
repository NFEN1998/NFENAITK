(function () {
  "use strict";

  var app = document.getElementById("app");
  var state = {
    token: "",
    view: "dashboard",
    page: 1,
    pageSize: 20,
    total: 0,
    sort: "desc",
    stream: null,
    live: true,
  };

  // siteName mirrors the system setting so generated OCS config carries the
  // configured question bank name instead of a hard-coded one.
  function readSiteName() {
    var m = document.querySelector('meta[name="app-site-name"]');
    return m && m.content ? m.content : "nfentik";
  }

  var siteName = readSiteName();

  function qs(id) { return document.getElementById(id); }

  function headers() {
    var h = { "Content-Type": "application/json" };
    if (state.token) h["Authorization"] = "Bearer " + state.token;
    return h;
  }

  function api(path, options) {
    options = options || {};
    options.headers = Object.assign(headers(), options.headers || {});
    return fetch("/api/admin" + path, options).then(function (res) {
      if (res.status === 401) {
        showLogin();
        throw new Error("未授权");
      }
      return res.json().catch(function () { return { success: false, message: "响应解析失败" }; });
    });
  }

  function esc(text) {
    return String(text == null ? "" : text)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
  }

  // toast shows a styled, auto-dismissing notification. type is one of
  // "success" | "error" | "warn" | "info" (default).
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
      wrap.className = "modal confirm-modal";
      wrap.innerHTML =
        '<div class="modal-body confirm-body">' +
        '<h3>' + esc(opts.title || "确认操作") + "</h3>" +
        '<p class="confirm-msg">' + esc(message) + "</p>" +
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

  // ---------------------------------------------------------------- navigation

  var titles = {
    dashboard: "概览", questions: "题库", pending: "待修正",
    models: "模型配置", users: "用户管理", accounts: "账号管理", redeem: "卡密管理",
    plans: "套餐设置", ocs: "OCS 配置", settings: "系统设置", logs: "请求日志",
    external: "第三方题库", import: "导入题库",
  };

  var subtitles = {
    dashboard: "运行概览与近 14 天请求量",
    questions: "本地题库的题目、来源与状态管理",
    pending: "被标记为待修正的题目",
    import: "从 MOGO 控制台按增量方式导入题库",
    models: "配置 AI 供应商、模型与调用优先级",
    external: "本地未命中时按优先级调用的第三方题库",
    users: "用户令牌、套餐与配额管理",
    accounts: "用户中心账号、绑定与登录状态",
    redeem: "生成与作废用户充值卡密",
    plans: "配置时长套餐、次数套餐与上下架状态",
    ocs: "生成 OCS 用户脚本所需的查询配置",
    settings: "站点、网络、缓存与安全设置",
    logs: "实时与历史的请求审计日志",
  };

  function switchView(view) {
    state.view = view;
    document.querySelectorAll(".nav-item").forEach(function (btn) {
      btn.classList.toggle("active", btn.dataset.view === view);
    });
    document.querySelectorAll(".view").forEach(function (sec) {
      sec.classList.toggle("active", sec.id === "view-" + view);
    });
    qs("view-title").textContent = titles[view] || view;
    var sub = qs("view-subtitle");
    if (sub) sub.textContent = subtitles[view] || "";
    if (view === "dashboard") loadDashboard();
    if (view === "questions") loadQuestions();
    if (view === "pending") loadPending();
    if (view === "import") loadMogoPanel();
    if (view === "models") loadModelConfig();
    if (view === "external") loadExternalBanks();
    if (view === "users") loadUsers();
    if (view === "accounts") loadAccounts();
    if (view === "redeem") loadRedeem();
    if (view === "plans") loadPlans();
    if (view === "ocs") loadOCS();
    if (view === "settings") loadSettings();
    if (view === "logs") loadLogs();
  }

  document.querySelectorAll(".nav-item").forEach(function (btn) {
    btn.addEventListener("click", function () {
      switchView(btn.dataset.view);
      setDrawer(false);
    });
  });
  qs("refresh-btn").addEventListener("click", function () { switchView(state.view); });

  // ---- mobile navigation drawer ----
  var sidebar = qs("sidebar");
  var navToggle = qs("nav-toggle");
  var navBackdrop = qs("nav-backdrop");

  function setDrawer(open) {
    if (!sidebar) return;
    sidebar.classList.toggle("open", open);
    if (navBackdrop) navBackdrop.classList.toggle("show", open);
    if (navToggle) navToggle.setAttribute("aria-expanded", open ? "true" : "false");
  }

  if (navToggle) {
    navToggle.addEventListener("click", function () {
      setDrawer(!sidebar.classList.contains("open"));
    });
  }
  if (navBackdrop) navBackdrop.addEventListener("click", function () { setDrawer(false); });
  document.addEventListener("keydown", function (e) { if (e.key === "Escape") setDrawer(false); });
  window.addEventListener("resize", function () { if (window.innerWidth > 720) setDrawer(false); });

  // ------------------------------------------------------------------ dashboard

  function loadDashboard() {
    api("/stats").then(function (data) {
      if (!data.success) { toast(data.message); return; }
      qs("stat-pending").textContent = data.pending;
      qs("stat-requests").textContent = data.total_requests;
      qs("stat-subs").textContent = data.subscribers;
      qs("stat-datapath").textContent = data.data_path;
      qs("stat-schemaversion").textContent = data.schema_version || "-";
      renderChart(data.daily || []);
    });
  }

  function renderChart(daily) {
    var el = qs("daily-chart");
    if (!daily.length) { el.innerHTML = "暂无数据"; return; }
    var recent = daily.slice(-14);
    var max = Math.max.apply(null, recent.map(function (d) { return d.count; })) || 1;
    el.innerHTML = recent.map(function (d) {
      var height = Math.max(3, Math.round((d.count / max) * 100));
      var label = (d.date || "").slice(5);
      return '<div class="bar" style="height:' + height + '%" title="' + esc(d.date) + ': ' + d.count + '">' +
        '<span class="count">' + d.count + '</span>' +
        '<span>' + esc(label) + '</span>' +
      '</div>';
    }).join("");
  }

  // ------------------------------------------------------------------ questions

  function loadQuestions() {
    var params = new URLSearchParams();
    params.set("page", state.page);
    params.set("page_size", state.pageSize);
    params.set("sort", state.sort);
    var keyword = qs("q-search").value.trim();

    var promise = keyword
      ? api("/questions/search?keyword=" + encodeURIComponent(keyword))
          .then(function (data) {
            return data.success
              ? { success: true, items: data.items, total: data.items.length }
              : data;
          })
      : api("/questions?" + params.toString());

    promise.then(function (data) {
      if (!data.success) { toast(data.message); return; }
      state.total = data.total || 0;
      renderQuestions(data.items || []);
      qs("q-pageinfo").textContent = "第 " + state.page + " 页 · 共 " + state.total + " 条";
    });
  }

  function renderQuestions(items) {
    var tbody = qs("q-table").querySelector("tbody");
    if (!items.length) {
      tbody.innerHTML = '<tr><td colspan="7" class="muted">暂无题目</td></tr>';
      return;
    }
    tbody.innerHTML = items.map(function (item) {
      var source = item.is_ai
        ? '<span class="pill ai">AI</span>'
        : '<span class="pill manual">手动</span>';
      var status = item.is_pending_correction
        ? '<span class="pill pending">待修正</span>' : '<span class="muted">正常</span>';
      return "<tr>" +
        '<td data-label="ID">' + item.id + "</td>" +
        '<td data-label="题目"><span class="clamp">' + esc(item.question) + "</span></td>" +
        '<td data-label="答案"><span class="clamp">' + esc(item.answer) + "</span></td>" +
        '<td data-label="题型" class="nowrap">' + esc(questionTypeLabel(item.question_type)) + "</td>" +
        '<td data-label="来源">' + source + "</td>" +
        '<td data-label="状态">' + status + "</td>" +
        '<td data-label="操作" class="row-actions">' +
          '<button class="btn ghost small" data-edit="' + item.id + '">编辑</button>' +
          '<button class="btn danger small" data-del="' + item.id + '">删除</button>' +
        "</td>" +
      "</tr>";
    }).join("");

    tbody.querySelectorAll("[data-edit]").forEach(function (btn) {
      btn.addEventListener("click", function () { editQuestion(btn.dataset.edit); });
    });
    tbody.querySelectorAll("[data-del]").forEach(function (btn) {
      btn.addEventListener("click", function () { deleteQuestion(btn.dataset.del); });
    });
  }

  function editQuestion(id) {
    api("/questions/" + id).then(function (data) {
      if (!data.success) { toast(data.message); return; }
      var item = data.item;
      openModal("编辑题目 #" + id, [
        { name: "question", label: "题目", type: "textarea", value: item.question },
        { name: "options", label: "选项", type: "textarea", value: item.options || "" },
        { name: "answer", label: "答案", type: "textarea", value: item.answer || "" },
        { name: "question_type", label: "题型", options: [{ value: "", label: "未指定" }].concat(QUESTION_TYPE_OPTIONS), value: questionTypeSelectValue(item.question_type) },
      ], function (values) {
        api("/questions/" + id, {
          method: "PUT",
          body: JSON.stringify({
            question: values.question,
            options: values.options,
            answer: values.answer,
            question_type: values.question_type,
          }),
        }).then(function (res) {
          if (!res.success) { toast(res.message); return; }
          closeModal();
          loadQuestions();
        });
      });
    });
  }

  async function deleteQuestion(id) {
    if (!(await confirmDialog("确定删除题目 #" + id + " 吗？", { danger: true, okText: "删除" }))) return;
    api("/questions/" + id, { method: "DELETE" }).then(function (res) {
      if (!res.success) { toast(res.message, "error"); return; }
      toast("已删除题目 #" + id, "success");
      loadQuestions();
    });
  }

  qs("q-search-btn").addEventListener("click", function () { state.page = 1; loadQuestions(); });
  qs("q-search").addEventListener("keydown", function (e) {
    if (e.key === "Enter") { state.page = 1; loadQuestions(); }
  });
  qs("q-sort").addEventListener("change", function () { state.sort = this.value; loadQuestions(); });
  qs("q-prev").addEventListener("click", function () {
    if (state.page > 1) { state.page--; loadQuestions(); }
  });
  qs("q-next").addEventListener("click", function () {
    if (state.page * state.pageSize < state.total) { state.page++; loadQuestions(); }
  });
  qs("q-add-btn").addEventListener("click", function () {
    openModal("新增题目", [
      { name: "question", label: "题目", type: "textarea", value: "" },
      { name: "options", label: "选项", type: "textarea", value: "" },
      { name: "answer", label: "答案", type: "textarea", value: "" },
      { name: "question_type", label: "题型", options: [{ value: "", label: "未指定" }].concat(QUESTION_TYPE_OPTIONS), value: "" },
    ], function (values) {
      api("/questions", {
        method: "POST",
        body: JSON.stringify({
          question: values.question, options: values.options, answer: values.answer,
          question_type: values.question_type,
        }),
      }).then(function (res) {
        if (!res.success) { toast(res.message); return; }
        closeModal();
        loadQuestions();
      });
    });
  });

  // -------------------------------------------------------------------- pending

  function loadPending() {
    api("/questions?pending=true&page=1&page_size=200").then(function (data) {
      if (!data.success) { toast(data.message); return; }
      var tbody = qs("p-table").querySelector("tbody");
      if (!data.items.length) {
        tbody.innerHTML = '<tr><td colspan="4" class="muted">没有待修正题目</td></tr>';
        return;
      }
      tbody.innerHTML = data.items.map(function (item) {
        return "<tr>" +
          "<td>" + item.id + "</td>" +
          '<td class="clamp-1" title="' + esc(item.question) + '">' + esc(item.question) + "</td>" +
        '<td class="clamp-1" title="' + esc(item.answer) + '">' + esc(item.answer) + "</td>" +
          "<td>" +
            '<button class="btn ghost" data-fix="' + item.id + '">编辑</button> ' +
            '<button class="btn" data-clear="' + item.id + '">取消标记</button>' +
          "</td>" +
        "</tr>";
      }).join("");
      tbody.querySelectorAll("[data-fix]").forEach(function (btn) {
        btn.addEventListener("click", function () { editQuestion(btn.dataset.fix); });
      });
      tbody.querySelectorAll("[data-clear]").forEach(function (btn) {
        btn.addEventListener("click", function () {
          api("/questions/" + btn.dataset.clear + "/pending-correction?value=false", { method: "POST" })
            .then(function (res) { if (!res.success) { toast(res.message); return; } loadPending(); });
        });
      });
    });
  }

  // --------------------------------------------------------------- model config

  // Live model configuration edited through the visual form. `modelConfig` is
  // the single source of truth; the form re-renders from it on every change.
  var modelConfig = null;

  function emptyModel() {
    return {
      id: "", name: "", displayName: "", platformId: "",
      maxTokens: 4096, temperature: 0.3, topP: 1, enabled: true,
      category: "text", description: "", enableThinking: false,
    };
  }

  function emptyPlatform() {
    return {
      id: "", name: "", displayName: "", baseUrl: "", apiKey: "",
      enabled: true, models: [], customHeaders: {},
    };
  }

  function normalizeModelConfig(cfg) {
    cfg = cfg || {};
    cfg.platforms = cfg.platforms || [];
    cfg.selectedTextModels = cfg.selectedTextModels || [];
    cfg.selectedSummaryModels = cfg.selectedSummaryModels || [];
    cfg.platforms.forEach(function (p) {
      p.models = p.models || [];
      p.customHeaders = p.customHeaders || {};
      p.models.forEach(function (m) { if (m.platformId == null) m.platformId = p.id; });
    });
    return cfg;
  }

  function loadModelConfig() {
    api("/model-config").then(function (data) {
      if (!data.success) { toast(data.message); return; }
      modelConfig = normalizeModelConfig(data.config);
      qs("m-editor").value = JSON.stringify(modelConfig, null, 2);
      renderModelForm();
      populateTestModels(modelConfig);
    });
  }

  function renderModelForm() {
    renderModelSelectors();
    renderPlatforms();
  }

  function allModels() {
    var out = [];
    (modelConfig.platforms || []).forEach(function (p) {
      (p.models || []).forEach(function (m) {
        out.push({ model: m, platform: p });
      });
    });
    return out;
  }

  function modelLabel(m) { return m.displayName || m.name || m.id || "(未命名模型)"; }

  var CATEGORY_LABELS = { text: "文本", vision: "视觉", summary: "总结", reasoning: "推理" };
  function categoryLabel(c) { return CATEGORY_LABELS[c] || c || "文本"; }

  var QUESTION_TYPE_OPTIONS = [
    { value: "single_choice", label: "单选题" },
    { value: "multiple_choice", label: "多选题" },
    { value: "judgement", label: "判断题" },
    { value: "completion", label: "填空题" },
    { value: "essay", label: "简答题" },
  ];
  function questionTypeLabel(t) {
    var v = (t || "").trim();
    if (!v) return "";
    var low = v.toLowerCase();
    if (low.indexOf("single") >= 0 || v.indexOf("单选") >= 0 || v.indexOf("单项选择") >= 0) return "单选题";
    if (low.indexOf("multiple") >= 0 || v.indexOf("多选") >= 0 || v.indexOf("多项选择") >= 0) return "多选题";
    if (low.indexOf("judgement") >= 0 || low.indexOf("judgment") >= 0 || v.indexOf("判断") >= 0) return "判断题";
    if (low.indexOf("completion") >= 0 || low.indexOf("fill") >= 0 || v.indexOf("填空") >= 0) return "填空题";
    if (low.indexOf("essay") >= 0 || low.indexOf("short_answer") >= 0 || low.indexOf("short-answer") >= 0 ||
        v.indexOf("简答") >= 0 || v.indexOf("问答") >= 0 || v.indexOf("论述") >= 0) return "简答题";
    return v;
  }
  function questionTypeSelectValue(t) {
    var v = (t || "").trim();
    var low = v.toLowerCase();
    if (low.indexOf("single") >= 0 || v.indexOf("单选") >= 0 || v.indexOf("单项选择") >= 0) return "single_choice";
    if (low.indexOf("multiple") >= 0 || v.indexOf("多选") >= 0 || v.indexOf("多项选择") >= 0) return "multiple_choice";
    if (low.indexOf("judgement") >= 0 || low.indexOf("judgment") >= 0 || v.indexOf("判断") >= 0) return "judgement";
    if (low.indexOf("completion") >= 0 || low.indexOf("fill") >= 0 || v.indexOf("填空") >= 0) return "completion";
    if (low.indexOf("essay") >= 0 || low.indexOf("short_answer") >= 0 || low.indexOf("short-answer") >= 0 ||
        v.indexOf("简答") >= 0 || v.indexOf("问答") >= 0 || v.indexOf("论述") >= 0) return "essay";
    return "";
  }

  function renderModelSelectors() {
    var models = allModels();

    var textSel = qs("m-text-models");
    textSel.innerHTML = models.map(function (e) {
      var sel = (modelConfig.selectedTextModels || []).indexOf(e.model.id) >= 0 ? " selected" : "";
      return '<option value="' + esc(e.model.id) + '"' + sel + ">" + esc(modelLabel(e.model)) +
        " (" + esc(categoryLabel(e.model.category || "text")) + ")</option>";
    }).join("");

    function singleSelect(el, current) {
      el.innerHTML = '<option value="">（未选择）</option>' + models.map(function (e) {
        var sel = current === e.model.id ? " selected" : "";
        return '<option value="' + esc(e.model.id) + '"' + sel + ">" + esc(modelLabel(e.model)) + "</option>";
      }).join("");
    }
    singleSelect(qs("m-vision-model"), modelConfig.selectedVisionModel || "");
    singleSelect(qs("m-summary-model"), modelConfig.selectedSummaryModel || "");
  }

  function field(label, control, hint) {
    return '<div class="field"><label>' + esc(label) + "</label>" + control +
      (hint ? '<span class="muted small">' + esc(hint) + "</span>" : "") + "</div>";
  }

  function renderPlatforms() {
    var host = qs("m-platforms");
    if (!modelConfig.platforms.length) {
      host.innerHTML = '<div class="card"><p class="empty-hint">还没有配置任何 AI 平台，点击右上角「新增平台」开始。</p></div>';
      return;
    }
    host.innerHTML = modelConfig.platforms.map(function (p, pi) {
      var badge = p.enabled
        ? '<span class="badge on">已启用</span>'
        : '<span class="badge">已停用</span>';
      var models = (p.models || []).map(function (m, mi) { return modelRowHTML(p, m, pi, mi); }).join("");
      if (!models) models = '<p class="empty-hint">该平台下还没有模型。</p>';

      return '<div class="card platform-card">' +
        '<div class="platform-head">' +
          '<span class="title">' + esc(p.displayName || p.name || p.id || "未命名平台") + "</span>" + badge +
          '<button class="btn ghost" data-toggle-platform="' + pi + '">' + (p.enabled ? "停用" : "启用") + "</button>" +
          '<button class="btn danger" data-del-platform="' + pi + '">删除平台</button>' +
        "</div>" +
        '<div class="grid-3">' +
          field("平台标识 id", '<input data-p="' + pi + '" data-f="id" value="' + esc(p.id) + '">', "用于关联模型，建议英文") +
          field("显示名称", '<input data-p="' + pi + '" data-f="displayName" value="' + esc(p.displayName) + '">') +
          field("Base URL", '<input data-p="' + pi + '" data-f="baseUrl" value="' + esc(p.baseUrl) + '" placeholder="https://api.openai.com/v1">') +
        "</div>" +
        field("API Key", '<input type="password" data-p="' + pi + '" data-f="apiKey" value="' + esc(p.apiKey) + '" placeholder="sk-...">') +
        renderHeaders(p, pi) +
        '<div class="section-title">模型列表</div>' + models +
        '<button class="btn" style="margin-top:10px;" data-add-model="' + pi + '">新增模型</button>' +
      "</div>";
    }).join("");
    bindPlatformEvents();
    bindModelEvents();
  }

  function renderHeaders(p, pi) {
    var keys = Object.keys(p.customHeaders || {});
    var rows = keys.map(function (k, hi) {
      return '<div class="header-row">' +
        '<input data-hp="' + pi + '" data-hi="' + hi + '" data-hf="k" value="' + esc(k) + '" placeholder="Header 名称">' +
        '<input data-hp="' + pi + '" data-hi="' + hi + '" data-hf="v" value="' + esc(p.customHeaders[k]) + '" placeholder="值">' +
        '<button class="icon-btn" data-del-header="' + pi + ':' + hi + '">删除</button>' +
      "</div>";
    }).join("");
    return '<div class="field"><label>自定义请求头（可选）</label><div class="headers-list">' +
      rows + "</div>" +
      '<button class="btn ghost" style="margin-top:6px;" data-add-header="' + pi + '">新增请求头</button></div>';
  }

  function modelRowHTML(p, m, pi, mi) {
    var prefix = 'data-p="' + pi + '" data-m="' + mi + '"';
    var catOptions = ["text", "vision", "summary", "reasoning"].map(function (c) {
      return '<option value="' + c + '"' + (m.category === c ? " selected" : "") + ">" + categoryLabel(c) + "</option>";
    }).join("");
    return '<div class="model-row">' +
      '<div class="row-head">' +
        '<span class="name">' + esc(modelLabel(m)) + "</span>" +
        '<label class="checkbox"><input type="checkbox" ' + prefix + ' data-mf="enabled"' + (m.enabled ? " checked" : "") + "> 启用</label>" +
        '<label class="checkbox"><input type="checkbox" ' + prefix + ' data-mf="enableThinking"' + (m.enableThinking ? " checked" : "") + "> 思考模式</label>" +
        '<button class="icon-btn" data-del-model="' + pi + ':' + mi + '">删除</button>' +
      "</div>" +
      '<div class="model-grid">' +
        field("模型 id", '<input ' + prefix + ' data-mf="id" value="' + esc(m.id) + '">') +
        field("显示名称", '<input ' + prefix + ' data-mf="displayName" value="' + esc(m.displayName) + '">') +
        field("类别", '<select ' + prefix + ' data-mf="category">' + catOptions + "</select>") +
        field("最大 Token", '<input type="number" min="1" ' + prefix + ' data-mf="maxTokens" value="' + esc(m.maxTokens) + '">') +
        field("Temperature", '<input type="number" step="0.1" min="0" max="2" ' + prefix + ' data-mf="temperature" value="' + esc(m.temperature) + '">') +
        field("Top P", '<input type="number" step="0.05" min="0" max="1" ' + prefix + ' data-mf="topP" value="' + esc(m.topP) + '">') +
      "</div>" +
    "</div>";
  }

  function readField(el) {
    var v = el.value;
    if (el.type === "number") {
      var n = Number(v);
      return isNaN(n) ? 0 : n;
    }
    return v;
  }

  function bindPlatformEvents() {
    var host = qs("m-platforms");

    host.querySelectorAll("[data-p][data-f]").forEach(function (el) {
      el.addEventListener("input", function () {
        var pi = Number(el.dataset.p);
        var f = el.dataset.f;
        modelConfig.platforms[pi][f] = readField(el);
        if (f === "id") {
          // Keep child models pointing at the renamed platform.
          (modelConfig.platforms[pi].models || []).forEach(function (m) { m.platformId = el.value; });
        }
        if (f === "id" || f === "displayName") refreshLabels();
      });
    });

    host.querySelectorAll("[data-add-model]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var pi = Number(btn.dataset.addModel);
        var m = emptyModel();
        m.platformId = modelConfig.platforms[pi].id;
        modelConfig.platforms[pi].models.push(m);
        renderModelForm();
      });
    });
    host.querySelectorAll("[data-del-platform]").forEach(function (btn) {
      btn.addEventListener("click", async function () {
        var pi = Number(btn.dataset.delPlatform);
        var p = modelConfig.platforms[pi];
        if (!(await confirmDialog("删除平台「" + (p.displayName || p.name || p.id) + "」及其模型？", { danger: true, okText: "删除" }))) return;
        modelConfig.platforms.splice(pi, 1);
        renderModelForm();
      });
    });
    host.querySelectorAll("[data-toggle-platform]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var pi = Number(btn.dataset.togglePlatform);
        modelConfig.platforms[pi].enabled = !modelConfig.platforms[pi].enabled;
        renderModelForm();
      });
    });
    host.querySelectorAll("[data-add-header]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var pi = Number(btn.dataset.addHeader);
        var headers = modelConfig.platforms[pi].customHeaders;
        var key = "X-Custom-" + (Object.keys(headers).length + 1);
        headers[key] = "";
        renderModelForm();
      });
    });
    host.querySelectorAll("[data-del-header]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var parts = btn.dataset.delHeader.split(":");
        var pi = Number(parts[0]);
        var keys = Object.keys(modelConfig.platforms[pi].customHeaders);
        delete modelConfig.platforms[pi].customHeaders[keys[Number(parts[1])]];
        renderModelForm();
      });
    });
    host.querySelectorAll("[data-hp][data-hf]").forEach(function (el) {
      el.addEventListener("input", function () {
        var pi = Number(el.dataset.hp);
        var hi = Number(el.dataset.hi);
        var key = el.dataset.hf;
        var headers = modelConfig.platforms[pi].customHeaders;
        var keys = Object.keys(headers);
        var oldKey = keys[hi];
        if (key === "k") {
          if (el.value === oldKey) return;
          var val = headers[oldKey];
          delete headers[oldKey];
          headers[el.value] = val;
        } else {
          headers[oldKey] = el.value;
        }
      });
    });
  }

  function bindModelEvents() {
    var host = qs("m-platforms");
    host.querySelectorAll("[data-p][data-m][data-mf]").forEach(function (el) {
      var handler = function () {
        var pi = Number(el.dataset.p);
        var mi = Number(el.dataset.m);
        var m = modelConfig.platforms[pi].models[mi];
        var f = el.dataset.mf;
        m[f] = el.type === "checkbox" ? el.checked : readField(el);
        if (f === "displayName" || f === "id") refreshLabels();
      };
      if (el.type === "checkbox" || el.tagName === "SELECT") {
        el.addEventListener("change", handler);
      } else {
        el.addEventListener("input", handler);
      }
    });
    host.querySelectorAll("[data-del-model]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var parts = btn.dataset.delModel.split(":");
        var pi = Number(parts[0]);
        var mi = Number(parts[1]);
        modelConfig.platforms[pi].models.splice(mi, 1);
        renderModelForm();
      });
    });
  }

  // refreshLabels updates the dropdown labels without rebuilding inputs so the
  // user does not lose focus while typing.
  function refreshLabels() {
    var models = allModels();
    var textSel = qs("m-text-models");
    Array.prototype.forEach.call(textSel.options, function (opt) {
      var found = models.filter(function (e) { return e.model.id === opt.value; })[0];
      if (found) opt.textContent = modelLabel(found.model) + " (" + categoryLabel(found.model.category || "text") + ")";
    });
  }

  qs("m-text-models").addEventListener("change", function () {
    modelConfig.selectedTextModels = Array.prototype.slice.call(this.selectedOptions)
      .map(function (o) { return o.value; }).filter(Boolean);
  });
  qs("m-vision-model").addEventListener("change", function () {
    modelConfig.selectedVisionModel = this.value || null;
  });
  qs("m-summary-model").addEventListener("change", function () {
    modelConfig.selectedSummaryModel = this.value || null;
  });

  qs("m-add-platform").addEventListener("click", function () {
    modelConfig.platforms.push(emptyPlatform());
    renderModelForm();
  });

  qs("m-raw-toggle").addEventListener("change", function () {
    var raw = this.checked;
    qs("m-visual").classList.toggle("hidden", raw);
    qs("m-editor").classList.toggle("hidden", !raw);
    if (raw) {
      qs("m-editor").value = JSON.stringify(modelConfig, null, 2);
    } else {
      try { modelConfig = normalizeModelConfig(JSON.parse(qs("m-editor").value)); renderModelForm(); }
      catch (e) { qs("m-status").textContent = "JSON 格式错误，无法切回可视化: " + e.message; this.checked = true; qs("m-visual").classList.add("hidden"); qs("m-editor").classList.remove("hidden"); }
    }
  });

  var testStream = null;

  qs("t-run").addEventListener("click", function () {
    var output = qs("t-output");
    var query = qs("t-query").value.trim();
    if (!query) { output.textContent = "请输入测试题目"; return; }
    output.textContent = "";

    if (testStream) { testStream.close(); testStream = null; }

    // Use fetch so we can POST and read the SSE body incrementally.
    fetch("/api/model/stream", {
      method: "POST",
      headers: headers(),
      body: JSON.stringify({ query: query, model_id: qs("t-model").value }),
    }).then(function (res) {
      if (!res.ok) throw new Error("HTTP " + res.status);
      var reader = res.body.getReader();
      var decoder = new TextDecoder();
      var buffer = "";
      (function pump() {
        reader.read().then(function (result) {
          if (result.done) return;
          buffer += decoder.decode(result.value, { stream: true });
          var parts = buffer.split("\n\n");
          buffer = parts.pop();
          parts.forEach(function (block) {
            var eventName = "";
            var data = "";
            block.split("\n").forEach(function (line) {
              if (line.indexOf("event:") === 0) eventName = line.slice(6).trim();
              if (line.indexOf("data:") === 0) data += line.slice(5).trim();
            });
            if (!data) return;
            var evt = JSON.parse(data);
            if (eventName === "delta") output.textContent = evt.full_reasoning
              ? evt.full_reasoning + "\n---\n" + evt.full_content : evt.full_content;
            if (eventName === "done") output.textContent += "\n\n[答案] " + (evt.answer || "(空)");
            if (eventName === "error") output.textContent = "[错误] " + evt.message;
          });
          pump();
        });
      })();
    }).catch(function (err) { output.textContent = "请求失败: " + err.message; });
  });

  qs("m-save").addEventListener("click", function () {
    var payload;
    if (qs("m-raw-toggle").checked) {
      try { payload = JSON.parse(qs("m-editor").value); }
      catch (e) { qs("m-status").textContent = "JSON 格式错误: " + e.message; return; }
    } else {
      // Validate that every model id is present before saving.
      var invalid = allModels().filter(function (e) { return !e.model.id; })[0];
      if (invalid) { qs("m-status").textContent = "存在未填写 id 的模型，请补齐后再保存"; return; }
      payload = modelConfig;
    }
    api("/model-config", { method: "PUT", body: JSON.stringify(payload) }).then(function (res) {
      if (!res.success) { qs("m-status").textContent = res.message || "保存失败"; return; }
      qs("m-status").textContent = "已保存";
      loadModelConfig();
    });
  });

  // ------------------------------------------------------------------- settings

  function loadSettings() {
    api("/settings").then(function (data) {
      if (!data.success) { toast(data.message); return; }
      var s = data.settings;
      qs("s-editor").value = JSON.stringify(s, null, 2);
      settingsJSON = s;
      if (s.siteName) {
        siteName = s.siteName;
        if (state.view === "ocs") renderOCS();
      }
      ensurePlans(function () { fillSettingsForm(s); });
    });
  }

  var settingsJSON = {};

  function fillSettingsForm(s) {
    qs("s-site-name").value = s.siteName || "nfentik";
    qs("s-site-subtitle").value = s.siteSubtitle || "智能题库查询服务";
    qs("s-port").value = (s.network && s.network.serverPort) || 3000;
    qs("s-bind").value = (s.network && s.network.bindAddress) || "";
    qs("s-lan").checked = !!(s.network && s.network.enableLanAccess);
    // Connection strings are read-only in the console; they live in the
    // bootstrap file next to the executable.
    qs("s-db-url").value = (s.database && s.database.url) || "";
    var r = s.redis || {};
    qs("s-redis-enabled").value = r.enabled ? "已启用" : "未启用";
    qs("s-redis-url").value = r.url || "";
    qs("s-redis-prefix").value = r.prefix || "nfentik";
    qs("s-redis-ttl").value = r.queryCacheTTL || 600;
    qs("s-model-timeout").value = s.modelResponseTimeout || 60;
    qs("s-heartbeat-timeout").value = s.modelHeartbeatTimeout || 6;
    qs("s-admin-token").value = s.adminToken || "";
    qs("s-users-enabled").checked = s.usersEnabled !== false;
    qs("s-require-token").checked = !!s.requireTokenForQuery;
    qs("s-ulog-days").value = s.userLogRetentionDays || 30;
    qs("s-ulog-peruser").value = s.userLogPerUserLimit || 200;
    qs("s-ulog-global").value = s.userLogGlobalLimit || 50000;
    qs("s-uaudit-days").value = s.userAuditRetentionDays || 90;
    qs("s-account-registration").checked = s.accountRegistrationEnabled !== false;
    qs("s-account-session-days").value = s.accountSessionTTLDays || 30;
    qs("s-account-login-failures").value = s.accountLoginMaxFailures || 5;
    qs("s-account-login-window").value = s.accountLoginWindowMinutes || 15;
    qs("s-redeem-failures").value = s.redeemMaxFailuresPerHour || 10;
    fillPlanOptions(qs("s-default-plan"), s.defaultPlanCode || "");
  }

  function collectSettingsForm() {
    // Start from the loaded settings so unexposed fields are preserved.
    var s = JSON.parse(JSON.stringify(settingsJSON || {}));
    s.siteName = qs("s-site-name").value.trim();
    s.siteSubtitle = qs("s-site-subtitle").value.trim();
    s.network = s.network || {};
    s.network.serverPort = Number(qs("s-port").value) || 3000;
    s.network.bindAddress = qs("s-bind").value.trim();
    s.network.enableLanAccess = qs("s-lan").checked;
    // database / redis connection fields are owned by the bootstrap file and
    // are intentionally not submitted here; only the cache TTL is editable.
    s.redis = s.redis || {};
    s.redis.queryCacheTTL = Number(qs("s-redis-ttl").value) || 600;
    s.modelResponseTimeout = Number(qs("s-model-timeout").value) || 60;
    s.modelHeartbeatTimeout = Number(qs("s-heartbeat-timeout").value) || 6;
    s.adminToken = qs("s-admin-token").value.trim();
    s.usersEnabled = qs("s-users-enabled").checked;
    s.requireTokenForQuery = qs("s-require-token").checked;
    s.userLogRetentionDays = Number(qs("s-ulog-days").value) || 30;
    s.userLogPerUserLimit = Number(qs("s-ulog-peruser").value) || 200;
    s.userLogGlobalLimit = Number(qs("s-ulog-global").value) || 50000;
    s.userAuditRetentionDays = Number(qs("s-uaudit-days").value) || 90;
    s.defaultPlanCode = qs("s-default-plan").value || "";
    s.accountRegistrationEnabled = qs("s-account-registration").checked;
    s.accountSessionTTLDays = Number(qs("s-account-session-days").value) || 30;
    s.accountLoginMaxFailures = Number(qs("s-account-login-failures").value) || 5;
    s.accountLoginWindowMinutes = Number(qs("s-account-login-window").value) || 15;
    s.redeemMaxFailuresPerHour = Number(qs("s-redeem-failures").value) || 10;
    return s;
  }

  qs("s-raw-toggle").addEventListener("change", function () {
    var raw = this.checked;
    qs("s-visual").classList.toggle("hidden", raw);
    qs("s-editor").classList.toggle("hidden", !raw);
    if (raw) {
      qs("s-editor").value = JSON.stringify(settingsJSON, null, 2);
    } else {
      try { settingsJSON = JSON.parse(qs("s-editor").value); fillSettingsForm(settingsJSON); }
      catch (e) { qs("s-status").textContent = "JSON 格式错误，无法切回可视化: " + e.message; this.checked = true; qs("s-visual").classList.add("hidden"); qs("s-editor").classList.remove("hidden"); }
    }
  });

  qs("s-save").addEventListener("click", function () {
    var payload;
    if (qs("s-raw-toggle").checked) {
      try { payload = JSON.parse(qs("s-editor").value); }
      catch (e) { qs("s-status").textContent = "JSON 格式错误: " + e.message; return; }
    } else {
      payload = collectSettingsForm();
    }
    api("/settings", { method: "PUT", body: JSON.stringify(payload) }).then(function (res) {
      if (!res.success) { qs("s-status").textContent = res.message || "保存失败"; return; }
      qs("s-status").textContent = "已保存" + (payload.network && payload.network.serverPort ? "（端口与监听地址需重启服务生效）" : "");
      loadSettings();
    });
  });

  // ------------------------------------------------------------------------ ocs

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
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand("copy"); } catch (e) { /* ignore */ }
    document.body.removeChild(ta);
    done();
  }

  function ocsConfigObject(baseURL, token) {
    var base = String(baseURL || window.location.origin).replace(/\/+$/, "");
    return [{
      name: siteName,
      homepage: base,
      url: base + "/query",
      method: "get",
      type: "GM_xmlhttpRequest",
      contentType: "json",
      data: {
        title: "${title}",
        options: "${options}",
        type: "${type}",
        token: token || "${token}",
      },
      handler: "return (res)=>res.code === 0 ? [res.message, undefined] : [res.data.question,res.data.answer,{ai: res.data.is_ai}]",
    }];
  }

  function renderOCS() {
    var base = qs("ocs-url-base").value.trim() || window.location.origin;
    var token = qs("ocs-token").value.trim();
    qs("ocs-json").value = JSON.stringify(ocsConfigObject(base, token), null, 2);
  }

  function loadOCS() {
    if (!qs("ocs-url-base").value) qs("ocs-url-base").value = window.location.origin;
    renderOCS();
  }

  qs("ocs-url-base").addEventListener("input", renderOCS);
  qs("ocs-token").addEventListener("input", renderOCS);
  qs("ocs-reset").addEventListener("click", function () {
    qs("ocs-url-base").value = window.location.origin;
    renderOCS();
  });
  qs("ocs-copy").addEventListener("click", function () {
    copyText(qs("ocs-json").value, "OCS 配置已复制");
  });

  // ----------------------------------------------------------- external banks

  var extState = [];

  function methodOption(value, want) {
    return (value || "GET").toUpperCase() === want ? " selected" : "";
  }

  function extRow(src, i) {
    return '<tr data-index="' + i + '">' +
      '<td><input type="checkbox" data-field="enabled"' + (src.enabled ? " checked" : "") + "></td>" +
      '<td><input type="text" data-field="name" value="' + esc(src.name || "") + '"></td>' +
      '<td><input type="number" data-field="priority" value="' + (src.priority || 0) + '" style="width:80px"></td>' +
      '<td><select data-field="method"><option' + methodOption(src.method, "GET") + '>GET</option>' +
      "<option" + methodOption(src.method, "POST") + '>POST</option></select></td>' +
      '<td><input type="text" data-field="url" value="' + esc(src.url || "") + '" style="min-width:220px"></td>' +
      '<td><input type="text" data-field="answerPath" value="' + esc(src.answerPath || "") + '" style="width:110px"></td>' +
      '<td><input type="checkbox" data-field="writeBack"' + (src.writeBack ? " checked" : "") + "></td>" +
      '<td><input type="checkbox" data-field="trust"' + (src.trust ? " checked" : "") + "></td>" +
      '<td><button class="btn ghost" data-ext-test="' + i + '">测试</button> ' +
      '<button class="btn danger" data-ext-del="' + i + '">删除</button></td>' +
      "</tr>";
  }

  function renderExternalBanks() {
    var body = qs("ext-table").querySelector("tbody");
    body.innerHTML = extState.length
      ? extState.map(extRow).join("")
      : '<tr><td colspan="9" class="muted">尚未配置第三方题库</td></tr>';
  }

  function loadExternalBanks() {
    api("/external-banks").then(function (data) {
      if (!data.success) { toast(data.message); return; }
      var cfg = data.config || {};
      extState = Array.isArray(cfg.sources) ? cfg.sources : [];
      renderExternalBanks();
    });
  }

  qs("ext-table").addEventListener("change", function (e) {
    var el = e.target;
    if (!el.dataset || !el.dataset.field) return;
    var row = el.closest("tr");
    var src = extState[Number(row.getAttribute("data-index"))];
    if (!src) return;
    var field = el.dataset.field;
    if (el.type === "checkbox") src[field] = el.checked;
    else if (field === "priority") src[field] = Number(el.value) || 0;
    else src[field] = el.value;
  });

  qs("ext-table").addEventListener("click", function (e) {
    var del = e.target.closest("[data-ext-del]");
    if (del) {
      extState.splice(Number(del.getAttribute("data-ext-del")), 1);
      renderExternalBanks();
      return;
    }
    var test = e.target.closest("[data-ext-test]");
    if (test) testExternalSource(Number(test.getAttribute("data-ext-test")));
  });

  qs("ext-add").addEventListener("click", function () {
    extState.push({
      id: "src-" + Date.now(), name: "", enabled: true,
      priority: (extState.length + 1) * 10, method: "GET", url: "",
      answerPath: "data.answer", writeBack: true, trust: false, timeoutSeconds: 10,
    });
    renderExternalBanks();
  });

  qs("ext-save").addEventListener("click", function () {
    api("/external-banks", { method: "PUT", body: JSON.stringify({ sources: extState }) }).then(function (res) {
      toast(res.success ? "已保存第三方题库配置" : (res.message || "保存失败"));
      if (res.success) loadExternalBanks();
    });
  });

  function testExternalSource(i) {
    var src = extState[i];
    if (!src) return;
    var question = qs("ext-test-question").value.trim() || "1+1=";
    var result = qs("ext-test-result");
    result.className = "test-result";
    result.textContent = "测试中…";
    api("/external-banks/test", {
      method: "POST",
      body: JSON.stringify({ source: src, question: question }),
    }).then(function (res) {
      result.className = "test-result " + (res.success ? "ok" : "fail");
      result.textContent = res.success ? ("成功，答案：" + (res.answer || "")) : (res.message || "失败");
    });
  }

  // ------------------------------------------------------------ import (MOGO)

  var mogoSources = [];
  var mogoTimer = null;

  function mogoScheduleText(s) {
    s = s || {};
    if (s.type === "interval") return "每 " + (s.everyMinutes || 0) + " 分钟";
    if (s.type === "daily") return "每天 " + (s.atTime || "00:00");
    return "手动";
  }

  function mogoSourceById(id) {
    for (var i = 0; i < mogoSources.length; i++) {
      if (mogoSources[i].id === id) return mogoSources[i];
    }
    return null;
  }

  function loadMogoPanel() {
    api("/mogo/sources").then(function (data) {
      if (!data || !data.success) return;
      mogoSources = data.sources || [];
      renderMogoTable();
      refreshMogoStatus();
    });
  }

  function renderMogoTable() {
    var body = qs("ms-table").querySelector("tbody");
    if (!mogoSources.length) {
      body.innerHTML = '<tr><td colspan="7" class="muted">尚未配置 MOGO 源</td></tr>';
      return;
    }
    body.innerHTML = mogoSources.map(function (s) {
      var isDB = s.type === "database";
      var db = s.database || {};
      var addr = isDB
        ? (db.host ? esc(db.host) + ":" + esc(db.port || "3306") + "/" + esc(db.database || "") + (db.table ? " · " + esc(db.table) : "") : "-")
        : esc(s.baseUrl);
      return '<tr data-id="' + esc(s.id) + '">' +
        "<td>" + esc(s.name) + "</td>" +
        "<td>" + (isDB ? (s.overwrite ? "数据库 · 覆盖" : "数据库") : "HTTP") + "</td>" +
        '<td class="mono small">' + addr + "</td>" +
        "<td>" + esc(mogoScheduleText(s.schedule)) + "</td>" +
        "<td>" + (s.enabled ? '<span class="pill s2">启用</span>' : '<span class="pill disabled">停用</span>') + "</td>" +
        '<td class="small">' + (s.lastRun ? esc(s.lastRun) + " · 导入 " + (s.lastImported || 0) + (s.lastUpdated ? " · 更新 " + s.lastUpdated : "") : "-") +
        (s.lastError ? '<br><span class="error small">' + esc(s.lastError) + "</span>" : "") + "</td>" +
        '<td><div class="row-actions">' +
        '<button class="btn small primary" data-act="run">运行</button>' +
        '<button class="btn small ghost" data-act="edit">编辑</button>' +
        '<button class="btn small ghost" data-act="test">测试</button>' +
        '<button class="btn small danger" data-act="del">删除</button>' +
        "</div></td></tr>";
    }).join("");
  }

  function openMogoEditor(src) {
    src = src || {
      type: "http", item: 50, mode: "incremental", enabled: true,
      schedule: { type: "manual", everyMinutes: 60, atTime: "03:00" },
      throttle: { pageDelayMs: 1500, rowDelayMs: 0, rowsPerSecond: 100, batchDelayMs: 0, maxPerRun: 0 },
      database: { port: "3306", batchSize: 500, columns: {} },
    };
    var db = src.database || {};
    var cols = db.columns || {};
    var thr = src.throttle || {};
    function isDB(v) { return v.type === "database"; }
    function isHTTP(v) { return v.type !== "database"; }
    openModal(src.id ? ("编辑源 · " + src.name) : "新增 MOGO 源", [
      { name: "name", label: "名称", type: "text", value: src.name || "" },
      { name: "type", label: "类型", options: [{ value: "http", label: "HTTP 接口" }, { value: "database", label: "数据库直连" }], value: src.type || "http" },

      { name: "baseUrl", label: "MOGO 地址", type: "text", value: src.baseUrl || "", showWhen: isHTTP },
      { name: "aKey", label: "aKey", type: "text", value: src.aKey || "", showWhen: isHTTP },
      { name: "item", label: "每页条数（1-500）", type: "number", value: String(src.item || 50), showWhen: isHTTP },

      { name: "dbHost", label: "数据库主机", type: "text", value: db.host || "", showWhen: isDB },
      { name: "dbPort", label: "端口", type: "text", value: db.port || "3306", showWhen: isDB },
      { name: "dbUser", label: "用户名", type: "text", value: db.user || "", showWhen: isDB },
      { name: "dbPassword", label: "密码", type: "password", value: db.password || "", showWhen: isDB },
      { name: "dbDatabase", label: "库名", type: "text", value: db.database || "", showWhen: isDB },
      { name: "dbTable", label: "题目表名", type: "text", value: db.table || "", showWhen: isDB },
      { name: "dbColId", label: "主键列", type: "text", value: cols.id || "id", showWhen: isDB },
      { name: "dbColQuestion", label: "题目列", type: "text", value: cols.question || "question", showWhen: isDB },
      { name: "dbColAnswer", label: "答案列", type: "text", value: cols.answer || "answer", showWhen: isDB },
      { name: "dbColOptions", label: "选项列", type: "text", value: cols.options || "opt", showWhen: isDB },
      { name: "dbColType", label: "题型列", type: "text", value: cols.questionType || "questionType", showWhen: isDB },
      { name: "dbBatchSize", label: "批次大小（1-5000）", type: "number", value: String(db.batchSize || 500), showWhen: isDB },
      { name: "rowsPerSecond", label: "每秒行数（0 不限速）", type: "number", value: String(typeof thr.rowsPerSecond === "number" ? thr.rowsPerSecond : 100), showWhen: isDB },
      { name: "batchDelayMs", label: "批间延迟毫秒", type: "number", value: String(thr.batchDelayMs || 0), showWhen: isDB },
      { name: "overwrite", label: "覆盖已有答案（开启会覆盖 AI/第三方/人工修正）", options: [{ value: "0", label: "跳过已存在（默认）" }, { value: "1", label: "覆盖并更新" }], value: src.overwrite ? "1" : "0", showWhen: isDB },

      { name: "retries", label: "失败重试次数（每次间隔 5 秒）", type: "number", value: String(src.retries || 0) },
      { name: "mode", label: "模式", options: [{ value: "incremental", label: "增量" }, { value: "backfill", label: "回填全部" }], value: src.mode || "incremental" },
      { name: "schedType", label: "计划", options: [{ value: "manual", label: "手动" }, { value: "interval", label: "间隔" }, { value: "daily", label: "每天定点" }], value: (src.schedule && src.schedule.type) || "manual" },
      { name: "everyMinutes", label: "间隔分钟（间隔模式）", type: "number", value: String((src.schedule && src.schedule.everyMinutes) || 60) },
      { name: "atTime", label: "每天时间 HH:MM（定点模式）", type: "text", value: (src.schedule && src.schedule.atTime) || "03:00" },
      { name: "pageDelayMs", label: "每页间隔毫秒（HTTP 限速）", type: "number", value: String(thr.pageDelayMs || 1500), showWhen: isHTTP },
      { name: "maxPerRun", label: "单次上限条数（0 不限）", type: "number", value: String(thr.maxPerRun || 0) },
      { name: "enabled", label: "启用", options: [{ value: "1", label: "启用" }, { value: "0", label: "停用" }], value: src.enabled ? "1" : "0" },
    ], function (v) {
      var obj = {
        id: src.id || "",
        name: v.name.trim(),
        type: v.type,
        enabled: v.enabled === "1",
        baseUrl: v.baseUrl.trim(),
        aKey: v.aKey.trim(),
        item: Number(v.item) || 50,
        retries: Number(v.retries) || 0,
        mode: v.mode,
        overwrite: v.overwrite === "1",
        schedule: { type: v.schedType, everyMinutes: Number(v.everyMinutes) || 0, atTime: v.atTime.trim() },
        throttle: {
          pageDelayMs: Number(v.pageDelayMs) || 0,
          rowDelayMs: Number(thr.rowDelayMs) || 0,
          rowsPerSecond: Number(v.rowsPerSecond) || 0,
          batchDelayMs: Number(v.batchDelayMs) || 0,
          maxPerRun: Number(v.maxPerRun) || 0,
        },
        database: {
          host: v.dbHost.trim(),
          port: v.dbPort.trim(),
          user: v.dbUser.trim(),
          password: v.dbPassword,
          database: v.dbDatabase.trim(),
          table: v.dbTable.trim(),
          batchSize: Number(v.dbBatchSize) || 0,
          columns: {
            id: v.dbColId.trim(),
            question: v.dbColQuestion.trim(),
            answer: v.dbColAnswer.trim(),
            options: v.dbColOptions.trim(),
            questionType: v.dbColType.trim(),
          },
        },
        maxId: src.maxId || 0,
        lastRun: src.lastRun || "",
        lastImported: src.lastImported || 0,
        lastUpdated: src.lastUpdated || 0,
        lastError: src.lastError || "",
      };
      if (!obj.name) { toast("请填写名称", "error"); return; }
      if (obj.type === "database") {
        if (!obj.database.host || !obj.database.user || !obj.database.database || !obj.database.table) {
          toast("请填写数据库主机/用户名/库名/表名", "error"); return;
        }
      } else {
        if (!obj.baseUrl) { toast("请填写 MOGO 地址", "error"); return; }
        if (!obj.aKey) { toast("请填写 aKey", "error"); return; }
      }
      var list = mogoSources.slice();
      var idx = -1;
      for (var i = 0; i < list.length; i++) { if (list[i].id === obj.id) idx = i; }
      if (idx >= 0) list[idx] = obj; else list.push(obj);
      api("/mogo/sources", { method: "PUT", body: JSON.stringify({ sources: list }) }).then(function (res) {
        if (!res.success) { toast(res.message || "保存失败", "error"); return; }
        toast("已保存", "success");
        closeModal();
        loadMogoPanel();
      });
    });
  }

  qs("ms-table").addEventListener("click", async function (e) {
    var btn = e.target.closest("button[data-act]");
    if (!btn) return;
    var tr = btn.closest("tr");
    var id = tr.getAttribute("data-id");
    var src = mogoSourceById(id);
    if (!src) return;
    var act = btn.getAttribute("data-act");
    if (act === "edit") { openMogoEditor(src); return; }
    if (act === "test") {
      if (src.type === "database") {
        var db = src.database || {};
        api("/mogo/db-test", { method: "POST", body: JSON.stringify({
          id: src.id, host: db.host, port: db.port, user: db.user, password: db.password,
          database: db.database, table: db.table, columns: db.columns,
        }) }).then(function (res) {
          var msg = res.message || (res.success ? "连接成功" : "连接失败");
          if (res.success && res.columns) msg += "；列: " + res.columns.join(", ");
          toast(msg, res.success ? "success" : "error");
        });
      } else {
        api("/mogo/test", { method: "POST", body: JSON.stringify({ base_url: src.baseUrl, a_key: src.aKey }) }).then(function (res) {
          toast(res.message || (res.success ? "连接成功" : "连接失败"), res.success ? "success" : "error");
        });
      }
      return;
    }
    if (act === "run") {
      api("/mogo/sources/" + encodeURIComponent(id) + "/run", { method: "POST" }).then(function (res) {
        if (!res.success) { toast(res.message || "启动失败", "error"); return; }
        toast("导入已开始", "success");
        refreshMogoStatus();
      });
      return;
    }
    if (act === "del") {
      if (!(await confirmDialog("删除源「" + src.name + "」？", { danger: true, okText: "删除" }))) return;
      var list = mogoSources.filter(function (s) { return s.id !== id; });
      api("/mogo/sources", { method: "PUT", body: JSON.stringify({ sources: list }) }).then(function (res) {
        if (!res.success) { toast(res.message || "删除失败", "error"); return; }
        toast("已删除", "success");
        loadMogoPanel();
      });
    }
  });

  qs("ms-add").addEventListener("click", function () { openMogoEditor(null); });
  qs("ms-refresh").addEventListener("click", loadMogoPanel);
  qs("ms-stop").addEventListener("click", function () {
    api("/mogo/stop", { method: "POST" }).then(function (res) {
      toast(res.message || "已请求停止");
      refreshMogoStatus();
    });
  });

  function refreshMogoStatus() {
    api("/mogo/status").then(function (data) {
      if (!data.success) return;
      var st = data.state || {};
      qs("ms-running").textContent = st.running ? "运行中…" : "未运行";
      qs("ms-source").textContent = st.sourceName || "-";
      qs("ms-imported").textContent = st.imported || 0;
      qs("ms-updated").textContent = st.updated || 0;
      qs("ms-skipped").textContent = st.skipped || 0;
      qs("ms-scanned").textContent = st.scanned || 0;
      qs("ms-total").textContent = st.total || "-";
      qs("ms-current").textContent = st.currentId || "-";
      qs("ms-maxid").textContent = st.maxId || "-";
      qs("ms-error").textContent = st.error || "";
      qs("ms-stop-wrap").style.display = st.running ? "" : "none";
      clearTimeout(mogoTimer);
      if (st.running) mogoTimer = setTimeout(refreshMogoStatus, 1200);
    });
  }

  // ----------------------------------------------------------------------- logs

  var logState = { page: 1, pageSize: 50, total: 0 };
  var logCache = {};
  var logSeq = 0;

  function cacheLog(log) {
    var key = "log" + (++logSeq);
    logCache[key] = log;
    return key;
  }

  function logQuery() {
    var params = ["page=" + logState.page, "page_size=" + logState.pageSize];
    var kw = qs("l-keyword").value.trim();
    if (kw) params.push("keyword=" + encodeURIComponent(kw));
    var method = qs("l-method").value;
    if (method) params.push("method=" + encodeURIComponent(method));
    var status = qs("l-status").value;
    if (status) params.push("status=" + encodeURIComponent(status));
    var path = qs("l-path").value.trim();
    if (path) params.push("path=" + encodeURIComponent(path));
    return params.join("&");
  }

  function statusClass(status) {
    if (status == null) return "";
    if (status >= 500) return "s5";
    if (status >= 400) return "s4";
    if (status >= 300) return "s3";
    if (status >= 200) return "s2";
    return "";
  }

  function shortUA(ua) {
    if (!ua) return "-";
    return ua.length > 40 ? ua.slice(0, 40) + "…" : ua;
  }

  function logRowInner(log, key) {
    var st = log.status != null ? '<span class="pill ' + statusClass(log.status) + '">' + log.status + "</span>" : "-";
    return '<td class="mono small">' + esc(log.timestamp) + "</td>" +
      "<td>" + esc(log.method) + "</td>" +
      '<td class="mono small">' + esc(log.path) + "</td>" +
      "<td>" + st + "</td>" +
      "<td>" + (log.response_time != null ? log.response_time + "ms" : "-") + "</td>" +
      '<td class="mono small">' + esc(log.ip || "-") + "</td>" +
      '<td class="small">' + esc(shortUA(log.user_agent)) + "</td>" +
      '<td><button class="icon-btn" data-log-key="' + key + '">详情</button></td>';
  }

  function renderLogsTable(logs) {
    var body = qs("l-table").querySelector("tbody");
    body.innerHTML = logs.length
      ? logs.map(function (log) {
          return '<tr class="log-tr" data-log-key="' + cacheLog(log) + '">' + logRowInner(log) + "</tr>";
        }).join("")
      : '<tr><td colspan="8" class="muted">暂无日志</td></tr>';
    var totalPages = Math.max(1, Math.ceil(logState.total / logState.pageSize));
    qs("l-pageinfo").textContent = "第 " + logState.page + " / " + totalPages + " 页 · 共 " + logState.total + " 条";
    qs("l-prev").disabled = logState.page <= 1;
    qs("l-next").disabled = logState.page >= totalPages;
  }

  function loadLogs() {
    api("/logs?" + logQuery()).then(function (data) {
      if (!data.success) { toast(data.message); return; }
      logState.total = data.total || 0;
      renderLogsTable(data.logs || []);
    });
  }

  qs("l-search").addEventListener("click", function () { logState.page = 1; loadLogs(); });
  qs("l-refresh").addEventListener("click", loadLogs);
  qs("l-prev").addEventListener("click", function () {
    if (logState.page > 1) { logState.page--; loadLogs(); }
  });
  qs("l-next").addEventListener("click", function () {
    logState.page++; loadLogs();
  });
  qs("l-keyword").addEventListener("keydown", function (e) {
    if (e.key === "Enter") { logState.page = 1; loadLogs(); }
  });
  qs("l-path").addEventListener("keydown", function (e) {
    if (e.key === "Enter") { logState.page = 1; loadLogs(); }
  });
  qs("l-live").addEventListener("change", function () {
    state.live = this.checked;
    if (state.live) startStream();
  });
  qs("l-clear").addEventListener("click", async function () {
    if (!(await confirmDialog("清空全部请求日志？", { danger: true, okText: "清空" }))) return;
    api("/logs/clear", { method: "DELETE" }).then(function () { toast("已清空请求日志", "success"); logState.page = 1; loadLogs(); });
  });

  // Detail modal for a single log entry.
  qs("l-table").addEventListener("click", function (e) {
    var target = e.target.closest("[data-log-key]");
    if (!target) return;
    var log = logCache[target.getAttribute("data-log-key")];
    if (log) openLogDetail(log);
  });

  function prettyJSON(text) {
    if (text == null || text === "") return "";
    try { return JSON.stringify(JSON.parse(text), null, 2); } catch (e) { return text; }
  }

  function detailBlock(label, value) {
    if (value == null || value === "") return "";
    return '<div class="detail-block"><div class="detail-label">' + esc(label) +
      '</div><pre class="detail-pre">' + esc(value) + "</pre></div>";
  }

  function headerBlock(headers) {
    var keys = Object.keys(headers || {});
    if (!keys.length) return "";
    var lines = keys.sort().map(function (k) { return k + ": " + headers[k]; }).join("\n");
    return detailBlock("请求头", lines);
  }

  function auditDetailText(a) {
    var d = a.detail;
    if (!d) return "";
    var obj;
    try { obj = JSON.parse(d); } catch (e) { return d; }
    var parts = [];
    if (obj.old_token) parts.push("旧令牌: " + obj.old_token);
    if (obj.new_token) parts.push("新令牌: " + obj.new_token);
    if (obj.ip) parts.push("IP: " + obj.ip);
    if (obj.user_agent) parts.push("UA: " + obj.user_agent);
    return parts.length ? parts.join("；") : d;
  }

  function openLogDetail(log) {
    var meta = [
      ["请求 ID", log.id], ["时间", log.timestamp], ["方法", log.method], ["路径", log.path],
      ["状态码", log.status != null ? String(log.status) : "-"],
      ["耗时", log.response_time != null ? log.response_time + " ms" : "-"],
      ["来源 IP", log.ip || "-"], ["User-Agent", log.user_agent || "-"],
    ].map(function (kv) {
      return '<div class="kv"><span class="k">' + esc(kv[0]) + '</span><span class="v">' + esc(kv[1] || "-") + "</span></div>";
    }).join("");
    var html = '<div class="detail-meta">' + meta + "</div>" +
      headerBlock(log.headers) +
      detailBlock("请求体", prettyJSON(log.request_body)) +
      detailBlock("响应体", prettyJSON(log.response_body));
    openDetail((log.method || "") + " " + (log.path || ""), html);
  }

  // ------------------------------------------------------------------------- SSE

  function startStream() {
    if (state.stream) return;
    var src = new EventSource("/api/logs/stream");
    source = src;
    src.addEventListener("connected", markConnected);
    ["request_log", "model_call_request", "model_call_progress", "model_call_response"].forEach(function (name) {
      src.addEventListener(name, function (e) {
        markConnected();
        if (!state.live || state.view !== "logs") return;
        appendLog(JSON.parse(e.data));
      });
    });
    src.onerror = function () { markDisconnected(); };
  }

  var source = null;

  function markConnected() {
    qs("conn-dot").classList.add("on");
    qs("conn-text").textContent = "已连接";
  }
  function markDisconnected() {
    qs("conn-dot").classList.remove("on");
    qs("conn-text").textContent = "已断开";
  }

  function appendLog(evt) {
    if (evt.type && evt.type !== "request_log") return;
    if (evt.stage && evt.stage !== "completed") return;
    var body = qs("l-table").querySelector("tbody");
    var key = cacheLog(evt);
    var row = document.createElement("tr");
    row.className = "log-tr";
    row.setAttribute("data-log-key", key);
    row.innerHTML = logRowInner({
      timestamp: evt.timestamp ? new Date(evt.timestamp).toLocaleString() : "",
      method: evt.method || "", path: evt.path || "", status: evt.status,
      response_time: evt.response_time, ip: evt.ip, user_agent: evt.user_agent,
    }, key);
    body.insertBefore(row, body.firstChild);
    while (body.childElementCount > 400) body.removeChild(body.lastChild);
    if (logSeq > 2000) { logCache = {}; logSeq = 0; }
  }

  // ---------------------------------------------------------------- users

  var userState = { plans: [], planMap: {} };
  var userMap = {};

  var userStatusLabels = {
    active: "有效", expiring: "即将到期", expired: "已过期",
    exhausted: "次数用尽", disabled: "已停用",
  };

  function syncPlans(plans) {
    userState.plans = plans || [];
    userState.planMap = {};
    userState.plans.forEach(function (p) { userState.planMap[p.Code] = p; });
  }

  function fillPlanOptions(sel, selected) {
    if (!sel) return;
    var plans = userState.plans.filter(function (p) {
      return p.Enabled !== false || p.Code === selected;
    });
    sel.innerHTML = '<option value="">未指定</option>' + plans.map(function (p) {
      return '<option value="' + esc(p.Code) + '">' + esc(p.Label) + (p.Enabled === false ? "（已停用）" : "") + "</option>";
    }).join("");
    sel.value = selected || "";
    if (!plans.length) sel.value = "";
  }

  function ensurePlans(cb) {
    if (userState.plans.length) { cb(); return; }
    api("/plans").then(function (data) {
      if (data.success) syncPlans(data.plans || []);
      cb();
    });
  }

  function planKind(code) {
    var p = userState.planMap[code];
    return p ? p.Kind : "";
  }

  function planLabel(code) {
    var p = userState.planMap[code];
    return p ? p.Label : (code || "-");
  }

  function userStatusPill(status) {
    var map = { active: "manual", expiring: "pending", expired: "expired", exhausted: "exhausted", disabled: "disabled" };
    var cls = map[status] || "manual";
    return '<span class="pill ' + cls + '">' + esc(userStatusLabels[status] || status) + "</span>";
  }

  function entitlementOf(u, kind) {
    var list = u.entitlements || [];
    for (var i = 0; i < list.length; i++) {
      if (list[i].kind === kind) return list[i];
    }
    return null;
  }

  function durationText(e) {
    if (!e) return '<span class="muted">未开通</span>';
    var label = esc(e.plan_label || e.plan_code || "-");
    var tail = e.unlimited ? "无限" : (e.expire_at ? e.expire_at + " 到期" : "不限");
    return label + '<br><span class="muted small">' + esc(tail) + "</span>";
  }

  function countText(e) {
    if (!e) return '<span class="muted">未开通</span>';
    var label = esc(e.plan_label || e.plan_code || "-");
    var remain = e.remain_count == null ? "不限" : (e.remain_count + " / " + (e.total_count == null ? "-" : e.total_count));
    var tail = "剩余 " + remain;
    if (e.expire_at) tail += " · " + e.expire_at + " 到期";
    return label + '<br><span class="muted small">' + esc(tail) + "</span>";
  }

  function userRemainText(u) {
    if (u.remain_count == null) return "不限";
    return u.remain_count + " / " + (u.total_count == null ? "-" : u.total_count);
  }

  function shortToken(t) {
    if (!t) return "(未知)";
    return t.length > 14 ? t.slice(0, 10) + "…" : t;
  }

  function loadUsers() {
    api("/users").then(function (data) {
      if (!data.success) { toast(data.message); return; }
      syncPlans(data.plans || []);
      renderUserTable(data.users || []);
      loadUserStats();
    });
  }

  function renderUserTable(users) {
    userMap = {};
    users.forEach(function (u) { userMap[String(u.id)] = u; });
    var body = qs("u-table").querySelector("tbody");
    if (!users.length) {
      body.innerHTML = '<tr><td colspan="7" class="muted">暂无用户，点击「新建用户」或「批量生成」创建。</td></tr>';
      return;
    }
    body.innerHTML = users.map(function (u) {
      var toggle = u.enabled
        ? '<button data-act="disable" data-id="' + u.id + '" class="btn ghost small">停用</button>'
        : '<button data-act="enable" data-id="' + u.id + '" class="btn ghost small">启用</button>';
      return '<tr class="user-row">' +
        '<td data-label="ID">' + u.id + "</td>" +
        '<td data-label="令牌"><span class="mono small token-cell">' + esc(u.token) + "</span></td>" +
        '<td data-label="备注"><span class="clamp">' + esc(u.note || "") + "</span></td>" +
        '<td data-label="时长权益">' + durationText(entitlementOf(u, "duration")) + "</td>" +
        '<td data-label="次数权益">' + countText(entitlementOf(u, "count")) + "</td>" +
        '<td data-label="状态">' + userStatusPill(u.status) + "</td>" +
        '<td data-label="操作" class="row-actions">' +
          '<button data-act="copy" data-id="' + u.id + '" class="btn ghost small">复制令牌</button>' +
          '<button data-act="renew" data-id="' + u.id + '" class="btn ghost small">续费</button>' +
          '<button data-act="reset" data-id="' + u.id + '" class="btn ghost small">重置令牌</button>' +
          '<button data-act="note" data-id="' + u.id + '" class="btn ghost small">备注</button>' +
          toggle +
          '<button data-act="delete" data-id="' + u.id + '" class="btn danger small">删除</button>' +
        "</td>" +
      "</tr>";
    }).join("");
  }

  function loadUserStats() {
    var range = qs("u-range").value || "14";
    api("/users/stats?range=" + range).then(function (data) {
      if (!data.success) return;
      var r = data.report || {};
      var summary = r.summary || {};
      var users = r.users || [];
      qs("u-stat-total").textContent = users.length;
      qs("u-stat-active").textContent = summary.active_users || 0;
      qs("u-stat-expired").textContent = summary.expired_users || 0;
      qs("u-stat-exhausted").textContent = summary.exhausted_users || 0;
      qs("u-stat-calls").textContent = summary.total_calls || 0;
      renderUserDaily(r.daily || []);
      renderUserUsage(users);
    });
  }

  function renderUserDaily(daily) {
    var el = qs("u-daily-chart");
    if (!daily.length) { el.innerHTML = "暂无数据"; return; }
    var recent = daily.slice(-14);
    var max = Math.max.apply(null, recent.map(function (d) { return d.count; })) || 1;
    el.innerHTML = recent.map(function (d) {
      var height = Math.max(3, Math.round((d.count / max) * 100));
      var label = (d.day || "").slice(5);
      return '<div class="bar" style="height:' + height + '%" title="' + esc(d.day) + ': ' + d.count + '"><span>' + esc(label) + '</span></div>';
    }).join("");
  }

  function renderUserUsage(users) {
    var body = qs("u-usage-table").querySelector("tbody");
    var sorted = users.slice().sort(function (a, b) { return b.total_calls - a.total_calls; });
    if (!sorted.length) {
      body.innerHTML = '<tr><td colspan="5" class="muted">暂无数据</td></tr>';
      return;
    }
    body.innerHTML = sorted.map(function (u) {
      var remain = u.remain == null ? "不限" : u.remain;
      return "<tr>" +
        "<td>" + u.id + "</td>" +
        '<td><span class="mono small clamp">' + esc(u.token) + "</span></td>" +
        "<td>" + u.total_calls + "</td>" +
        "<td>" + remain + "</td>" +
        "<td>" + userStatusPill(u.status) + "</td>" +
      "</tr>";
    }).join("");
  }

  function planOptionsHtml(selected, kind) {
    return userState.plans.filter(function (p) {
      if (kind && p.Kind !== kind) return false;
      return p.Enabled !== false || p.Code === selected;
    }).map(function (p) {
      var sel = p.Code === selected ? " selected" : "";
      return '<option value="' + esc(p.Code) + '"' + sel + ">" + esc(p.Label) + (p.Enabled === false ? "（已停用）" : "") + "</option>";
    }).join("");
  }

  // planFields renders the shared plan controls and returns a sync function.
  function planFields(prefix) {
    qs("modal-content").insertAdjacentHTML("beforeend",
      '<div class="field"><label>套餐</label><select id="' + prefix + '-plan">' + planOptionsHtml("") + "</select></div>" +
      '<div class="field" id="' + prefix + '-fixed-wrap" style="display:none;"><label>套餐额度</label><input id="' + prefix + '-fixed" type="text" readonly></div>' +
      '<div class="field" id="' + prefix + '-count-wrap" style="display:none;"><label>自定义总次数</label><input id="' + prefix + '-count" type="number" min="1" value="100"></div>' +
      '<div class="field" id="' + prefix + '-days-wrap" style="display:none;"><label>自定义天数</label><input id="' + prefix + '-days" type="number" min="1" value="30"></div>' +
      '<div class="field"><label>备注（可选）</label><input id="' + prefix + '-note" type="text"></div>' +
      '<label class="checkbox" id="' + prefix + '-expiry-wrap" style="display:none;"><input type="checkbox" id="' + prefix + '-expiry"> 设置次数套餐有效期</label>' +
      '<div class="field" id="' + prefix + '-expiry-days-wrap" style="display:none;"><label>有效期天数</label><input id="' + prefix + '-expiry-days" type="number" min="1" value="30"></div>');

    function syncFields() {
      var code = qs(prefix + "-plan").value;
      var p = userState.planMap[code] || {};
      var kind = p.Kind || "";
      var custom = !!p.Custom;
      qs(prefix + "-fixed-wrap").style.display = kind && !custom ? "" : "none";
      qs(prefix + "-count-wrap").style.display = kind === "count" && custom ? "" : "none";
      qs(prefix + "-days-wrap").style.display = kind === "duration" && custom ? "" : "none";
      qs(prefix + "-expiry-wrap").style.display = kind === "count" ? "" : "none";
      if (kind === "count") {
        var fixed = !custom;
        qs(prefix + "-fixed").value = fixed ? ((p.QuotaCount || 0) + " 次" + (p.ExpiryDays ? " / " + p.ExpiryDays + " 天" : "")) : "";
        if (p.ExpiryDays) {
          qs(prefix + "-expiry").checked = true;
          qs(prefix + "-expiry-days").value = p.ExpiryDays;
        }
        qs(prefix + "-expiry").disabled = !!p.ExpiryDays;
      } else if (kind === "duration") {
        qs(prefix + "-fixed").value = p.Unlimited ? "无限时长" : ((p.Days || 0) + " 天");
      }
      qs(prefix + "-expiry-days-wrap").style.display = kind === "count" && qs(prefix + "-expiry").checked ? "" : "none";
    }
    qs(prefix + "-plan").addEventListener("change", syncFields);
    qs(prefix + "-expiry").addEventListener("change", syncFields);
    var def = qs("s-default-plan") ? qs("s-default-plan").value : "";
    if (def && userState.planMap[def] && userState.planMap[def].Enabled !== false) qs(prefix + "-plan").value = def;
    syncFields();

    return function () {
      var code = qs(prefix + "-plan").value;
      var p = userState.planMap[code] || {};
      var payload = { plan_code: code, note: qs(prefix + "-note").value.trim() };
      if (p.Kind === "count") {
        if (p.Custom) payload.total_count = Number(qs(prefix + "-count").value) || 0;
        payload.enforce_expiry = p.ExpiryDays ? true : qs(prefix + "-expiry").checked;
        payload.expiry_days = p.ExpiryDays ? p.ExpiryDays : (qs(prefix + "-expiry").checked ? (Number(qs(prefix + "-expiry-days").value) || 0) : 0);
      } else if (p.Custom) {
        payload.days = Number(qs(prefix + "-days").value) || 0;
      }
      return payload;
    };
  }

  function openCreateUser() {
    qs("modal-title").textContent = "新建用户";
    qs("modal-content").innerHTML = "";
    var collect = planFields("cu");

    modalSubmit = function () {
      api("/users", { method: "POST", body: JSON.stringify(collect()) }).then(function (res) {
        if (!res.success) { toast(res.message); return; }
        closeModal();
        loadUsers();
      });
    };
    qs("modal").classList.remove("hidden");
  }

  function openBatchUser() {
    qs("modal-title").textContent = "批量生成用户";
    qs("modal-content").innerHTML =
      '<div class="field"><label>生成数量（1-500）</label><input id="bu-quantity" type="number" min="1" max="500" value="10"></div>';
    var collect = planFields("bu");

    modalSubmit = function () {
      var payload = collect();
      payload.count = Number(qs("bu-quantity").value) || 0;
      api("/users/batch", { method: "POST", body: JSON.stringify(payload) }).then(function (res) {
        if (!res.success) { toast(res.message); return; }
        showBatchResult(res.tokens || []);
        loadUsers();
      });
    };
    qs("modal").classList.remove("hidden");
  }

  function showBatchResult(tokens) {
    var text = tokens.join("\n");
    qs("modal-title").textContent = "已生成 " + tokens.length + " 个令牌";
    qs("modal-content").innerHTML =
      '<p class="muted small">请复制并妥善保存，令牌仅在此处完整展示。</p>' +
      '<div class="field"><textarea id="bu-result" rows="10" class="mono" readonly>' + esc(text) + "</textarea></div>";
    modalSubmit = function () {
      copyText(text, "已复制全部令牌");
    };
    qs("modal-ok").textContent = "批量复制";
    qs("modal-ok").classList.remove("hidden");
    qs("modal-cancel").textContent = "关闭";
    qs("modal").classList.remove("hidden");
  }

  function openRenewUser(u) {
    var duration = entitlementOf(u, "duration");
    var count = entitlementOf(u, "count");
    var defaultKind = duration ? "duration" : (count ? "count" : "duration");
    var defaultCode = defaultKind === "duration" ? (duration && duration.plan_code) : (count && count.plan_code);

    qs("modal-title").textContent = "续费 / 切换套餐 - " + shortToken(u.token);
    qs("modal-content").innerHTML =
      '<p class="muted small">时长套餐与时次套餐独立叠加；当前时长有效时，查询优先使用时长。</p>' +
      '<div class="field"><label>本次操作</label><select id="ru-kind">' +
        '<option value="duration">时长套餐</option><option value="count">次数套餐</option></select></div>' +
      '<div class="field"><label>套餐</label><select id="ru-plan"></select></div>' +
      '<div class="field" id="ru-fixed-wrap" style="display:none;"><label>套餐额度</label><input id="ru-fixed" type="text" readonly></div>' +
      '<div class="field" id="ru-count-wrap" style="display:none;"><label>新增次数</label><input id="ru-count" type="number" min="1" value="100"></div>' +
      '<div class="field" id="ru-days-wrap" style="display:none;"><label>自定义天数</label><input id="ru-days" type="number" min="1" value="30"></div>' +
      '<label class="checkbox" id="ru-expiry-wrap" style="display:none;"><input type="checkbox" id="ru-expiry"> 设置/延长有效期</label>' +
      '<div class="field" id="ru-expiry-days-wrap" style="display:none;"><label>有效期天数</label><input id="ru-expiry-days" type="number" min="1" value="30"></div>';

    function fillPlanSelect() {
      var kind = qs("ru-kind").value;
      var current = kind === "duration" ? duration : count;
      var code = current && current.plan_code;
      qs("ru-plan").innerHTML = planOptionsHtml(code, kind);
      if (!qs("ru-plan").value && qs("ru-plan").options.length) qs("ru-plan").value = qs("ru-plan").options[0].value;
      syncFields();
    }

    function syncFields() {
      var code = qs("ru-plan").value;
      var p = userState.planMap[code] || {};
      var kind = p.Kind || "";
      var custom = !!p.Custom;
      qs("ru-fixed-wrap").style.display = kind && !custom ? "" : "none";
      qs("ru-count-wrap").style.display = kind === "count" && custom ? "" : "none";
      qs("ru-days-wrap").style.display = kind === "duration" && custom ? "" : "none";
      qs("ru-expiry-wrap").style.display = kind === "count" ? "" : "none";
      if (kind === "count") {
        qs("ru-fixed").value = custom ? "" : ((p.QuotaCount || 0) + " 次" + (p.ExpiryDays ? " / " + p.ExpiryDays + " 天" : ""));
        if (p.ExpiryDays) {
          qs("ru-expiry").checked = true;
          qs("ru-expiry-days").value = p.ExpiryDays;
        }
        qs("ru-expiry").disabled = !!p.ExpiryDays;
      } else if (kind === "duration") {
        qs("ru-fixed").value = p.Unlimited ? "无限时长" : ((p.Days || 0) + " 天");
      }
      qs("ru-expiry-days-wrap").style.display = kind === "count" && qs("ru-expiry").checked ? "" : "none";
    }

    qs("ru-kind").value = defaultKind;
    qs("ru-kind").addEventListener("change", fillPlanSelect);
    qs("ru-plan").addEventListener("change", syncFields);
    qs("ru-expiry").addEventListener("change", syncFields);
    fillPlanSelect();
    if (defaultCode && userState.planMap[defaultCode]) qs("ru-plan").value = defaultCode;
    syncFields();

    modalSubmit = function () {
      var code = qs("ru-plan").value;
      var p = userState.planMap[code] || {};
      var payload = { plan_code: code };
      if (p.Kind === "count") {
        if (p.Custom) payload.total_count = Number(qs("ru-count").value) || 0;
        payload.enforce_expiry = p.ExpiryDays ? true : qs("ru-expiry").checked;
        payload.expiry_days = p.ExpiryDays ? p.ExpiryDays : (qs("ru-expiry").checked ? (Number(qs("ru-expiry-days").value) || 0) : 0);
      } else if (p.Custom) {
        payload.days = Number(qs("ru-days").value) || 0;
      }
      api("/users/" + u.id + "/renew", { method: "POST", body: JSON.stringify(payload) }).then(function (res) {
        if (!res.success) { toast(res.message, "error"); return; }
        closeModal();
        loadUsers();
      });
    };
    qs("modal").classList.remove("hidden");
  }

  function openNoteUser(u) {
    qs("modal-title").textContent = "编辑备注 - " + shortToken(u.token);
    qs("modal-content").innerHTML =
      '<div class="field"><label>备注（最多 200 字）</label><textarea id="nu-note" rows="3">' + esc(u.note || "") + "</textarea></div>";
    modalSubmit = function () {
      api("/users/" + u.id, { method: "PUT", body: JSON.stringify({ note: qs("nu-note").value }) }).then(function (res) {
        if (!res.success) { toast(res.message); return; }
        closeModal();
        loadUsers();
      });
    };
    qs("modal").classList.remove("hidden");
  }

  // ---------------------------------------------------------------- plans

  function planTypeLabel(kind) { return kind === "count" ? "次数套餐" : "时长套餐"; }

  function planQuotaText(p) {
    if (p.Kind === "count") {
      return p.Custom ? "自定义次数" : ((p.QuotaCount || 0) + " 次");
    }
    if (p.Unlimited) return "无限时长";
    return p.Custom ? "自定义天数" : ((p.Days || 0) + " 天");
  }

  function planPriceText(p) {
    if (!p.PriceCents) return "-";
    return "¥" + (p.PriceCents / 100).toFixed(2);
  }

  function planToPayload(p, overrides) {
    var payload = {
      label: p.Label, kind: p.Kind, days: p.Days || 0,
      quota_count: p.QuotaCount || 0, unlimited: !!p.Unlimited, custom: !!p.Custom,
      expiry_days: p.ExpiryDays || 0, enabled: p.Enabled !== false, sort: p.Sort || 0,
      price_cents: p.PriceCents || 0, currency: p.Currency || "CNY",
      description: p.Description || "", badge: p.Badge || "",
    };
    if (overrides) Object.assign(payload, overrides);
    return payload;
  }

  function renderPlanTable(plans) {
    var body = qs("pl-table").querySelector("tbody");
    if (!plans.length) {
      body.innerHTML = '<tr><td colspan="10" class="muted">暂无套餐，点击「新增套餐」创建。</td></tr>';
      return;
    }
    body.innerHTML = plans.map(function (p) {
      var toggle = p.Enabled !== false
        ? '<button data-act="toggle" data-code="' + esc(p.Code) + '" class="btn ghost small">停用</button>'
        : '<button data-act="toggle" data-code="' + esc(p.Code) + '" class="btn ghost small">启用</button>';
      return "<tr>" +
        "<td>" + (p.Sort || 0) + "</td>" +
        '<td class="mono small">' + esc(p.Code) + "</td>" +
        "<td>" + esc(p.Label) + "</td>" +
        "<td>" + planTypeLabel(p.Kind) + "</td>" +
        "<td>" + esc(planQuotaText(p)) + "</td>" +
        "<td>" + esc(planPriceText(p)) + "</td>" +
        "<td>" + (p.Enabled !== false ? '<span class="pill manual">启用</span>' : '<span class="pill disabled">停用</span>') + "</td>" +
        "<td>" + (p.UserCount || 0) + "</td>" +
        "<td>" + (p.BatchCount || 0) + "</td>" +
        '<td><div class="row-actions">' +
          '<button data-act="edit" data-code="' + esc(p.Code) + '" class="btn ghost small">编辑</button>' +
          toggle +
          '<button data-act="delete" data-code="' + esc(p.Code) + '" class="btn danger small">删除</button>' +
        "</div></td>" +
      "</tr>";
    }).join("");
  }

  function loadPlans() {
    api("/plans").then(function (data) {
      if (!data.success) { toast(data.message || "加载套餐失败", "error"); return; }
      syncPlans(data.plans || []);
      renderPlanTable(userState.plans);
      qs("pl-status").textContent = "共 " + userState.plans.length + " 个套餐，启用 " +
        userState.plans.filter(function (p) { return p.Enabled !== false; }).length + " 个";
      fillPlanOptions(qs("s-default-plan"), qs("s-default-plan") ? qs("s-default-plan").value : "");
    });
  }

  function openPlanModal(existing) {
    var p = existing || {
      Code: "", Label: "", Kind: "duration", Days: 30, QuotaCount: 0,
      Unlimited: false, Custom: false, ExpiryDays: 0, Enabled: true,
      Sort: (userState.plans.length + 1) * 10, PriceCents: 0, Currency: "CNY",
      Description: "", Badge: "",
    };
    var editing = !!existing;
    qs("modal-title").textContent = editing ? "编辑套餐 - " + p.Code : "新增套餐";
    qs("modal-content").innerHTML =
      '<div class="grid-3">' +
        '<div class="field"><label>套餐代码</label><input id="pf-code" type="text" value="' + esc(p.Code) + '"' + (editing ? " readonly" : "") + ' placeholder="例如 count_1000"></div>' +
        '<div class="field"><label>套餐名称</label><input id="pf-label" type="text" value="' + esc(p.Label) + '"></div>' +
        '<div class="field"><label>套餐类型</label><select id="pf-kind"' + (editing ? " disabled" : "") + '><option value="duration">时长套餐</option><option value="count">次数套餐</option></select></div>' +
      '</div>' +
      '<div class="grid-3">' +
        '<div class="field"><label>排序</label><input id="pf-sort" type="number" min="0" value="' + (p.Sort || 0) + '"></div>' +
        '<div class="field"><label>价格（元，可选）</label><input id="pf-price" type="number" min="0" step="0.01" value="' + ((p.PriceCents || 0) / 100) + '"></div>' +
        '<div class="field"><label>币种</label><input id="pf-currency" type="text" value="' + esc(p.Currency || "CNY") + '"></div>' +
      '</div>' +
      '<div class="grid-3">' +
        '<div class="field" id="pf-days-wrap"><label>固定天数</label><input id="pf-days" type="number" min="1" value="' + (p.Days || 30) + '"></div>' +
        '<div class="field" id="pf-count-wrap"><label>固定次数</label><input id="pf-count" type="number" min="1" value="' + (p.QuotaCount || 1000) + '"></div>' +
        '<div class="field" id="pf-expiry-days-wrap"><label>次数有效期（天，0 表示不过期）</label><input id="pf-expiry-days" type="number" min="0" value="' + (p.ExpiryDays || 0) + '"></div>' +
      '</div>' +
      '<label class="checkbox" id="pf-unlimited-wrap"><input type="checkbox" id="pf-unlimited"> 无限时长</label>' +
      '<label class="checkbox" id="pf-custom-wrap"><input type="checkbox" id="pf-custom"> 分配时自定义额度</label>' +
      '<label class="checkbox"><input type="checkbox" id="pf-enabled"> 启用</label>' +
      '<div class="field"><label>说明</label><input id="pf-description" type="text" value="' + esc(p.Description || "") + '"></div>' +
      '<div class="field"><label>角标</label><input id="pf-badge" type="text" value="' + esc(p.Badge || "") + '"></div>';

    qs("pf-kind").value = p.Kind;
    qs("pf-unlimited").checked = !!p.Unlimited;
    qs("pf-custom").checked = !!p.Custom;
    qs("pf-enabled").checked = p.Enabled !== false;

    function syncFields() {
      var kind = qs("pf-kind").value;
      var unlimited = qs("pf-unlimited").checked;
      var custom = qs("pf-custom").checked;
      qs("pf-days-wrap").style.display = kind === "duration" && !unlimited && !custom ? "" : "none";
      qs("pf-count-wrap").style.display = kind === "count" && !custom ? "" : "none";
      qs("pf-expiry-days-wrap").style.display = kind === "count" ? "" : "none";
      qs("pf-unlimited-wrap").style.display = kind === "duration" ? "" : "none";
      if (kind === "count") qs("pf-unlimited").checked = false;
      if (unlimited || custom) qs("pf-days-wrap").style.display = "none";
    }
    qs("pf-kind").addEventListener("change", syncFields);
    qs("pf-unlimited").addEventListener("change", syncFields);
    qs("pf-custom").addEventListener("change", syncFields);
    syncFields();

    modalSubmit = function () {
      var kind = qs("pf-kind").value;
      var unlimited = qs("pf-unlimited").checked;
      var custom = qs("pf-custom").checked;
      var payload = {
        label: qs("pf-label").value.trim(), kind: kind,
        days: kind === "duration" && !unlimited && !custom ? (Number(qs("pf-days").value) || 0) : 0,
        quota_count: kind === "count" && !custom ? (Number(qs("pf-count").value) || 0) : 0,
        unlimited: kind === "duration" && unlimited,
        custom: custom,
        expiry_days: kind === "count" ? (Number(qs("pf-expiry-days").value) || 0) : 0,
        enabled: qs("pf-enabled").checked,
        sort: Number(qs("pf-sort").value) || 0,
        price_cents: Math.round((Number(qs("pf-price").value) || 0) * 100),
        currency: qs("pf-currency").value.trim() || "CNY",
        description: qs("pf-description").value.trim(),
        badge: qs("pf-badge").value.trim(),
      };
      var url = editing ? "/plans/" + encodeURIComponent(p.Code) : "/plans";
      api(url, { method: editing ? "PUT" : "POST", body: JSON.stringify(payload) }).then(function (res) {
        if (!res.success) { toast(res.message, "error"); return; }
        closeModal();
        loadPlans();
        toast(editing ? "套餐已更新" : "套餐已创建", "success");
      });
    };
    qs("modal").classList.remove("hidden");
  }

  qs("pl-add").addEventListener("click", function () { openPlanModal(null); });
  qs("pl-refresh").addEventListener("click", loadPlans);
  qs("pl-table").addEventListener("click", async function (e) {
    var btn = e.target.closest("button[data-act]");
    if (!btn) return;
    var code = btn.getAttribute("data-code");
    var p = userState.planMap[code];
    if (!p) return;
    var act = btn.getAttribute("data-act");
    if (act === "edit") { openPlanModal(p); return; }
    if (act === "toggle") {
      api("/plans/" + encodeURIComponent(code), {
        method: "PUT", body: JSON.stringify(planToPayload(p, { enabled: p.Enabled === false })),
      }).then(function (res) {
        if (!res.success) { toast(res.message, "error"); return; }
        loadPlans();
      });
      return;
    }
    if (act === "delete") {
      if (!(await confirmDialog("删除套餐「" + p.Label + "」？有用户或卡密引用时只能停用。", { danger: true, okText: "删除" }))) return;
      api("/plans/" + encodeURIComponent(code), { method: "DELETE" }).then(function (res) {
        if (!res.success) { toast(res.message, "error"); return; }
        loadPlans();
        toast("套餐已删除", "success");
      });
    }
  });

  qs("u-table").addEventListener("click", async function (e) {
    var btn = e.target.closest("button[data-act]");
    if (!btn) return;
    var id = btn.getAttribute("data-id");
    var u = userMap[id];
    if (!u) return;
    var act = btn.getAttribute("data-act");
    if (act === "copy") { copyText(u.token, "已复制令牌"); return; }
    if (act === "renew") { openRenewUser(u); return; }
    if (act === "note") { openNoteUser(u); return; }
    if (act === "reset") {
      if (!(await confirmDialog("重置「" + shortToken(u.token) + "」的令牌？旧令牌将立即失效。", { danger: true, okText: "重置" }))) return;
      api("/users/" + id + "/reset-token", { method: "POST" }).then(function (res) {
        if (!res.success) { toast(res.message, "error"); return; }
        toast("已重置令牌", "success");
        openDetail("新令牌", '<p class="muted small">请立即复制并告知用户，旧令牌已失效。</p><pre class="editor">' + esc(res.token) + "</pre>");
        loadUsers();
      });
      return;
    }
    if (act === "enable" || act === "disable") {
      api("/users/" + id, { method: "PUT", body: JSON.stringify({ enabled: act === "enable" }) }).then(function (res) {
        if (!res.success) { toast(res.message, "error"); return; }
        toast(act === "enable" ? "已启用用户" : "已停用用户", "success");
        loadUsers();
      });
      return;
    }
    if (act === "delete") {
      if (!(await confirmDialog("删除用户「" + shortToken(u.token) + "」及其调用记录？此操作不可恢复。", { danger: true, okText: "删除" }))) return;
      api("/users/" + id, { method: "DELETE" }).then(function (res) {
        if (!res.success) { toast(res.message, "error"); return; }
        toast("已删除用户", "success");
        loadUsers();
      });
    }
  });

  qs("u-add-btn").addEventListener("click", function () {
    if (!userState.plans.length) { toast("套餐列表加载中，请稍后重试"); return; }
    openCreateUser();
  });
  qs("u-batch-btn").addEventListener("click", function () {
    if (!userState.plans.length) { toast("套餐列表加载中，请稍后重试"); return; }
    openBatchUser();
  });
  qs("u-refresh").addEventListener("click", loadUsers);
  qs("u-range").addEventListener("change", loadUserStats);
  qs("u-audit-btn").addEventListener("click", function () {
    api("/users/audits?page=1&page_size=50").then(function (data) {
      if (!data.success) { toast(data.message); return; }
      var rows = (data.audits || []).map(function (a) {
        return '<tr><td class="small">' + esc(a.created_at) + "</td><td>" + esc(a.action) +
          "</td><td>" + esc(a.user_token || a.user_id) + "</td><td>" + esc(a.actor) +
          "</td><td class=\"small\">" + esc(auditDetailText(a)) + "</td></tr>";
      }).join("");
      if (!rows) rows = '<tr><td colspan="5" class="muted">暂无审计记录</td></tr>';
      openDetail("审计日志（最近 50 条）",
        '<div class="table-wrap"><table><thead><tr><th>时间</th><th>操作</th><th>用户</th><th>操作者</th><th>详情</th></tr></thead><tbody>' +
        rows + "</tbody></table></div>");
    });
  });

  // ---------------------------------------------------------------- accounts

  var accountState = { page: 1, pageSize: 50, total: 0, q: "" };

  function accountStatusPill(status) {
    if (status === "enabled") return '<span class="pill s2">正常</span>';
    if (status === "unbound") return '<span class="pill disabled">未绑定</span>';
    return '<span class="pill disabled">停用</span>';
  }

  function loadAccounts() {
    var params = new URLSearchParams({
      page: String(accountState.page),
      page_size: String(accountState.pageSize),
      q: accountState.q || "",
    });
    api("/accounts?" + params.toString()).then(function (data) {
      if (!data.success) { toast(data.message || "加载失败", "error"); return; }
      accountState.total = data.total || 0;
      renderAccounts(data.accounts || []);
      qs("acc-pageinfo").textContent = "第 " + accountState.page + " 页 / 共 " +
        Math.max(1, Math.ceil(accountState.total / accountState.pageSize)) + " 页";
    });
  }

  function renderAccounts(items) {
    var body = qs("acc-table").querySelector("tbody");
    if (!items.length) {
      body.innerHTML = '<tr><td colspan="8" class="muted">暂无账号</td></tr>';
      return;
    }
    body.innerHTML = items.map(function (a) {
      var plan = a.plan_label || a.plan_code || "-";
      var remain = a.remain_count == null ? "不限" : a.remain_count;
      var actions = '<div class="row-actions">' +
        '<button class="btn small ghost" data-act="password" data-id="' + a.id + '">重置密码</button>';
      if (a.status === "enabled") {
        actions += '<button class="btn small ghost" data-act="disable" data-id="' + a.id + '">停用</button>' +
          '<button class="btn small ghost" data-act="unbind" data-id="' + a.id + '">解绑</button>' +
          '<button class="btn small ghost" data-act="revoke" data-id="' + a.id + '">撤销会话</button>';
      } else if (a.status === "unbound") {
        actions += '<button class="btn small primary" data-act="bind" data-id="' + a.id + '">绑定令牌</button>' +
          '<button class="btn small danger" data-act="delete" data-id="' + a.id + '">删除</button>';
      } else {
        actions += '<button class="btn small primary" data-act="enable" data-id="' + a.id + '">启用</button>';
      }
      actions += "</div>";
      return "<tr>" +
        "<td>" + a.id + "</td>" +
        "<td>" + esc(a.username) + "</td>" +
        '<td><span class="mono small clamp">' + esc(a.token || "-") + "</span></td>" +
        "<td>" + esc(plan) + "</td>" +
        "<td>" + accountStatusPill(a.status) + "</td>" +
        "<td>" + remain + "</td>" +
        '<td class="small">' + esc(a.last_login_at || "-") + "</td>" +
        "<td>" + actions + "</td>" +
      "</tr>";
    }).join("");
  }

  function accountAction(id, action, extra) {
    return api("/accounts/" + encodeURIComponent(id) + "/" + action, {
      method: "POST", body: extra ? JSON.stringify(extra) : undefined,
    });
  }

  qs("acc-table").addEventListener("click", async function (e) {
    var btn = e.target.closest("button[data-act]");
    if (!btn) return;
    var id = btn.getAttribute("data-id");
    var act = btn.getAttribute("data-act");
    if (act === "password") {
      openModal("重置账号密码", [
        { name: "password", label: "新密码（至少 8 位）", type: "password", value: "" },
      ], function (v) {
        accountAction(id, "reset-password", { password: v.password }).then(function (res) {
          if (!res.success) { toast(res.message || "重置失败", "error"); return; }
          toast("密码已重置", "success");
          closeModal();
        });
      });
      return;
    }
    if (act === "delete") {
      if (!(await confirmDialog("删除该未绑定账号？用户名将被释放。", { danger: true }))) return;
      api("/accounts/" + encodeURIComponent(id), { method: "DELETE" }).then(function (res) {
        if (!res.success) { toast(res.message || "删除失败", "error"); return; }
        toast("已删除", "success");
        loadAccounts();
      });
      return;
    }
    if (act === "bind") {
      openModal("绑定令牌", [
        { name: "token", label: "用户令牌", type: "text", value: "" },
      ], function (v) {
        accountAction(id, "bind", { token: v.token.trim() }).then(function (res) {
          if (!res.success) { toast(res.message || "绑定失败", "error"); return; }
          toast("已绑定", "success");
          closeModal();
          loadAccounts();
        });
      });
      return;
    }
    var messages = {
      disable: "停用该账号？现有登录会话会被撤销。",
      enable: "启用该账号？",
      unbind: "解除账号与令牌的绑定？账号将无法登录，直到重新绑定。",
      revoke: "撤销该账号的全部登录会话？",
    };
    if (!(await confirmDialog(messages[act] || "确认操作？", { danger: act === "disable" || act === "unbind" }))) return;
    accountAction(id, act).then(function (res) {
      if (!res.success) { toast(res.message || "操作失败", "error"); return; }
      toast("操作成功", "success");
      loadAccounts();
    });
  });

  qs("acc-search-btn").addEventListener("click", function () {
    accountState.q = qs("acc-search").value.trim();
    accountState.page = 1;
    loadAccounts();
  });
  qs("acc-refresh").addEventListener("click", loadAccounts);
  qs("acc-prev").addEventListener("click", function () {
    if (accountState.page <= 1) return;
    accountState.page--;
    loadAccounts();
  });
  qs("acc-next").addEventListener("click", function () {
    if (accountState.page * accountState.pageSize >= accountState.total) return;
    accountState.page++;
    loadAccounts();
  });

  // ------------------------------------------------------------------- redeem

  var redeemState = { page: 1, pageSize: 50, total: 0 };

  function fillRedeemPlans() {
    var kind = qs("rd-kind").value;
    var sel = qs("rd-plan");
    var keep = sel.value;
    sel.innerHTML = userState.plans.filter(function (p) {
      if (p.Enabled === false) return false;
      if (kind === "count") return p.Kind === "count";
      return p.Kind === "duration" && !p.Unlimited;
    }).map(function (p) {
      return '<option value="' + esc(p.Code) + '">' + esc(p.Label) + "</option>";
    }).join("");
    if (keep && userState.planMap[keep] && userState.planMap[keep].Enabled !== false) sel.value = keep;
    if (!sel.value && sel.options.length) sel.value = sel.options[0].value;
    syncRedeemFields();
  }

  function syncRedeemFields() {
    var kind = qs("rd-kind").value;
    var p = userState.planMap[qs("rd-plan").value] || {};
    if (kind === "count") {
      qs("rd-amount").value = p.Custom ? (qs("rd-amount").value || 100) : (p.QuotaCount || 0);
      qs("rd-amount").disabled = !p.Custom;
      qs("rd-days").value = p.ExpiryDays || 0;
      qs("rd-days").disabled = true;
    } else {
      qs("rd-days").value = p.Custom ? (qs("rd-days").value || 30) : (p.Days || 0);
      qs("rd-days").disabled = !p.Custom;
      qs("rd-amount").value = 0;
      qs("rd-amount").disabled = true;
    }
  }

  function loadRedeem() {
    ensurePlans(function () {
      fillRedeemPlans();
      loadRedeemBatches();
    });
  }

  function loadRedeemBatches() {
    var params = new URLSearchParams({
      page: String(redeemState.page),
      page_size: String(redeemState.pageSize),
    });
    api("/redeem/batches?" + params.toString()).then(function (data) {
      if (!data.success) { toast(data.message || "加载失败", "error"); return; }
      redeemState.total = data.total || 0;
      renderRedeemBatches(data.batches || []);
      qs("rd-pageinfo").textContent = "第 " + redeemState.page + " 页 / 共 " +
        Math.max(1, Math.ceil(redeemState.total / redeemState.pageSize)) + " 页";
    });
  }

  function renderRedeemBatches(items) {
    var body = qs("rd-table").querySelector("tbody");
    if (!items.length) {
      body.innerHTML = '<tr><td colspan="9" class="muted">暂无卡密批次</td></tr>';
      return;
    }
    body.innerHTML = items.map(function (b) {
      var amount = b.kind === "count" ? (b.amount + " 次") : (b.days + " 天");
      return "<tr>" +
        "<td>" + b.id + "</td>" +
        '<td class="mono small">' + esc(b.batch_no) + "</td>" +
        "<td>" + (b.kind === "count" ? "次数卡" : "时长卡") + "</td>" +
        "<td>" + esc(planLabel(b.plan_code)) + "</td>" +
        "<td>" + amount + "</td>" +
        "<td>" + b.quantity + "</td>" +
        "<td>" + (b.used_count || 0) + "</td>" +
        '<td class="small">' + esc(b.created_at || "-") + "</td>" +
        '<td><div class="row-actions"><button class="btn small danger" data-act="revoke" data-id="' + b.id + '">作废未用</button></div></td>' +
      "</tr>";
    }).join("");
  }

  qs("rd-kind").addEventListener("change", fillRedeemPlans);
  qs("rd-plan").addEventListener("change", syncRedeemFields);

  qs("rd-create").addEventListener("click", function () {
    var kind = qs("rd-kind").value;
    var payload = {
      kind: kind,
      plan_code: qs("rd-plan").value,
      quantity: Number(qs("rd-quantity").value) || 0,
      expires_at: qs("rd-expires").value || "",
      note: qs("rd-note").value.trim(),
    };
    if (kind === "count") {
      payload.amount = Number(qs("rd-amount").value) || 0;
      payload.days = 0;
    } else {
      payload.days = Number(qs("rd-days").value) || 0;
      payload.amount = 0;
    }
    api("/redeem/batches", { method: "POST", body: JSON.stringify(payload) }).then(function (res) {
      if (!res.success) { qs("rd-status").textContent = res.message || "生成失败"; return; }
      qs("rd-status").textContent = "已生成 " + res.codes.length + " 个卡密";
      openModal("卡密已生成", [
        {
          name: "codes", label: "请立即复制保存，关闭后无法再次查看", type: "textarea",
          value: (res.codes || []).join("\n"),
        },
      ], function () { closeModal(); });
      loadRedeemBatches();
    });
  });

  qs("rd-table").addEventListener("click", async function (e) {
    var btn = e.target.closest("button[data-act='revoke']");
    if (!btn) return;
    var id = btn.getAttribute("data-id");
    if (!(await confirmDialog("作废该批次所有未使用的卡密？", { danger: true }))) return;
    api("/redeem/batches/" + encodeURIComponent(id) + "/revoke", { method: "POST" }).then(function (res) {
      if (!res.success) { toast(res.message || "作废失败", "error"); return; }
      toast("已作废 " + (res.revoked || 0) + " 个卡密", "success");
      loadRedeemBatches();
    });
  });

  qs("rd-refresh").addEventListener("click", loadRedeemBatches);
  qs("rd-prev").addEventListener("click", function () {
    if (redeemState.page <= 1) return;
    redeemState.page--;
    loadRedeemBatches();
  });
  qs("rd-next").addEventListener("click", function () {
    if (redeemState.page * redeemState.pageSize >= redeemState.total) return;
    redeemState.page++;
    loadRedeemBatches();
  });

  // ---------------------------------------------------------------------- modal

  var modalSubmit = null;
  var modalSync = null;

  function openModal(title, fields, onSubmit) {
    qs("modal-title").textContent = title;
    qs("modal-content").innerHTML = fields.map(function (f) {
      var control;
      if (f.options) {
        control = '<select name="' + f.name + '">' +
          f.options.map(function (o) {
            return '<option value="' + esc(o.value) + '">' + esc(o.label) + "</option>";
          }).join("") + "</select>";
      } else if (f.type === "textarea") {
        control = '<textarea name="' + f.name + '" rows="3"></textarea>';
      } else {
        control = '<input name="' + f.name + '" type="' + f.type + '">';
      }
      return '<div class="field" data-field="' + f.name + '"><label>' + esc(f.label) + "</label>" + control + "</div>";
    }).join("");
    fields.forEach(function (f) {
      qs("modal-content").querySelector('[name="' + f.name + '"]').value = f.value || "";
    });
    function readValues() {
      var values = {};
      fields.forEach(function (f) {
        values[f.name] = qs("modal-content").querySelector('[name="' + f.name + '"]').value;
      });
      return values;
    }
    // Conditionally show fields (e.g. HTTP vs database MOGO source fields).
    function syncVisibility() {
      var values = readValues();
      fields.forEach(function (f) {
        if (typeof f.showWhen !== "function") return;
        var wrap = qs("modal-content").querySelector('[data-field="' + f.name + '"]');
        if (wrap) wrap.style.display = f.showWhen(values) ? "" : "none";
      });
    }
    if (modalSync) {
      qs("modal-content").removeEventListener("input", modalSync);
      qs("modal-content").removeEventListener("change", modalSync);
    }
    modalSync = syncVisibility;
    qs("modal-content").addEventListener("input", modalSync);
    qs("modal-content").addEventListener("change", modalSync);
    syncVisibility();
    modalSubmit = function () {
      onSubmit(readValues());
    };
    qs("modal").classList.remove("hidden");
  }

  function openDetail(title, html) {
    qs("modal-title").textContent = title;
    qs("modal-content").innerHTML = html;
    modalSubmit = null;
    qs("modal-ok").classList.add("hidden");
    qs("modal-cancel").textContent = "关闭";
    qs("modal").classList.remove("hidden");
  }

  function closeModal() {
    qs("modal").classList.add("hidden");
    modalSubmit = null;
    qs("modal-ok").classList.remove("hidden");
    qs("modal-ok").textContent = "确定";
    qs("modal-cancel").textContent = "取消";
  }

  qs("modal-cancel").addEventListener("click", closeModal);
  qs("modal-ok").addEventListener("click", function () { if (modalSubmit) modalSubmit(); });

  // ---------------------------------------------------------------------- login

  function showLogin() {
    qs("login").classList.remove("hidden");
  }
  function hideLogin() {
    qs("login").classList.add("hidden");
    qs("logout-btn").classList.remove("hidden");
  }

  qs("login-btn").addEventListener("click", function () {
    var token = qs("login-token").value.trim();
    fetch("/api/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ token: token }),
    }).then(function (res) { return res.json(); }).then(function (data) {
      if (!data.success) { qs("login-error").textContent = data.message || "登录失败"; return; }
      state.token = token;
      try { sessionStorage.setItem("nfentik_token", token); } catch (e) {}
      hideLogin();
      switchView("dashboard");
      startStream();
    });
  });

  qs("login-token").addEventListener("keydown", function (e) {
    if (e.key === "Enter") qs("login-btn").click();
  });

  qs("login-toggle").addEventListener("click", function () {
    var input = qs("login-token");
    var show = input.type === "password";
    input.type = show ? "text" : "password";
    this.textContent = show ? "隐藏" : "显示";
  });

  qs("logout-btn").addEventListener("click", function () {
    state.token = "";
    try { sessionStorage.removeItem("nfentik_token"); } catch (e) {}
    if (source) { source.close(); source = null; }
    showLogin();
  });

  // ------------------------------------------------------------------- bootstrap

  function boot() {
    var hasToken = app.dataset.hasToken === "1";
    if (hasToken) {
      try { state.token = sessionStorage.getItem("nfentik_token") || ""; } catch (e) {}
      if (state.token) { hideLogin(); } else { showLogin(); return; }
    } else {
      qs("logout-btn").classList.remove("hidden");
    }
    switchView("dashboard");
    startStream();
  }

  boot();
})();
