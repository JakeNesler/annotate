(function () {
  "use strict";

  var script = document.currentScript || document.querySelector('script[src="/.__annotate/site-review.js"]');
  var config = window.__ANNOTATE_SITE_REVIEW__ || {};
  if (script && script.dataset) {
    config.id = config.id || script.dataset.session || "";
    config.token = config.token || script.dataset.token || "";
  }
  var status = "pending";
  var root;
  var note;
  var error;
  var result;
  var send;
  var approve;

  function pageKey() {
    return location.pathname + location.search;
  }

  function refreshPage() {
    if (window.Annotate && typeof window.Annotate.setPage === "function") {
      window.Annotate.setPage(pageKey());
    }
  }

  function installNavigationHooks() {
    ["pushState", "replaceState"].forEach(function (name) {
      var original = history[name];
      history[name] = function () {
        var value = original.apply(this, arguments);
        window.setTimeout(refreshPage, 0);
        return value;
      };
    });
    window.addEventListener("popstate", function () {
      window.setTimeout(refreshPage, 0);
    });
  }

  function allOpenComments() {
    if (!window.Annotate) return [];
    var getter = typeof window.Annotate.allComments === "function"
      ? window.Annotate.allComments
      : window.Annotate.comments;
    return getter().filter(function (comment) {
      return comment && !comment.resolved;
    });
  }

  function setBusy(busy) {
    send.disabled = busy || status !== "pending";
    approve.disabled = busy || status !== "pending";
    note.disabled = status !== "pending";
  }

  function showError(message) {
    error.textContent = message;
    error.hidden = false;
  }

  function showResult(message) {
    result.textContent = message;
    result.hidden = false;
  }

  async function decide(decision) {
    if (status !== "pending") return;
    error.hidden = true;
    result.hidden = true;
    var feedback = allOpenComments();
    if (decision === "changes_requested" && !note.value.trim() && feedback.length === 0) {
      showError("Pin at least one comment or add an overall note before sending.");
      return;
    }
    setBusy(true);
    try {
      var response = await fetch("/.__annotate/decision", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-Annotate-Decision-Token": config.token || "",
        },
        body: JSON.stringify({
          token: config.token || "",
          decision: decision,
          summary: note.value.trim(),
          feedback: feedback,
        }),
      });
      var payload = await response.json();
      if (!response.ok) throw new Error(payload.error || "Could not send review decision");
      status = payload.status;
      root.classList.add("is-decided");
      if (window.Annotate && typeof window.Annotate.disable === "function") window.Annotate.disable();
      showResult(status === "approved"
        ? "Approved. The waiting agent can continue."
        : "Comments sent. The waiting agent will resume with your feedback.");
    } catch (err) {
      setBusy(false);
      showError(err.message || "Could not send review decision");
    }
  }

  function buildUI() {
    var style = document.createElement("style");
    style.textContent = [
      "#__annotate_site_review{position:fixed;right:14px;top:14px;z-index:2147483250;width:min(360px,calc(100vw - 28px));font:14px/1.4 Inter,-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;color:#f8fafc;background:rgba(15,23,42,.94);border:1px solid rgba(255,255,255,.16);box-shadow:0 18px 60px rgba(15,23,42,.32);border-radius:8px;padding:12px;backdrop-filter:blur(12px)}",
      "#__annotate_site_review *{box-sizing:border-box}",
      "#__annotate_site_review h2{font-size:14px;line-height:1.2;margin:0 0 8px;font-weight:700;letter-spacing:0}",
      "#__annotate_site_review label{display:block;font-size:12px;color:#cbd5e1;margin:0 0 6px}",
      "#__annotate_site_review textarea{display:block;width:100%;min-height:72px;resize:vertical;border:1px solid rgba(255,255,255,.18);border-radius:6px;background:#020617;color:#f8fafc;padding:8px;font:inherit}",
      "#__annotate_site_review .asr-row{display:flex;gap:8px;margin-top:10px}",
      "#__annotate_site_review button{appearance:none;border:0;border-radius:6px;padding:9px 10px;font:inherit;font-weight:700;cursor:pointer}",
      "#__annotate_site_review button:disabled{opacity:.58;cursor:not-allowed}",
      "#__annotate_site_review .asr-send{flex:1;background:#ff6b35;color:#111827}",
      "#__annotate_site_review .asr-approve{background:#e2e8f0;color:#111827}",
      "#__annotate_site_review .asr-message{margin:8px 0 0;font-size:12px}",
      "#__annotate_site_review .asr-error{color:#fecdd3}",
      "#__annotate_site_review .asr-result{color:#bbf7d0}",
      "#__annotate_site_review.is-decided textarea{opacity:.72}",
      "@media (max-width:640px){#__annotate_site_review{top:auto;bottom:74px;right:10px;width:calc(100vw - 20px)}#__annotate_site_review .asr-row{flex-direction:column}}",
    ].join("\n");
    document.head.appendChild(style);

    root = document.createElement("section");
    root.id = "__annotate_site_review";
    root.setAttribute("aria-label", "Annotate site review handoff");
    root.innerHTML = [
      "<h2>Annotate site review</h2>",
      "<label for='__annotate_site_note'>Overall note</label>",
      "<textarea id='__annotate_site_note' placeholder='Add context for the waiting agent...'></textarea>",
      "<p id='__annotate_site_error' class='asr-message asr-error' role='alert' hidden></p>",
      "<p id='__annotate_site_result' class='asr-message asr-result' role='status' hidden></p>",
      "<div class='asr-row'>",
      "<button id='__annotate_site_send' class='asr-send' type='button'>Send comments to agent</button>",
      "<button id='__annotate_site_approve' class='asr-approve' type='button'>Approve and continue</button>",
      "</div>",
    ].join("");
    document.body.appendChild(root);
    note = document.getElementById("__annotate_site_note");
    error = document.getElementById("__annotate_site_error");
    result = document.getElementById("__annotate_site_result");
    send = document.getElementById("__annotate_site_send");
    approve = document.getElementById("__annotate_site_approve");
    send.addEventListener("click", function () { decide("changes_requested"); });
    approve.addEventListener("click", function () { decide("approved"); });
  }

  function boot() {
    refreshPage();
    installNavigationHooks();
    buildUI();
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot);
  } else {
    boot();
  }
})();
