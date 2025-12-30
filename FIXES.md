# 修復內容

## 1. 介面語言根據網站的語言設定 ✅

### 修改文件
- `internal/server/middleware_i18n.go`

### 修改內容
- 調整語言優先級順序：
  1. **後台路由**：優先使用用戶的 cookie 設定（個人偏好）
  2. **前台路由**：使用資料庫的 `site_language` 設定（網站預設語言）
  3. **Query 參數**：`?lang=` 可以覆蓋以上設定
  4. **預設值**：`zh-TW`（繁體中文）

### 邏輯說明
- 後台管理員可以通過右上角的語言切換器選擇自己的介面語言（保存在 cookie）
- 前台訪客看到的語言由網站設定決定（`site_language` 選項）
- 這樣管理員可以使用英文介面管理中文網站，或反之

## 2. 設定頁面修復 ✅

### 修改文件
- `internal/server/handlers_admin.go`
- `web/admin/settings.html`

### 問題
1. `/api/admin/settings` GET 端點沒有從資料庫讀取設定
2. 表單沒有正確載入設定值
3. 保存後沒有錯誤處理

### 修復
1. **後端**：`api.GET("/settings")` 現在會從資料庫讀取所有選項
   ```go
   api.GET("/settings", func(c echo.Context) error {
       var options []core.Option
       data.DB.Find(&options)  // 添加這行
       return c.JSON(http.StatusOK, options)
   })
   ```

2. **前端**：改進 `loadSettings()` 函數
   - 使用 `form.elements[opt.key]` 正確存取表單元素
   - 添加錯誤處理
   - 初始化保存按鈕狀態

3. **前端**：改進表單提交
   - 添加完整的錯誤處理
   - 語言變更後自動重新載入頁面
   - 顯示詳細的錯誤訊息

## 3. 側欄卷軸美化 ✅

### 修改文件
- `web/admin/layout.html`

### 修改內容
添加自訂 scrollbar 樣式到 `.sidebar-nav`：

```css
/* Custom scrollbar for sidebar */
.sidebar-nav::-webkit-scrollbar {
    width: 6px;
}

.sidebar-nav::-webkit-scrollbar-track {
    background: transparent;
}

.sidebar-nav::-webkit-scrollbar-thumb {
    background: rgba(255, 255, 255, 0.2);
    border-radius: 3px;
}

.sidebar-nav::-webkit-scrollbar-thumb:hover {
    background: rgba(255, 255, 255, 0.3);
}
```

### 效果
- 卷軸寬度：6px（更細）
- 軌道：透明
- 滑塊：半透明白色，圓角
- 懸停效果：稍微變亮

## 4. 啟用的主題顯示修復 ✅

### 修改文件
- `web/admin/index.html`

### 問題
- 儀表板顯示的是主題資料夾名稱，而不是 `theme.json` 中的 `name` 欄位

### 修復
修改 `loadStats()` 函數中的主題載入邏輯：

1. 從 `/api/admin/settings` 獲取 `active_theme` 設定
2. 使用 `/api/admin/themes/:name/config` 獲取主題的 `theme.json`
3. 顯示 `theme.json` 中的 `name` 欄位（例如 "Jelly"）
4. 如果無法獲取，則回退到資料夾名稱
5. 添加完整的錯誤處理

### 範例
- 資料夾名稱：`default`
- `theme.json` 中的 `name`：`"Jelly"`
- 儀表板顯示：**Jelly** ✅

## 測試清單

### 1. 語言設定測試
- [ ] 在設定頁面將網站語言改為英文
- [ ] 保存後頁面自動重新載入
- [ ] 前台頁面顯示英文
- [ ] 後台可以通過右上角切換器獨立切換語言
- [ ] 重新登入後後台語言保持用戶選擇

### 2. 設定頁面測試
- [ ] 打開設定頁面，確認所有欄位都有正確的值
- [ ] 修改網站標題，保存成功
- [ ] 修改網站描述，保存成功
- [ ] 修改語言設定，保存後自動重新載入
- [ ] 嘗試保存空值，應該顯示錯誤

### 3. 側欄卷軸測試
- [ ] 調整瀏覽器高度，使側欄出現卷軸
- [ ] 確認卷軸是細的（6px）
- [ ] 確認卷軸是半透明白色
- [ ] 滑鼠懸停時卷軸變亮

### 4. 主題顯示測試
- [ ] 打開儀表板
- [ ] 確認「啟用主題」顯示 "Jelly"（而不是 "default"）
- [ ] 切換到其他主題
- [ ] 確認儀表板顯示新主題的 `name`

## 相關 API 端點

### 設定相關
- `GET /api/admin/settings` - 獲取所有設定
- `POST /api/admin/settings` - 保存設定

### 主題相關
- `GET /api/admin/themes` - 獲取所有主題列表
- `GET /api/admin/themes/:name/config` - 獲取主題的 theme.json
- `POST /api/admin/themes/activate` - 啟用主題

## 技術細節

### 語言優先級邏輯
```go
// 後台路由：用戶偏好 > 網站設定 > 預設值
if c.Path()[:6] == "/admin" {
    // 1. Cookie (用戶偏好)
    // 2. DB site_language (網站設定)
    // 3. Default: zh-TW
}

// 前台路由：網站設定 > 預設值
else {
    // 1. DB site_language (網站設定)
    // 2. Query param (臨時覆蓋)
    // 3. Default: zh-TW
}
```

### 表單元素存取
```javascript
// ❌ 錯誤方式
if (form[opt.key]) {
    form[opt.key].value = opt.value;
}

// ✅ 正確方式
const input = form.elements[opt.key];
if (input) {
    input.value = opt.value;
}
```

### 主題名稱獲取
```javascript
// 1. 獲取 active_theme 設定
const activeThemeSetting = settings.find(s => s.key === 'active_theme');

// 2. 獲取主題配置
const themeRes = await fetch(`/api/admin/themes/${activeThemeSetting.value}/config`);
const themeConfig = await themeRes.json();

// 3. 使用 theme.json 中的 name
const displayName = themeConfig.name || activeThemeSetting.value;
```

