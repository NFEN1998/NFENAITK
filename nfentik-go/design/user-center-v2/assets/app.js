(function () {
  "use strict";

  var root = document.documentElement;
  var accents = ["", "blue", "amber"];

  // ----- accent theme (persisted across the separate pages) -----
  function applyAccent(name) {
    if (name) root.setAttribute("data-accent", name);
    else root.removeAttribute("data-accent");
    try { localStorage.setItem("nfentik-accent", name || ""); } catch (e) {}
  }
  document.querySelectorAll("[data-accent-toggle]").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var current = root.getAttribute("data-accent") || "";
      var next = accents[(accents.indexOf(current) + 1) % accents.length];
      applyAccent(next);
      toast("主题已切换");
    });
  });

  // ----- mobile sidebar -----
  var sidebar = document.getElementById("sidebar");
  var backdrop = document.getElementById("backdrop");
  var menuBtn = document.getElementById("menu-btn");
  function setMenu(open) {
    if (!sidebar) return;
    sidebar.classList.toggle("open", open);
    if (backdrop) backdrop.classList.toggle("show", open);
    if (menuBtn) menuBtn.setAttribute("aria-expanded", open ? "true" : "false");
  }
  if (menuBtn) menuBtn.addEventListener("click", function () { setMenu(!sidebar.classList.contains("open")); });
  if (backdrop) backdrop.addEventListener("click", function () { setMenu(false); });
  document.addEventListener("keydown", function (e) { if (e.key === "Escape") setMenu(false); });

  // ----- toast -----
  function host() {
    var el = document.querySelector("[data-toast-host]");
    if (!el) {
      el = document.createElement("div");
      el.className = "toast";
      el.setAttribute("data-toast-host", "");
      document.body.appendChild(el);
    }
    return el;
  }
  var timer = null;
  function toast(message) {
    if (!message) return;
    var el = host();
    el.textContent = message;
    el.classList.add("show");
    clearTimeout(timer);
    timer = setTimeout(function () { el.classList.remove("show"); }, 1800);
  }
  window.__uiToast = toast;

  // ----- delegated actions -----
  document.addEventListener("click", function (e) {
    var copy = e.target.closest("[data-copy]");
    if (copy) {
      var value = copy.getAttribute("data-copy");
      if (value.charAt(0) === "#") {
        var src = document.querySelector(value);
        if (src) {
          value = (src.tagName === "INPUT" || src.tagName === "TEXTAREA") ? src.value : src.textContent;
        }
      }
      if (navigator.clipboard) navigator.clipboard.writeText(value).catch(function () {});
      toast("已复制");
      return;
    }
    var t = e.target.closest("[data-toast]");
    if (t) toast(t.getAttribute("data-toast"));
  });

  // ----- login tabs -----
  var loginTabs = document.querySelector("[data-login-tabs]");
  if (loginTabs) {
    loginTabs.addEventListener("click", function (e) {
      var btn = e.target.closest("[data-login-tab]");
      if (!btn) return;
      loginTabs.querySelectorAll("[data-login-tab]").forEach(function (b) { b.classList.toggle("active", b === btn); });
      document.querySelectorAll("[data-login-pane]").forEach(function (p) {
        p.classList.toggle("active", p.getAttribute("data-login-pane") === btn.getAttribute("data-login-tab"));
      });
    });
  }

  // ----- password visibility -----
  document.querySelectorAll("[data-pw-toggle]").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var input = document.getElementById(btn.getAttribute("data-pw-toggle"));
      if (!input) return;
      var show = input.type === "password";
      input.type = show ? "text" : "password";
      btn.textContent = show ? "隐藏" : "显示";
    });
  });

  // ----- integration tabs -----
  var codeTabs = document.querySelector("[data-tabs]");
  if (codeTabs) {
    codeTabs.addEventListener("click", function (e) {
      var btn = e.target.closest("[data-tab]");
      if (!btn) return;
      codeTabs.querySelectorAll("[data-tab]").forEach(function (b) { b.classList.toggle("active", b === btn); });
      document.querySelectorAll("[data-pane]").forEach(function (p) {
        p.classList.toggle("active", p.getAttribute("data-pane") === btn.getAttribute("data-tab"));
      });
    });
  }
})();
