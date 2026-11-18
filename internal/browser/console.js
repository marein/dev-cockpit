// dcConsole: options.own is the address prefix of the caller's own scripts,
// which never show up. On globalThis, not a declaration, so a caller in the
// page's own world can take the name away before the page's scripts run.
globalThis.dcConsole = function (options) {
  "use strict";
  var own = typeof options.own === "string" ? options.own : "";
  var tell = options.tell;
  var MAX_REPORTS = 200;
  var MAX_MESSAGE = 2000;
  var MAX_STACK = 2000;
  var reports = [];
  var reported = 0;
  var dropped = 0;
  var flushTimer = 0;
  var unloading = false;
  var inside = 0;

  function mine(text) {
    return own !== "" && String(text).indexOf(own) >= 0;
  }

  function clip(value, max) {
    value = String(value == null ? "" : value);
    return value.length > max ? value.slice(0, max - 1) + "…" : value;
  }

  function cleanStack(stack) {
    if (typeof stack !== "string" || !stack) return "";
    var lines = stack.split("\n").filter(function (line) {
      return line.trim() && !mine(line);
    });
    if (lines.length && lines[0].trim() === "Error") lines.shift();
    return clip(lines.slice(0, 15).join("\n"), MAX_STACK);
  }

  // A page's error that only passed through the caller's wrappers stays the
  // page's.
  function ours(filename, stack) {
    if (!filename || !mine(filename)) return false;
    var lines = String(stack || "").split("\n").filter(function (line) { return /:\d+:\d+/.test(line); });
    return lines.every(mine);
  }

  function report(entry) {
    if (unloading) return;
    if (reported >= MAX_REPORTS) {
      dropped++;
    } else {
      reported++;
      entry.message = clip(entry.message, MAX_MESSAGE);
      entry.stack = cleanStack(entry.stack);
      reports.push(entry);
    }
    if (!flushTimer) flushTimer = setTimeout(flushReports, 80);
  }

  function flushReports() {
    clearTimeout(flushTimer);
    flushTimer = 0;
    if (!reports.length && !dropped) return;
    var entries = reports;
    reports = [];
    tell("console", { entries: entries, dropped: dropped });
  }

  function describeValue(value) {
    var type = typeof value;
    if (value === null || type === "undefined" || type === "number" || type === "boolean" || type === "bigint") return String(value);
    if (type === "string") return value;
    if (type === "symbol") return value.toString();
    if (type === "function") return "function " + (value.name || "") + "()";
    try {
      if (value instanceof Error || (typeof value.message === "string" && typeof value.stack === "string")) return (value.name || "Error") + ": " + value.message;
      if (window.Element && value instanceof Element) {
        var name = value.localName + (value.id ? "#" + value.id : "");
        return "<" + name + (value.classList && value.classList.length ? "." + Array.prototype.slice.call(value.classList, 0, 3).join(".") : "") + ">";
      }
    } catch (e) {
      return "[object]";
    }
    var seen = [];
    var text;
    try {
      text = JSON.stringify(value, function (key, inner) {
        if (inner && typeof inner === "object") {
          if (seen.indexOf(inner) >= 0) return "[Circular]";
          if (seen.length > 200) return "…";
          seen.push(inner);
        }
        if (typeof inner === "function") return "function " + (inner.name || "") + "()";
        if (typeof inner === "bigint") return String(inner);
        return inner;
      });
    } catch (e) {
      text = "";
    }
    if (typeof text !== "string" || !text) {
      try {
        text = Object.prototype.toString.call(value);
      } catch (e) {
        text = "[object]";
      }
    }
    return clip(text, 1000);
  }

  function formatArgs(args) {
    var list = Array.prototype.slice.call(args, 0, 20);
    var out = "";
    if (typeof list[0] === "string" && list[0].indexOf("%") >= 0) {
      var first = list.shift();
      out = first.replace(/%[sdifoOc%]/g, function (token) {
        if (token === "%%") return "%";
        if (!list.length) return token;
        var arg = list.shift();
        if (token === "%c") return "";
        if (token === "%d" || token === "%i") return String(parseInt(arg, 10));
        if (token === "%f") return String(parseFloat(arg));
        return describeValue(arg);
      });
    }
    var rest = list.map(function (arg) { return describeValue(arg); });
    return (out ? [out].concat(rest) : rest).join(" ");
  }

  function errorIn(args) {
    for (var i = 0; i < args.length && i < 20; i++) {
      var arg = args[i];
      try {
        if (arg && typeof arg === "object" && typeof arg.stack === "string" && typeof arg.message === "string") return arg;
      } catch (e) {}
    }
    return null;
  }

  function requestURL(raw) {
    try {
      return new URL(String(raw), location.href).href;
    } catch (e) {
      return String(raw);
    }
  }

  function failed(method, url, status, text) {
    if (unloading || (own !== "" && url.indexOf(own) === 0)) return;
    report({ level: "error", kind: "request", method: method, url: url, status: status, message: text || "" });
  }

  ["error", "warn"].forEach(function (level) {
    var original = console[level];
    if (typeof original !== "function") return;
    console[level] = function () {
      if (!inside) {
        inside++;
        try {
          var error = errorIn(arguments);
          report({ level: level, kind: "console", message: formatArgs(arguments), stack: error ? error.stack : reported < MAX_REPORTS ? new Error().stack : "" });
        } catch (e) {} finally {
          inside--;
        }
      }
      return original.apply(this, arguments);
    };
  });
  var originalAssert = console.assert;
  if (typeof originalAssert === "function") {
    console.assert = function (condition) {
      if (!condition && !inside) {
        inside++;
        try {
          var rest = Array.prototype.slice.call(arguments, 1);
          report({ level: "error", kind: "console", message: "Assertion failed" + (rest.length ? ": " + formatArgs(rest) : ""), stack: new Error().stack });
        } catch (e) {} finally {
          inside--;
        }
      }
      return originalAssert.apply(this, arguments);
    };
  }

  window.addEventListener("error", function (event) {
    var node = event.target;
    if (node && node !== window && node.nodeType === 1) {
      var url = node.currentSrc || node.src || node.href || "";
      if (typeof url !== "string" || !url || (own !== "" && url.indexOf(own) === 0)) return;
      var status = 0;
      try {
        var timing = performance.getEntriesByName(url);
        var last = timing[timing.length - 1];
        if (last && typeof last.responseStatus === "number") status = last.responseStatus;
      } catch (e) {}
      if (status && status < 400) return;
      failed("GET", requestURL(url), status, status ? "(" + node.localName + ")" : "(" + node.localName + " did not load)");
      return;
    }
    if (inside) return;
    var stack = event.error && typeof event.error.stack === "string" ? event.error.stack : "";
    if (ours(event.filename, stack)) return;
    var where = event.filename ? event.filename + (event.lineno ? ":" + event.lineno + (event.colno ? ":" + event.colno : "") : "") : "";
    report({ level: "error", kind: "exception", message: event.message || describeValue(event.error), stack: stack || (where ? "    at " + where : "") });
  }, true);

  // A file a style sheet loads, a background or a font, fails without an
  // event, only its timing tells.
  if (typeof PerformanceObserver === "function") {
    try {
      new PerformanceObserver(function (list) {
        list.getEntries().forEach(function (entry) {
          if (entry.initiatorType === "css" && entry.responseStatus >= 400) failed("GET", entry.name, entry.responseStatus, "(css)");
        });
      }).observe({ type: "resource", buffered: true });
    } catch (e) {}
  }

  window.addEventListener("unhandledrejection", function (event) {
    if (inside) return;
    var reason = event.reason;
    var stack = "";
    try {
      if (reason && typeof reason.stack === "string") stack = reason.stack;
    } catch (e) {}
    report({ level: "error", kind: "rejection", message: "Uncaught (in promise) " + describeValue(reason), stack: stack });
  });

  var originalFetch = window.fetch;
  if (typeof originalFetch === "function") {
    window.fetch = function (input, init) {
      var method = "GET";
      var url = "";
      try {
        method = String((init && init.method) || (input && typeof input === "object" && input.method) || "GET").toUpperCase();
        url = requestURL(input && typeof input === "object" && typeof input.url === "string" ? input.url : input);
      } catch (e) {}
      return originalFetch.apply(this, arguments).then(function (response) {
        if (response && response.status >= 400) failed(method, url || response.url, response.status, response.statusText);
        return response;
      }, function (error) {
        if (!error || error.name !== "AbortError") failed(method, url, 0, error && error.message ? String(error.message) : "");
        throw error;
      });
    };
  }

  var XHR = window.XMLHttpRequest;
  if (XHR && XHR.prototype) {
    var requests = new WeakMap();
    var open = XHR.prototype.open;
    XHR.prototype.open = function (method, url) {
      var xhr = this;
      try {
        var request = requests.get(xhr);
        if (!request) {
          request = {};
          requests.set(xhr, request);
          xhr.addEventListener("abort", function () { request.aborted = true; });
          xhr.addEventListener("loadend", function () {
            if (request.aborted) return;
            if (xhr.status >= 400) failed(request.method, request.url, xhr.status, xhr.statusText);
            else if (xhr.status === 0) failed(request.method, request.url, 0, "");
          });
        }
        request.method = String(method || "GET").toUpperCase();
        request.url = requestURL(url);
        request.aborted = false;
      } catch (e) {}
      return open.apply(this, arguments);
    };
  }

  window.addEventListener("pagehide", function () {
    flushReports();
    unloading = true;
  });
  window.addEventListener("pageshow", function (event) {
    if (!event.persisted) return;
    unloading = false;
    reported = 0;
    dropped = 0;
  });
};
