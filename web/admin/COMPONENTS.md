# Jelly CMS Admin Components

這是 Jelly CMS 後台管理界面的組件系統。所有組件都定義在 `components.html` 中，可以在任何後台頁面中重複使用。

## 組件列表

### 基礎組件

#### 1. Button（按鈕）
```html
{{template "button" dict "text" "保存" "icon" "💾"}}
{{template "button" dict "text" "取消" "secondary" true}}
{{template "link-button" dict "text" "新增" "href" "/admin/posts/new"}}
```

#### 2. Badge（徽章）
```html
{{template "badge" dict "type" "success" "text" "已發布"}}
{{template "badge" dict "type" "warning" "text" "草稿"}}
{{template "badge" dict "type" "danger" "text" "已刪除"}}
```

#### 3. Avatar（頭像）
```html
{{template "avatar" dict "initial" "A"}}
{{template "avatar" dict "src" "/avatar.jpg" "large" true}}
```

### 容器組件

#### 4. Card（卡片）
```html
{{template "card" dict 
    "title" "卡片標題" 
    "header" true
    "action" true
    "actionLink" "/more"
    "actionText" "查看更多"
    "content" "卡片內容"
}}
```

#### 5. Modal（模態框）
```html
{{template "modal" dict 
    "id" "myModal" 
    "title" "模態標題"
    "content" "模態內容"
}}
```

#### 6. Grid（網格）
```html
{{template "grid" dict 
    "columns" "3" 
    "gap" "1.5rem"
    "content" "網格項目"
}}
```

### 數據展示組件

#### 7. Stat Card（統計卡片）
```html
{{template "stat-card" dict 
    "class" "stat-card-1" 
    "title" "文章" 
    "value" "-" 
    "id" "postsCount" 
    "icon" "📝" 
    "description" "總共發布的文章數"
}}
```

#### 8. Table（表格）
```html
{{template "table" dict 
    "id" "dataTable"
    "loading" true
    "loadingText" "載入中..."
    "headers" (list 
        (dict "text" "標題")
        (dict "text" "狀態")
        (dict "text" "操作" "align" "right")
    )
}}
```

#### 9. Empty State（空狀態）
```html
{{template "empty-state" dict 
    "icon" "📦" 
    "title" "沒有數據" 
    "description" "還沒有任何內容"
}}
```

### 表單組件

#### 10. Form Group（表單組）
```html
{{template "form-group" dict 
    "for" "title" 
    "label" "標題" 
    "required" true
    "hint" "請輸入文章標題"
    "input" "<input type='text' id='title'>"
}}
```

#### 11. Toggle（開關）
```html
{{template "toggle" dict 
    "id" "enableFeature" 
    "checked" true 
    "label" "啟用功能"
    "onchange" "handleToggle()"
}}
```

#### 12. Search Box（搜尋框）
```html
{{template "search-box" dict 
    "id" "search" 
    "placeholder" "搜尋..."
    "oninput" "handleSearch(this.value)"
}}
```

### 導航組件

#### 13. Page Header（頁面頭部）
```html
{{template "page-header" dict 
    "title" "頁面標題" 
    "description" "頁面說明"
    "actions" "<button class='btn'>操作</button>"
}}
```

#### 14. Breadcrumb（麵包屑）
```html
{{template "breadcrumb" dict 
    "items" (list
        (dict "href" "/admin" "text" "首頁")
        (dict "text" "當前頁")
    )
}}
```

#### 15. Pagination（分頁）
```html
{{template "pagination" dict 
    "currentPage" 1 
    "totalPages" 10
    "prevText" "上一頁"
    "nextText" "下一頁"
}}
```

#### 16. Tabs（標籤頁）
```html
{{template "tabs" dict 
    "id" "myTabs"
    "tabs" (list
        (dict "label" "標籤1" "content" "內容1")
        (dict "label" "標籤2" "content" "內容2")
    )
}}
```

### 其他組件

#### 17. Alert（警告框）
```html
{{template "alert" dict 
    "type" "success" 
    "icon" "✓" 
    "message" "操作成功"
    "dismissible" true
}}
```

#### 18. Loading（載入）
```html
{{template "loading" dict "text" "載入中..."}}
```

#### 19. Dropdown（下拉選單）
```html
{{template "dropdown" dict 
    "id" "menu" 
    "trigger" "選單 ▼"
    "items" (list
        (dict "href" "/link1" "text" "項目1")
        (dict "onclick" "action()" "text" "項目2")
    )
}}
```

#### 20. Quick Action Card（快捷操作）
```html
{{template "quick-action-card" dict 
    "href" "/admin/posts/new" 
    "icon" "📝" 
    "title" "創建文章"
}}
```

## 輔助函數

組件系統提供了以下輔助函數：

### dict
創建字典（參數映射）
```go
dict "key1" "value1" "key2" "value2"
```

### list
創建列表
```go
list item1 item2 item3
```

### add / sub
數字運算
```go
{{add .page 1}}
{{sub .page 1}}
```

## 使用示例

### 完整頁面示例

```html
{{define "title"}}我的頁面{{end}}

{{define "styles"}}
<style>
/* 自定義樣式 */
.my-custom-class {
    /* ... */
}
</style>
{{end}}

{{define "content"}}
{{/* 頁面標題 */}}
{{template "page-header" dict 
    "title" "我的頁面" 
    "description" "頁面說明"
    "actions" "<button class='btn'>新增</button>"
}}

{{/* 統計卡片 */}}
<div class="stats-grid">
    {{template "stat-card" dict 
        "class" "stat-card-1"
        "title" "總數"
        "value" "100"
        "id" "totalCount"
        "icon" "📊"
        "description" "總數量"
    }}
</div>

{{/* 數據表格 */}}
{{template "card" dict 
    "title" "數據列表"
    "header" true
    "action" true
    "actionLink" "/admin/more"
    "actionText" "查看更多"
    "content" (template "table" dict 
        "id" "dataTable"
        "headers" (list 
            (dict "text" "名稱")
            (dict "text" "狀態")
            (dict "text" "操作" "align" "right")
        )
    )
}}
{{end}}

{{define "scripts"}}
<script>
// 頁面邏輯
</script>
{{end}}
```

## 組件定制

### 添加自定義 class
所有組件都支持 `class` 參數：

```html
{{template "button" dict "text" "按鈕" "class" "my-btn-class"}}
```

### 條件渲染
使用 Go template 的條件語句：

```html
{{if .showButton}}
    {{template "button" dict "text" "顯示"}}
{{end}}
```

### 循環渲染
```html
{{range .items}}
    {{template "badge" dict "type" "info" "text" .name}}
{{end}}
```

## 樣式定制

組件的基礎樣式統一定義在 `layout.html` 的 `<style>` 區塊中（`components.html` 只放結構；放在它裡面的頂層 `<style>` 不會被輸出）。如需在特定頁面中覆蓋樣式，在頁面的 `{{define "styles"}}` 區塊中添加：

```html
{{define "styles"}}
<style>
.my-card .card-header {
    background: var(--primary);
    color: white;
}
</style>
{{end}}
```

### 狀態元件

四個狀態元件的樣式在 `layout.html`，視覺對照 `design/project/States.dc.html`。`actions` 是 HTML，
要用 `(safeHTML "<a class='btn' …>…</a>")` 傳入；直接傳字串會被跳脫成純文字。`dict` 缺的鍵視為空。

#### 20. State Empty（空白狀態）
```html
{{template "state-empty" dict
    "title" "還沒有文章。"
    "text" "寫第一篇，或把 WordPress 的文章搬過來。"
    "actions" (safeHTML "<a class='btn btn-primary' href='/admin/posts/new'>…</a>")
}}
```
必填 `title`；選填 `text`、`actions`。右側的果凍丁拼貼在 1100px 以下隱藏。

#### 21. State Loading（載入中）
```html
{{template "state-loading" dict "text" .T.loadingText "note" "超過 3 秒才顯示" "skeleton" true}}
```
果凍丁 spinner + 文字，`skeleton` 為 true 時加佔位線。`role="status"`，減少動態偏好下動畫停止。

#### 22. State Error（整頁錯誤，墨色底）
```html
{{template "state-error" dict
    "code" 500 "id" "req_8f3a21"
    "title" .T.errorInternal "text" .T.errorInternalHint
    "backText" .T.backToDashboard "backHref" "/admin"
    "retryText" .T.retry
    "copyText" .T.copyErrorCode "copyValue" "500 req_8f3a21 /admin/posts"
}}
```
必填 `title`；選填 `code`、`id`（顯示成「500 · req_8f3a21」）、`text`、`backText` / `backHref`（預設 `/admin`）、
`retryText`（重新整理）、`copyText` + `copyValue`（複製到剪貼簿，成功時跳 toast）、`actions`。`admin/error.html` 用它處理 404、5xx。

#### 23. State Forbidden（無權限）
```html
{{template "state-forbidden" dict
    "title" .T.errorForbidden
    "text" (printf .T.forbiddenRole "編輯")
    "backText" .T.backToDashboard
}}
```
鎖頭 + 說明 + 回儀表板。必填 `title`；選填 `text`、`backText` / `backHref`、`actions`。`error.html` 對 401、403 使用。

### Toast（通知條）

右下角堆疊（手機是底部滿寬），墨色底、左邊口味色方點：success 薄荷、error 覆盆子、warning 黃、info 藍。
全域函式（定義在 `layout.html`，頁面的 `scripts` 區塊與行內腳本都可直接呼叫）：

```js
Jelly.toast('已發布', { kind: 'success', detail: '', action: { label: '看前台 ↗', href: '/post/x' } });
Jelly.toast('儲存失敗', { kind: 'error', detail: '連線中斷', action: { label: '重試', onClick: save } });
Jelly.toast('已移到回收桶', { kind: 'warning', action: { label: '復原', onClick: undo } });
```

- `kind`：`success` | `error` | `info`（預設）| `warning`。
- 自動消失：預設 4 秒，帶 `action` 的 8 秒；`error` 不自動消失；`duration`（毫秒，0 為不消失）可覆寫。滑過或聚焦時暫停。
- `action`：`{label, onClick}` 或 `{label, href}`。`onClick` 收到 `dismiss` 函式；點了動作預設會關閉這則 toast。
- 回傳 `{dismiss(), element}`。最多同時 4 則，超過會移除最舊的。訊息一律當純文字，不會被當 HTML。
- `error` 用 `role="alert"`，其餘 `role="status"`，容器有 `aria-label`。

伺服器端要發一則（例如 flash 訊息）時用模板：

```html
{{template "toast" dict "kind" "success" "message" .T.saved "detail" "" "actionLabel" .T.view "actionHref" "/post/x"}}
```

既有頁面的 `alert()` 沒有被攔截，之後逐頁改成 `Jelly.toast`。

## 外殼（layout.html）

- **側欄**：墨色（`--rail`、`--rail-hi`、`--rail-text`、`--rail-muted`），由 `.Nav` 產生，不寫死連結；
  `Active` 項目是覆盆子色並帶 `aria-current="page"`；`Badge > 0` 時顯示數字。`Icon` 是 sprite symbol id（例如 `i-grid`）。
  sprite 已含設計板用到的圖示（`i-comment`、`i-tag`、`i-users`、`i-bell`、`i-lock`、`i-db`、`i-tools` 等）；新圖示加在 `layout.html` 的 `<svg>` sprite。
- **麵包屑**：`.Crumbs` 為空時顯示「區段 / 頁面標題」（區段來自 `.Nav` 中 Active 項目所在的區段）；有值時接在區段後面，最後一個是目前頁。`.Title` 非空時覆寫瀏覽器分頁標題。
- **⌘K 指令面板**：Ctrl/⌘+K 或點頂欄搜尋框開啟；輸入即查（debounce 150ms，呼叫 `GET /api/admin/palette?q=`），空字串也查一次；
  ↑↓ 移動、Enter 開啟、Ctrl/⌘+Enter 新分頁、Esc 關閉並還原焦點；`>` 開頭只找指令、`/` 開頭只找頁面；
  結果分組的標籤列可篩選。面板開啟時背景為 `inert`。焦點在 Markdown 編輯器內時 Ctrl+K 仍是插入連結。
  程式介面：`Jelly.palette.open()`、`Jelly.palette.close()`。
- **通知鈴鐺**：連到 `/admin/notifications`，`.Viewer.Unread > 0` 顯示數字（99 以上顯示 99+）。
- **使用者區塊**：頭像（`.Viewer.Avatar` 或 `.Viewer.Initial`）、名稱、角色，連到 `/account`；登出按鈕沿用 `fetch('/auth/logout')`。
- **手機（≤900px）**：側欄收成抽屜（選單鈕、遮罩、Esc 關閉、關閉時不可被 Tab 到），頂欄只留目前頁名稱，搜尋與查看網站縮成圖示鈕。
- **預覽**：`go run ./cmd/jellypreview -out <dir>` 把每個後台頁面輸出成靜態 HTML，
  `PLAYWRIGHT_MODULE=… node design/tools/preview-admin.mjs <dir>` 截圖到 `design/shots/`。

## 最佳實踐

1. **保持組件單一職責**：每個組件只做一件事
2. **使用語義化參數**：參數名稱要清晰易懂
3. **提供預設值**：盡可能為參數提供合理的預設值
4. **保持一致性**：所有組件使用相同的設計語言
5. **文檔化**：為新組件添加使用說明

## 新增組件

要添加新組件，在 `components.html` 中定義：

```html
{{define "my-component"}}
<div class="my-component {{.class}}">
    <h3>{{.title}}</h3>
    <p>{{.content}}</p>
</div>
{{end}}
```

然後在 `layout.html` 的 `<style>` 區塊中添加樣式：

```css
.my-component {
    padding: 1rem;
    border: 1px solid var(--border-color);
    border-radius: var(--radius);
}
```

## 技術細節

- **模板引擎**：Go `html/template`
- **組件定義**：使用 `{{define "name"}}` 定義組件
- **組件使用**：使用 `{{template "name" data}}` 引用組件
- **參數傳遞**：使用 `dict` 函數創建參數對象
- **樣式作用域**：組件樣式全局生效，使用 BEM 命名避免衝突

