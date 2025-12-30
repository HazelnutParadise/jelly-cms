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

組件的基礎樣式定義在 `components.html` 中。如需在特定頁面中覆蓋樣式，在頁面的 `{{define "styles"}}` 區塊中添加：

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

然後在 `components.html` 的 `<style>` 區塊中添加樣式：

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

