// lanchat service worker（v1.4 PWA）。
//
// 策略：网络优先 + 缓存回退。静态壳（首页 HTML + /assets/*）在线时每次
// 都刷新缓存，离线时回退缓存（UI 壳可用，消息/文件是动态的、不可用）。
// 只处理同源 GET，SSE / 上传 / 已读等请求原样透传（fetch 默认行为）。
//
// 版本号变更即触发 activate 清理旧缓存（每次发版改 VERSION）。
var VERSION = "lanchat-sw-v1.4.0";

self.addEventListener("install", function (e) {
  e.waitUntil(
    caches.open(VERSION).then(function (c) {
      return c.addAll([
        "/",
        "/assets/style.css",
        "/assets/app.js",
        "/assets/htmx.min.js",
        "/assets/htmx-sse.js",
        "/assets/icon-192.png",
        "/assets/icon-512.png",
        "/assets/manifest.json"
      ]);
    }).then(function () { return self.skipWaiting(); })
  );
});

self.addEventListener("activate", function (e) {
  e.waitUntil(
    caches.keys().then(function (keys) {
      return Promise.all(keys.map(function (k) {
        if (k !== VERSION) return caches.delete(k);
      }));
    }).then(function () { return self.clients.claim(); })
  );
});

self.addEventListener("fetch", function (e) {
  if (e.request.method !== "GET") return;
  var url = new URL(e.request.url);
  if (url.origin !== self.location.origin) return;
  e.respondWith(
    fetch(e.request).then(function (res) {
      if (res && res.ok) {
        if (url.pathname === "/" || url.pathname.indexOf("/assets/") === 0) {
          var copy = res.clone();
          caches.open(VERSION).then(function (c) { c.put(e.request, copy); });
        }
      }
      return res;
    }).catch(function () {
      return caches.match(e.request).then(function (hit) {
        return hit || caches.match("/");
      });
    })
  );
});
