// dcPicker names an element alike for a pick, a read and a click.
function dcPicker(options) {
  "use strict";
  var tag = options.tag;
  var closed = options.closed === true;
  var trusted = options.trusted === true;
  var tell = options.tell;
  var skip = options.skip || function () { return false; };
  var picking = false;
  var overlay = null;
  var hovered = null;
  var marked = null;
  var handles = new Map();
  var nextHandle = 1;
  var widened = new WeakMap();
  var redrawQueued = false;
  var MAX_HANDLES = 64;
  var HOVER_RGB = "43, 125, 233";
  var MARK_RGB = "247, 103, 7";
  var zoom = 1;
  var TEST_ATTRS = ["data-testid", "data-test-id", "data-test", "data-cy", "data-qa", "data-test-subj"];

  function cut(value, max) {
    value = String(value == null ? "" : value);
    return value.length > max ? value.slice(0, max - 1) + "…" : value;
  }

  function squash(value) {
    return String(value == null ? "" : value).replace(/\s+/g, " ").trim();
  }

  function setStyle(node, props) {
    for (var name in props) node.style.setProperty(name, props[name], "important");
  }

  function makeOverlay() {
    var host = document.createElement(tag);
    setStyle(host, { all: "initial", display: "block", position: "fixed", left: "0", top: "0", width: "0", height: "0", margin: "0", padding: "0", border: "0", overflow: "visible", background: "transparent", "pointer-events": "none", "z-index": "2147483647" });
    var root = host.attachShadow({ mode: closed ? "closed" : "open" });
    var glass = document.createElement("div");
    setStyle(glass, { display: "none", position: "fixed", left: "0", top: "0", right: "0", bottom: "0", margin: "0", cursor: "crosshair", "pointer-events": "auto", background: "transparent", "-webkit-tap-highlight-color": "transparent" });
    glass.addEventListener("click", function () {});
    root.appendChild(glass);
    return { host: host, glass: glass, hover: makeBox(root, HOVER_RGB), mark: makeBox(root, MARK_RGB) };
  }

  function makeBox(root, rgb) {
    var box = document.createElement("div");
    setStyle(box, { display: "none", position: "fixed", "box-sizing": "border-box", margin: "0", padding: "0", border: "2px solid rgb(" + rgb + ")", "border-radius": "2px", background: "rgba(" + rgb + ", 0.12)", "pointer-events": "none" });
    var tag = document.createElement("div");
    setStyle(tag, { position: "absolute", left: "-2px", display: "flex", "max-width": "min(80vw, 420px)", "white-space": "pre", padding: "2px 6px", "border-radius": "3px", background: "rgb(" + rgb + ")", color: "#fff", font: "600 11px/1.4 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace", "letter-spacing": "0", "text-transform": "none" });
    var name = document.createElement("span");
    setStyle(name, { overflow: "hidden", "min-width": "0", "text-overflow": "ellipsis" });
    var size = document.createElement("span");
    setStyle(size, { flex: "none" });
    tag.appendChild(name);
    tag.appendChild(size);
    box.appendChild(tag);
    root.appendChild(box);
    return { box: box, tag: tag, name: name, size: size };
  }

  function modal(node) {
    try {
      return node.localName === "dialog" && node.matches(":modal");
    } catch (e) {
      return false;
    }
  }

  function lightPopover(node) {
    try {
      return (node.popover === "auto" || node.popover === "hint") && node.matches(":popover-open");
    } catch (e) {
      return false;
    }
  }

  function holds(node) {
    return modal(node) || lightPopover(node);
  }

  // A modal dialog makes everything outside it inert, the overlay too, and a
  // press outside an open popover closes it. So the overlay stands inside the
  // topmost of them.
  function holder() {
    var dialog = topModal();
    var popover = topPopover();
    return popover && (!dialog || dialog.contains(popover)) ? popover : dialog;
  }

  function topModal() {
    var dialogs = document.getElementsByTagName("dialog");
    for (var i = dialogs.length - 1; i >= 0; i--) {
      if (modal(dialogs[i])) return dialogs[i];
    }
    return null;
  }

  // Open popovers stack, each above the one that holds it or the button that
  // opened it, and hints above the rest.
  function topPopover() {
    var open;
    try {
      open = Array.prototype.filter.call(document.querySelectorAll(":popover-open"), lightPopover);
    } catch (e) {
      return null;
    }
    var top = null;
    for (var i = 0; i < open.length; i++) {
      if (covered(open[i], open)) continue;
      if (!top || open[i].popover === "hint" || top.popover !== "hint") top = open[i];
    }
    return top;
  }

  function covered(popover, open) {
    for (var i = 0; i < open.length; i++) {
      var other = open[i];
      if (other === popover) continue;
      if (popover.contains(other)) return true;
      if (!other.id) continue;
      var buttons = popover.querySelectorAll("[popovertarget], [commandfor]");
      for (var j = 0; j < buttons.length; j++) {
        if (buttons[j].getAttribute("popovertarget") === other.id || buttons[j].getAttribute("commandfor") === other.id) return true;
      }
    }
    return false;
  }

  function raise(into) {
    raiseHost(overlay.host, into || holder());
  }

  function settle(into) {
    var parent = overlay.host.parentNode;
    if (into ? parent === into : overlay.host.isConnected && (parent === document.documentElement || holds(parent))) return;
    raise(into);
  }

  // A popover the page opens while picking would close on a press beside it,
  // so the overlay moves into it before any press: moved during a press, it
  // loses the click. It never leaves its modal dialog, outside it is inert.
  function onToggle(event) {
    if (!overlay || !overlay.host.isConnected || !event.isTrusted || event.target === overlay.host) return;
    var popover = topPopover();
    var dialog = overlay.host.closest("dialog");
    settle(popover && (!dialog || !modal(dialog) || dialog.contains(popover)) ? popover : null);
  }
  window.addEventListener("toggle", onToggle, true);

  function raiseHost(host, parent) {
    parent = parent || document.documentElement;
    if (host.parentNode !== parent) parent.appendChild(host);
    if (typeof host.showPopover !== "function") return;
    try {
      if (!host.hasAttribute("popover")) host.setAttribute("popover", "manual");
      if (host.matches(":popover-open")) host.hidePopover();
      host.showPopover();
    } catch (e) {}
  }

  function syncOverlay() {
    if (!picking && !marked) {
      if (overlay && overlay.host.isConnected) {
        try {
          if (overlay.host.matches(":popover-open")) overlay.host.hidePopover();
        } catch (e) {}
        overlay.host.remove();
      }
      return;
    }
    if (!overlay) overlay = makeOverlay();
    settle(null);
    setStyle(overlay.glass, { display: picking ? "block" : "none" });
    draw(overlay.hover, picking && hovered !== marked ? hovered : null);
    draw(overlay.mark, marked);
  }

  function queueRedraw() {
    if (redrawQueued || !overlay || !overlay.host.isConnected) return;
    redrawQueued = true;
    window.requestAnimationFrame(function () {
      redrawQueued = false;
      if (overlay) syncOverlay();
    });
  }
  window.addEventListener("scroll", queueRedraw, { capture: true, passive: true });
  window.addEventListener("resize", queueRedraw, { passive: true });

  function frameOffset(win) {
    var x = 0;
    var y = 0;
    while (win && win !== window) {
      var frame = null;
      try {
        frame = win.frameElement;
      } catch (e) {}
      if (!frame) break;
      var box = frame.getBoundingClientRect();
      var cs = frame.ownerDocument.defaultView.getComputedStyle(frame);
      x += box.left + frame.clientLeft + parseFloat(cs.paddingLeft || "0");
      y += box.top + frame.clientTop + parseFloat(cs.paddingTop || "0");
      win = frame.ownerDocument.defaultView;
    }
    return { x: x, y: y };
  }

  function topRect(el) {
    var box = el.getBoundingClientRect();
    var off = frameOffset(el.ownerDocument.defaultView);
    return { x: box.left + off.x, y: box.top + off.y, width: box.width, height: box.height };
  }

  function describe(el) {
    var out = el.localName || "";
    if (el.id) out += "#" + el.id;
    var classes = el.classList ? Array.prototype.slice.call(el.classList, 0, 3) : [];
    if (classes.length) out += "." + classes.join(".");
    if (el.classList && el.classList.length > 3) out += "…";
    return cut(out, 80);
  }

  // On a page shown scaled down, the outline and its label grow by as much
  // to stay readable.
  function setZoom(value) {
    var next = isFinite(value) && value >= 0.05 && value <= 1 ? value : 1;
    if (next === zoom) return;
    zoom = next;
    if (overlay) syncOverlay();
  }

  function draw(part, el) {
    if (!el || !el.isConnected) {
      setStyle(part.box, { display: "none" });
      return;
    }
    var r = topRect(el);
    var line = 2 / zoom;
    var head = 20 / zoom;
    setStyle(part.box, { display: "block", left: r.x + "px", top: r.y + "px", width: Math.max(r.width, line) + "px", height: Math.max(r.height, line) + "px", "border-width": line + "px" });
    part.name.textContent = describe(el);
    part.size.textContent = "  " + Math.round(r.width) + "×" + Math.round(r.height);
    var top = r.y >= head ? -head + "px" : r.y + r.height + head + line <= window.innerHeight ? "calc(100% + " + line + "px)" : "0px";
    setStyle(part.tag, { top: top, left: -line + "px", "font-size": 11 / zoom + "px", "max-width": "min(80vw, " + 420 / zoom + "px)", padding: 2 / zoom + "px " + 6 / zoom + "px", "border-radius": 3 / zoom + "px" });
    var room = document.documentElement.clientWidth || window.innerWidth;
    var at = Math.max(0, Math.min(r.x, room - part.tag.offsetWidth));
    if (at !== r.x) setStyle(part.tag, { left: at - r.x - line + "px" });
  }

  function firstHit(list, accept) {
    for (var i = 0; i < list.length; i++) {
      if (accept(list[i])) return list[i];
    }
    return null;
  }

  function hit(x, y) {
    var host = overlay && overlay.host;
    var el = firstHit(document.elementsFromPoint(x, y), function (node) { return node !== host && !skip(node); });
    for (var depth = 0; el && depth < 12; depth++) {
      var shadow = el.shadowRoot;
      if (shadow && shadow.elementsFromPoint) {
        var inner = firstHit(shadow.elementsFromPoint(x, y), function (node) { return node !== el && node.getRootNode() === shadow; });
        if (inner) {
          el = inner;
          continue;
        }
      }
      if (el.localName === "iframe" || el.localName === "frame") {
        var doc = null;
        try {
          doc = el.contentDocument;
        } catch (e) {}
        if (doc && doc.elementsFromPoint) {
          var box = el.getBoundingClientRect();
          var cs = window.getComputedStyle(el);
          var ix = x - box.left - el.clientLeft - parseFloat(cs.paddingLeft || "0");
          var iy = y - box.top - el.clientTop - parseFloat(cs.paddingTop || "0");
          var nested = firstHit(doc.elementsFromPoint(ix, iy), function () { return true; });
          if (nested) {
            el = nested;
            x = ix;
            y = iy;
            continue;
          }
        }
      }
      break;
    }
    return el;
  }

  ["pointerdown", "pointerup", "pointermove", "pointerover", "pointerout", "pointerenter", "pointerleave", "pointercancel",
    "mousedown", "mouseup", "mousemove", "mouseover", "mouseout", "mouseenter", "mouseleave",
    "click", "dblclick", "auxclick", "contextmenu", "touchstart", "touchmove", "touchend", "touchcancel", "dragstart", "selectstart"].forEach(function (type) {
    window.addEventListener(type, function (event) {
      if (!picking || !overlay) return;
      if (event.target !== overlay.host) {
        if (event.isTrusted) stray(event);
        return;
      }
      event.stopImmediatePropagation();
      if (trusted && !event.isTrusted) return;
      onGlass(event);
    }, { capture: true, passive: type === "touchstart" || type === "touchmove" });
  });

  // Input that misses the overlay while picking, as under a modal dialog the
  // page opened, still belongs to the picker: the overlay moves into it and
  // takes the event. A pointer leaving an element names that element, not
  // what lies under the pointer now.
  function stray(event) {
    event.stopImmediatePropagation();
    if (/(over|out|enter|leave)$/.test(event.type)) return;
    settle(firstHit(event.composedPath(), holds));
    onGlass(event);
  }

  function onGlass(event) {
    switch (event.type) {
      case "mousedown":
      case "dblclick":
      case "auxclick":
      case "contextmenu":
      case "dragstart":
      case "selectstart":
        event.preventDefault();
        break;
      case "pointermove":
        if (event.pointerType === "touch") break;
        var under = hit(event.clientX, event.clientY);
        if (under !== hovered) {
          hovered = under;
          syncOverlay();
        }
        break;
      case "pointerleave":
        hovered = null;
        syncOverlay();
        break;
      case "click":
        event.preventDefault();
        var el = hit(event.clientX, event.clientY);
        if (el) pick(el, 0);
        break;
      default:
    }
  }

  function onWheel(event) {
    if (!picking || !overlay || event.target !== overlay.host) return;
    event.stopImmediatePropagation();
    if (trusted && !event.isTrusted) return;
    var scroller = scrollerOf(hit(event.clientX, event.clientY), event.deltaX, event.deltaY);
    if (!scroller) return;
    event.preventDefault();
    var unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? scroller.clientHeight : 1;
    scroller.scrollBy(event.deltaX * unit, event.deltaY * unit);
  }

  function canScroll(node, dx, dy, root) {
    var cs = node.ownerDocument.defaultView.getComputedStyle(node);
    var y = dy && (root || /(auto|scroll|overlay)/.test(cs.overflowY)) && (dy > 0 ? node.scrollTop + node.clientHeight < node.scrollHeight - 1 : node.scrollTop > 0);
    var x = dx && (root || /(auto|scroll|overlay)/.test(cs.overflowX)) && (dx > 0 ? node.scrollLeft + node.clientWidth < node.scrollWidth - 1 : node.scrollLeft > 0);
    return Boolean(x || y);
  }

  function scrollerOf(el, dx, dy) {
    for (var node = el, depth = 0; node && depth < 200; node = parentOf(node), depth++) {
      var doc = node.ownerDocument;
      if (node === doc.documentElement || node === doc.body) {
        if (doc === document) return null;
        var root = doc.scrollingElement;
        if (root && canScroll(root, dx, dy, true)) return root;
        continue;
      }
      if (canScroll(node, dx, dy, false)) return node;
    }
    return null;
  }

  function setPicking(on) {
    if (picking === on) return;
    picking = on;
    hovered = null;
    if (on) {
      if (!overlay) overlay = makeOverlay();
      raise();
      window.addEventListener("wheel", onWheel, { capture: true, passive: false });
    } else {
      window.removeEventListener("wheel", onWheel, { capture: true });
    }
    syncOverlay();
  }

  function parentOf(el) {
    if (el.parentElement) return el.parentElement;
    var root = el.getRootNode ? el.getRootNode() : null;
    if (root && root.host) return root.host;
    if (el.ownerDocument === document || el !== el.ownerDocument.documentElement) return null;
    try {
      return el.ownerDocument.defaultView.frameElement || null;
    } catch (e) {
      return null;
    }
  }

  function childOf(el) {
    var back = widened.get(el);
    if (back && back.isConnected && parentOf(back) === el) return back;
    var kids = el.children && el.children.length ? el.children : el.shadowRoot ? el.shadowRoot.children : [];
    var first = null;
    for (var i = 0; i < kids.length; i++) {
      var kid = kids[i];
      if (/^(script|style|template|noscript|link|meta|title|head)$/.test(kid.localName) || (overlay && kid === overlay.host)) continue;
      if (!first) first = kid;
      if (kid.getClientRects().length) return kid;
    }
    if (!first && (el.localName === "iframe" || el.localName === "frame")) {
      try {
        first = el.contentDocument ? el.contentDocument.documentElement : null;
      } catch (e) {}
    }
    return first;
  }

  function pick(el, replaces) {
    var handle = nextHandle++;
    handles.set(handle, el);
    if (handles.size > MAX_HANDLES) handles.delete(handles.keys().next().value);
    marked = el;
    hovered = null;
    syncOverlay();
    var element;
    try {
      element = capture(el);
    } catch (e) {
      element = { tag: el.localName || "", selector: "", html: "", text: "" };
    }
    tell("picked", { handle: handle, replaces: replaces || 0, element: element });
  }

  function adjust(handle, toward) {
    var el = handles.get(handle);
    if (!el || !el.isConnected) return;
    var next = toward === "parent" ? parentOf(el) : childOf(el);
    if (!next) return;
    if (toward === "parent") widened.set(next, el);
    pick(next, handle);
  }

  function highlight(handle, selector, reveal, key) {
    var el = handles.get(handle) || null;
    if (!el || !el.isConnected) el = selector ? resolve(selector) : null;
    marked = el;
    if (el && reveal) {
      var r = topRect(el);
      if (r.y < 0 || r.x < 0 || r.y + Math.min(r.height, 40) > window.innerHeight || r.x + Math.min(r.width, 40) > window.innerWidth) {
        try {
          el.scrollIntoView({ block: "center", inline: "nearest" });
        } catch (e) {}
      }
    }
    syncOverlay();
    if (key) tell("highlighted", { key: key, found: Boolean(el) });
  }

  function resolve(selector) {
    var parts = selector.split(" >>> ");
    var root = document;
    var el = null;
    for (var i = 0; i < parts.length && i < 12; i++) {
      if (!root) return null;
      try {
        el = root.querySelector(parts[i]);
      } catch (e) {
        return null;
      }
      if (!el) return null;
      if (i < parts.length - 1) {
        root = el.shadowRoot;
        if (!root) {
          try {
            root = el.contentDocument;
          } catch (e) {
            root = null;
          }
        }
      }
    }
    return el;
  }

  function quote(value) {
    return '"' + String(value).replace(/["\\]/g, "\\$&").replace(/\n/g, "\\a ") + '"';
  }

  function cssEscape(value) {
    return window.CSS && CSS.escape ? CSS.escape(value) : String(value).replace(/[^\w-]/g, "\\$&");
  }

  function stableId(id) {
    return Boolean(id) && id.length <= 64 && !/^[\d:]|:|[0-9a-f]{8,}|\d{4,}|^(radix-|headlessui-|ember\d|mui-|react-select-|downshift-|rc-)/i.test(id);
  }

  function ownSelectors(el) {
    var out = [];
    var tag = el.localName;
    if (stableId(el.id)) out.push("#" + cssEscape(el.id));
    for (var i = 0; i < TEST_ATTRS.length; i++) {
      var value = el.getAttribute(TEST_ATTRS[i]);
      if (value && value.length <= 80) out.push("[" + TEST_ATTRS[i] + "=" + quote(value) + "]");
    }
    var name = el.getAttribute("name");
    if (name && name.length <= 80 && /^(input|select|textarea|button|form|iframe|fieldset|output)$/.test(tag)) out.push(tag + "[name=" + quote(name) + "]");
    var label = el.getAttribute("aria-label");
    if (label && label.length <= 60) out.push(tag + "[aria-label=" + quote(label) + "]");
    var role = el.getAttribute("role");
    if (role && role.length <= 30) out.push(tag + "[role=" + quote(role) + "]");
    var href = tag === "a" ? el.getAttribute("href") : "";
    if (href && href.length <= 80) out.push("a[href=" + quote(href) + "]");
    return out.filter(function (selector) { return selector.indexOf(" >>> ") < 0; });
  }

  function step(el) {
    var tag = el.localName;
    var parent = el.parentNode;
    if (!parent || !parent.children) return cssEscape(tag);
    var same = 0;
    var index = 0;
    for (var i = 0; i < parent.children.length; i++) {
      var sibling = parent.children[i];
      if (sibling.localName !== tag) continue;
      same++;
      if (sibling === el) index = same;
    }
    return cssEscape(tag) + (same > 1 ? ":nth-of-type(" + index + ")" : "");
  }

  function unique(selector, root, el) {
    try {
      var found = root.querySelectorAll(selector);
      return found.length === 1 && found[0] === el;
    } catch (e) {
      return false;
    }
  }

  function selectorIn(el, root) {
    var own = ownSelectors(el);
    for (var i = 0; i < own.length; i++) {
      if (unique(own[i], root, el)) return own[i];
    }
    var path = [];
    for (var node = el, depth = 0; node && node.nodeType === 1 && depth < 40; node = node.parentElement, depth++) {
      path.unshift(step(node));
      var chain = path.join(" > ");
      if (unique(chain, root, el)) return chain;
      var parent = node.parentElement;
      if (!parent) break;
      var anchors = ownSelectors(parent);
      for (var j = 0; j < anchors.length; j++) {
        if (unique(anchors[j] + " > " + chain, root, el)) return anchors[j] + " > " + chain;
      }
    }
    return path.join(" > ");
  }

  function selectorOf(el) {
    var parts = [];
    for (var node = el, depth = 0; node && depth < 8; depth++) {
      var root = node.getRootNode ? node.getRootNode() : node.ownerDocument;
      parts.unshift(selectorIn(node, root));
      if (root.host) {
        node = root.host;
        continue;
      }
      var frame = null;
      try {
        frame = node.ownerDocument !== document ? node.ownerDocument.defaultView.frameElement : null;
      } catch (e) {}
      node = frame;
    }
    return cut(parts.join(" >>> "), 600);
  }

  var IMPLICIT_ROLES = {
    article: "article", aside: "complementary", button: "button", dialog: "dialog", fieldset: "group", figure: "figure",
    footer: "contentinfo", form: "form", h1: "heading", h2: "heading", h3: "heading", h4: "heading", h5: "heading", h6: "heading",
    header: "banner", hr: "separator", li: "listitem", main: "main", menu: "list", meter: "meter", nav: "navigation", ol: "list",
    option: "option", output: "status", progress: "progressbar", search: "search", section: "region", select: "combobox",
    summary: "button", table: "table", tbody: "rowgroup", td: "cell", textarea: "textbox", th: "columnheader", thead: "rowgroup",
    tr: "row", ul: "list", details: "group", dl: "list",
  };
  var INPUT_ROLES = {
    button: "button", submit: "button", reset: "button", image: "button", checkbox: "checkbox", radio: "radio", range: "slider",
    number: "spinbutton", search: "searchbox", email: "textbox", tel: "textbox", text: "textbox", url: "textbox",
  };

  function roleOf(el) {
    var explicit = squash(el.getAttribute("role")).split(" ")[0];
    if (explicit) return explicit;
    var tag = el.localName;
    if (tag === "a" || tag === "area") return el.hasAttribute("href") ? "link" : "";
    if (tag === "input") return INPUT_ROLES[(el.getAttribute("type") || "text").toLowerCase()] || "";
    if (tag === "img") return el.getAttribute("alt") === "" ? "presentation" : "img";
    return IMPLICIT_ROLES[tag] || "";
  }

  function textOf(el) {
    return typeof el.innerText === "string" ? el.innerText : el.textContent || "";
  }

  function nameOf(el) {
    var doc = el.ownerDocument;
    var ids = squash(el.getAttribute("aria-labelledby"));
    if (ids) {
      var labelled = squash(ids.split(" ").slice(0, 8).map(function (id) {
        var node = doc.getElementById(id);
        return node ? node.textContent : "";
      }).join(" "));
      if (labelled) return labelled;
    }
    var label = squash(el.getAttribute("aria-label"));
    if (label) return label;
    var tag = el.localName;
    if (/^(input|select|textarea|meter|progress|output)$/.test(tag)) {
      if (el.labels && el.labels.length) return Array.prototype.map.call(el.labels, function (node) { return textOf(node); }).join(" ");
      var type = (el.getAttribute("type") || "").toLowerCase();
      if (tag === "input" && /^(button|submit|reset)$/.test(type)) return el.value || "";
      if (tag === "input" && type === "image") return el.getAttribute("alt") || "";
      if (el.getAttribute("placeholder")) return el.getAttribute("placeholder");
    }
    if (tag === "img" || tag === "area") return el.getAttribute("alt") || el.getAttribute("title") || "";
    if (tag === "svg") {
      var title = el.querySelector("title");
      if (title) return title.textContent;
    }
    if (/^(button|a|h[1-6]|summary|option|legend|caption|figcaption|label|th|td|li|dt|dd)$/.test(tag) ||
      /^(button|link|tab|menuitem|menuitemcheckbox|menuitemradio|heading|option|cell|checkbox|radio|switch|treeitem|row|columnheader)$/.test(roleOf(el))) {
      return textOf(el);
    }
    return el.getAttribute("title") || "";
  }

  var TELLING = /^(name|type|role|href|src|alt|title|placeholder|for|action|method|target|rel|disabled|checked|selected|required|readonly|hidden|tabindex|value|lang|dir|aria-[\w-]+)$/;

  function attrsOf(el) {
    var tests = [];
    var telling = [];
    var data = [];
    for (var i = 0; i < el.attributes.length; i++) {
      var attr = el.attributes[i];
      var name = attr.name.toLowerCase();
      if (TEST_ATTRS.indexOf(name) >= 0) tests.push([name, attr.value]);
      else if (TELLING.test(name)) {
        if (name === "value" && !(/^(button|option)$/.test(el.localName) || /^(button|submit|reset|checkbox|radio)$/i.test(el.getAttribute("type") || ""))) continue;
        telling.push([name, attr.value]);
      } else if (name.indexOf("data-") === 0) data.push([name, attr.value]);
    }
    return tests.concat(telling, data).slice(0, 12).map(function (pair) { return [cut(pair[0], 40), cut(pair[1], 120)]; });
  }

  var VOID = /^(area|base|br|col|embed|hr|img|input|link|meta|param|source|track|wbr)$/;

  function escapeText(value) {
    return value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  }

  function htmlOf(el, budget) {
    var out = [];
    var used = 0;
    function push(text) {
      out.push(text);
      used += text.length;
    }
    function attrs(node) {
      var text = "";
      for (var i = 0; i < node.attributes.length; i++) {
        if (i === 16) {
          text += " …";
          break;
        }
        var attr = node.attributes[i];
        var value = attr.value.length > 60 ? attr.value.slice(0, 57) + "…" : attr.value;
        text += " " + attr.name + (attr.value === "" ? "" : '="' + value.replace(/&/g, "&amp;").replace(/"/g, "&quot;") + '"');
      }
      return text;
    }
    function walk(node, depth) {
      if (overlay && node === overlay.host) return;
      if (node.nodeType === 3) {
        var text = node.data.replace(/\s+/g, " ");
        if (text.trim()) push(escapeText(cut(text, 120)));
        return;
      }
      if (node.nodeType !== 1) return;
      var tag = node.localName;
      push("<" + tag + attrs(node) + ">");
      if (VOID.test(tag)) return;
      if (tag === "script" || tag === "style") {
        var code = node.textContent || "";
        if (code.trim()) push(code.length > 80 ? "/* " + code.length + " characters */" : escapeText(code));
      } else if (tag === "svg" && node.children.length) {
        push("<!-- " + node.children.length + " shapes -->");
      } else {
        var kids = node.childNodes;
        for (var i = 0; i < kids.length; i++) {
          if (used >= budget || depth >= 8) {
            var rest = 0;
            for (var j = i; j < kids.length; j++) if (kids[j].nodeType === 1) rest++;
            if (rest) push("<!-- " + rest + " more -->");
            break;
          }
          walk(kids[i], depth + 1);
        }
      }
      push("</" + tag + ">");
    }
    walk(el, 0);
    return cut(out.join(""), budget * 2);
  }

  function four(cs, prefix, suffix) {
    var t = cs.getPropertyValue(prefix + "-top" + suffix).trim();
    var r = cs.getPropertyValue(prefix + "-right" + suffix).trim();
    var b = cs.getPropertyValue(prefix + "-bottom" + suffix).trim();
    var l = cs.getPropertyValue(prefix + "-left" + suffix).trim();
    if (t === r && r === b && b === l) return t;
    if (t === b && r === l) return t + " " + r;
    return [t, r, b, l].join(" ");
  }

  function corners(cs) {
    var values = ["top-left", "top-right", "bottom-right", "bottom-left"].map(function (corner) {
      return cs.getPropertyValue("border-" + corner + "-radius").trim();
    });
    return values.every(function (value) { return value === values[0]; }) ? values[0] : values.join(" ");
  }

  function stylesOf(el) {
    var win = el.ownerDocument.defaultView;
    var cs = win.getComputedStyle(el);
    var out = [];
    function get(name) {
      return cs.getPropertyValue(name).trim();
    }
    function add(name, value) {
      if (value) out.push([name, cut(value, 160)]);
    }
    var display = get("display");
    var position = get("position");
    add("display", display);
    add("position", position);
    if (position !== "static") ["top", "right", "bottom", "left"].forEach(function (side) { if (get(side) !== "auto") add(side, get(side)); });
    add("box-sizing", get("box-sizing"));
    add("width", get("width"));
    add("height", get("height"));
    ["min-width", "min-height"].forEach(function (name) { if (!/^(0px|auto)$/.test(get(name))) add(name, get(name)); });
    ["max-width", "max-height"].forEach(function (name) { if (get(name) !== "none") add(name, get(name)); });
    add("margin", four(cs, "margin", ""));
    add("padding", four(cs, "padding", ""));
    var borderWidth = four(cs, "border", "-width");
    add("border", borderWidth === "0px" ? "none" : borderWidth + " " + four(cs, "border", "-style") + " " + four(cs, "border", "-color"));
    var radius = corners(cs);
    if (radius && radius !== "0px") add("border-radius", radius);
    add("font-family", get("font-family"));
    add("font-size", get("font-size"));
    add("font-weight", get("font-weight"));
    add("line-height", get("line-height"));
    if (get("font-style") !== "normal") add("font-style", get("font-style"));
    if (!/^(start|left)$/.test(get("text-align"))) add("text-align", get("text-align"));
    add("color", get("color"));
    add("background-color", get("background-color"));
    if (get("background-image") !== "none") add("background-image", get("background-image"));
    if (/flex/.test(display)) ["flex-direction", "flex-wrap", "justify-content", "align-items"].forEach(function (name) { add(name, get(name)); });
    if (/grid/.test(display)) ["grid-template-columns", "grid-template-rows", "grid-auto-flow", "justify-items", "align-items"].forEach(function (name) { add(name, get(name)); });
    if (/flex|grid/.test(display)) {
      var rowGap = get("row-gap");
      var columnGap = get("column-gap");
      add("gap", rowGap === columnGap ? rowGap : rowGap + " " + columnGap);
    }
    var parent = parentOf(el);
    var parentDisplay = parent && parent.nodeType === 1 ? win.getComputedStyle(parent).display : "";
    if (/flex/.test(parentDisplay)) {
      add("flex", get("flex-grow") + " " + get("flex-shrink") + " " + get("flex-basis"));
      if (get("align-self") !== "auto") add("align-self", get("align-self"));
      if (get("order") !== "0") add("order", get("order"));
    }
    if (/grid/.test(parentDisplay)) {
      var column = get("grid-column-start") + " / " + get("grid-column-end");
      var row = get("grid-row-start") + " / " + get("grid-row-end");
      if (column !== "auto / auto") add("grid-column", column);
      if (row !== "auto / auto") add("grid-row", row);
    }
    add("z-index", get("z-index"));
    var overflowX = get("overflow-x");
    var overflowY = get("overflow-y");
    add("overflow", overflowX === overflowY ? overflowX : overflowX + " " + overflowY);
    add("opacity", get("opacity"));
    if (get("visibility") !== "visible") add("visibility", get("visibility"));
    if (get("transform") !== "none") add("transform", get("transform"));
    if (get("box-shadow") !== "none") add("box-shadow", get("box-shadow"));
    return out.slice(0, 40);
  }

  function componentOf(el) {
    for (var node = el, depth = 0; node && depth < 40; node = parentOf(node), depth++) {
      var names = [];
      var keys;
      try {
        keys = Object.keys(node);
      } catch (e) {
        keys = [];
      }
      for (var i = 0; i < keys.length; i++) {
        if (keys[i].indexOf("__reactFiber$") !== 0 && keys[i].indexOf("__reactInternalInstance$") !== 0) continue;
        for (var fiber = node[keys[i]], hops = 0; fiber && names.length < 3 && hops < 200; fiber = fiber.return, hops++) {
          var type = fiber.type;
          var name = typeof type === "function" ? type.displayName || type.name : type && typeof type === "object" ? type.displayName || (type.render && (type.render.displayName || type.render.name)) || (type.type && (type.type.displayName || type.type.name)) : "";
          if (name && names.indexOf(name) < 0) names.push(name);
        }
        if (names.length) return { framework: "React", names: names };
      }
      var vue = node.__vueParentComponent;
      if (vue) {
        for (var instance = vue, steps = 0; instance && names.length < 3 && steps < 50; instance = instance.parent, steps++) {
          var options = instance.type || {};
          var vueName = options.name || options.__name || (options.__file ? String(options.__file).split("/").pop().replace(/\.vue$/, "") : "");
          if (vueName && names.indexOf(vueName) < 0) names.push(vueName);
        }
        if (names.length) return { framework: "Vue", names: names };
      }
      var legacy = node.__vue__;
      if (legacy) {
        for (var vm = legacy, hops2 = 0; vm && names.length < 3 && hops2 < 50; vm = vm.$parent, hops2++) {
          var legacyName = vm.$options && (vm.$options.name || vm.$options._componentTag);
          if (legacyName && names.indexOf(legacyName) < 0) names.push(legacyName);
        }
        if (names.length) return { framework: "Vue", names: names };
      }
      var svelte = node.__svelte_meta;
      if (svelte && svelte.loc && svelte.loc.file) {
        return { framework: "Svelte", names: [String(svelte.loc.file) + (svelte.loc.line ? ":" + svelte.loc.line : "")] };
      }
    }
    return null;
  }

  function capture(el) {
    var r = topRect(el);
    var component = componentOf(el);
    return {
      title: cut(document.title, 200),
      viewport: { w: window.innerWidth, h: window.innerHeight, dpr: window.devicePixelRatio || 1 },
      selector: selectorOf(el),
      tag: cut(el.localName, 40),
      id: cut(el.id || "", 100),
      classes: el.classList ? Array.prototype.slice.call(el.classList, 0, 20).map(function (name) { return cut(name, 60); }) : [],
      role: cut(roleOf(el), 40),
      name: cut(squash(nameOf(el)), 200),
      text: cut(squash(textOf(el)), 300),
      attrs: attrsOf(el),
      html: htmlOf(el, 1500),
      styles: stylesOf(el),
      box: { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height), sx: Math.round(window.scrollX), sy: Math.round(window.scrollY) },
      component: component ? { framework: component.framework, names: component.names.slice(0, 3).map(function (name) { return cut(name, 80); }) } : null,
      parent: Boolean(parentOf(el)),
      child: Boolean(childOf(el)),
    };
  }

  return {
    setPicking: setPicking,
    picking: function () { return picking; },
    adjust: adjust,
    highlight: highlight,
    setZoom: setZoom,
    zoom: function () { return zoom; },
    hit: hit,
    describe: describe,
    selectorOf: selectorOf,
    nameOf: nameOf,
    textOf: textOf,
    cut: cut,
    squash: squash,
    setStyle: setStyle,
    raiseHost: raiseHost,
    holder: holder,
  };
}
