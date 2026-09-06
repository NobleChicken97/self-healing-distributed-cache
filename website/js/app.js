/* SHDC field console — same-origin by default, CORS allows node switching.
   Content renders without JS; this file only fills live readings. */
(function () {
  "use strict";
  document.documentElement.classList.add("js");
  var REDUCED = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  /* ---------- tiny animation helper (anime.js if present, else instant) ---------- */
  function anim(targets, vars) {
    try {
      var A = window.anime;
      if (A && typeof A.animate === "function") { A.animate(targets, vars); return; }       // v4
      if (typeof A === "function") { var o = { targets: targets }; for (var k in vars) o[k] = vars[k]; A(o); return; } // v3
    } catch (e) { /* fall through to instant */ }
    if (typeof targets === "string") {
      document.querySelectorAll(targets).forEach(function (el) {
        if (vars.opacity) el.style.opacity = vars.opacity[vars.opacity.length - 1];
      });
    }
  }

  /* ---------- reveals: content visible by default, motion only enhances ---------- */
  function reveals() {
    var els = document.querySelectorAll(".hero-copy > *, .mesh-copy > *, .lab, .tgroup, .ledger-rows li");
    els.forEach(function (el) { el.classList.add("rv"); });
    if (REDUCED || !("IntersectionObserver" in window)) {
      els.forEach(function (el) { el.classList.add("in"); });
      return;
    }
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (en, i) {
        if (!en.isIntersecting) return;
        io.unobserve(en.target);
        en.target.classList.add("in");
        anim(en.target, { opacity: [0, 1], translateX: [-18, 0], duration: 700, delay: (i % 4) * 70, ease: "outExpo" });
      });
    }, { threshold: 0.12 });
    els.forEach(function (el) { io.observe(el); });
  }

  function esc(s) {
    return String(s == null ? "" : s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  /* ---------- state ---------- */
  var state = { base: "", nodes: [], ring: [], self: "", timer: null };

  function url(path) { return state.base + path; }
  function nodeLabel(addr) {
    var h = String(addr).split(":")[0];
    var parts = h.split(".");
    return parts.length === 4 ? "…" + parts.slice(-2).join(".") : h;
  }

  function fetchTimeout(path, opts, ms) {
    var ctl = new AbortController();
    var t = setTimeout(function () { ctl.abort(); }, ms || 8000);
    var init = opts || {};
    init.signal = ctl.signal;
    return fetch(url(path), init).finally(function () { clearTimeout(t); });
  }
  function fetchNode(addr, path, ms) {
    var ctl = new AbortController();
    var t = setTimeout(function () { ctl.abort(); }, ms || 8000);
    return fetch("http://" + addr + path, { signal: ctl.signal }).finally(function () { clearTimeout(t); });
  }
  function getJSON(res) {
    if (!res.ok) throw new Error("HTTP " + res.status);
    return res.json();
  }

  /* ---------- ops log ---------- */
  var opsLog = document.getElementById("opsLog");
  function logOp(html) {
    var li = document.createElement("li");
    var ts = new Date().toLocaleTimeString("en-GB", { hour12: false });
    li.innerHTML = "<b>" + ts + "</b> " + html;
    opsLog.prepend(li);
    while (opsLog.children.length > 60) opsLog.lastChild.remove();
  }

  /* ---------- clock ---------- */
  setInterval(function () {
    document.getElementById("clock").textContent =
      new Date().toLocaleTimeString("en-GB", { hour12: false });
  }, 1000);

  /* ---------- mesh SVG ---------- */
  var meshSvg = document.getElementById("meshSvg");
  var NS = "http://www.w3.org/2000/svg";
  function ellipsePos(i, n) {
    var a = -Math.PI / 2 + (i * 2 * Math.PI) / Math.max(n, 1);
    return { x: 320 + 225 * Math.cos(a), y: 200 + 128 * Math.sin(a) };
  }
  function renderMesh(nodes) {
    while (meshSvg.firstChild) meshSvg.removeChild(meshSvg.firstChild);
    function el(tag, attrs, parent) {
      var e = document.createElementNS(NS, tag);
      for (var k in attrs) e.setAttribute(k, attrs[k]);
      (parent || meshSvg).appendChild(e);
      return e;
    }
    // backdrop ring
    el("ellipse", { cx: 320, cy: 200, rx: 225, ry: 128, fill: "none", stroke: "currentColor", "stroke-opacity": 0.25, "stroke-width": 1.5 });
    var pos = nodes.map(function (_, i) { return ellipsePos(i, nodes.length); });
    // all-pairs gossip threads
    for (var a = 0; a < nodes.length; a++) {
      for (var b = a + 1; b < nodes.length; b++) {
        var silent = nodes[a].silent || nodes[b].silent;
        el("line", {
          x1: pos[a].x, y1: pos[a].y, x2: pos[b].x, y2: pos[b].y,
          stroke: silent ? "#8a8474" : "#A8562A", "stroke-width": silent ? 1 : 1.6,
          "class": silent ? "" : "flow", opacity: silent ? 0.4 : 0.75
        });
      }
    }
    nodes.forEach(function (n, i) {
      var g = el("g", {});
      var lamp = el("circle", { cx: pos[i].x, cy: pos[i].y, r: 17, fill: n.silent ? "#8a8474" : "#3E5C3F", stroke: "#201A13", "stroke-width": 2 }, g);
      el("circle", { cx: pos[i].x - 5, cy: pos[i].y - 5, r: 4, fill: "#EFE7D8", opacity: 0.85 }, g);
      var t = el("text", { x: pos[i].x, y: pos[i].y + 36, "text-anchor": "middle", "font-size": 13, fill: "#201A13" }, g);
      t.textContent = nodeLabel(n.addr);
      var ti = document.createElementNS(NS, "title");
      ti.textContent = n.id + " · entries " + n.entries + " · mem " + n.mem + "B · alive sees " + n.alive;
      g.appendChild(ti);
      if (!REDUCED && !n.silent) {
        try {
          var A = window.anime;
          if (A && typeof A.animate === "function") A.animate(lamp, { scale: [1, 1.12, 1], duration: 1600, ease: "inOutSine" });
        } catch (e) {}
      }
    });
    // arch mini-mesh mirrors the hero art
    var arch = document.getElementById("archMesh");
    if (arch) {
      while (arch.firstChild) arch.removeChild(arch.firstChild);
      nodes.forEach(function (n, i) {
        var a = -Math.PI / 2 + (i * 2 * Math.PI) / Math.max(nodes.length, 1);
        var cx = 180 + 105 * Math.cos(a), cy = 235 + 120 * Math.sin(a);
        var c = document.createElementNS(NS, "circle");
        c.setAttribute("cx", cx); c.setAttribute("cy", cy); c.setAttribute("r", 9);
        c.setAttribute("fill", n.silent ? "#8a8474" : "#D8A03C");
        c.setAttribute("stroke", "#EFE7D8"); c.setAttribute("stroke-width", "2");
        arch.appendChild(c);
      });
    }
  }

  /* ---------- ledger rows + telemetry ---------- */
  function bar(pct, cls) {
    return '<div class="bar"><i class="' + (cls || "") + '" style="width:' +
      Math.max(0, Math.min(100, pct)).toFixed(1) + '%"></i></div>';
  }
  function renderNodes(nodes) {
    var ul = document.getElementById("nodeRows");
    ul.innerHTML = nodes.map(function (n) {
      var cls = n.silent ? "st-bad" : "st-ok";
      var st = n.silent ? "SILENT" : "ALIVE · sees " + n.alive;
      return "<li><div class=\"who\">" + esc(n.short) + "<small>" + esc(n.id) + "</small></div>" +
        "<div class=\"what\">" + n.entries + " entries · " + n.mem + " B · " + n.pending + " pending" +
        bar(Math.min(100, n.entries / 4), "") + "</div>" +
        "<div class=\"st " + cls + "\">" + st + "</div></li>";
    }).join("");
    var tel = document.getElementById("telemetryBars");
    var maxE = Math.max.apply(null, nodes.map(function (n) { return n.entries; }).concat([1]));
    var maxM = Math.max.apply(null, nodes.map(function (n) { return n.mem; }).concat([1]));
    tel.innerHTML = nodes.map(function (n) {
      function row(label, v, max, unit, bad) {
        var pct = max > 0 ? (100 * v) / max : 0;
        return "<div class=\"tbar\"><span>" + label + "</span><span class=\"track\"><span class=\"fill" +
          (bad ? " bad" : "") + "\" style=\"width:" + pct.toFixed(1) + "%\"></span></span>" +
          "<span class=\"val\">" + v + (unit || "") + "</span></div>";
      }
      return "<div class=\"tgroup\"><p><b>" + esc(n.short) + "</b> · " + esc(n.addr) + "</p>" +
        row("entries", n.entries, maxE, "", false) +
        row("memory", n.mem, maxM, " B", false) +
        row("pending", n.pending, Math.max.apply(null, nodes.map(function (x) { return x.pending; }).concat([1])), "", n.pending > 0) +
        "</div>";
    }).join("");
  }

  /* ---------- polling ---------- */
  var lastAlive = {};
  var pollFails = 0;
  var lastRenderSig = "";
  function poll() {
    if (document.hidden) return;
    fetchTimeout("/ring/info").then(getJSON).then(function (ring) {
      pollFails = 0;
      var addrs = (ring.ring_nodes || []).map(function (n) { return n.Addr; }).filter(Boolean);
      state.ring = addrs;
      state.self = ring.node_id || "";
      document.getElementById("servedBy").textContent = window.location.host + " · " + (ring.node_id || "?");
      syncSwitcher(addrs);
      return Promise.allSettled(addrs.map(function (addr) {
        return Promise.all([
          fetchNode(addr, "/health").then(getJSON),
          fetchNode(addr, "/metrics").then(getJSON).catch(function () { return null; })
        ]).then(function (pair) {
          var h = pair[0], m = pair[1] || {};
          return {
            id: (h && h.node_id) || addr, addr: addr, short: nodeLabel(addr),
            alive: (h && h.alive_nodes) || 0,
            entries: m.entry_count || 0, mem: m.memory_usage || 0,
            pending: m.pending_repls || 0, silent: false
          };
        }).catch(function () {
          return { id: addr, addr: addr, short: nodeLabel(addr), alive: 0, entries: 0, mem: 0, pending: 0, silent: true };
        });
      }));
    }).then(function (results) {
      if (!results) return;
      state.nodes = results.map(function (r) { return r.value; });
      // Skip re-render when nothing changed: rebuilding the SVG/select every
      // poll would restart animations and steal focus from open controls.
      var sig = JSON.stringify(state.nodes);
      if (sig !== lastRenderSig) {
        lastRenderSig = sig;
        renderMesh(state.nodes);
        renderNodes(state.nodes);
      }
      // masthead
      var down = state.nodes.filter(function (n) { return n.silent; }).length;
      var lamp = document.getElementById("liveLamp");
      var txt = document.getElementById("liveText");
      if (down === 0) {
        lamp.className = "lamp on";
        var total = state.nodes.reduce(function (s, n) { return s + n.entries; }, 0);
        txt.textContent = "live · " + state.nodes.length + "/" + state.nodes.length + " nodes · " + total + " entries";
      } else {
        lamp.className = "lamp off";
        txt.textContent = "degraded · " + down + " silent";
      }
      // transitions → ops log
      state.nodes.forEach(function (n) {
        if (lastAlive[n.addr] !== undefined && lastAlive[n.addr] !== n.alive) {
          logOp(esc(n.short) + " now sees <b>" + n.alive + "</b> alive (was " + lastAlive[n.addr] + ")");
        }
        lastAlive[n.addr] = n.alive;
      });
      // ticker (N/N uses live membership, never a hardcoded cluster size)
      var facts = state.nodes.map(function (n) {
        return n.short + " " + (n.silent ? "silent" : n.alive + "/" + state.nodes.length + " alive · " + n.entries + " entries");
      }).join("  ·  ") + "  ·  RF 2 · 150 vnodes · gossip :7946  ·  ";
      document.getElementById("tickerA").textContent = facts;
      document.getElementById("tickerB").textContent = facts;
      document.getElementById("meshSummary").textContent =
        state.nodes.map(function (n) { return n.short + ": " + (n.silent ? "silent" : n.alive + " alive"); }).join(" — ");
    }).catch(function (err) {
      pollFails++;
      document.getElementById("liveLamp").className = "lamp off";
      document.getElementById("liveText").textContent = "unreachable — is a node serving this page?";
      document.getElementById("meshSummary").textContent = "poll failed: " + err.message;
      // Don't grey the board on a single blip; after repeats, mark stale.
      if (pollFails >= 2) {
        lastRenderSig = "";
        state.nodes.forEach(function (n) { n.silent = true; });
        renderMesh(state.nodes);
        renderNodes(state.nodes);
      }
    });
  }

  var lastSwitcherSig = "";
  function syncSwitcher(addrs) {
    // Rebuild only when membership changes: rebuilding every poll would
    // close an open dropdown and steal focus mid-interaction.
    var sig = addrs.join(",");
    if (sig === lastSwitcherSig) return;
    lastSwitcherSig = sig;
    var sel = document.getElementById("nodeSelect");
    var cur = sel.value;
    var opts = [{ v: "", t: "serving node (" + window.location.host + ")" }].concat(
      addrs.map(function (a) { return { v: "http://" + a, t: a }; })
    );
    sel.innerHTML = opts.map(function (o) {
      return "<option value=\"" + esc(o.v) + "\">" + esc(o.t) + "</option>";
    }).join("");
    sel.value = opts.some(function (o) { return o.v === cur; }) ? cur : "";
    state.base = sel.value;
  }
  document.getElementById("nodeSelect").addEventListener("change", function (e) {
    state.base = e.target.value;
    logOp("console now talks to <b>" + esc(state.base || window.location.host + " (serving node)") + "</b>");
  });

  /* ---------- inspector + console ---------- */
  var inspector = document.getElementById("inspector");
  function inspect(method, path, status, ms, body) {
    var cls = status >= 200 && status < 300 ? "ok" : "err";
    inspector.innerHTML = "<span class=\"" + cls + "\">" + method + " " + esc(path) + " → " + status +
      "</span> · " + ms + "ms\n" + esc(body);
  }
  function timed(path, opts) {
    var t0 = performance.now();
    return fetchTimeout(path, opts).then(function (res) {
      var ms = Math.round(performance.now() - t0);
      return res.text().then(function (body) { return { status: res.status, ms: ms, body: body }; });
    }).catch(function (err) {
      return { status: 0, ms: Math.round(performance.now() - t0), body: "request failed: " + err.message };
    });
  }
  document.getElementById("setForm").addEventListener("submit", function (e) {
    e.preventDefault();
    var f = e.target;
    // Empty TTL means the 1h dashboard default; an explicit 0 means no
    // expiry (parseInt(x)||default would silently turn 0 into 1h).
    var rawTtl = f.ttl.value.trim();
    var ttlMs = rawTtl === "" ? 3600000 : (parseInt(rawTtl, 10) || 0);
    var payload = { key: f.key.value.trim(), value: f.value.value, ttl_ms: ttlMs };
    timed("/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) })
      .then(function (r) {
        inspect("POST", "/set", r.status, r.ms, r.body);
        logOp("SET <b>" + esc(payload.key) + "</b> → " + r.status);
      });
  });
  document.getElementById("getForm").addEventListener("submit", function (e) {
    e.preventDefault();
    var key = e.target.key.value.trim();
    timed("/get?key=" + encodeURIComponent(key)).then(function (r) {
      inspect("GET", "/get?key=" + key, r.status, r.ms, r.body);
      logOp("GET <b>" + esc(key) + "</b> → " + r.status);
    });
  });
  document.getElementById("delForm").addEventListener("submit", function (e) {
    e.preventDefault();
    var key = e.target.key.value.trim();
    timed("/delete?key=" + encodeURIComponent(key), { method: "DELETE" }).then(function (r) {
      inspect("DELETE", "/delete?key=" + key, r.status, r.ms, r.body);
      logOp("DELETE <b>" + esc(key) + "</b> → " + r.status);
    });
  });

  /* ---------- quorum lab ---------- */
  document.getElementById("quorumForm").addEventListener("submit", function (e) {
    e.preventDefault();
    var f = e.target, out = document.getElementById("quorumOut");
    var payload = { key: f.key.value.trim(), value: f.value.value, ttl_ms: 120000 };
    out.textContent = "writing with majority…";
    timed("/quorum/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) })
      .then(function (w) {
        if (w.status !== 200) { out.textContent = "write → " + w.status + "\n" + w.body; return null; }
        return timed("/quorum/get?key=" + encodeURIComponent(payload.key));
      })
      .then(function (g) {
        if (!g) return;
        out.textContent = "read → " + g.status + "\n" + g.body;
        logOp("quorum round-trip on <b>" + esc(payload.key) + "</b> → " + g.status);
      });
  });

  /* ---------- TTL drill ---------- */
  var ttlRunning = false;
  document.getElementById("ttlDrill").addEventListener("click", function () {
    if (ttlRunning) return;
    ttlRunning = true;
    var box = document.getElementById("ttlBars");
    var key = "ttl-drill-" + Date.now().toString(36);
    var addrs = state.nodes.map(function (n) { return n.addr; });
    box.innerHTML = "<p class=\"mono small\">planting " + esc(key) + " with a 15s fuse…</p>";
    timed("/set", { method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ key: key, value: "here today", ttl_ms: 15000 }) }).then(function (w) {
      if (w.status !== 200) { box.innerHTML = "<p class=\"mono small\">plant failed: " + esc(w.body) + "</p>"; ttlRunning = false; return; }
      var t0 = Date.now();
      box.innerHTML = addrs.map(function (a, i) {
        return "<div class=\"ttl-row\" id=\"ttl-" + i + "\"><span>" + esc(nodeLabel(a)) +
          "</span><span class=\"ttl-track\"><span class=\"ttl-fill\" style=\"width:100%\"></span></span><span>…</span></div>";
      }).join("");
      var iv = setInterval(function () {
        var left = Math.max(0, 15000 - (Date.now() - t0));
        var jobs = addrs.map(function (a, i) {
          return fetchNode(a, "/get?key=" + encodeURIComponent(key), 4000)
            .then(function (r) { return r.ok; }).catch(function () { return null; })
            .then(function (alive) {
              var row = document.getElementById("ttl-" + i);
              if (!row) return alive;
              row.querySelector(".ttl-fill").style.width = (100 * left / 15000).toFixed(1) + "%";
              row.lastElementChild.textContent = alive === null ? "??" : (alive ? (left / 1000).toFixed(0) + "s" : "gone");
              if (alive === false) row.classList.add("gone");
              return alive;
            });
        });
        Promise.all(jobs).then(function (states) {
          if (left <= 0 || states.every(function (s) { return s === false; })) {
            clearInterval(iv);
            var allGone = states.every(function (s) { return s === false; });
            box.insertAdjacentHTML("beforeend", "<p class=\"mono small\">" +
              (allGone ? "expired everywhere within one poll round — absolute expiries, drift &lt;1ms." : "round over.") + "</p>");
            logOp("TTL drill on <b>" + esc(key) + "</b>: " + (allGone ? "simultaneous expiry ✓" : "incomplete"));
            ttlRunning = false;
          }
        });
      }, 1000);
    });
  });

  /* ---------- rebalance ---------- */
  document.getElementById("rebalanceForm").addEventListener("submit", function (e) {
    e.preventDefault();
    var out = document.getElementById("rebalanceOut");
    out.textContent = "triggering…";
    timed("/rebalance", { method: "POST" }).then(function (r) {
      return timed("/rebalance/status").then(function (s) {
        var txt = "trigger → " + r.status + "; ";
        try {
          var j = JSON.parse(s.body);
          var lr = j.last_result || {};
          txt += "moved " + (lr.MovedKeys || 0) + "/" + (lr.TotalKeys || 0) + ", failed " + (lr.FailedKeys || 0);
        } catch (err) { txt += s.body.slice(0, 120); }
        out.textContent = txt;
        logOp("rebalance triggered → " + r.status);
      });
    });
  });

  /* ---------- background drift packets (hero only, cheap by design) ----------
     Three dots ride the backdrop orbits parametrically: transform-free SVG
     attribute writes, rAF-throttled, paused when the tab hides, the hero
     scrolls out, or reduced-motion is set. Without JS the static orbits +
     dot field still read as an intentional technical illustration. */
  function bgMotion() {
    var layer = document.getElementById("driftPackets");
    if (!layer || REDUCED) return;
    var NS = "http://www.w3.org/2000/svg";
    var orbits = [
      { cx: 950, cy: 300, rx: 230, ry: 160, speed: 0.00011, phase: 0.0, r: 6.5, fill: "#A8562A" },
      { cx: 950, cy: 300, rx: 160, ry: 112, speed: -0.00022, phase: 2.1, r: 5.5, fill: "#A98A2F" },
      { cx: 180, cy: 520, rx: 200, ry: 130, speed: 0.00009, phase: 4.2, r: 5.5, fill: "#A8562A" }
    ];
    var dots = orbits.map(function (o) {
      var c = document.createElementNS(NS, "circle");
      c.setAttribute("r", o.r);
      c.setAttribute("fill", o.fill);
      layer.appendChild(c);
      return c;
    });
    var hero = document.querySelector(".hero");
    var onscreen = true;
    if ("IntersectionObserver" in window && hero) {
      new IntersectionObserver(function (entries) {
        onscreen = entries[0].isIntersecting;
      }, { threshold: 0 }).observe(hero);
    }
    function frame(t) {
      requestAnimationFrame(frame);
      if (document.hidden || !onscreen) return;
      for (var i = 0; i < orbits.length; i++) {
        var o = orbits[i];
        var a = o.phase + t * o.speed;
        dots[i].setAttribute("cx", (o.cx + o.rx * Math.cos(a)).toFixed(1));
        dots[i].setAttribute("cy", (o.cy + o.ry * Math.sin(a)).toFixed(1));
      }
    }
    requestAnimationFrame(frame);
  }

  /* ---------- boot ---------- */
  reveals();
  bgMotion();
  logOp("console attached to <b>" + esc(window.location.host) + "</b>");
  poll();
  state.timer = setInterval(poll, 5000);
})();
