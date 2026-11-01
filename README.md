# Jelly CMS

一個使用 Go 語言開發的現代化開源 CMS 系統，目標是比 WordPress 更快，並提供豐富的擴展功能。

## ✨ 特性

- 🚀 **高效能**：使用 Go 語言開發，比 PHP 更快
- 🌍 **多語言支援**：內建繁體中文和英文，可輕鬆擴展
- 🎨 **主題系統**：支援自訂頁面布局和配色，無需重啟即可切換主題
- 🔌 **外掛系統**：使用 JavaScript 開發外掛，支援熱重載
- 🔐 **OAuth 登入**：支援 Google、GitHub 等第三方登入，自動創建帳號
- 💳 **金流整合**：支援綠界 ECPay、藍新 NewebPay 等台灣常見金流服務
- 📦 **電商功能**：內建產品管理和訂單系統
- 🛠️ **安裝精靈**：像 WordPress 一樣的圖形化安裝流程

## 📋 需求

- Go 1.27.3 或更高版本
- PostgreSQL 資料庫
- 可選：Docker 和 Docker Compose（用於容器化部署）

後台瀏覽器支援與執行環境需求見[系統需求](docs/system-requirements.md)。

## 🚀 快速開始

### 使用 Docker Compose（推薦）

```bash
# 複製環境變數範例
cp .env.example .env

# 編輯 .env 檔案，設定資料庫和金流服務的配置

# 啟動服務
docker-compose up -d

# 訪問 http://localhost:8080 進行安裝
```

### 手動安裝

```bash
# 1. 克隆專案
git clone https://github.com/HazelnutParadise/jelly-cms.git
cd jelly-cms

# 2. 安裝依賴
go mod download

# 3. 設定環境變數
export DB_HOST=localhost
export DB_PORT=5432
export DB_USER=jelly
export DB_PASSWORD=jellypassword
export DB_NAME=jellycms
# 登入金鑰預設會自動產生並保存，無須設定範例字串

# 4. 編譯
go build -o server ./cmd/server

# 5. 執行
./server

# 6. 訪問 http://localhost:8080 進行安裝
```

### 登入金鑰

未設定 `SESSION_SECRET` 或 `JWT_SECRET` 時，伺服器會各自產生安全亂數金鑰，保存於 `data/auth-secrets.json`，重新啟動後沿用。請將這個檔案納入備份並限制存取；刪除或替換它會使既有登入憑證失效。金鑰檔損壞或無法讀寫時，伺服器會停止啟動，不會使用預設金鑰。

明確設定的環境變數各自優先，請使用獨立且足夠長的隨機值並在重啟後保持一致。兩個值都設定時不需要本機金鑰檔；只設定一個時，另一個仍由檔案保存，環境值不會寫入檔案。從舊版固定預設金鑰升級後，使用者需要重新登入。

Windows 目前未提供金鑰目錄的斷電持久化保證；需要這項保證的部署請以環境變數提供並保存兩組金鑰。

### 登入期限與登出

每次登入的 JWT 與 Session 最長有效七天；重新儲存 Cookie 不會延長登入期限。登出會撤銷本次登入的兩種憑證，同一帳號在其他裝置的登入仍保留。撤銷紀錄保存在資料庫，重新啟動後仍有效，請與網站資料一併備份。

資料庫無法完成撤銷時，登出回傳失敗且保留 Cookie 供重試；無法查詢撤銷狀態時會拒絕受保護的請求。升級前沒有登入 ID 或固定到期時間的舊憑證需要重新登入。回退至不支援撤銷檢查的舊版會失去此保護。

### 登入嘗試限制

本機帳密登入按連線來源計數，在第一次嘗試開始的 60 秒內最多可嘗試 30 次，成功與失敗皆計數。超量回應 JSON `429`，`Retry-After` 標頭與錯誤訊息提供等待秒數；等待到期後可重新登入，已登入的後台操作與公開閱讀不受影響。

限制保存在單一程序記憶體，重新啟動會重設。反向代理後的登入共用該代理的額度；多程序部署與分散來源的嘗試須在入口代理額外限制。應用程式使用實際連線 IP，不採用客戶端自行提供的轉送標頭。

### 登入返回網址

登入頁與本機／OAuth 登入只接受以單一 `/` 開頭的站內返回網址，例如 `/admin/posts?search=hello#section`。站外網址、反斜線、控制字元或錯誤編碼會改回 `/admin`，帳密正確時仍可登入。

### OAuth 登入驗證

Google 與 GitHub 授權由路徑選定提供者，伺服器每次產生新的 state 驗證值。回呼缺少、重複或不符合原始簽章 Session 的 state 時回應 `400`，請從登入頁重新開始授權。提供者驗證失敗時回應 `502`，不回傳其原始錯誤內容。無法產生驗證值或保存 Session 時回應 `503`。

每次授權須在 10 分鐘內完成，同一個回呼只能處理一次，逾時或重送回應 `400`。同一瀏覽器只保留最近開始的一次待授權流程。已通過驗證的授權拒絕與提供者失敗也會結束本次授權，請重新從登入頁開始。已使用狀態保存在資料庫，重新啟動後仍會拒絕重送；資料庫不可用時回應 `503`，不聯絡提供者或簽發登入。第三方權杖不保存在瀏覽器 Cookie，應用程式登入維持七天期限。

伺服器向提供者交換授權碼與讀取個人資料時，共用 30 秒期限。原請求較早取消或逾時時會中止操作，回應 `502`，不建立帳號或登入憑證。請從登入頁重新開始授權。

Google 與 GitHub 入口共用另一份來源額度，每個連線來源在 60 秒內最多開始授權 30 次，超量回應 `429` 與 `Retry-After`。此額度與本機帳密登入分開，代理與多程序部署的限制同前節。

### 瀏覽器寫入保護

登入與安裝，以及帶有登入 Cookie 的寫入請求，必須提供同源的 Origin、Referer 或瀏覽器 Sec-Fetch-Site 證據。來源皆缺少的自訂客戶端須傳送 `X-Jelly-CSRF: 1`；明確的外站或矛盾來源仍會被拒絕。拒絕時回應 JSON `403`，不執行寫入。正常瀏覽器操作會自動附帶來源證據。

網站放在終止 HTTPS 的反向代理後時，請設定 `APP_URL` 為公開來源，例如 `https://cms.example.com`。僅接受 HTTP(S) 網址與可選的根路徑 `/`；無效設定使受保護的寫入回應 `503`。未設定時使用實際 TLS 與 Host，不信任轉送標頭。

無 Cookie 的 Bearer API 保留原有認證方式；付款通知仍使用各金流的獨立驗證。登出會檢查已提供的來源，即使請求沒有登入 Cookie。完全未登入且沒有來源標頭的登出可冪等成功，不設定或清除登入 Cookie。

### 完成安裝與重新啟動

開啟 `/install`，先確認 PostgreSQL 連線，再填入網站名稱、管理者帳號、電子郵件與密碼。密碼長度須為 12 至 72 個 UTF-8 位元組。安裝完成後會前往登入頁，無須重新啟動即可登入與建立內容。

資料庫設定保存在 `data/config.json`，檔案權限為 `0600`。請保留 `data/` 目錄，並將它與資料庫一併納入備份。已安裝的網站會拒絕再次安裝與匿名資料庫連線測試。

若安裝中途停止，下一次啟動會檢查 `data/install-pending.json` 並核對資料庫的安裝紀錄，再完成設定或撤回尚未提交的安裝。無法確認結果時會保留紀錄並停止初始化，請先修復資料庫連線或檔案權限，再重新啟動，不要刪除安裝紀錄來強制重裝。

設定損壞、資料庫無法連線或資料表更新失敗時，既有網站不會重新開放安裝；一般網站要求會回應 `503`。

## 🔌 外掛開發

Jelly CMS 使用 JavaScript（QuickJS）作為外掛運行時，讓開發者可以輕鬆擴展系統功能。

每個外掛以獨立子程序執行。入口、事件及結束回呼各有 1 秒執行上限，啟動子程序的上限為 5 秒；逾時會終止該外掛程序。熱重載只有在新版本完成載入與啟動後才替換舊版本，失敗時保留舊版本。

外掛只能存取隔離的暫存目錄，不能直接讀寫網站檔案。入口上限為 1 MiB，事件資料須可轉換為 JSON 且不超過 1 MiB。日誌有大小與佇列限制，輸出受阻時可能捨棄日誌。

宣告並獲准 `settings` 權限的外掛，可用 `JellyCMS.config` 或 `JellyCMS.settings` 讀寫 manifest 宣告的設定，值保存在資料庫，秘密欄位另行加密。沒有這項權限的舊外掛，其 `config` 保持程序內暫存行為。`JellyCMS.store` 需獲准 `store` 權限，資料按外掛 ID 隔離；`JellyCMS.http.fetch` 需獲准 `http.fetch`，只能連線到 manifest 的允許主機，並封鎖私有網路位址。

後台系統工具、備份與換版的操作限制見[系統維運](docs/system-operations.md)，搜尋與儲存回呼契約見 [JavaScript 擴充點](docs/plugin-extensions.md)。目前驗收狀態見 [STATUS.md](STATUS.md)。

若將 runtime 嵌入其他 Go 執行檔，必須在 `main` 最前面呼叫 `plugin.RunWorkerIfRequested()`，並在結束前呼叫 `Runtime.Close()`。一般伺服器入口已完成這項接線。

### 外掛結構

每個外掛需要包含以下檔案：

```
data/plugins/my-plugin/
├── plugin.json    # 外掛配置檔案
└── index.js       # 外掛主程式（entrypoint）
```

### plugin.json 範例

```json
{
  "id": "my-plugin",
  "name": "我的外掛",
  "version": "1.0.0",
  "description": "這是一個範例外掛",
  "author": "開發者名稱",
  "entrypoint": "index.js"
}
```

### 外掛 API

外掛可以透過全域物件 `JellyCMS` 存取系統功能：

```javascript
// 註冊 Hook
JellyCMS.registerHook('OnPostSave', function(data) {
    JellyCMS.log.info('文章已儲存: ' + data.post.title);
});

// 記錄日誌
JellyCMS.log.info('資訊訊息');
JellyCMS.log.error('錯誤訊息');

// 讀取設定
const value = JellyCMS.config.get('setting_key');

// 儲存設定
JellyCMS.config.set('setting_key', 'value');
```

### 可用的 Hook（鉤子）

#### 生命週期鉤子

- **OnBoot**: 外掛載入完成後觸發
  ```javascript
  JellyCMS.registerHook('OnBoot', function(data) {
      // data.plugin: 外掛 ID
      // 觸發時機：外掛腳本執行完成後
  });
  ```

- **OnShutdown**: 外掛卸載前觸發
  ```javascript
  JellyCMS.registerHook('OnShutdown', function(data) {
      // 觸發時機：外掛被卸載前，可用於清理資源
  });
  ```

#### 請求鉤子

- **OnRequest**: 每個 HTTP 請求處理前觸發
  ```javascript
  JellyCMS.registerHook('OnRequest', function(data) {
      // data.path: 請求路徑
      // data.method: HTTP 方法
      // 觸發時機：路由處理前，可用於記錄、驗證、修改請求等
  });
  ```

- **OnResponse**: 每個 HTTP 回應發送後觸發
  ```javascript
  JellyCMS.registerHook('OnResponse', function(data) {
      // data.path: 請求路徑
      // data.status: HTTP 狀態碼
      // 觸發時機：回應已發送給客戶端後，可用於記錄、統計等
  });
  ```

#### 內容鉤子

- **OnPostSave**: 文章儲存到資料庫後觸發（新增或更新）
  ```javascript
  JellyCMS.registerHook('OnPostSave', function(data) {
      // data.post: 文章物件（已儲存）
      // data.action: 'create' 或 'update'
      // 觸發時機：文章已成功儲存到資料庫後，可用於發送通知、更新快取等
  });
  ```

- **OnPostDelete**: 文章從資料庫刪除後觸發
  ```javascript
  JellyCMS.registerHook('OnPostDelete', function(data) {
      // data.post: 被刪除的文章物件
      // 觸發時機：文章已從資料庫刪除後，可用於清理相關資料、發送通知等
  });
  ```

- **OnPostView**: 文章被瀏覽時觸發（讀取前）
  ```javascript
  JellyCMS.registerHook('OnPostView', function(data) {
      // data.post: 文章物件
      // 觸發時機：文章被讀取並準備顯示前，可用於記錄瀏覽次數、推薦相關內容等
  });
  ```

#### 產品鉤子

- **OnProductSave**: 產品儲存到資料庫後觸發
  ```javascript
  JellyCMS.registerHook('OnProductSave', function(data) {
      // data.product: 產品物件（已儲存）
      // 觸發時機：產品已成功儲存到資料庫後，可用於更新庫存、發送通知等
  });
  ```

- **OnProductDelete**: 產品從資料庫刪除後觸發
  ```javascript
  JellyCMS.registerHook('OnProductDelete', function(data) {
      // data.product: 被刪除的產品物件
      // 觸發時機：產品已從資料庫刪除後，可用於清理相關資料等
  });
  ```

- **OnProductView**: 產品被瀏覽時觸發（讀取前）
  ```javascript
  JellyCMS.registerHook('OnProductView', function(data) {
      // data.product: 產品物件
      // 觸發時機：產品被讀取並準備顯示前，可用於記錄瀏覽次數、推薦相關產品等
  });
  ```

#### 訂單鉤子

- **OnOrderCreate**: 訂單建立並儲存到資料庫後觸發
  ```javascript
  JellyCMS.registerHook('OnOrderCreate', function(data) {
      // data.order: 訂單物件（已建立）
      // 觸發時機：訂單已成功建立並儲存到資料庫後，可用於發送確認 Email、更新庫存等
  });
  ```

- **OnOrderUpdate**: 訂單狀態更新並儲存到資料庫後觸發
  ```javascript
  JellyCMS.registerHook('OnOrderUpdate', function(data) {
      // data.order: 訂單物件（已更新）
      // data.oldStatus: 舊狀態
      // data.newStatus: 新狀態
      // 觸發時機：訂單狀態已更新並儲存到資料庫後，可用於發送狀態變更通知等
  });
  ```

- **OnOrderPaid**: 訂單付款完成後觸發
  ```javascript
  JellyCMS.registerHook('OnOrderPaid', function(data) {
      // data.order: 訂單物件
      // 觸發時機：訂單付款確認完成後，可用於發送收據、準備出貨等
  });
  ```

#### 使用者鉤子

- **OnUserLogin**: 使用者登入驗證成功後觸發
  ```javascript
  JellyCMS.registerHook('OnUserLogin', function(data) {
      // data.user: 使用者物件
      // 觸發時機：使用者通過驗證並建立 Session 後，可用於記錄登入日誌、發送通知等
  });
  ```

- **OnUserLogout**: 使用者登出前觸發
  ```javascript
  JellyCMS.registerHook('OnUserLogout', function(data) {
      // data.user: 使用者物件
      // 觸發時機：Session 被清除前，可用於記錄登出日誌、清理暫存資料等
  });
  ```

- **OnUserCreate**: 使用者建立並儲存到資料庫後觸發
  ```javascript
  JellyCMS.registerHook('OnUserCreate', function(data) {
      // data.user: 新使用者物件（已建立）
      // 觸發時機：使用者已成功建立並儲存到資料庫後，可用於發送歡迎 Email 等
  });
  ```

#### 支付鉤子

- **OnPaymentSuccess**: 支付成功並更新訂單狀態後觸發
  ```javascript
  JellyCMS.registerHook('OnPaymentSuccess', function(data) {
      // data.order: 訂單物件
      // data.payment: 支付資訊
      // 觸發時機：支付驗證成功且訂單狀態已更新後，可用於發送收據、準備出貨等
  });
  ```

- **OnPaymentFailed**: 支付失敗後觸發
  ```javascript
  JellyCMS.registerHook('OnPaymentFailed', function(data) {
      // data.order: 訂單物件
      // data.error: 錯誤訊息
      // 觸發時機：支付驗證失敗或處理失敗後，可用於發送失敗通知、記錄錯誤等
  });
  ```

#### 主題鉤子

- **OnThemeActivate**: 主題啟用並載入完成後觸發
  ```javascript
  JellyCMS.registerHook('OnThemeActivate', function(data) {
      // data.theme: 主題名稱
      // 觸發時機：主題已成功啟用並載入後，可用於執行主題相關的初始化操作等
  });
  ```

### 外掛範例

```javascript
// index.js
JellyCMS.log.info('外掛載入中...');

// 在文章儲存時發送通知
JellyCMS.registerHook('OnPostSave', function(data) {
    JellyCMS.log.info('文章已儲存: ' + data.post.title);
    
    // 可以在這裡執行自訂邏輯
    // 例如：發送 Email、更新快取、同步到其他系統等
});

// 在訂單付款完成時執行操作
JellyCMS.registerHook('OnOrderPaid', function(data) {
    JellyCMS.log.info('訂單已付款: ' + data.order.id);
    
    // 例如：發送確認 Email、更新庫存等
});

// 外掛載入完成
JellyCMS.registerHook('OnBoot', function(data) {
    JellyCMS.log.info('外掛已成功載入！');
});
```

## 🎨 主題開發

Jelly CMS 的主題系統支援完整的顏色和布局自訂，讓開發者可以輕鬆創建美觀且可客製化的主題。

### 主題結構

主題位於 `web/themes/` 目錄，每個主題需要包含以下檔案：

```
web/themes/my-theme/
├── theme.json      # 主題配置檔案（必需）
├── layout.html     # 布局模板（必需）
├── index.html      # 首頁模板
├── post.html       # 文章詳情頁模板
├── posts.html      # 文章列表頁模板
├── page.html       # 靜態頁面模板
└── search.html     # 搜尋結果頁模板
```

### theme.json 配置

`theme.json` 是主題的核心配置檔案，定義了主題的基本資訊、預設顏色和布局設定：

```json
{
  "name": "Jelly",
  "version": "1.0.0",
  "description": "Default Jelly CMS Theme",
  "author": "Jelly Team",
  "colors": {
    "primary": "#E8285B",
    "secondary": "#2F6BFF",
    "background": "#FFFFFF",
    "text": "#15161A",
    "text_muted": "#5F6470",
    "accent": "#13C2A3",
    "link": "#2F6BFF",
    "link_hover": "#E8285B",
    "border": "#DDE0E7",
    "dark": "#15161A",
    "success": "#15A06A",
    "warning": "#F5B800",
    "error": "#E03B2E"
  },
  "layout": {
    "header_style": "sticky",
    "sidebar": false,
    "sidebar_position": "left",
    "footer": true,
    "container_width": "wide",
    "post_layout": "grid"
  }
}
```

#### 顏色設定

主題支援以下顏色設定，這些顏色可以在管理後台進行客製化：

- `primary`: 主要顏色（按鈕、強調元素等）
- `secondary`: 次要顏色（輔助元素）
- `background`: 背景顏色
- `text`: 主要文字顏色
- `text_muted`: 次要文字顏色
- `accent`: 強調色
- `link`: 連結顏色
- `link_hover`: 連結懸停顏色
- `border`: 邊框顏色
- `dark`: 深色（用於 footer 等）
- `success`: 成功狀態顏色
- `warning`: 警告狀態顏色
- `error`: 錯誤狀態顏色

後台的顏色選擇器使用 `<input type="color">`，顏色值請用六位數十六進位（例如 `#15161A`，不要用 `#333`），否則選擇器會顯示成黑色。

#### 自訂欄位

`theme.json` 可以加上 `custom_fields`，後台「客製化」會依型別產生對應欄位，值會注入到 `.ThemeCustom`，用 `themeLayout .ThemeCustom "欄位 id" "預設值"` 讀取：

```json
"custom_fields": [
  { "id": "tagline", "label": "網站標語", "type": "text", "default": "寫下來，才算數。" }
]
```

支援的 `type`：`text`、`textarea`、`number`、`color`、`select`（需提供 `options`）。

#### Layout 設定

- `header_style`: Header 位置樣式
  - `"fixed"`: 固定定位，始終在頂部
  - `"sticky"`: 粘性定位，滾動時固定在頂部
  - `"static"`: 靜態定位，正常流動
- `sidebar`: 是否顯示側邊欄（`true`/`false`）
- `sidebar_position`: 側邊欄位置（`"left"`/`"right"`）
- `footer`: 是否顯示頁腳（`true`/`false`）
- `container_width`: 容器寬度
  - `"full"`: 全寬
  - `"wide"`: 寬版（1200px）
  - `"narrow"`: 窄版（800px）
- `post_layout`: 文章列表布局
  - `"grid"`: 網格布局
  - `"list"`: 列表布局
  - `"single"`: 單欄布局

### 模板文件

#### layout.html

`layout.html` 是主題的基礎布局模板，必須定義一個名為 `layout` 的模板：

```html
{{define "layout"}}
<!DOCTYPE html>
<html lang="zh-TW">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{if .Title}}{{.Title}} - {{end}}{{.SiteTitle}}</title>
    <style>
        :root {
            --primary: {{themeColor .ThemeColors "primary" "#FF6B6B"}};
            --secondary: {{themeColor .ThemeColors "secondary" "#4ECDC4"}};
            --background: {{themeColor .ThemeColors "background" "#F7F9FC"}};
            --text: {{themeColor .ThemeColors "text" "#333"}};
            /* ... 更多顏色變數 ... */
        }
        
        body {
            background: var(--background);
            color: var(--text);
        }
        
        header {
            {{$headerStyle := themeLayout .ThemeLayout "header_style" "sticky"}}
            position: {{if eq $headerStyle "fixed"}}fixed{{else if eq $headerStyle "sticky"}}sticky{{else}}static{{end}};
        }
        
        main {
            {{$containerWidth := themeLayout .ThemeLayout "container_width" "wide"}}
            max-width: {{if eq $containerWidth "full"}}100%{{else if eq $containerWidth "narrow"}}800px{{else}}1200px{{end}};
        }
        
        footer {
            {{$footer := themeLayout .ThemeLayout "footer" true}}
            {{if eq $footer false}}display: none;{{end}}
        }
    </style>
</head>
<body>
    <header>
        <!-- Header 內容 -->
    </header>
    <main>
        {{template "content" .}}
    </main>
    <footer>
        <!-- Footer 內容 -->
    </footer>
</body>
</html>
{{end}}
```

#### 頁面模板

其他頁面模板（如 `index.html`、`post.html` 等）需要定義 `content` 模板：

```html
{{define "content"}}
<div class="posts-grid">
    {{range .Posts}}
    <article class="post-card">
        <h2><a href="/post/{{.Slug}}">{{.Title}}</a></h2>
        <p class="post-excerpt">{{excerpt .Content 150}}</p>
    </article>
    {{end}}
</div>
{{end}}
```

### 模板函數

Jelly CMS 提供了兩個專用的模板函數來處理主題設定：

#### themeColor

用於取得主題顏色，如果找不到則使用預設值：

```html
{{themeColor .ThemeColors "primary" "#FF6B6B"}}
```

- 第一個參數：`.ThemeColors` - 主題顏色 map
- 第二個參數：`"primary"` - 要查找的顏色鍵名
- 第三個參數：`"#FF6B6B"` - 預設顏色值（如果找不到或為空）

**範例：**

```html
<style>
    :root {
        --primary: {{themeColor .ThemeColors "primary" "#FF6B6B"}};
        --secondary: {{themeColor .ThemeColors "secondary" "#4ECDC4"}};
        --text: {{themeColor .ThemeColors "text" "#333"}};
    }
    
    .btn {
        background: var(--primary);
        color: white;
    }
</style>
```

#### themeLayout

用於取得主題布局設定，如果找不到則使用預設值：

```html
{{themeLayout .ThemeLayout "header_style" "sticky"}}
```

- 第一個參數：`.ThemeLayout` - 主題布局 map
- 第二個參數：`"header_style"` - 要查找的布局鍵名
- 第三個參數：`"sticky"` - 預設值（如果找不到）

**範例：**

```html
<style>
    header {
        {{$headerStyle := themeLayout .ThemeLayout "header_style" "sticky"}}
        position: {{if eq $headerStyle "fixed"}}fixed{{else if eq $headerStyle "sticky"}}sticky{{else}}static{{end}};
    }
    
    main {
        {{$containerWidth := themeLayout .ThemeLayout "container_width" "wide"}}
        max-width: {{if eq $containerWidth "full"}}100%{{else if eq $containerWidth "narrow"}}800px{{else}}1200px{{end}};
    }
</style>
```

### 可用的模板數據

在模板中，你可以使用以下數據：

- `.SiteTitle`: 網站標題
- `.Title`: 當前頁面標題
- `.Posts`: 文章列表（首頁、文章列表頁）
- `.Post`: 單篇文章（文章詳情頁）
- `.Page`: 當前頁碼（分頁時）
- `.TotalPages`: 總頁數（分頁時）
- `.Query`: 搜尋關鍵字（搜尋頁）
- `.ThemeColors`: 主題顏色 map（從資料庫讀取的自訂顏色）
- `.ThemeLayout`: 主題布局 map（從資料庫讀取的自訂布局）
- `.ThemeCustom`: 自訂欄位 map（如果有定義）
- `.Query`: 搜尋關鍵字（只有搜尋頁有，其他頁面請用 `{{with .Query}}…{{end}}` 判斷）
- `.IsHome`、`.IsPosts`、`.IsPost`、`.IsPage`、`.IsSearch`: 目前頁面類型，用來標示導覽列的目前項目

### 其他模板函數

除了主題相關的函數，還可以使用以下內建函數：

- `{{safeHTML .Post.Content}}`: 直接輸出文章的 HTML 內容（不轉義）。`.Post.Content` 是字串，若寫成 `{{.Post.Content}}` 會被轉義成純文字。
- `{{excerpt .Post.Content 150}}`: 去掉 HTML 標籤後截取前 150 個「字」（以字元計，不會切壞中文），超過時結尾加上「…」
- `{{initial .SiteTitle}}`: 取網站名稱第一個字母或中文字（大寫），適合做小圖示
- `{{substr .Content 0 150}}`: 以位元組截取字串（中文請改用 `excerpt`）
- `{{add .Page 1}}`: 數字相加
- `{{sub .Page 1}}`: 數字相減
- `{{.Content | html}}`: HTML 轉義

### 完整範例

以下是一個完整的主題範例：

**theme.json:**
```json
{
  "name": "My Theme",
  "version": "1.0.0",
  "description": "我的自訂主題",
  "author": "開發者",
  "colors": {
    "primary": "#007bff",
    "secondary": "#6c757d",
    "background": "#ffffff",
    "text": "#212529"
  },
  "layout": {
    "header_style": "sticky",
    "footer": true,
    "container_width": "wide"
  }
}
```

**layout.html:**
```html
{{define "layout"}}
<!DOCTYPE html>
<html lang="zh-TW">
<head>
    <meta charset="UTF-8">
    <title>{{if .Title}}{{.Title}} - {{end}}{{.SiteTitle}}</title>
    <style>
        :root {
            --primary: {{themeColor .ThemeColors "primary" "#007bff"}};
            --background: {{themeColor .ThemeColors "background" "#ffffff"}};
            --text: {{themeColor .ThemeColors "text" "#212529"}};
        }
        body {
            font-family: sans-serif;
            background: var(--background);
            color: var(--text);
        }
        .btn {
            background: var(--primary);
            color: white;
            padding: 0.5rem 1rem;
            border: none;
            border-radius: 4px;
        }
    </style>
</head>
<body>
    <header>
        <h1>{{.SiteTitle}}</h1>
    </header>
    <main>
        {{template "content" .}}
    </main>
</body>
</html>
{{end}}
```

**index.html:**
```html
{{define "content"}}
<h1>歡迎來到 {{.SiteTitle}}</h1>
{{if .Posts}}
    <div class="posts">
        {{range .Posts}}
        <article>
            <h2><a href="/post/{{.Slug}}">{{.Title}}</a></h2>
            <p>{{excerpt .Content 200}}</p>
        </article>
        {{end}}
    </div>
{{end}}
{{end}}
```

### 主題客製化

用戶可以在管理後台的「主題」頁面中：

1. **選擇主題**：從已安裝的主題中選擇一個啟用
2. **自訂顏色**：使用顏色選擇器修改主題顏色
3. **調整布局**：修改 header 樣式、容器寬度、文章布局等
4. **即時預覽**：修改後可以即時看到效果

所有自訂設定都會儲存到資料庫，並在渲染模板時自動注入到模板數據中。

### 最佳實踐

1. **使用 CSS 變數**：將主題顏色定義為 CSS 變數，方便管理和覆蓋
2. **提供預設值**：使用 `themeColor` 和 `themeLayout` 函數時，務必提供合理的預設值
3. **響應式設計**：確保主題在不同裝置上都能正常顯示
4. **語義化 HTML**：使用適當的 HTML 標籤，提升可訪問性
5. **性能優化**：避免過多的嵌套和複雜的 CSS 選擇器

## 🔐 OAuth 設定

管理員在後台「設定」的 OAuth 分頁填入 Google 或 GitHub 的 Client ID 與 Client Secret，啟用提供者後儲存。

以 `APP_URL` 指定網站的公開網址，例如 `https://cms.example.com`，並在提供者的應用程式設定中登記對應回呼網址：Google 使用 `/auth/google/callback`，GitHub 使用 `/auth/github/callback`。未設定 `APP_URL` 時使用 `http://localhost:8080`。

### 本人登入方式與舊帳號復原

登入後可從後台的「本人帳號」進入 `/account`，連結或解除 Google／GitHub 登入。所有已登入角色都可使用。系統會保留至少一種目前可用的登入方式。連結成功不會延長原登入期限。

OAuth 以提供者的固定身分識別帳號，信箱變更不會建立另一個帳號。新的第三方身分若使用既有信箱，系統回應 `409`，不會自動合併；請先用原方式登入，再從本人帳號連結。

升級前建立的 OAuth 帳號若仍有有效登入，可自行連結。已無法登入時，網站擁有者須先透過可信管道核實本人，再於網站主機執行復原工具：

```bash
go build -o recover-oauth ./cmd/recover-oauth
./recover-oauth list --site-dir /srv/jelly
./recover-oauth issue --site-dir /srv/jelly --user-id 42 --provider google
```

將 `/srv/jelly` 換成存有既有 `data/config.json` 與登入金鑰的網站目錄，帳號 ID 與提供者依 `list` 結果填入。若伺服器使用 `JWT_SECRET`，工具也須使用相同的環境設定。工具不會建立金鑰或更新資料表，請先完成網站升級。

將輸出的 `grant` 經可信管道交給本人，在 `/account/recover` 貼上，再完成原提供者的授權。復原碼有效十分鐘，成功復原後即失效。工具的分頁、參數與錯誤說明可用 `./recover-oauth --help` 查看。

## 💳 金流設定

金流服務的設定可以在管理後台的「設定」頁面中進行配置，無需使用環境變數。

### 管理後台設定

1. 登入管理後台
2. 前往「設定」頁面
3. 找到「金流設定」區塊
4. 選擇要啟用的金流服務（綠界 ECPay 或藍新 NewebPay）
5. 填入以下資訊：
   - **商店代號 (Merchant ID)**: 金流服務商提供的商店代號
   - **Hash Key**: 金流服務商提供的 Hash Key
   - **Hash IV**: 金流服務商提供的 Hash IV
   - **測試模式**: 是否啟用測試模式（開發時建議開啟）

金流設定目前只能在管理後台的「設定」頁面進行，機器 API（`/api/v1`）尚未提供金流端點。

**注意**：設定完成後，系統會自動重新載入金流配置，無需重啟服務。

## 📚 API 文檔

認證介面的操作索引位於 `GET /api/auth`，完整輸入、回應與登入方式見 `GET /api/auth/openapi.json`。

### 安裝 API

- `GET /install`：顯示安裝表單；已安裝時轉向首頁。
- `POST /api/install/test-db`：測試連線，不更換使用中的連線或儲存設定。
- `POST /api/install`：建立網站與第一個管理者，成功後回應 `200`。

兩個 POST 端點只在尚未安裝時開放。輸入無效回應 `400`，重複安裝回應 `409`，安裝過程失敗回應 `500`，不在 HTTP 錯誤內容回傳資料庫密碼或內部路徑。

### 管理後台 API

程式與 API 串接請在管理後台建立存取金鑰，並用 `Authorization: Bearer jly_…` 呼叫機器 API `/api/v1`。舊的 `/api/agent/v1` 已移除，不留別名：

```bash
curl -H "Authorization: Bearer jly_xxxxxxxx" https://your-site.example/api/v1/posts
```

可用的主要路徑與各路徑需要的權限清單，請見 `GET /api/v1`；`GET /api/v1/me` 會回傳此存取金鑰目前的有效權限。

`/api/admin/*` 只給管理後台網頁使用，只接受網頁的登入 Cookie（`jwt_token` 或 Session）。帶有任何 `Authorization` 標頭的請求一律回 `401`，寫入還要帶 `X-Jelly-CSRF` 標頭；程式與 API 串接請用上方的存取金鑰呼叫 `/api/v1`。

匿名要求回應 JSON `401`，不依賴 `Accept` 標頭；已登入但角色不符回應 `403`。商品、訂單、系統設定、金流、主題與外掛 API 僅允許管理員。內容與媒體 API 接受管理員、編輯者、作者，未知角色會被拒絕。作者的媒體所有權限制仍待完成，整體驗收狀態見 [STATUS.md](STATUS.md)。

#### 文章管理

- `GET /api/admin/posts` - 取得文章列表
- `POST /api/admin/posts` - 建立文章
- `GET /api/admin/posts/:id` - 取得文章
- `PUT /api/admin/posts/:id` - 更新文章
- `DELETE /api/admin/posts/:id` - 刪除文章

管理員與編輯者可管理所有文章及頁面；作者只能列出、讀取、更新及刪除自己的文章，不能建立頁面或將文章改成頁面。不可見或不存在的內容回應 `404`。新內容的作者使用目前登入帳號。

新增與更新使用單一 JSON 物件，支援 `title`、`slug`、`content`、`type`、`status`、`template`、`meta`、`published_at`。`type` 限 `post` 或 `page`；`published_at` 接受 RFC 3339 時間或 `null`。更新時省略的欄位保留原值。ID、作者、建立時間與修改時間由伺服器管理，輸入中的這些欄位及其他未知欄位不會套用。空內容、JSON 陣列、`null`、多個 JSON 值或尾端雜訊回應 `400`。

#### 產品管理

- `GET /api/admin/products` - 取得產品列表
- `POST /api/admin/products` - 建立產品
- `GET /api/admin/products/:id` - 取得產品
- `PUT /api/admin/products/:id` - 更新產品
- `DELETE /api/admin/products/:id` - 刪除產品

#### 訂單管理

- `GET /api/admin/orders` - 取得訂單列表

#### 主題管理

- `GET /api/admin/themes` - 取得主題列表
- `POST /api/admin/themes/activate` - 啟用主題
- `GET /api/admin/themes/:name/config` - 取得主題配置
- `GET /api/admin/themes/:name/settings` - 取得主題設定
- `POST /api/admin/themes/:name/settings` - 儲存主題設定

#### 外掛管理

- `GET /api/admin/plugins` - 取得外掛列表
- `POST /api/admin/plugins/:id/reload` - 重新載入外掛

重載只接受此網站已載入的外掛 ID，從 `data/plugins/<id>` 讀取新版本。manifest 的 ID 必須與目標相同，腳本與 OnBoot 成功後才交換版本並回應 `200`。未知 ID 回應 `404`，網站未提供 runtime 回應 `503`，候選載入失敗回應一般 `500` 並保留執行中的版本。匿名／非管理員依上述規則回應 `401`／`403`。

### 支付 API

- `POST /api/payment/create` - 建立支付訂單
- `POST /api/payment/:gateway/callback` - 支付回調
- `GET /api/payment/:gateway/return` - 支付返回
- `GET /api/payment/order/:id/status` - 查詢訂單狀態

## 🎨 後台組件系統

Jelly CMS 提供了一套完整的可複用 UI 組件，讓後台頁面開發更加高效。

### 可用組件

- **基礎組件**：Button、Badge、Avatar、Alert、Loading
- **容器組件**：Card、Modal、Grid
- **數據展示**：Stat Card、Table、Empty State
- **表單組件**：Form Group、Toggle、Search Box
- **導航組件**：Page Header、Breadcrumb、Pagination、Tabs
- **其他**：Dropdown、Quick Action Card

### 使用範例

```html
{{define "content"}}
{{/* 頁面標題 */}}
{{template "page-header" dict 
    "title" "文章管理" 
    "description" "管理您的所有文章"
    "actions" "<button class='btn'>新增文章</button>"
}}

{{/* 統計卡片 */}}
<div class="stats-grid">
    {{template "stat-card" dict 
        "title" "總文章數"
        "value" "123"
        "icon" "📝"
        "description" "已發布的文章"
    }}
</div>

{{/* 數據表格 */}}
{{template "card" dict 
    "title" "文章列表"
    "header" true
    "content" "表格內容..."
}}
{{end}}
```

詳細文檔請參考 [web/admin/COMPONENTS.md](web/admin/COMPONENTS.md)

## 🛠️ 開發

### 專案結構

```
jelly-cms/
├── cmd/
│   ├── server/          # 主程式入口
│   └── recover-oauth/   # 網站擁有者的舊 OAuth 帳號復原工具
├── internal/
│   ├── auth/            # 認證系統
│   ├── config/          # 配置管理
│   ├── core/            # 核心領域模型
│   ├── data/            # 資料庫層
│   ├── i18n/            # 國際化
│   ├── install/         # 安裝精靈
│   ├── payment/         # 金流系統
│   ├── plugin/          # 外掛系統
│   ├── server/          # HTTP 伺服器
│   └── theme/           # 主題系統
├── web/
│   ├── admin/           # 管理後台頁面
│   ├── locales/         # 語言檔案
│   └── themes/          # 主題檔案
├── data/                # 資料目錄（上傳檔案、外掛等）
├── docker-compose.yml   # Docker Compose 配置
├── Dockerfile           # Docker 映像檔配置
└── go.mod              # Go 模組定義
```

## 📝 授權

MIT License

## 🤝 貢獻

歡迎提交 Issue 和 Pull Request！

## 📧 聯絡

如有問題或建議，請透過 GitHub Issues 聯絡。
