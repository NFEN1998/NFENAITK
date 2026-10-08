(function () {
  "use strict";

  var form = document.getElementById("setup-form");
  var errorBox = document.getElementById("setup-error");
  var submitBtn = document.getElementById("setup-submit");
  var doneBox = document.getElementById("setup-done");
  var donePath = document.getElementById("done-path");
  var testBtn = document.getElementById("db-test");
  var testResult = document.getElementById("db-test-result");
  var dsnPreview = document.getElementById("db-dsn-preview");

  function showError(msg) {
    errorBox.textContent = msg;
    errorBox.classList.remove("hidden");
  }

  function clearError() {
    errorBox.textContent = "";
    errorBox.classList.add("hidden");
  }

  function el(id) { return document.getElementById(id); }

  function dbFields() {
    return {
      host: el("db-host").value.trim(),
      port: el("db-port").value.trim(),
      user: el("db-user").value.trim(),
      password: el("db-password").value,
      database: el("db-name").value.trim(),
      sslmode: el("db-sslmode").value,
    };
  }

  // buildDSN assembles a PostgreSQL URL from the individual fields. Values are
  // percent-encoded so special characters in the password stay valid.
  function buildDSN(f) {
    var host = f.host || "127.0.0.1";
    var port = f.port || "5432";
    var user = encodeURIComponent(f.user || "");
    var pass = f.password ? ":" + encodeURIComponent(f.password) : "";
    var db = f.database ? "/" + encodeURIComponent(f.database) : "";
    var ssl = f.sslmode ? "?sslmode=" + encodeURIComponent(f.sslmode) : "";
    return "postgres://" + user + pass + "@" + host + ":" + port + db + ssl;
  }

  function refreshPreview() {
    dsnPreview.textContent = buildDSN(dbFields());
  }

  ["db-host", "db-port", "db-user", "db-password", "db-name", "db-sslmode"].forEach(function (id) {
    el(id).addEventListener("input", refreshPreview);
    el(id).addEventListener("change", refreshPreview);
  });

  // Prefill from the server so the operator only edits what is needed.
  fetch("/setup/state", { headers: { Accept: "application/json" } })
    .then(function (r) { return r.json(); })
    .then(function (s) {
      if (!s || !s.success) return;
      var d = s.state || {};
      if (d.db_host) el("db-host").value = d.db_host;
      if (d.db_port) el("db-port").value = d.db_port;
      if (d.db_user) el("db-user").value = d.db_user;
      if (d.db_password) el("db-password").value = d.db_password;
      if (d.db_name) el("db-name").value = d.db_name;
      if (d.db_sslmode) el("db-sslmode").value = d.db_sslmode;
      if (d.redis_url) el("redis-url").value = d.redis_url;
      if (d.redis_enabled) el("redis-enabled").checked = true;
      if (d.admin_token) el("admin-token").value = d.admin_token;
      if (d.site_name) el("site-name").value = d.site_name;
      if (d.site_subtitle) el("site-subtitle").value = d.site_subtitle;
      refreshPreview();
    })
    .catch(function () { refreshPreview(); });

  refreshPreview();

  testBtn.addEventListener("click", function () {
    clearError();
    var f = dbFields();
    if (!f.host || !f.port || !f.user || !f.database) {
      testResult.className = "test-result fail";
      testResult.textContent = "请先填写主机、端口、用户名和数据库名";
      return;
    }
    testBtn.disabled = true;
    testBtn.textContent = "测试中…";
    testResult.className = "test-result";
    testResult.textContent = "";

    fetch("/setup/test-db", {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify(f),
    })
      .then(function (r) { return r.json().catch(function () { return { success: false, message: "响应解析失败" }; }); })
      .then(function (res) {
        testResult.className = "test-result " + (res.success ? "ok" : "fail");
        testResult.textContent = res.message || (res.success ? "连接成功" : "连接失败");
      })
      .catch(function () {
        testResult.className = "test-result fail";
        testResult.textContent = "网络错误，请重试";
      })
      .then(function () {
        testBtn.disabled = false;
        testBtn.textContent = "测试连接";
      });
  });

  form.addEventListener("submit", function (e) {
    e.preventDefault();
    clearError();

    var f = dbFields();
    var payload = {
      db_host: f.host,
      db_port: f.port,
      db_user: f.user,
      db_password: f.password,
      db_name: f.database,
      db_sslmode: f.sslmode,
      redis_enabled: el("redis-enabled").checked,
      redis_url: el("redis-url").value.trim(),
      admin_token: el("admin-token").value.trim(),
      site_name: el("site-name").value.trim(),
      site_subtitle: el("site-subtitle").value.trim(),
    };

    if (!f.host || !f.port || !f.user || !f.database) {
      showError("请完整填写数据库主机、端口、用户名和数据库名");
      return;
    }
    if (!payload.admin_token) { showError("请设置管理员令牌"); return; }

    submitBtn.disabled = true;
    submitBtn.textContent = "保存中…";

    fetch("/setup", {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify(payload),
    })
      .then(function (r) { return r.json().catch(function () { return { success: false, message: "响应解析失败" }; }); })
      .then(function (res) {
        if (!res.success) {
          showError(res.message || "保存失败");
          submitBtn.disabled = false;
          submitBtn.textContent = "保存配置";
          return;
        }
        form.classList.add("hidden");
        doneBox.classList.remove("hidden");
        if (res.bootstrap_path) donePath.textContent = res.bootstrap_path;
      })
      .catch(function () {
        showError("网络错误，请重试");
        submitBtn.disabled = false;
        submitBtn.textContent = "保存配置";
      });
  });
})();
