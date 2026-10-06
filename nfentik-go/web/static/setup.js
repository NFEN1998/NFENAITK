(function () {
  "use strict";

  var form = document.getElementById("setup-form");
  var errorBox = document.getElementById("setup-error");
  var submitBtn = document.getElementById("setup-submit");
  var doneBox = document.getElementById("setup-done");
  var donePath = document.getElementById("done-path");

  function showError(msg) {
    errorBox.textContent = msg;
    errorBox.classList.remove("hidden");
  }

  function clearError() {
    errorBox.textContent = "";
    errorBox.classList.add("hidden");
  }

  // Prefill from the server so the operator only edits what is needed.
  fetch("/setup/state", { headers: { Accept: "application/json" } })
    .then(function (r) { return r.json(); })
    .then(function (s) {
      if (!s || !s.success) return;
      var d = s.state || {};
      if (d.database_url) document.getElementById("database-url").value = d.database_url;
      if (d.redis_url) document.getElementById("redis-url").value = d.redis_url;
      if (d.redis_enabled) document.getElementById("redis-enabled").checked = true;
      if (d.admin_token) document.getElementById("admin-token").value = d.admin_token;
      if (d.site_name) document.getElementById("site-name").value = d.site_name;
      if (d.site_subtitle) document.getElementById("site-subtitle").value = d.site_subtitle;
    })
    .catch(function () {});

  form.addEventListener("submit", function (e) {
    e.preventDefault();
    clearError();

    var payload = {
      database_url: document.getElementById("database-url").value.trim(),
      redis_enabled: document.getElementById("redis-enabled").checked,
      redis_url: document.getElementById("redis-url").value.trim(),
      admin_token: document.getElementById("admin-token").value.trim(),
      site_name: document.getElementById("site-name").value.trim(),
      site_subtitle: document.getElementById("site-subtitle").value.trim(),
    };

    if (!payload.database_url) { showError("请填写 PostgreSQL 连接串"); return; }
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
