// Global client-side error surfacing. Loaded deferred after SweetAlert2 at the
// end of <body> so window.Swal is available when a toast fires.
//
// It turns unhandled promise rejections (where most uncaught fetch failures
// land) into a non-blocking toast and exposes window.notifyError so modules
// that already catch their own errors can report them through the same channel
// instead of just console.error.
(function () {
  "use strict";

  let last = { text: "", at: 0 };

  function toast(text, icon, timer, force) {
    const now = Date.now();
    if (!force && text === last.text && now - last.at < 4000) {
      return; // collapse bursts of the same message
    }
    last = { text: text, at: now };

    if (!window.Swal) {
      if (icon === "error") console.error(text);
      else console.log(text);
      return;
    }
    window.Swal.fire({
      toast: true,
      position: "top-end",
      icon: icon || "error",
      title: text,
      showConfirmButton: false,
      timer: timer || 6000,
      timerProgressBar: true,
      background: "#1f2937",
      color: "#f8fafc",
    });
  }

  function clean(value, fallback) {
    const text = value == null ? "" : String(value).trim();
    return text || fallback;
  }

  // Public helper for modules that handle their own fetch/logic errors.
  window.notifyError = function (message) {
    toast(clean(message, "Something went wrong."));
  };

  // Counterpart for reporting a recovered/successful state through the same channel.
  window.notifySuccess = function (message) {
    toast(clean(message, "Done."), "success", 3000);
  };

  // Acknowledges a direct user action right away; forced so a repeated click
  // always surfaces instead of being collapsed by the burst guard.
  window.notifyInfo = function (message) {
    toast(clean(message, "Working…"), "info", 3000, true);
  };

  // Best-effort human message from a failed fetch Response: prefers the server's
  // JSON {error} or plain-text body, and ignores HTML error pages (which are
  // meant for navigations, not AJAX).
  window.errorText = async function (response, fallback) {
    try {
      const type = response.headers.get("content-type") || "";
      if (type.includes("application/json")) {
        const data = await response.json();
        if (data && data.error) return String(data.error);
      } else if (type.includes("text/plain")) {
        const text = (await response.text()).trim();
        if (text) return text;
      }
    } catch (_) {
      // fall through to the fallback
    }
    return clean(fallback, "Something went wrong.");
  };

  // Promises rejected without a .catch (most fetch failures land here).
  window.addEventListener("unhandledrejection", function (event) {
    const reason = event.reason;
    const message = reason && reason.message ? reason.message : reason;
    toast(clean(message, "An unexpected error occurred."));
  });
})();
