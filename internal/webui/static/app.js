// lanchat Web 端浏览器逻辑（M8.1 已读回执）。
//
// 不引入框架：纯 vanilla JS + htmx 事件钩子。htmx-sse 扩展在每次 SSE 帧
// swap 前后于 .app 上触发 htmx:sseBeforeMessage / htmx:sseMessage
// （CustomEvent，detail 是原生 MessageEvent，bubbles 到 document），
// 这里在 document 级统一监听，按帧类型分流。
//
// 职责：
//   1. read 帧到达 → 给 data-self="true" 且 data-seq <= 游标的消息打勾；
//   2. 用户贴底（含新消息到达前已贴底、消息列表短到无需滚动）→ POST /read
//      上发已读回执，让其它设备看到「我读到了哪里」。
(function () {
  "use strict";

  function messagesEl() {
    return document.getElementById("messages");
  }

  // currentConv 返回当前渲染会话：URL ?conv=（缺省空串 = 大厅）。
  // 所有上发动作（read/upload）都带它，避免多 tab 错标到其它会话。
  function currentConv() {
    var p = new URLSearchParams(window.location.search);
    return p.get("conv") || "";
  }

  // atBottom 报告消息列表是否已滚到底（或列表短到无需滚动）。
  function atBottom() {
    var el = messagesEl();
    if (!el) return false;
    return el.scrollTop + el.clientHeight >= el.scrollHeight - 8;
  }

  // latestSeq 返回列表里最新一条消息的 ServerSeq（无则 0）。
  function latestSeq() {
    var el = messagesEl();
    if (!el) return 0;
    var max = 0;
    var items = el.querySelectorAll("li[data-seq]");
    for (var i = 0; i < items.length; i++) {
      var n = parseInt(items[i].getAttribute("data-seq"), 10);
      if (n > max) max = n;
    }
    return max;
  }

  // uploadFile 把浏览器本地文件传到 hub（M9）：
  // web 的 POST /api/files 代理端点 → hub blob 存储，返回 FileRef 后发一条
  // 附件消息；消息回显走 SSE（与文本消息同一管线），这里不做 DOM 插入。
  // 失败时把错误写进状态行（.status-err 由 htmx 渲的样式复用）。
  function uploadFile(file) {
    if (!file) return;
    var fd = new FormData();
    fd.append("file", file);
    fd.append("conv", currentConv());
    var btn = document.getElementById("file-btn");
    if (btn) btn.disabled = true;
    fetch("/api/files", {
      method: "POST",
      body: fd,
      credentials: "same-origin"
    }).then(function (resp) {
      if (!resp.ok) {
        return resp.text().then(function (t) {
          throw new Error("upload failed (" + resp.status + ")" + (t ? ": " + t : ""));
        });
      }
    }).catch(function (err) {
      var el = document.getElementById("conn-state");
      if (el) el.textContent = "✗ " + err.message;
    }).finally(function () {
      if (btn) btn.disabled = false;
    });
  }

  // uploadFileFromInput 处理隐藏 input 的文件选择（点击后清空，允许重复选同一文件）。
  function uploadFileFromInput(input) {
    uploadFile(input.files && input.files[0]);
    input.value = "";
  }

  // bindFileComposer 挂上传交互：按钮、粘贴、拖拽（M9）。
  function bindFileComposer() {
    var btn = document.getElementById("file-btn");
    var input = document.getElementById("file-input");
    if (!btn || !input) return;

    btn.addEventListener("click", function () { input.click(); });
    input.addEventListener("change", function () { uploadFileFromInput(input); });

    // 粘贴（截图工具 / 剪贴板图片）。
    document.addEventListener("paste", function (e) {
      var files = e.clipboardData && e.clipboardData.files;
      if (files && files.length) {
        e.preventDefault();
        uploadFile(files[0]);
      }
    });

    // 拖拽文件到窗口任意处即上传（enter/over 阻止默认让 drop 可触发）。
    var dragDepth = 0;
    document.addEventListener("dragenter", function (e) {
      e.preventDefault();
      if (e.dataTransfer && e.dataTransfer.types && e.dataTransfer.types.indexOf("Files") >= 0) {
        dragDepth++;
        document.body.classList.add("drag-over");
      }
    });
    document.addEventListener("dragover", function (e) { e.preventDefault(); });
    document.addEventListener("dragleave", function (e) {
      e.preventDefault();
      dragDepth = Math.max(0, dragDepth - 1);
      if (dragDepth === 0) document.body.classList.remove("drag-over");
    });
    document.addEventListener("drop", function (e) {
      e.preventDefault();
      dragDepth = 0;
      document.body.classList.remove("drag-over");
      if (e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length) {
        uploadFile(e.dataTransfer.files[0]);
      }
    });
  }

  // sendRead 把「已读到 seq」上发到 hub（hub 盖戳身份并广播给其它设备）。
  // 失败静默忽略：下次贴底滚动/新消息会再触发，无需重试退避。
  function sendRead(seq) {
    if (!seq) return;
    fetch("/read", {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: "seq=" + encodeURIComponent(seq) + "&conv=" + encodeURIComponent(currentConv()),
      credentials: "same-origin"
    }).catch(function () {});
  }

  // applyRead 给 seq 之前的所有自发消息打已读双勾（幂等）。
  function applyRead(seq) {
    var el = messagesEl();
    if (!el) return;
    var items = el.querySelectorAll("li[data-seq][data-self='true']");
    for (var i = 0; i < items.length; i++) {
      var li = items[i];
      if (parseInt(li.getAttribute("data-seq"), 10) > seq) continue;
      if (li.classList.contains("read")) continue;
      li.classList.add("read");
      // 新结构：双勾插进 .msg-meta（时间旁），与服务端渲染位置一致。
      var meta = li.querySelector(".msg-meta");
      var mark = document.createElement("span");
      mark.className = "msg-read";
      mark.textContent = "\u2713\u2713";
      if (meta) {
        meta.appendChild(mark);
      } else {
        li.appendChild(mark);
      }
    }
  }

  // maybeSendRead 在用户已滚到底（或列表无需滚动）时上发已读回执。
  function maybeSendRead() {
    if (!atBottom()) return;
    sendRead(latestSeq());
  }

  var pinned = false; // 新消息到达前用户是否已贴底（append 后要补滚 + 已读）

  document.addEventListener("htmx:sseBeforeMessage", function (evt) {
    var msg = evt.detail;
    if (msg && msg.type === "message" && atBottom()) {
      pinned = true;
    }
  });

  document.addEventListener("htmx:sseMessage", function (evt) {
    var msg = evt.detail;
    if (!msg) return;
    if (msg.type === "read") {
      try {
        applyRead(JSON.parse(msg.data).seq);
      } catch (e) {
        /* 坏帧忽略：下一条 read 帧会覆盖 */
      }
    } else if (msg.type === "message") {
      var el = messagesEl();
      if (pinned && el) {
        el.scrollTop = el.scrollHeight; // 用户贴底时跟随新消息
      }
      maybeSendRead();
      pinned = false;
      // v1.1 未读计数：其它会话的消息给该会话角标 +1（当前会话不算）。
      var conv = convOfFrameData(msg.data);
      if (conv !== null && conv !== currentConv()) bumpUnread(conv);
    }
  });

  // ---- M12-A 会话侧栏：建群表单显隐 + 当前会话高亮 ----
  // active 类由服务端首屏/SSE conversations 帧渲染；这里补一层兜底，
  // 确保每次 conversations 帧 swap 后当前会话项保持高亮。
  function bindConvSidebar() {
    var btn = document.getElementById("conv-create-btn");
    var form = document.getElementById("conv-create-form");
    if (btn && form) {
      btn.addEventListener("click", function () {
        form.classList.toggle("hidden");
      });
    }
    highlightCurrentConv();
  }

  function highlightCurrentConv() {
    var conv = currentConv();
    var items = document.querySelectorAll(".conv-item[data-conv]");
    for (var i = 0; i < items.length; i++) {
      items[i].classList.toggle("active", items[i].getAttribute("data-conv") === conv);
    }
  }

  document.addEventListener("htmx:sseMessage", function (evt) {
    var msg = evt.detail;
    if (msg && msg.type === "conversations") highlightCurrentConv();
  });

  // 手动滚动贴底即已读（防抖）。
  var scrollTimer = null;
  document.addEventListener(
    "scroll",
    function (evt) {
      if (evt.target !== messagesEl()) return;
      if (scrollTimer) clearTimeout(scrollTimer);
      scrollTimer = setTimeout(maybeSendRead, 200);
    },
    true
  );

  // ---- v1.1 未读计数（纯前端：SSE 期非当前会话消息累加角标）----
  var unread = {}; // convID -> count；整页刷新（切会话）即清零，符合 IM 习惯

  function convOfFrameData(data) {
    try {
      var d = new DOMParser().parseFromString(data, "text/html");
      var li = d.querySelector("li[data-conv]");
      return li ? li.getAttribute("data-conv") : null;
    } catch (e) {
      return null;
    }
  }

  function bumpUnread(conv) {
    unread[conv] = (unread[conv] || 0) + 1;
    renderUnread();
  }

  function renderUnread() {
    var items = document.querySelectorAll(".conv-item[data-conv]");
    for (var i = 0; i < items.length; i++) {
      var c = items[i].getAttribute("data-conv");
      var n = unread[c] || 0;
      var link = items[i].querySelector(".conv-link");
      var badge = items[i].querySelector(".unread-badge");
      if (n > 0) {
        if (!badge && link) {
          badge = document.createElement("span");
          badge.className = "unread-badge";
          link.appendChild(badge);
        }
        if (badge) badge.textContent = n > 99 ? "99+" : String(n);
      } else if (badge) {
        badge.remove();
      }
    }
  }

  // ---- v1.1 消息引用/回复 ----
  // 点消息的 ↩ 按钮 → composer 上方出现回复条（label + ✕ 取消），
  // 隐藏域 reply 填 ReplyRef JSON；提交后 after-request 清空（templ 侧
  // 调 window.__clearReply）。
  window.__clearReply = function () {
    window.__replyCtx = null;
    var bar = document.getElementById("reply-bar");
    var input = document.getElementById("reply-input");
    if (bar) bar.hidden = true;
    if (input) input.value = "";
  };

  function bindReply() {
    document.addEventListener("click", function (e) {
      var cancel = e.target.closest(".reply-bar-cancel");
      if (cancel) {
        window.__clearReply();
        return;
      }
      var btn = e.target.closest(".msg-reply-btn");
      if (!btn) return;
      var bar = document.getElementById("reply-bar");
      var input = document.getElementById("reply-input");
      var label = document.querySelector(".reply-bar-label");
      if (!bar || !input || !label) return;
      var ctx = {
        id: btn.getAttribute("data-reply-id"),
        suid: btn.getAttribute("data-reply-sender"),
        body: btn.getAttribute("data-reply-body") || ""
      };
      window.__replyCtx = ctx;
      label.textContent = "\u21A9 " + (ctx.suid || "?") + ": " + ctx.body;
      input.value = JSON.stringify(ctx);
      bar.hidden = false;
      var ta = document.querySelector(".composer-form textarea");
      if (ta) ta.focus();
    });
  }

  // ---- v1.1 消息搜索 ----
  // Enter 触发 GET /search?q=…，结果片段塞进 #search-panel；点面板外
  // 或 Esc 关闭。命中项是跳转链接（整页刷新到所属会话）。
  function bindSearch() {
    var input = document.getElementById("search-input");
    var panel = document.getElementById("search-panel");
    if (!input || !panel) return;
    input.addEventListener("keydown", function (e) {
      if (e.key === "Escape") {
        panel.hidden = true;
        return;
      }
      if (e.key !== "Enter") return;
      var q = input.value.trim();
      if (!q) {
        panel.hidden = true;
        return;
      }
      fetch("/search?q=" + encodeURIComponent(q), { credentials: "same-origin" })
        .then(function (r) {
          if (!r.ok) throw new Error("search failed (" + r.status + ")");
          return r.text();
        })
        .then(function (html) {
          panel.innerHTML = html;
          panel.hidden = false;
        })
        .catch(function (err) {
          panel.innerHTML = '<div class="search-empty">\u2717 ' + err.message + "</div>";
          panel.hidden = false;
        });
    });
    document.addEventListener("click", function (e) {
      if (!panel.hidden && !panel.contains(e.target) && e.target !== input) {
        panel.hidden = true;
      }
    });
  }

  // ---- v1.1 图片预览 lightbox（替代 target=_blank 新窗口）----
  function bindLightbox() {
    document.addEventListener("click", function (e) {
      var img = e.target.closest(".msg-file.image img");
      if (!img) return;
      e.preventDefault();
      var ov = document.getElementById("lightbox");
      if (!ov) {
        ov = document.createElement("div");
        ov.id = "lightbox";
        ov.className = "lightbox";
        ov.addEventListener("click", function () { ov.remove(); });
        document.body.appendChild(ov);
      } else {
        ov.innerHTML = "";
      }
      var big = document.createElement("img");
      big.src = img.getAttribute("src");
      big.alt = img.getAttribute("alt") || "";
      ov.appendChild(big);
    });
  }

  // 文件上传交互（按钮 / 粘贴 / 拖拽），M9。
  bindFileComposer();
  bindReply();
  bindSearch();
  bindLightbox();

  // 会话侧栏交互（建群表单 + 高亮兜底），M12-A。
  bindConvSidebar();

  // ---- 主题切换（M11）：data-theme 持久化到 localStorage ----
  function currentTheme() {
    return document.documentElement.getAttribute("data-theme") || "dark";
  }
  function setThemeIcon() {
    var btn = document.getElementById("theme-toggle");
    if (!btn) return;
    btn.textContent = currentTheme() === "dark" ? "\u2600\uFE0F" : "\uD83C\uDF19";
  }
  function toggleTheme() {
    var next = currentTheme() === "dark" ? "light" : "dark";
    document.documentElement.setAttribute("data-theme", next);
    try { localStorage.setItem("lanchat-theme", next); } catch (e) {}
    setThemeIcon();
  }
  var themeBtn = document.getElementById("theme-toggle");
  if (themeBtn) {
    themeBtn.addEventListener("click", toggleTheme);
  }
  setThemeIcon();

  // 首屏：列表短到无需滚动或已在底部 → 立即上发已读。
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", maybeSendRead);
  } else {
    maybeSendRead();
  }
})();