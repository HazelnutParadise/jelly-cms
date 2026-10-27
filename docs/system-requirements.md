# 系統需求

## 後台瀏覽器

請使用最新版 Chrome、Edge、Safari 或 Firefox，並保持 JavaScript 啟用。後台的 Jelly UI 元件需要瀏覽器支援 Custom Elements、Shadow DOM、ElementInternals、ResizeObserver、Canvas 2D、`color-mix()` 與 CSS logical properties。

若瀏覽器缺少其中任一功能，或停用 JavaScript，後台開關、分段選擇、確認對話框與通知可能無法運作。這些元件不提供原生控制項後備。登入、安裝、個人帳號與前台頁面不載入 Jelly UI，不受這項限制影響。

## 建置自帶套件

一般執行 Jelly CMS 不需要 Node.js。只有在更新自帶前端套件時，才需要各套件重建腳本所列的建置工具；Jelly UI 需要 Node.js、npm、Git 與網路連線，以取得釘選的上游原始碼與 npm 相依套件。
