// dcDrawing: marks keep the page's coordinates and follow the boxes that
// scroll under them, so they move with what they were drawn on.
// While drawing a glass takes every pointer, so the page neither scrolls nor
// takes a click.
function dcDrawing(options) {
  "use strict";
  var picker = options.picker;
  var tag = options.tag;
  var closed = options.closed === true;
  var trusted = options.trusted === true;
  var tell = options.tell;
  var setStyle = picker.setStyle;
  var DRAW_COLORS = { red: "#d63939", orange: "#f76707", green: "#2fb344", blue: "#066fd1", purple: "#ae3ec9" };
  var DRAW_TOOLS = { pen: true, arrow: true, rect: true, text: true };
  var DRAW_FONT = "system-ui, -apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif";
  var MAX_MARKS = 100;
  var MAX_POINTS = 4000;
  var MAX_STEPS = 100;
  var MAX_MARK_TEXT = 200;
  var MAX_CANVAS = 16000000;
  var drawing = false;
  var drawTool = "pen";
  var drawColor = "red";
  var marks = [];
  var past = [];
  var future = [];
  var stroke = null;
  var typing = null;
  var sheet = null;
  var paintQueued = false;
  var committedText = false;
  var drawPath = location.pathname + location.search;

  function makeSheet() {
    var host = document.createElement(tag);
    setStyle(host, { all: "initial", display: "block", position: "fixed", left: "0", top: "0", width: "0", height: "0", margin: "0", padding: "0", border: "0", overflow: "visible", background: "transparent", "pointer-events": "none", "z-index": "2147483647" });
    var root = host.attachShadow({ mode: closed ? "closed" : "open" });
    var canvas = document.createElement("canvas");
    setStyle(canvas, { display: "block", position: "fixed", left: "0", top: "0", width: "0", height: "0", margin: "0", padding: "0", border: "0", "pointer-events": "none" });
    var glass = document.createElement("div");
    setStyle(glass, { display: "none", position: "fixed", left: "0", top: "0", right: "0", bottom: "0", margin: "0", cursor: "crosshair", "pointer-events": "auto", background: "transparent", "touch-action": "none", "user-select": "none", "-webkit-user-select": "none", "-webkit-touch-callout": "none", "-webkit-tap-highlight-color": "transparent" });
    glass.addEventListener("click", function () {});
    root.appendChild(canvas);
    root.appendChild(glass);
    return { host: host, root: root, canvas: canvas, glass: glass };
  }

  // Inside the page's topmost modal dialog or open popover: outside a modal
  // dialog it would be inert, a press outside a popover would close it. Raised
  // again, it stands over what the page put into the top layer since.
  function settle(again) {
    var into = picker.holder() || document.documentElement;
    if (!again && sheet.host.parentNode === into && sheet.host.matches(":popover-open")) return;
    picker.raiseHost(sheet.host, into);
  }

  function syncSheet() {
    if (!drawing && !marks.length && !stroke && !typing) {
      if (sheet && sheet.host.isConnected) {
        try {
          if (sheet.host.matches(":popover-open")) sheet.host.hidePopover();
        } catch (e) {}
        sheet.host.remove();
      }
      return;
    }
    if (!sheet) sheet = makeSheet();
    if (!sheet.host.isConnected) settle();
    setStyle(sheet.glass, { display: drawing ? "block" : "none", cursor: drawTool === "text" ? "text" : "crosshair" });
    paintSheet();
  }

  function paintSheet() {
    paintQueued = false;
    if (!sheet || !sheet.host.isConnected) return;
    var w = window.innerWidth;
    var h = window.innerHeight;
    var ratio = Math.min(window.devicePixelRatio || 1, Math.sqrt(MAX_CANVAS / Math.max(1, w * h)));
    var canvas = sheet.canvas;
    var cw = Math.max(1, Math.round(w * ratio));
    var ch = Math.max(1, Math.round(h * ratio));
    if (canvas.width !== cw || canvas.height !== ch) {
      canvas.width = cw;
      canvas.height = ch;
      setStyle(canvas, { width: w + "px", height: h + "px" });
    }
    var ctx = canvas.getContext("2d");
    if (!ctx) return;
    ctx.setTransform(cw / w, 0, 0, ch / h, 0, 0);
    ctx.clearRect(0, 0, w, h);
    paintMarks(ctx, window.scrollX, window.scrollY);
  }

  function queuePaint() {
    if (paintQueued) return;
    paintQueued = true;
    window.requestAnimationFrame(paintSheet);
  }

  window.addEventListener("scroll", function () {
    if (sheet && sheet.host.isConnected) paintSheet();
  }, { capture: true, passive: true });
  window.addEventListener("resize", function () {
    if (sheet && sheet.host.isConnected) paintSheet();
  }, { passive: true });
  window.addEventListener("toggle", function (event) {
    if (sheet && sheet.host.isConnected && event.isTrusted && event.target !== sheet.host) settle();
  }, true);

  function paintMarks(ctx, dx, dy) {
    for (var i = 0; i < marks.length; i++) paintMark(ctx, marks[i], dx, dy);
    if (stroke) paintMark(ctx, stroke, dx, dy);
  }

  // A mark that moved with a box is cut to it, as the box cuts its content.
  function scrolled(mark, ctx) {
    var x = 0;
    var y = 0;
    var list = mark.scrollers || [];
    for (var i = 0; i < list.length; i++) {
      var one = list[i];
      if (!one.el.isConnected) continue;
      var mx = one.el.scrollLeft - one.left;
      var my = one.el.scrollTop - one.top;
      x += mx;
      y += my;
      if (ctx && (mx || my)) {
        var r = one.el.getBoundingClientRect();
        ctx.beginPath();
        ctx.rect(r.left + one.el.clientLeft, r.top + one.el.clientTop, one.el.clientWidth, one.el.clientHeight);
        ctx.clip();
      }
    }
    return [x, y];
  }

  function scrollersOf(el) {
    var out = [];
    var node = el;
    while (node && node.ownerDocument !== document) node = node.ownerDocument.defaultView.frameElement;
    for (; node && node.nodeType === 1 && node !== document.documentElement && node !== document.scrollingElement && out.length < 8; node = node.parentElement || (node.getRootNode().host || null)) {
      var style = getComputedStyle(node);
      if ((/auto|scroll|overlay/.test(style.overflowY) && node.scrollHeight > node.clientHeight) || (/auto|scroll|overlay/.test(style.overflowX) && node.scrollWidth > node.clientWidth)) {
        out.push({ el: node, left: node.scrollLeft, top: node.scrollTop });
      }
    }
    return out;
  }

  function paintMark(ctx, mark, dx, dy) {
    var p = mark.points;
    var color = DRAW_COLORS[mark.color] || DRAW_COLORS.red;
    ctx.save();
    var moved = scrolled(mark, ctx);
    ctx.translate(-dx - moved[0], -dy - moved[1]);
    ctx.strokeStyle = color;
    ctx.fillStyle = color;
    ctx.lineWidth = mark.width;
    ctx.lineCap = "round";
    ctx.lineJoin = "round";
    if (mark.kind === "text") {
      ctx.font = "600 " + mark.size + "px " + DRAW_FONT;
      ctx.textBaseline = "top";
      ctx.lineWidth = mark.size / 5;
      ctx.strokeStyle = "rgba(255, 255, 255, 0.9)";
      ctx.strokeText(mark.text, p[0][0], p[0][1]);
      ctx.fillText(mark.text, p[0][0], p[0][1]);
    } else if (mark.kind === "pen") {
      ctx.beginPath();
      ctx.moveTo(p[0][0], p[0][1]);
      for (var i = 1; i < p.length - 1; i++) {
        ctx.quadraticCurveTo(p[i][0], p[i][1], (p[i][0] + p[i + 1][0]) / 2, (p[i][1] + p[i + 1][1]) / 2);
      }
      var last = p[p.length - 1];
      ctx.lineTo(last[0] + (p.length === 1 ? 0.01 : 0), last[1]);
      ctx.stroke();
    } else if (p.length > 1) {
      var a = p[0];
      var b = p[p.length - 1];
      if (mark.kind === "rect") {
        ctx.strokeRect(Math.min(a[0], b[0]), Math.min(a[1], b[1]), Math.abs(b[0] - a[0]), Math.abs(b[1] - a[1]));
      } else {
        var angle = Math.atan2(b[1] - a[1], b[0] - a[0]);
        var head = Math.min(Math.hypot(b[0] - a[0], b[1] - a[1]) * 0.6, mark.width * 5);
        ctx.beginPath();
        ctx.moveTo(a[0], a[1]);
        ctx.lineTo(b[0] - Math.cos(angle) * head * 0.7, b[1] - Math.sin(angle) * head * 0.7);
        ctx.stroke();
        ctx.beginPath();
        ctx.moveTo(b[0], b[1]);
        ctx.lineTo(b[0] - head * Math.cos(angle - 0.45), b[1] - head * Math.sin(angle - 0.45));
        ctx.lineTo(b[0] - head * Math.cos(angle + 0.45), b[1] - head * Math.sin(angle + 0.45));
        ctx.closePath();
        ctx.fill();
        ctx.stroke();
      }
    }
    ctx.restore();
  }

  function onText(event) {
    if (!typing) return false;
    if (typeof event.clientX === "number" && /^(pointer|mouse|click|dblclick|auxclick|contextmenu|wheel)/.test(event.type)) return sheet.root.elementFromPoint(event.clientX, event.clientY) === typing.input;
    return sheet.root.activeElement === typing.input;
  }

  // Input that misses the sheet while drawing, as when the page opened a modal
  // dialog over it, reaches no page either, and the sheet moves to where the
  // next press finds it.
  function stray(event) {
    event.stopImmediatePropagation();
    if (event.cancelable && /^(pointerdown|mousedown|click|dblclick|auxclick|contextmenu|touchmove|wheel|dragstart|selectstart)$/.test(event.type)) event.preventDefault();
    if (event.type === "pointerdown") settle(true);
  }

  ["pointerdown", "pointerup", "pointermove", "pointerover", "pointerout", "pointerenter", "pointerleave", "pointercancel",
    "mousedown", "mouseup", "mousemove", "mouseover", "mouseout", "mouseenter", "mouseleave",
    "click", "dblclick", "auxclick", "contextmenu", "touchstart", "touchmove", "touchend", "touchcancel", "dragstart", "selectstart", "wheel",
    "keyup", "keypress", "beforeinput", "input", "compositionstart", "compositionend", "paste", "copy", "cut", "focusin", "focusout"].forEach(function (type) {
    window.addEventListener(type, function (event) {
      if (!sheet) return;
      if (event.target !== sheet.host) {
        if (drawing && event.isTrusted && !/^(key|beforeinput|input|composition|paste|copy|cut|focus)/.test(type)) stray(event);
        return;
      }
      event.stopImmediatePropagation();
      if (trusted && !event.isTrusted) return;
      if (onText(event)) return;
      if (drawing) onSheet(event);
    }, { capture: true, passive: type === "touchstart" });
  });

  function onSheet(event) {
    switch (event.type) {
      case "pointerdown":
        committedText = Boolean(typing);
        if (typing) finishTyping(true);
        if (stroke || drawTool === "text" || (event.pointerType === "mouse" && event.button !== 0)) break;
        beginStroke(event);
        break;
      case "pointermove":
        if (stroke && event.pointerId === stroke.pointer) extendStroke(event);
        break;
      case "pointerup":
        if (stroke && event.pointerId === stroke.pointer) {
          extendStroke(event);
          endStroke();
        }
        break;
      case "pointercancel":
        if (stroke && event.pointerId === stroke.pointer) {
          stroke = null;
          paintSheet();
        }
        break;
      case "click":
        event.preventDefault();
        if (drawTool === "text" && !committedText) placeText(event.clientX, event.clientY);
        committedText = false;
        break;
      case "touchmove":
      case "wheel":
      case "mousedown":
      case "dblclick":
      case "auxclick":
      case "contextmenu":
      case "dragstart":
      case "selectstart":
        if (event.cancelable) event.preventDefault();
        break;
      default:
    }
  }

  function zoom() {
    return picker.zoom();
  }

  function pagePoint(x, y) {
    return [Math.round((x + window.scrollX) * 10) / 10, Math.round((y + window.scrollY) * 10) / 10];
  }

  function beginStroke(event) {
    stroke = { kind: drawTool, color: drawColor, width: 3 / zoom(), pointer: event.pointerId, points: [pagePoint(event.clientX, event.clientY)] };
    try {
      sheet.glass.setPointerCapture(event.pointerId);
    } catch (e) {}
    paintSheet();
  }

  function extendStroke(event) {
    var list = typeof event.getCoalescedEvents === "function" ? event.getCoalescedEvents() : [];
    if (!list.length) list = [event];
    for (var i = 0; i < list.length; i++) {
      var at = pagePoint(list[i].clientX, list[i].clientY);
      if (stroke.kind !== "pen") {
        stroke.points[1] = at;
        continue;
      }
      var last = stroke.points[stroke.points.length - 1];
      if (stroke.points.length < MAX_POINTS && (at[0] !== last[0] || at[1] !== last[1])) stroke.points.push(at);
    }
    queuePaint();
  }

  function boxOf(mark) {
    var box = { x1: Infinity, y1: Infinity, x2: -Infinity, y2: -Infinity };
    for (var i = 0; i < mark.points.length; i++) {
      var p = mark.points[i];
      box.x1 = Math.min(box.x1, p[0]);
      box.y1 = Math.min(box.y1, p[1]);
      box.x2 = Math.max(box.x2, p[0]);
      box.y2 = Math.max(box.y2, p[1]);
    }
    return box;
  }

  function endStroke() {
    var done = stroke;
    stroke = null;
    try {
      sheet.glass.releasePointerCapture(done.pointer);
    } catch (e) {}
    delete done.pointer;
    var box = boxOf(done);
    if (Math.max(box.x2 - box.x1, box.y2 - box.y1) < (done.kind === "pen" ? 4 : 8) / zoom()) {
      paintSheet();
      return;
    }
    place(done);
    commit(marks.concat([done]));
  }

  function placeText(x, y) {
    if (typing) {
      finishTyping(true);
      return;
    }
    if (!sheet) return;
    var size = 18 / zoom();
    var color = DRAW_COLORS[drawColor];
    var left = Math.max(0, Math.min(x, window.innerWidth - 8 * size));
    var top = Math.max(0, Math.min(y - size * 0.8, window.innerHeight - size * 1.6));
    var input = document.createElement("input");
    input.type = "text";
    input.maxLength = MAX_MARK_TEXT;
    input.autocomplete = "off";
    input.spellcheck = false;
    input.setAttribute("enterkeyhint", "done");
    input.setAttribute("aria-label", "Text on the drawing");
    setStyle(input, { display: "block", position: "fixed", left: left + "px", top: top + "px", "box-sizing": "border-box", width: Math.max(8 * size, Math.min(20 * size, window.innerWidth - left - 4)) + "px", height: size * 1.6 + "px", margin: "0", padding: "0 " + size / 4 + "px", font: "600 " + size + "px " + DRAW_FONT, "line-height": size * 1.4 + "px", color: color, background: "rgba(255, 255, 255, 0.92)", border: 1 / zoom() + "px dashed " + color, "border-radius": 3 / zoom() + "px", outline: "none", "box-shadow": "none", "pointer-events": "auto", "-webkit-user-select": "text", "user-select": "text" });
    sheet.root.appendChild(input);
    typing = { input: input, at: pagePoint(left + size / 4 + 1 / zoom(), top + size * 0.3), color: drawColor, size: size };
    input.addEventListener("blur", function () {
      if (typing && typing.input === input) finishTyping(true);
    });
    try {
      input.focus({ preventScroll: true });
    } catch (e) {
      input.focus();
    }
  }

  function finishTyping(keep) {
    if (!typing) return;
    var done = typing;
    typing = null;
    var text = picker.squash(done.input.value).slice(0, MAX_MARK_TEXT);
    done.input.remove();
    if (!keep || !text) {
      syncSheet();
      return;
    }
    var at = done.at;
    var ctx = sheet.canvas.getContext("2d");
    if (ctx) {
      ctx.save();
      ctx.font = "600 " + done.size + "px " + DRAW_FONT;
      var right = window.scrollX + window.innerWidth - 4 / zoom() - ctx.measureText(text).width;
      ctx.restore();
      if (at[0] > right) at = [Math.max(window.scrollX + 2 / zoom(), right), at[1]];
    }
    var mark = { kind: "text", color: done.color, width: 1, size: done.size, text: text, points: [at] };
    place(mark);
    commit(marks.concat([mark]));
  }

  function anchorOf(mark) {
    var p = mark.points;
    if (mark.kind === "arrow") return p[p.length - 1];
    if (mark.kind === "text") return [p[0][0] + mark.size / 2, p[0][1] + mark.size / 2];
    var box = boxOf(mark);
    return [(box.x1 + box.x2) / 2, (box.y1 + box.y2) / 2];
  }

  function place(mark) {
    var at = anchorOf(mark);
    var x = Math.max(0, Math.min(window.innerWidth - 1, at[0] - window.scrollX));
    var y = Math.max(0, Math.min(window.innerHeight - 1, at[1] - window.scrollY));
    var el = null;
    try {
      el = picker.hit(x, y);
    } catch (e) {}
    mark.target = null;
    mark.scrollers = [];
    if (!el || el.nodeType !== 1) return;
    try {
      mark.scrollers = scrollersOf(el);
    } catch (e) {}
    var name = "";
    try {
      name = picker.squash(picker.nameOf(el)) || picker.squash(picker.textOf(el));
    } catch (e) {}
    mark.target = { label: picker.cut(picker.describe(el) + (name ? ' "' + picker.cut(name, 60) + '"' : ""), 160), selector: picker.selectorOf(el) };
  }

  function commit(next) {
    past.push(marks);
    if (past.length > MAX_STEPS) past.shift();
    marks = next.slice(-MAX_MARKS);
    future = [];
    drawn();
  }

  function state() {
    return { drawing: drawing, count: marks.length, undo: past.length > 0, redo: future.length > 0 };
  }

  function drawn() {
    syncSheet();
    tell("drawn", state());
  }

  function stepDrawing(step) {
    finishTyping(false);
    if (step < 0 && past.length) {
      future.push(marks);
      marks = past.pop();
    } else if (step > 0 && future.length) {
      past.push(marks);
      marks = future.pop();
    } else {
      return;
    }
    drawn();
  }

  function clearDrawing() {
    finishTyping(false);
    if (marks.length) commit([]);
  }

  function leftDrawing() {
    var path = location.pathname + location.search;
    if (path === drawPath) return;
    drawPath = path;
    resetDrawing();
  }

  function resetDrawing() {
    if (!marks.length && !past.length && !future.length && !typing && !stroke) return;
    finishTyping(false);
    stroke = null;
    marks = [];
    past = [];
    future = [];
    drawn();
  }

  function setDrawing(on, tool, color) {
    var nextTool = DRAW_TOOLS[tool] === true ? tool : drawTool;
    var nextColor = Object.prototype.hasOwnProperty.call(DRAW_COLORS, color) ? color : drawColor;
    if (!on || nextTool !== "text") finishTyping(true);
    if (!on) stroke = null;
    drawTool = nextTool;
    drawColor = nextColor;
    drawing = on;
    if (typing) {
      typing.color = drawColor;
      setStyle(typing.input, { color: DRAW_COLORS[drawColor], "border-color": DRAW_COLORS[drawColor] });
    }
    drawn();
  }

  window.addEventListener("keydown", function (event) {
    if (!drawing || (trusted && !event.isTrusted)) return;
    var key = String(event.key || "");
    if (sheet && event.target === sheet.host && onText(event)) {
      event.stopImmediatePropagation();
      if (key === "Enter" && !event.isComposing) {
        event.preventDefault();
        finishTyping(true);
      } else if (key === "Escape") {
        event.preventDefault();
        finishTyping(false);
      }
      return;
    }
    var mod = (event.ctrlKey || event.metaKey) && !event.altKey;
    var lower = key.toLowerCase();
    var step = mod && lower === "z" ? (event.shiftKey ? 1 : -1) : mod && lower === "y" ? 1 : 0;
    if (step || key === "Escape" || /^( |Spacebar|PageUp|PageDown|Home|End|ArrowUp|ArrowDown|ArrowLeft|ArrowRight)$/.test(key)) {
      event.preventDefault();
      event.stopImmediatePropagation();
      if (step) stepDrawing(step);
      if (key === "Escape") {
        setDrawing(false);
        tell("draw-off");
      }
    }
  }, true);

  function markList() {
    return marks.slice(0, 50).map(function (mark) {
      var at = anchorOf(mark);
      var moved = scrolled(mark, null);
      var x = at[0] - moved[0] - window.scrollX;
      var y = at[1] - moved[1] - window.scrollY;
      return { kind: mark.kind, color: mark.color, text: mark.text || "", target: mark.target || null, seen: x >= 0 && y >= 0 && x <= window.innerWidth && y <= window.innerHeight };
    });
  }

  return {
    set: setDrawing,
    step: stepDrawing,
    clear: clearDrawing,
    reset: resetDrawing,
    left: leftDrawing,
    state: state,
    finish: function () { finishTyping(true); },
    marks: markList,
    owns: function (node) { return Boolean(sheet) && node === sheet.host; },
  };
}
