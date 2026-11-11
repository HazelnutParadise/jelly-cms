# Hello Jelly

`hello-jelly` 是可直接安裝的最小外掛範例。它會：

- 依主題注入設定，在公開頁面頁尾顯示可設定的招呼語。
- 在文章發布後累計已發布文章數。
- 在後台提供 `stats` 動作，把目前統計寫進日誌。

## 檔案

```text
hello-jelly/
├── README.md
├── index.js
└── plugin.json
```

`plugin.json` 宣告：

- 設定 `banner`，型別 `string`，預設 `Hello from Jelly`。
- 權限 `settings`、`store`、`events`。
- 後台動作 `stats`。
- 擴充點 `theme.inject`。

`index.js` 使用 `JellyCMS.settings` 讀取招呼語，使用 `JellyCMS.store` 保存發布計數，並以 `JellyCMS.registerExtension` 注入頁尾 HTML。

## 打包

在 `examples/plugins/hello-jelly` 目錄執行：

```sh
zip -r ../hello-jelly.zip plugin.json index.js
```

產生的檔案是 `examples/plugins/hello-jelly.zip`。

## 安裝

1. 登入後台，開「外掛」頁。
2. 上傳 `hello-jelly.zip`。
3. 預覽 manifest 與權限，確認後同意並安裝。
4. 到外掛詳情頁按「啟用」。
5. 開公開頁面，頁尾會出現招呼語。
6. 在設定頁修改 `banner`，儲存後重新整理頁面。

新安裝的外掛預設停用，必須手動啟用。

## 驗證

- 發布一篇文章後，詳情頁日誌會出現 `已發布文章累計 1 篇：...`。
- 執行 `stats` 動作時，API 回應只有 `{"dispatched": true}`。這是目前行為，動作回呼的回傳值不會顯示在後台；實際統計請看日誌。
- 重新載入或停用再啟用後，招呼語與統計仍會保留。

完整 API、限制、生命週期與除錯說明見 [`docs/plugin-development.md`](../../../docs/plugin-development.md)。
