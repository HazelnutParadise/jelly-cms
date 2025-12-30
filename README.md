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

主題位於 `web/themes/` 目錄，每個主題需要包含：

- `theme.json`: 主題配置檔案
- `layout.html`: 布局模板
- `index.html`: 首頁模板（或其他頁面模板）

### theme.json 範例

```json
{
  "name": "my-theme",
  "version": "1.0.0",
  "description": "我的主題",
  "author": "開發者名稱",
  "colors": {
    "primary": "#007bff",
    "secondary": "#6c757d",
    "background": "#ffffff",
    "text": "#212529"
  },
  "layout": {
    "header_style": "sticky",
    "sidebar": true,
    "sidebar_position": "right",
    "footer": true,
    "container_width": "wide"
  }
}
```

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
