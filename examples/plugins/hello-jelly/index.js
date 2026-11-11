function escapeHTML(value) {
  return String(value == null ? "" : value).replace(/[&<>"']/g, function (character) {
    switch (character) {
      case "&": return "&amp;";
      case "<": return "&lt;";
      case ">": return "&gt;";
      case "\"": return "&quot;";
      default: return "&#39;";
    }
  });
}

function readBanner() {
  var banner = JellyCMS.settings.get("banner");
  return banner == null ? "Hello from Jelly" : String(banner);
}

JellyCMS.registerHook("OnBoot", function () {
  JellyCMS.log.info("Hello Jelly 已啟動");
});

JellyCMS.registerHook("OnPostPublish", function (data) {
  var current = JellyCMS.store.get("hello-jelly", "published");
  var next = (typeof current === "number" ? current : 0) + 1;
  JellyCMS.store.set("hello-jelly", "published", next);
  JellyCMS.log.info("已發布文章累計 " + next + " 篇：" + (data.post && data.post.title ? data.post.title : "未命名"));
});

JellyCMS.registerHook("action:stats", function () {
  var current = JellyCMS.store.get("hello-jelly", "published");
  var total = typeof current === "number" ? current : 0;
  JellyCMS.log.info("已發布文章共 " + total + " 篇");
  return { published: total };
});

JellyCMS.registerExtension("theme.inject", function () {
  var banner = escapeHTML(readBanner());
  return {
    footer: "<div class=\"hello-jelly-banner\">" + banner + "</div>"
  };
});
