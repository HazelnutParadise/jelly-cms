# Jelly CMS 開發狀態報告

## 專案概述
Jelly CMS 是一個使用 Go 語言開發的開源 CMS 系統，目標是比 WordPress 更快，並提供現代化的功能。

## 需求對照表

### ✅ 已完成功能

1. **比 WordPress 快，不要 PHP**
   - ✅ 使用 Go 語言開發（Echo 框架）
   - ✅ 使用 PostgreSQL 資料庫
   - ✅ 使用 goccy/go-json 提升 JSON 處理效能

2. **至少支援繁體中文和英文**
   - ✅ 已建立 i18n 系統（使用 go-i18n）
   - ✅ 已建立 `web/locales/zh-TW.json` 和 `web/locales/en.json`
   - ✅ 已實作 I18nMiddleware 支援語言切換
   - ⚠️ 翻譯內容還很少，需要擴充

3. **有支援自訂頁面布局和配色的主題系統**
   - ✅ 已建立主題管理器（`internal/theme/manager.go`）
   - ✅ 已建立主題配置結構（`internal/core/theme.go`）
   - ✅ 支援主題切換（`/api/admin/themes/activate`）
   - ✅ 主題模板渲染系統
   - ✅ **已完成：主題配色自訂功能（ThemeColors 結構，支援儲存和讀取）**
   - ✅ **已完成：主題布局自訂功能（ThemeLayout 結構，支援自訂欄位）**
   - ✅ **已完成：主題設定 API（GET/POST `/api/admin/themes/:name/settings`）**

4. **有外掛系統**
   - ✅ 已建立外掛管理器架構（`internal/plugin/manager.go`）
   - ✅ 使用 qjs (QuickJS) 作為 JavaScript 運行時
   - ✅ 支援外掛載入和卸載
   - ✅ **已完成：外掛 API 註冊功能（JellyCMS 全域物件，包含 registerHook, log, config）**
   - ✅ **已完成：外掛 Hook 系統基礎架構（支援 Hook 註冊和觸發）**
   - ✅ **已完成：外掛熱重載機制（Reload 方法）**
   - ✅ **已完成：外掛管理 API（GET /api/admin/plugins）**

5. **更改主題或外掛不用重啟主程式**
   - ✅ 主題切換已支援熱重載（清除快取後重新載入）
   - ✅ **已完成：外掛系統熱重載機制（Reload 方法，支援動態重新載入外掛）**

6. **支援 OAuth 登入，可直接使用第三方登入自動創建帳號**
   - ✅ 已整合 goth 庫支援 OAuth
   - ✅ 已實作 Google 和 GitHub OAuth
   - ✅ 已實作自動創建帳號功能
   - ✅ **已完成：Session 管理（使用 gorilla/sessions）**
   - ✅ **已完成：JWT Token 發放（使用 golang-jwt/jwt/v5）**
   - ✅ **已完成：登入狀態驗證中間件（RequireAuth, RequireAdmin, RequireRole）**
   - ✅ **已完成：登出功能（`/auth/logout`）**
   - ✅ **已完成：當前使用者查詢 API（`/api/auth/me`）**

7. **有像 WordPress 的初始化安裝精靈**
   - ✅ 已實作安裝精靈（`internal/install/service.go`）
   - ✅ 已建立安裝頁面（`web/admin/install.html`）
   - ✅ 支援資料庫連線測試
   - ✅ 支援建立管理員帳號
   - ✅ 安裝狀態檢查中間件

8. **希望能串金流**
   - ✅ **已完成：金流介面抽象層（PaymentGateway interface）**
   - ✅ **已完成：綠界 ECPay 整合（`internal/payment/ecpay.go`）**
   - ✅ **已完成：藍新 NewebPay 整合（`internal/payment/newebpay.go`）**
   - ✅ **已完成：金流服務管理器（`internal/payment/service.go`）**
   - ✅ **已完成：支付訂單建立 API（`POST /api/payment/create`）**
   - ✅ **已完成：支付回調處理（`POST/GET /api/payment/:gateway/callback`）**
   - ✅ **已完成：支付返回處理（`GET /api/payment/:gateway/return`）**
   - ✅ **已完成：訂單狀態查詢（`GET /api/payment/order/:id/status`）**

## 詳細功能狀態

### 核心功能
- ✅ 使用者管理（User model）
- ✅ 文章/頁面管理（Post model, PostService）
- ✅ 電商產品管理（Product model, ProductService）
- ✅ 訂單管理（Order, OrderItem models）
- ✅ 設定管理（Option model）
- ✅ 媒體上傳功能
- ✅ 資料庫自動遷移

### 管理後台
- ✅ 管理後台路由結構
- ✅ 文章/頁面編輯器頁面
- ✅ 產品管理頁面
- ✅ 訂單管理頁面
- ✅ 主題管理頁面
- ✅ 外掛管理頁面
- ✅ 設定頁面
- ⚠️ 缺少認證中間件（目前所有路由都開放）

### API 端點
- ✅ `/api/admin/posts` - 文章 CRUD
- ✅ `/api/admin/products` - 產品 CRUD
- ✅ `/api/admin/orders` - 訂單查詢
- ✅ `/api/admin/media` - 媒體管理
- ✅ `/api/admin/themes` - 主題管理
- ✅ `/api/admin/plugins` - 外掛列表
- ✅ `/api/admin/settings` - 設定管理
- ✅ `/api/install` - 安裝 API
- ✅ `/auth/:provider` - OAuth 登入
- ✅ `/auth/:provider/callback` - OAuth 回調

## 待完成工作

### 高優先級
1. ✅ **完善 OAuth 登入系統** - 已完成
   - ✅ 實作 Session 管理
   - ✅ 實作 JWT Token 發放
   - ✅ 實作認證中間件
   - ✅ 實作登出功能

2. ✅ **實作金流系統** - 已完成
   - ✅ 建立金流介面抽象層
   - ✅ 實作綠界 ECPay 整合
   - ✅ 實作藍新 NewebPay 整合
   - ✅ 建立訂單支付流程
   - ✅ 實作支付回調處理

3. ✅ **完善主題系統** - 已完成
   - ✅ 實作主題配色自訂功能
   - ✅ 實作主題布局自訂功能（自訂欄位）
   - ✅ 建立主題設定 API

4. **完善外掛系統**
   - 完成外掛 API 註冊
   - 實作 Hook 系統
   - 實作外掛熱重載機制
   - 建立外掛開發文檔

### 中優先級
5. **擴充 i18n 翻譯**
   - 補充管理後台所有頁面的翻譯
   - 補充錯誤訊息的翻譯

6. **完善管理後台**
   - 實作認證中間件
   - 實作權限管理（Role-based access control）
   - 完善前端 UI

### 低優先級
7. **效能優化**
   - 實作快取機制
   - 資料庫查詢優化

8. **安全性增強**
   - 實作 CSRF 保護
   - 實作 XSS 防護
   - 實作 SQL 注入防護（GORM 已提供部分保護）

## 技術架構

### 後端
- **語言**: Go 1.24.1
- **框架**: Echo v4
- **資料庫**: PostgreSQL (GORM)
- **JSON**: goccy/go-json
- **OAuth**: goth
- **Session**: gorilla/sessions
- **i18n**: go-i18n
- **外掛運行時**: qjs (QuickJS)

### 前端
- HTML 模板系統
- 管理後台使用傳統 HTML 頁面（可考慮未來改用 SPA）

### 資料庫結構
- Users (使用者)
- Posts (文章/頁面)
- Products (產品)
- Orders (訂單)
- OrderItems (訂單項目)
- Options (設定)

## 下一步行動計劃

1. ✅ 完善 OAuth 登入系統（Session + JWT） - **已完成**
2. ✅ 實作金流系統基礎架構 - **已完成**
3. ✅ 完善主題配色和布局自訂功能 - **已完成**
4. 完善外掛系統的 API 和 Hook
5. 擴充 i18n 翻譯內容

## 最新更新（本次開發）

### 已完成的主要功能

1. **OAuth 登入系統完善**
   - 實作了完整的 Session 管理機制
   - 整合 JWT Token 發放和驗證
   - 建立了認證中間件（RequireAuth, RequireAdmin, RequireRole）
   - 實作了登出功能和當前使用者查詢 API
   - 所有管理後台路由已加上認證保護

2. **金流系統實作**
   - 建立了完整的金流介面抽象層（PaymentGateway）
   - 實作了綠界 ECPay 完整整合（建立訂單、驗證回調、查詢狀態）
   - 實作了藍新 NewebPay 完整整合
   - 建立了金流服務管理器，支援多個金流服務
   - 實作了支付訂單建立、回調處理、返回處理等完整流程
   - 建立了訂單狀態查詢 API

3. **主題系統完善**
   - 擴展了主題配置結構，支援配色（ThemeColors）和布局（ThemeLayout）
   - 實作了主題自訂欄位系統（ThemeField）
   - 建立了主題設定資料庫模型（ThemeSettings）
   - 實作了主題設定 API（讀取和儲存）
   - 支援主題設定的熱重載（更改設定後自動清除快取）

### 技術實現細節

- **認證系統**：使用 `gorilla/sessions` 管理 Session，使用 `golang-jwt/jwt/v5` 處理 JWT
- **金流系統**：使用介面模式實現可擴展的金流整合，支援多個金流服務商
- **主題系統**：使用 JSONB 儲存主題設定，支援動態配置和熱重載

### 待完成功能

1. **外掛系統完善**：需要完成外掛 API 註冊和 Hook 系統
2. **i18n 翻譯擴充**：需要補充更多翻譯內容

