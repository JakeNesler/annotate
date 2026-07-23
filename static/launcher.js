(function () {
  "use strict";

  var form = document.getElementById("review-form");
  var error = document.getElementById("form-error");
  var created = document.getElementById("review-created");
  var link = document.getElementById("review-link");
  var copy = document.getElementById("copy-link");
  var submit = form.querySelector('button[type="submit"]');

  function showError(message) {
    error.textContent = message;
    error.hidden = false;
  }

  form.addEventListener("submit", async function (event) {
    event.preventDefault();
    error.hidden = true;
    created.hidden = true;
    submit.disabled = true;
    submit.querySelector("span").textContent = "Opening room…";
    try {
      var response = await fetch("/api/sessions", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          title: document.getElementById("title").value,
          markdown: document.getElementById("markdown").value,
        }),
      });
      var payload = await response.json();
      if (!response.ok) throw new Error(payload.error || "Could not open the review room");
      link.href = payload.url;
      link.textContent = payload.url;
      created.hidden = false;
      link.focus();
    } catch (err) {
      showError(err.message || "Could not open the review room");
    } finally {
      submit.disabled = false;
      submit.querySelector("span").textContent = "Open review room";
    }
  });

  copy.addEventListener("click", async function () {
    try {
      await navigator.clipboard.writeText(link.href);
      copy.textContent = "Copied";
      window.setTimeout(function () { copy.textContent = "Copy link"; }, 1600);
    } catch (_) {
      link.focus();
    }
  });
})();
