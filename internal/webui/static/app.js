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

  // 文件上传交互（按钮 / 粘贴 / 拖拽），M9。
  bindFileComposer();

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