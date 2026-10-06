(function () {
  "use strict";

  var app = document.getElementById("app");
  var state = {
    token: "",
    view: "dashboard",
    page: 1,
    pageSize: 20,
    total: 0,
    questionFolder: "all",
    sort: "desc",
    folderMap: {},
    stream: null,
    live: true,
  };

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

  function toast(message) {
    if (!message) return;
    window.alert(message);
  }

  // ---------------------------------------------------------------- navigation

  var titles = {
    dashboard: "概览", questions: "题库", folders: "文件夹", pending: "待修正",
    models: "模型配置", users: "用户管理", ocs: "OCS 配置", settings: "系统设置", logs: "请求日志",
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
    if (view === "dashboard") loadDashboard();
    if (view === "questions") loadQuestions();
    if (view === "folders") loadFolders();
    if (view === "pending") loadPending();
    if (view === "models") loadModelConfig();
    if (view === "users") loadUsers();
    if (view === "ocs") loadOCS();
    if (view === "settings") loadSettings();
    if (view === "logs") loadLogs();
  }

  document.querySelectorAll(".nav-item").forEach(function (btn) {
    btn.addEventListener("click", function () { switchView(btn.dataset.view); });
  });
  qs("refresh-btn").addEventListener("click", function () { switchView(state.view); });

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
      return '<div class="bar" style="height:' + height + '%" title="' + esc(d.date) + ': ' + d.count + '"><span>' + esc(label) + '</span></div>';
    }).join("");
  }

  // ------------------------------------------------------------------ questions

  function loadQuestions() {
    var params = new URLSearchParams();
    params.set("page", state.page);
    params.set("page_size", state.pageSize);
    params.set("sort", state.sort);
    if (state.questionFolder !== "all") params.set("folder_id", state.questionFolder);
    var keyword = qs("q-search").value.trim();

    var promise = keyword
      ? api("/questions/search?keyword=" + encodeURIComponent(keyword) +
          (state.questionFolder !== "all" ? "&folder_id=" + state.questionFolder : ""))
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
      tbody.innerHTML = '<tr><td colspan="6" class="muted">暂无题目</td></tr>';
      return;
    }
    tbody.innerHTML = items.map(function (item) {
      var source = item.is_ai
        ? '<span class="pill ai">AI</span>'
        : '<span class="pill manual">手动</span>';
      var status = item.is_pending_correction
        ? '<span class="pill pending">待修正</span>' : '<span class="muted">正常</span>';
      return "<tr>" +
        "<td>" + item.id + "</td>" +
        '<td class="clamp">' + esc(item.question) + "</td>" +
        '<td class="clamp">' + esc(item.answer) + "</td>" +
        "<td>" + esc(questionTypeLabel(item.question_type)) + "</td>" +
        "<td>" + source + "</td>" +
        "<td>" + status + "</td>" +
        '<td class="row-actions">' +
          '<button class="btn ghost" data-edit="' + item.id + '">编辑</button> ' +
          '<button class="btn danger" data-del="' + item.id + '">删除</button>' +
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

  function deleteQuestion(id) {
    if (!window.confirm("确定删除题目 #" + id + " 吗？")) return;
    api("/questions/" + id, { method: "DELETE" }).then(function (res) {
      if (!res.success) { toast(res.message); return; }
      loadQuestions();
    });
  }

  qs("q-search-btn").addEventListener("click", function () { state.page = 1; loadQuestions(); });
  qs("q-search").addEventListener("keydown", function (e) {
    if (e.key === "Enter") { state.page = 1; loadQuestions(); }
  });
  qs("q-sort").addEventListener("change", function () { state.sort = this.value; loadQuestions(); });
  qs("q-folder").addEventListener("change", function () { state.questionFolder = this.value; state.page = 1; loadQuestions(); });
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
    ], function (values) {
      api("/questions", {
        method: "POST",
        body: JSON.stringify({
          question: values.question, options: values.options, answer: values.answer,
          folder_id: state.questionFolder === "all" ? 0 : Number(state.questionFolder),
        }),
      }).then(function (res) {
        if (!res.success) { toast(res.message); return; }
        closeModal();
        loadQuestions();
      });
    });
  });

  // -------------------------------------------------------------------- folders

  function loadFolders() {
    api("/folders").then(function (data) {
      if (!data.success) { toast(data.message); return; }
      state.folderMap = {};
      (data.folders || []).forEach(function (f) { state.folderMap[f.id] = f; });
      renderFolderOptions(data.folders || []);
      renderFolderTree(data.folders || [], data.stats || []);
    });
  }

  function renderFolderOptions(folders) {
    var sel = qs("q-folder");
    var current = sel.value;
    sel.innerHTML = '<option value="all">全部文件夹</option>' + folders.map(function (f) {
      return '<option value="' + f.id + '">' + esc(f.name) + "</option>";
    }).join("");
    sel.value = current || "all";
  }

  function renderFolderTree(folders, stats) {
    var countMap = {};
    stats.forEach(function (s) { countMap[s.folder_id] = s.question_count; });
    var children = {};
    folders.forEach(function (f) {
      var parent = f.parent_id || 0;
      (children[parent] = children[parent] || []).push(f);
    });

    function build(parentId) {
      var list = children[parentId] || [];
      if (!list.length) return "";
      return '<div class="folder-children">' + list.map(function (f) {
        var count = countMap[f.id] || 0;
        return '<div class="folder-node">' +
          '<span class="name">' + esc(f.name) + "</span>" +
          '<span class="count">' + count + " 题</span>" +
          '<button class="btn ghost" data-add-sub="' + f.id + '">子文件夹</button> ' +
          '<button class="btn ghost" data-rename="' + f.id + '">重命名</button> ' +
          '<button class="btn danger" data-remove="' + f.id + '">删除</button>' +
          build(f.id) +
        "</div>";
      }).join("") + "</div>";
    }

    var el = qs("folder-tree");
    el.innerHTML = build(0) || '<p class="muted">暂无文件夹</p>';

    el.querySelectorAll("[data-add-sub]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        openModal("新建子文件夹", [{ name: "name", label: "名称", type: "text", value: "" }], function (values) {
          api("/folders", { method: "POST", body: JSON.stringify({ name: values.name, parent_id: Number(btn.dataset.addSub) }) })
            .then(function (res) { if (!res.success) { toast(res.message); return; } closeModal(); loadFolders(); });
        });
      });
    });
    el.querySelectorAll("[data-rename]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var folder = state.folderMap[btn.dataset.rename];
        openModal("重命名文件夹", [{ name: "name", label: "名称", type: "text", value: folder ? folder.name : "" }], function (values) {
          api("/folders/" + btn.dataset.rename, { method: "PUT", body: JSON.stringify({ name: values.name }) })
            .then(function (res) { if (!res.success) { toast(res.message); return; } closeModal(); loadFolders(); });
        });
      });
    });
    el.querySelectorAll("[data-remove]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        if (!window.confirm("删除该文件夹？其子文件夹也会被删除。")) return;
        api("/folders/" + btn.dataset.remove, { method: "DELETE" })
          .then(function (res) { if (!res.success) { toast(res.message); return; } loadFolders(); });
      });
    });
  }

  qs("f-add-root").addEventListener("click", function () {
    openModal("新建根文件夹", [{ name: "name", label: "名称", type: "text", value: "" }], function (values) {
      api("/folders", { method: "POST", body: JSON.stringify({ name: values.name, parent_id: 0 }) })
        .then(function (res) { if (!res.success) { toast(res.message); return; } closeModal(); loadFolders(); });
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
          '<td class="clamp">' + esc(item.question) + "</td>" +
          '<td class="clamp">' + esc(item.answer) + "</td>" +
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
      btn.addEventListener("click", function () {
        var pi = Number(btn.dataset.delPlatform);
        var p = modelConfig.platforms[pi];
        if (!window.confirm("删除平台「" + (p.displayName || p.name || p.id) + "」及其模型？")) return;
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
      ensurePlans(function () { fillSettingsForm(s); });
      loadFolderOptions(s);
    });
  }

  var settingsJSON = {};

  function loadFolderOptions(s) {
    api("/folders").then(function (data) {
      if (!data.success) return;
      var sel = qs("s-save-folder");
      sel.innerHTML = '<option value="0">默认文件夹</option>' + (data.folders || []).map(function (f) {
        return '<option value="' + f.id + '">' + esc(f.name) + "</option>";
      }).join("");
      if (s.questionSaveFolderId != null) sel.value = String(s.questionSaveFolderId);
    });
  }

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
    qs("s-multiuser-enabled").checked = !!(s.multiUser && s.multiUser.enabled);
    qs("s-users-enabled").checked = s.usersEnabled !== false;
    qs("s-require-token").checked = !!s.requireTokenForQuery;
    qs("s-ulog-days").value = s.userLogRetentionDays || 30;
    qs("s-ulog-peruser").value = s.userLogPerUserLimit || 200;
    qs("s-ulog-global").value = s.userLogGlobalLimit || 50000;
    qs("s-uaudit-days").value = s.userAuditRetentionDays || 90;
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
    s.multiUser = s.multiUser || { users: [] };
    s.multiUser.enabled = qs("s-multiuser-enabled").checked;
    s.usersEnabled = qs("s-users-enabled").checked;
    s.requireTokenForQuery = qs("s-require-token").checked;
    s.userLogRetentionDays = Number(qs("s-ulog-days").value) || 30;
    s.userLogPerUserLimit = Number(qs("s-ulog-peruser").value) || 200;
    s.userLogGlobalLimit = Number(qs("s-ulog-global").value) || 50000;
    s.userAuditRetentionDays = Number(qs("s-uaudit-days").value) || 90;
    s.defaultPlanCode = qs("s-default-plan").value || "";
    var folderVal = Number(qs("s-save-folder").value);
    s.questionSaveFolderId = folderVal > 0 ? folderVal : null;
    return s;
  }

  qs("s-raw-toggle").addEventListener("change", function () {
    var raw = this.checked;
    qs("s-visual").classList.toggle("hidden", raw);
    qs("s-editor").classList.toggle("hidden", !raw);
    if (raw) {
      qs("s-editor").value = JSON.stringify(settingsJSON, null, 2);
    } else {
      try { settingsJSON = JSON.parse(qs("s-editor").value); fillSettingsForm(settingsJSON); loadFolderOptions(settingsJSON); }
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

  function ocsConfigObject(baseURL) {
    var base = String(baseURL || window.location.origin).replace(/\/+$/, "");
    return [{
      name: "NFENAI题库",
      homepage: base,
      url: base + "/query",
      method: "get",
      type: "GM_xmlhttpRequest",
      contentType: "json",
      data: {
        title: "${title}",
        options: "${options}",
        type: "${type}",
      },
      handler: "return (res)=>res.code === 0 ? [res.message, undefined] : [res.data.question,res.data.answer,{ai: res.data.is_ai}]",
    }];
  }

  function renderOCS() {
    var base = qs("ocs-url-base").value.trim() || window.location.origin;
    qs("ocs-json").value = JSON.stringify(ocsConfigObject(base), null, 2);
  }

  function loadOCS() {
    if (!qs("ocs-url-base").value) qs("ocs-url-base").value = window.location.origin;
    renderOCS();
  }

  qs("ocs-url-base").addEventListener("input", renderOCS);
  qs("ocs-reset").addEventListener("click", function () {
    qs("ocs-url-base").value = window.location.origin;
    renderOCS();
  });
  qs("ocs-copy").addEventListener("click", function () {
    copyText(qs("ocs-json").value, "OCS 配置已复制");
  });

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
  qs("l-clear").addEventListener("click", function () {
    if (!window.confirm("清空全部请求日志？")) return;
    api("/logs/clear", { method: "DELETE" }).then(function () { logState.page = 1; loadLogs(); });
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

  function fillPlanOptions(sel, selected) {
    if (!sel) return;
    var plans = userState.plans;
    sel.innerHTML = '<option value="">未指定</option>' + plans.map(function (p) {
      return '<option value="' + esc(p.Code) + '">' + esc(p.Label) + "</option>";
    }).join("");
    sel.value = selected || "";
    if (!plans.length) sel.value = "";
  }

  function ensurePlans(cb) {
    if (userState.plans.length) { cb(); return; }
    api("/users").then(function (data) {
      if (data.success) {
        userState.plans = data.plans || [];
        userState.planMap = {};
        userState.plans.forEach(function (p) { userState.planMap[p.Code] = p; });
      }
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
      userState.plans = data.plans || [];
      userState.planMap = {};
      userState.plans.forEach(function (p) { userState.planMap[p.Code] = p; });
      renderUserTable(data.users || []);
      loadUserStats();
    });
  }

  function renderUserTable(users) {
    userMap = {};
    users.forEach(function (u) { userMap[String(u.id)] = u; });
    var body = qs("u-table").querySelector("tbody");
    if (!users.length) {
      body.innerHTML = '<tr><td colspan="8" class="muted">暂无用户，点击「新建用户」或「批量生成」创建。</td></tr>';
      return;
    }
    body.innerHTML = users.map(function (u) {
      var toggle = u.enabled
        ? '<button data-act="disable" data-id="' + u.id + '" class="btn ghost small">停用</button>'
        : '<button data-act="enable" data-id="' + u.id + '" class="btn ghost small">启用</button>';
      return '<tr class="user-row">' +
        '<td data-label="ID">' + u.id + "</td>" +
        '<td data-label="令牌"><span class="mono small token-cell">' + esc(u.token) + "</span></td>" +
        '<td data-label="备注" class="clamp">' + esc(u.note || "") + "</td>" +
        '<td data-label="套餐">' + esc(planLabel(u.plan_code)) + "</td>" +
        '<td data-label="状态">' + userStatusPill(u.status) + "</td>" +
        '<td data-label="剩余">' + esc(userRemainText(u)) + "</td>" +
        '<td data-label="到期">' + esc(u.expire_at || "不限") + "</td>" +
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

  function planOptionsHtml(selected) {
    return userState.plans.map(function (p) {
      var sel = p.Code === selected ? " selected" : "";
      return '<option value="' + esc(p.Code) + '"' + sel + ">" + esc(p.Label) + "</option>";
    }).join("");
  }

  // planFields renders the shared plan controls and returns a sync function.
  function planFields(prefix) {
    qs("modal-content").insertAdjacentHTML("beforeend",
      '<div class="field"><label>套餐</label><select id="' + prefix + '-plan">' + planOptionsHtml("") + "</select></div>" +
      '<div class="field" id="' + prefix + '-count-wrap"><label>总次数</label><input id="' + prefix + '-count" type="number" min="1" value="100"></div>' +
      '<div class="field" id="' + prefix + '-days-wrap" style="display:none;"><label>自定义天数</label><input id="' + prefix + '-days" type="number" min="1" value="30"></div>' +
      '<div class="field"><label>备注（可选）</label><input id="' + prefix + '-note" type="text"></div>' +
      '<label class="checkbox" id="' + prefix + '-expiry-wrap"><input type="checkbox" id="' + prefix + '-expiry"> 次数套餐设置有效期</label>' +
      '<div class="field" id="' + prefix + '-expiry-days-wrap" style="display:none;"><label>有效期天数</label><input id="' + prefix + '-expiry-days" type="number" min="1" value="30"></div>');

    function syncFields() {
      var code = qs(prefix + "-plan").value;
      var kind = planKind(code);
      var custom = userState.planMap[code] && userState.planMap[code].Custom;
      qs(prefix + "-count-wrap").style.display = kind === "count" ? "" : "none";
      qs(prefix + "-expiry-wrap").style.display = kind === "count" ? "" : "none";
      qs(prefix + "-expiry-days-wrap").style.display = kind === "count" && qs(prefix + "-expiry").checked ? "" : "none";
      qs(prefix + "-days-wrap").style.display = kind === "duration" && custom ? "" : "none";
    }
    qs(prefix + "-plan").addEventListener("change", syncFields);
    qs(prefix + "-expiry").addEventListener("change", syncFields);
    var def = qs("s-default-plan") ? qs("s-default-plan").value : "";
    if (def && userState.planMap[def]) qs(prefix + "-plan").value = def;
    syncFields();

    return function () {
      var payload = {
        plan_code: qs(prefix + "-plan").value,
        note: qs(prefix + "-note").value.trim(),
      };
      if (planKind(payload.plan_code) === "count") {
        payload.total_count = Number(qs(prefix + "-count").value) || 0;
        payload.enforce_expiry = qs(prefix + "-expiry").checked;
        payload.expiry_days = Number(qs(prefix + "-expiry-days").value) || 0;
      } else if (userState.planMap[payload.plan_code] && userState.planMap[payload.plan_code].Custom) {
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
      '<div class="field"><label>生成数量（1-500）</label><input id="bu-count" type="number" min="1" max="500" value="10"></div>';
    var collect = planFields("bu");

    modalSubmit = function () {
      var payload = collect();
      payload.count = Number(qs("bu-count").value) || 0;
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
    var planOptions = userState.plans.map(function (p) {
      var sel = p.Code === u.plan_code ? " selected" : "";
      return '<option value="' + esc(p.Code) + '"' + sel + ">" + esc(p.Label) + "</option>";
    }).join("");
    qs("modal-title").textContent = "续费 / 切换套餐 - " + shortToken(u.token);
    qs("modal-content").innerHTML =
      '<div class="field"><label>套餐</label><select id="ru-plan">' + planOptions + "</select></div>" +
      '<div class="field" id="ru-count-wrap"><label>新增次数</label><input id="ru-count" type="number" min="1" value="100"></div>' +
      '<div class="field" id="ru-days-wrap" style="display:none;"><label>自定义天数</label><input id="ru-days" type="number" min="1" value="30"></div>' +
      '<label class="checkbox" id="ru-expiry-wrap"><input type="checkbox" id="ru-expiry"> 设置/延长有效期</label>' +
      '<div class="field" id="ru-expiry-days-wrap" style="display:none;"><label>有效期天数</label><input id="ru-expiry-days" type="number" min="1" value="30"></div>';

    function syncFields() {
      var code = qs("ru-plan").value;
      var kind = planKind(code);
      var custom = userState.planMap[code] && userState.planMap[code].Custom;
      qs("ru-count-wrap").style.display = kind === "count" ? "" : "none";
      qs("ru-expiry-wrap").style.display = kind === "count" ? "" : "none";
      qs("ru-expiry-days-wrap").style.display = kind === "count" && qs("ru-expiry").checked ? "" : "none";
      qs("ru-days-wrap").style.display = kind === "duration" && custom ? "" : "none";
    }
    qs("ru-plan").addEventListener("change", syncFields);
    qs("ru-expiry").addEventListener("change", syncFields);
    syncFields();

    modalSubmit = function () {
      var payload = { plan_code: qs("ru-plan").value };
      if (planKind(payload.plan_code) === "count") {
        payload.total_count = Number(qs("ru-count").value) || 0;
        payload.enforce_expiry = qs("ru-expiry").checked;
        payload.expiry_days = Number(qs("ru-expiry-days").value) || 0;
      } else if (userState.planMap[payload.plan_code] && userState.planMap[payload.plan_code].Custom) {
        payload.days = Number(qs("ru-days").value) || 0;
      }
      api("/users/" + u.id + "/renew", { method: "POST", body: JSON.stringify(payload) }).then(function (res) {
        if (!res.success) { toast(res.message); return; }
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

  qs("u-table").addEventListener("click", function (e) {
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
      if (!window.confirm("重置「" + shortToken(u.token) + "」的令牌？旧令牌将立即失效。")) return;
      api("/users/" + id + "/reset-token", { method: "POST" }).then(function (res) {
        if (!res.success) { toast(res.message); return; }
        openDetail("新令牌", '<p class="muted small">请立即复制并告知用户，旧令牌已失效。</p><pre class="editor">' + esc(res.token) + "</pre>");
        loadUsers();
      });
      return;
    }
    if (act === "enable" || act === "disable") {
      api("/users/" + id, { method: "PUT", body: JSON.stringify({ enabled: act === "enable" }) }).then(function (res) {
        if (!res.success) { toast(res.message); return; }
        loadUsers();
      });
      return;
    }
    if (act === "delete") {
      if (!window.confirm("删除用户「" + shortToken(u.token) + "」及其调用记录？此操作不可恢复。")) return;
      api("/users/" + id, { method: "DELETE" }).then(function (res) {
        if (!res.success) { toast(res.message); return; }
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
          "</td><td>" + esc(a.user_token || a.user_id) + "</td><td>" + esc(a.actor) + "</td></tr>";
      }).join("");
      if (!rows) rows = '<tr><td colspan="4" class="muted">暂无审计记录</td></tr>';
      openDetail("审计日志（最近 50 条）",
        '<div class="table-wrap"><table><thead><tr><th>时间</th><th>操作</th><th>用户</th><th>操作者</th></tr></thead><tbody>' +
        rows + "</tbody></table></div>");
    });
  });

  // ---------------------------------------------------------------------- modal

  var modalSubmit = null;

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
      return '<div class="field"><label>' + esc(f.label) + "</label>" + control + "</div>";
    }).join("");
    fields.forEach(function (f) {
      qs("modal-content").querySelector('[name="' + f.name + '"]').value = f.value || "";
    });
    modalSubmit = function () {
      var values = {};
      fields.forEach(function (f) {
        values[f.name] = qs("modal-content").querySelector('[name="' + f.name + '"]').value;
      });
      onSubmit(values);
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
