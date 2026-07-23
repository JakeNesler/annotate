(function () {
  "use strict";

  var id = location.pathname.split("/").filter(Boolean).pop();
  var statusChip = document.getElementById("room-status");
  var title = document.getElementById("review-title");
  var content = document.getElementById("review-content");
  var sessionID = document.getElementById("session-id");
  var expires = document.getElementById("expires-at");
  var summary = document.getElementById("decision-summary");
  var approve = document.getElementById("approve");
  var requestChanges = document.getElementById("request-changes");
  var error = document.getElementById("decision-error");
  var result = document.getElementById("decision-result");
  var copy = document.getElementById("copy-room-link");
  var currentStatus = "pending";

  function setStatus(status) {
    currentStatus = status;
    statusChip.className = "status-chip " + status;
    statusChip.innerHTML = '<i aria-hidden="true"></i> ' + status.replace("_", " ").toUpperCase();
    var decided = status !== "pending";
    approve.disabled = decided;
    requestChanges.disabled = decided;
    summary.disabled = decided;
    if (decided && window.Annotate) window.Annotate.disable();
  }

  function showError(message) {
    error.textContent = message;
    error.hidden = false;
  }

  function readableTime(value) {
    return new Intl.DateTimeFormat(undefined, {
      month: "short",
      day: "numeric",
      hour: "numeric",
      minute: "2-digit",
    }).format(new Date(value));
  }

  async function load() {
    try {
      var response = await fetch("/api/sessions/" + encodeURIComponent(id));
      var payload = await response.json();
      if (!response.ok) throw new Error(payload.error || "Review not found");
      title.textContent = payload.title;
      document.title = payload.title + " · Annotate Review";
      content.innerHTML = payload.html;
      sessionID.textContent = "SESSION " + payload.id.slice(0, 8).toUpperCase();
      expires.textContent = "EXPIRES " + readableTime(payload.expires_at).toUpperCase();
      if (payload.decision && payload.decision.summary) summary.value = payload.decision.summary;
      setStatus(payload.status);
      if (payload.status !== "pending") {
        result.textContent = payload.status === "approved"
          ? "Approved. The waiting agent can continue."
          : "Changes sent. The waiting agent has the annotations.";
        result.hidden = false;
      }
    } catch (err) {
      title.textContent = "Review unavailable";
      content.innerHTML = "<p>" + escapeHTML(err.message || "Could not load this review") + "</p>";
      showError(err.message || "Could not load this review");
      approve.disabled = true;
      requestChanges.disabled = true;
    }
  }

  async function decide(decision) {
    if (currentStatus !== "pending") return;
    error.hidden = true;
    result.hidden = true;
    var feedback = window.Annotate
      ? window.Annotate.comments().filter(function (comment) { return !comment.resolved; })
      : [];
    if (decision === "changes_requested" && !summary.value.trim() && feedback.length === 0) {
      showError("Pin at least one annotation or add a decision note.");
      return;
    }
    approve.disabled = true;
    requestChanges.disabled = true;
    try {
      var response = await fetch("/api/sessions/" + encodeURIComponent(id) + "/decision", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          decision: decision,
          summary: summary.value.trim(),
          feedback: feedback,
        }),
      });
      var payload = await response.json();
      if (!response.ok) throw new Error(payload.error || "Could not save the decision");
      setStatus(payload.status);
      result.textContent = payload.status === "approved"
        ? "Approved. The waiting agent can continue."
        : "Changes sent. The waiting agent has the annotations.";
      result.hidden = false;
    } catch (err) {
      approve.disabled = false;
      requestChanges.disabled = false;
      showError(err.message || "Could not save the decision");
    }
  }

  function escapeHTML(value) {
    var node = document.createElement("div");
    node.textContent = value;
    return node.innerHTML;
  }

  approve.addEventListener("click", function () { decide("approved"); });
  requestChanges.addEventListener("click", function () { decide("changes_requested"); });
  copy.addEventListener("click", async function () {
    try {
      await navigator.clipboard.writeText(location.href);
      copy.textContent = "Copied";
      window.setTimeout(function () { copy.textContent = "Copy room link"; }, 1600);
    } catch (_) {
      copy.textContent = "Copy failed";
    }
  });

  load();
})();
