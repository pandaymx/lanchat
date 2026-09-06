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

  // sendRead 把「已读到 seq」上发到 hub（hub 盖戳身份并广播给其它设备）。
  // 失败静默忽略：下次贴底滚动/新消息会再触发，无需重试退避。
  function sendRead(seq) {
    if (!seq) return;
    fetch("/read", {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: "seq=" + encodeURIComponent(seq),
      credentials: "same-origin"
    }).catch(function () {});
  }

  // applyRead 给 seq 之前的所有自发消息打已读勾（幂等）。
  function applyRead(seq) {
    var el = messagesEl();
    if (!el) return;
    var items = el.querySelectorAll("li[data-seq][data-self='true']");
    for (var i = 0; i < items.length; i++) {
      var li = items[i];
      if (parseInt(li.getAttribute("data-seq"), 10) > seq) continue;
      if (li.classList.contains("read")) continue;
      li.classList.add("read");
      var mark = document.createElement("span");
      mark.className = "msg-read";
      mark.textContent = "\u2713";
      // 与服务端渲染位置一致：插到正文块之前（meta/sender 之后）。
      var body = li.querySelector(".msg-body");
      if (body) {
        li.insertBefore(mark, body);
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

  // 首屏：列表短到无需滚动或已在底部 → 立即上发已读。
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", maybeSendRead);
  } else {
    maybeSendRead();
  }
})();