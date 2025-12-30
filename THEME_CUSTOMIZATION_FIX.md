# 主題客製化設定修復

## 問題描述
1. 主題客製化設定沒有載入預設值（從 `theme.json`）
2. 缺少「重置為預設值」的功能

## 解決方案

### 1. 載入預設值 ✅

#### 修改文件
- `web/admin/themes.html`

#### 實現邏輯
```javascript
// 1. 儲存預設值
let themeDefaults = {}; // 全域變數

async function customizeTheme(name) {
    // 載入 theme.json 配置
    const config = await configRes.json();
    
    // 儲存預設值
    themeDefaults = {
        colors: config.colors || {},
        layout: config.layout || {},
        custom: {}
    };
    
    // 顯示客製化模態框
    showCustomizeModal(config, settings);
}

// 2. 合併預設值和已儲存的值
function showCustomizeModal(config, settings) {
    // 先使用預設值
    let colors = {...(config.colors || {})};
    let layout = {...(config.layout || {})};
    
    // 如果有已儲存的設定，覆蓋預設值
    if (settings) {
        if (settings.colors) {
            const savedColors = JSON.parse(settings.colors);
            colors = {...colors, ...savedColors}; // 合併
        }
        if (settings.layout) {
            const savedLayout = JSON.parse(settings.layout);
            layout = {...layout, ...savedLayout}; // 合併
        }
    }
    
    // 使用合併後的值生成表單
    generateColorFields(colors, config.colors);
    generateLayoutFields(layout, config.layout);
}
```

### 2. 重置為預設值功能 ✅

#### 新增功能
- 在客製化模態框中添加「重置為預設值」按鈕
- 點擊後將設定重置為 `theme.json` 中的預設值
- 需要確認對話框防止誤操作

#### 實現代碼
```javascript
async function resetToDefaults() {
    // 確認對話框
    if (!confirm(T.resetConfirm)) {
        return;
    }
    
    try {
        // 將預設值保存回資料庫
        const res = await fetch(`/api/admin/themes/${currentCustomizingTheme}/settings`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                colors: JSON.stringify(themeDefaults.colors),
                layout: JSON.stringify(themeDefaults.layout),
                custom: JSON.stringify(themeDefaults.custom || {})
            })
        });
        
        if (res.ok) {
            alert(T.themeSettingsReset);
            closeCustomizeModal();
            // 重新載入客製化模態框以顯示預設值
            await customizeTheme(currentCustomizingTheme);
        } else {
            const err = await res.json();
            alert(T.failedToReset + ': ' + (err.error || 'Unknown error'));
        }
    } catch (e) {
        alert(T.failedToReset + ': ' + e.message);
    }
}
```

### 3. UI 改進 ✅

#### 模態框按鈕佈局
```html
<div style="margin-top: 2rem; display: flex; gap: 1rem; flex-wrap: wrap;">
    <button type="submit" class="btn">${T.saveSettings}</button>
    <button type="button" class="btn btn-secondary" onclick="resetToDefaults()">${T.resetToDefaults}</button>
    <button type="button" class="btn btn-secondary" onclick="closeCustomizeModal()">${T.cancel}</button>
</div>
```

- 使用 `flex-wrap: wrap` 確保在小螢幕上按鈕會換行
- 三個按鈕：儲存、重置、取消

### 4. i18n 支持 ✅

#### 新增翻譯
**繁體中文 (`web/locales/zh-TW.json`)**
```json
{
    "customize": "客製化",
    "activate": "啟用",
    "customizeTheme": "客製化主題",
    "colors": "顏色",
    "layout": "佈局",
    "customFields": "自訂欄位",
    "saveSettings": "儲存設定",
    "resetToDefaults": "重置為預設值",
    "resetConfirm": "確定要重置所有設定為預設值嗎？這將捨棄您的客製化設定。",
    "themeSettingsSaved": "主題設定已儲存！",
    "themeSettingsReset": "主題設定已重置為預設值！",
    "failedToSave": "儲存失敗",
    "failedToReset": "重置失敗",
    "themeActivated": "主題已啟用！",
    "failedToActivate": "啟用主題失敗",
    "noThemesInstalled": "沒有安裝任何主題。",
    "activeThemeBadge": "啟用中"
}
```

**英文 (`web/locales/en.json`)**
```json
{
    "customize": "Customize",
    "activate": "Activate",
    "customizeTheme": "Customize Theme",
    "colors": "Colors",
    "layout": "Layout",
    "customFields": "Custom Fields",
    "saveSettings": "Save Settings",
    "resetToDefaults": "Reset to Defaults",
    "resetConfirm": "Are you sure you want to reset all settings to defaults? This will discard your customizations.",
    "themeSettingsSaved": "Theme settings saved!",
    "themeSettingsReset": "Theme settings reset to defaults!",
    "failedToSave": "Failed to save",
    "failedToReset": "Failed to reset",
    "themeActivated": "Theme activated!",
    "failedToActivate": "Failed to activate theme",
    "noThemesInstalled": "No themes installed.",
    "activeThemeBadge": "Active"
}
```

## 工作流程

### 首次客製化（無已儲存設定）
1. 用戶點擊「客製化」按鈕
2. 系統載入 `theme.json` 的預設值
3. 表單顯示預設顏色和佈局
4. 用戶可以修改並儲存

### 已有客製化設定
1. 用戶點擊「客製化」按鈕
2. 系統載入 `theme.json` 的預設值
3. 系統載入已儲存的設定
4. **合併**：已儲存的值覆蓋預設值
5. 表單顯示合併後的值
6. 用戶可以繼續修改

### 重置為預設值
1. 用戶點擊「重置為預設值」按鈕
2. 顯示確認對話框
3. 確認後，將 `theme.json` 的預設值保存到資料庫
4. 關閉模態框並重新打開，顯示預設值

## 資料流

```
theme.json (預設值)
    ↓
載入到 themeDefaults
    ↓
合併已儲存的設定 (如果有)
    ↓
顯示在表單中
    ↓
用戶修改
    ↓
儲存到資料庫
```

## 範例：Jelly 主題預設值

### theme.json
```json
{
    "name": "Jelly",
    "colors": {
        "primary": "#FF6B6B",
        "secondary": "#4ECDC4",
        "background": "#F7F9FC",
        "text": "#333"
    },
    "layout": {
        "header_style": "sticky",
        "sidebar": false,
        "footer": true,
        "container_width": "wide"
    }
}
```

### 首次打開客製化
- Primary Color: `#FF6B6B` (從 theme.json)
- Secondary Color: `#4ECDC4` (從 theme.json)
- Header Style: `sticky` (從 theme.json)
- Footer: `true` (從 theme.json)

### 用戶修改後
- Primary Color: `#FF0000` (用戶修改)
- Secondary Color: `#4ECDC4` (保持預設)
- Header Style: `fixed` (用戶修改)
- Footer: `true` (保持預設)

### 重置後
- 所有值回到 theme.json 的預設值

## 測試清單

- [ ] 首次客製化時，表單顯示 theme.json 的預設值
- [ ] 修改並儲存後，重新打開顯示已儲存的值
- [ ] 只修改部分值時，其他值保持預設
- [ ] 點擊「重置為預設值」顯示確認對話框
- [ ] 確認重置後，所有值回到預設
- [ ] 取消重置時，不做任何更改
- [ ] 所有按鈕和提示訊息都有正確的 i18n 翻譯
- [ ] 在中文和英文介面下都能正常工作

## 技術細節

### 物件合併
```javascript
// 使用展開運算符合併物件
const defaults = { a: 1, b: 2, c: 3 };
const saved = { b: 20 };
const merged = {...defaults, ...saved}; 
// 結果: { a: 1, b: 20, c: 3 }
```

### 深拷貝
```javascript
// 使用展開運算符創建淺拷貝，避免修改原始物件
let colors = {...(config.colors || {})};
```

### 錯誤處理
```javascript
try {
    const savedColors = typeof settings.colors === 'string' 
        ? JSON.parse(settings.colors) 
        : settings.colors;
    colors = {...colors, ...savedColors};
} catch (e) {
    console.error('Failed to parse colors:', e);
    // 繼續使用預設值
}
```

## 相關文件
- `web/admin/themes.html` - 主題管理頁面
- `web/themes/default/theme.json` - 預設主題配置
- `internal/theme/manager.go` - 主題管理器
- `web/locales/zh-TW.json` - 繁體中文翻譯
- `web/locales/en.json` - 英文翻譯

