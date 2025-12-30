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

- Go 1.24.1 或更高版本
- PostgreSQL 資料庫
- 可選：Docker 和 Docker Compose（用於容器化部署）

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
export SESSION_SECRET=your-secret-key
export JWT_SECRET=your-jwt-secret

# 4. 編譯
go build -o server ./cmd/server

# 5. 執行
./server

# 6. 訪問 http://localhost:8080 進行安裝
```

## 🔌 外掛開發

Jelly CMS 使用 JavaScript（QuickJS）作為外掛運行時，讓開發者可以輕鬆擴展系統功能。

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
    "primary": "#FF6B6B",
    "secondary": "#4ECDC4",
    "background": "#F7F9FC",
    "text": "#333",
    "text_muted": "#666",
    "accent": "#FF6B6B",
    "link": "#4ECDC4",
    "link_hover": "#FF6B6B",
    "border": "#e0e0e0",
    "dark": "#2C3E50",
    "success": "#28a745",
    "warning": "#ffc107",
    "error": "#dc3545"
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
        <div class="post-content">
            {{.Content | html}}
        </div>
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

### 其他模板函數

除了主題相關的函數，還可以使用以下內建函數：

- `{{substr .Content 0 150}}`: 截取字串
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
            <p>{{substr .Content 0 200 | html}}...</p>
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

在環境變數中設定 OAuth 提供者的金鑰：

```bash
# Google OAuth
export GOOGLE_KEY=your-google-client-id
export GOOGLE_SECRET=your-google-client-secret

# GitHub OAuth
export GITHUB_KEY=your-github-client-id
export GITHUB_SECRET=your-github-client-secret

# 應用程式 URL
export APP_URL=http://localhost:8080
```

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

### API 設定

也可以透過 API 設定金流服務：

#### 取得金流設定

```http
GET /api/admin/payment/gateways
Authorization: Bearer <your-jwt-token>
```

#### 設定綠界 ECPay

```http
POST /api/admin/payment/gateways/ecpay
Authorization: Bearer <your-jwt-token>
Content-Type: application/json

{
  "enabled": true,
  "merchant_id": "your-merchant-id",
  "hash_key": "your-hash-key",
  "hash_iv": "your-hash-iv",
  "test_mode": true
}
```

#### 設定藍新 NewebPay

```http
POST /api/admin/payment/gateways/newebpay
Authorization: Bearer <your-jwt-token>
Content-Type: application/json

{
  "enabled": true,
  "merchant_id": "your-merchant-id",
  "hash_key": "your-hash-key",
  "hash_iv": "your-hash-iv",
  "test_mode": true
}
```

**注意**：設定完成後，系統會自動重新載入金流配置，無需重啟服務。

## 📚 API 文檔

### 管理後台 API

所有管理後台 API 都需要認證，請在請求頭中包含 JWT Token：

```
Authorization: Bearer <your-jwt-token>
```

或使用 Session Cookie（網頁端）。

#### 文章管理

- `GET /api/admin/posts` - 取得文章列表
- `POST /api/admin/posts` - 建立文章
- `GET /api/admin/posts/:id` - 取得文章
- `PUT /api/admin/posts/:id` - 更新文章
- `DELETE /api/admin/posts/:id` - 刪除文章

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

### 支付 API

- `POST /api/payment/create` - 建立支付訂單
- `POST /api/payment/:gateway/callback` - 支付回調
- `GET /api/payment/:gateway/return` - 支付返回
- `GET /api/payment/order/:id/status` - 查詢訂單狀態

## 🛠️ 開發

### 專案結構

```
jelly-cms/
├── cmd/
│   └── server/          # 主程式入口
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
