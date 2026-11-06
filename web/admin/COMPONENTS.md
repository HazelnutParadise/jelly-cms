# Jelly CMS Admin Components

這是 Jelly CMS 後台管理界面的組件系統。自家組件定義在 `components.html`，Jelly UI 元件由 `layout.html` 載入的 `/admin/assets/jelly-ui/jelly.js` 提供；兩者怎麼分工看下面的〈界線〉。決定與理由在 `docs/tickets/26-jelly-ui-consistency.md`，設計板上的樣子在 `design/README.md`〈Jelly UI 元件對照（界線）〉。

## 界線：同一類控制項在整個後台只有一種實作

| 類別 | 控制項 | 用什麼 |
|---|---|---|
| 選擇與切換 | 開關、分段選擇、勾選框、單選群組 | Jelly UI：`<jelly-switch>`、`<jelly-segmented>`／`<jelly-segment>`、`<jelly-checkbox>`、`<jelly-radio-group>`／`<jelly-radio>` |
| 回饋與覆蓋層 | 通知、確認、一般對話框 | Jelly UI：`Jelly.toast()`、`Jelly.confirm()`、`<jelly-dialog>`（以 `Jelly.dialog()` 打開） |
| 輸入與動作 | 文字欄位、多行文字、下拉選單、按鈕、徽章、卡片、表格、側欄、⌘K 面板 | 自家（底線欄位、對角單角按鈕、覆盆子紅主要動作）；不用 `jelly-button`、`jelly-input`、`jelly-textarea`、`jelly-select`、`jelly-badge`、`jelly-chip`、`jelly-tabs`、`jelly-card`、`jelly-pagination`、`jelly-tooltip`、`jelly-menu`、`jelly-drawer` |
| 導覽 | 會換網址或切換整頁區塊的分頁列：設定頁分頁列、個人資料分頁、附件策略頁與轉址頁的 `.sys-tabs` | 自家分頁 |

**分段選擇還是分頁？** 在卡片或清單內選一個值、篩選清單、切換卡片內的來源，用 `<jelly-segmented>`（文章狀態、留言狀態篩選、使用者角色篩選、折扣碼類型、選單「加入項目」的來源）；換網址或切換整頁的主要區塊，用自家分頁（`<nav class="tabs">`、`.profile-tabs`、`.sys-tabs`）。

不在界線內：登入、安裝、帳號復原與前台主題不載入 Jelly UI；編輯器預覽與文章內容裡的待辦清單 `<input type="checkbox" disabled>` 是使用者內容，不是後台控制項。

## Jelly UI 元件寫法

共同規則：

- **讀寫一律用屬性。** `jelly.js` 是 deferred module，頁面的行內腳本比它先跑；元素升級前寫 `el.checked = …`、`el.value = …` 會變成元素自己的屬性，永遠遮住升級後的 accessor。開關、勾選框、單選讀 `el.hasAttribute('checked')`、寫 `el.toggleAttribute('checked', v)`；分段讀 `el.getAttribute('value')`、寫 `el.setAttribute('value', v)`；停用寫 `el.toggleAttribute('disabled', v)`（`el.disabled = …` 對 Jelly 勾選框與單選沒有作用）。不要用 `.checked`、`.indeterminate` 或 `:checked` 選擇器，改用 `[checked]`。
- **`change` 只在使用者操作時發出**（`bubbles`、`composed`，在元素本身上）；程式改屬性不發事件。
- **尺寸**：勾選框、單選與單選群組一律 `size="sm"`（28px，和開關軌道等高；腳本建立的要 `setAttribute('size', 'sm')`）。開關與分段選擇用預設大小，不加 `size`。
- **可存取名稱**：沒有看得見的文字時給 `label` 屬性（`aria-label` 寫在主機上不會傳進 shadow DOM）。
- 表單：開關、分段、勾選框、單選都是 form-associated（單選群組不是）。開關、勾選框、單選勾選時 `FormData` 有 `name=value`（`value` 預設 `"on"`），沒勾時沒有這個鍵，與原生勾選框相同；分段送出選中段的 `value`。勾選框與單選**沒有** `formResetCallback`：`form.reset()` 不會還原它們，要自己把屬性設回預設。

### 開關 `<jelly-switch>`

```html
<div class="tx-toggle"><span>不在列表顯示<small>說明</small></span>
    <jelly-switch id="fHide" name="hide_from_list" label="不在列表顯示"></jelly-switch></div>
```

```js
const on = $('fHide').hasAttribute('checked');
$('fHide').toggleAttribute('checked', !!item.hide_from_list);
$('fHide').addEventListener('change', () => { /* 使用者切換 */ });
```

空白鍵與 Enter 都會切換，所以表單裡的開關按 Enter 不會送出表單。點旁邊的 `<span>` 文字不會切換；要整列可點，把文字放進開關的內容（slot）。

### 分段選擇 `<jelly-segmented>`

```html
<div class="seg-scroll">
    <jelly-segmented id="statusSeg" name="status" value="pending" label="狀態">
        <jelly-segment value="all">全部</jelly-segment>
        <jelly-segment value="pending">待審</jelly-segment>
    </jelly-segmented>
</div>
```

```js
$('statusSeg').addEventListener('change', (e) => load(e.detail.value));
$('statusSeg').setAttribute('value', 'approved');            // 程式選取
Jelly.segmentText($('statusSeg'), 'pending', '待審 12');      // 只在文字真的變了才改
```

- 段的內容只能是純文字（不能放圖示或 `<b>`）；數字就接在文字後面（`待審 12`）。
- 改 `<jelly-segment>` 的文字會讓分段重建內部按鈕、焦點掉出：更新篇數一律用 `Jelly.segmentText(segmented, value, text)`，文字沒變就不動，焦點原本在分段裡會放回選中的段。
- 方向鍵、Home、End 移動並選取（roving tabindex）。分段沒有「都不選」的狀態，`value` 不對應任何段時顯示第一段。
- 分段可能比欄位寬（手機、窄卡片、英文）：包一層 `.seg-scroll`，只在自己的列內橫向捲動，不讓整頁溢出（沿用文章列表的做法）。
- 不要在載入期間對分段加 `disabled`：屬性一變就重建內部按鈕，焦點會掉出。

### 勾選框 `<jelly-checkbox>`

```html
<jelly-checkbox size="sm" id="fShip">需要運送</jelly-checkbox>
<jelly-checkbox size="sm" id="selectAll" label="全選"></jelly-checkbox>
```

```js
const cb = document.createElement('jelly-checkbox');
cb.setAttribute('size', 'sm');
cb.setAttribute('label', item.title);
cb.toggleAttribute('checked', selected.has(item.id));
tbody.addEventListener('change', (e) => { const c = e.target.closest('[data-select]'); if (c) toggle(c.dataset.select, c.hasAttribute('checked')); });
all.toggleAttribute('indeterminate', some && !every);    // 表格全選的部分選取；使用者一點就清掉
```

點勾選框的任何地方（方塊或文字）都會切換；點一下主機會收到兩次 `click`（文字與內部 input），所以要聽 `change`，不要聽 `click`。

### 單選 `<jelly-radio-group>`／`<jelly-radio>`

```html
<jelly-radio-group size="sm" direction="vertical" label="角色">
    <div class="invite-role-card"><jelly-radio size="sm" name="role" value="editor" checked><strong>編輯</strong><span class="hint">…</span></jelly-radio></div>
    <div class="invite-role-card"><jelly-radio size="sm" name="role" value="author"><strong>作者</strong><span class="hint">…</span></jelly-radio></div>
</jelly-radio-group>
```

```js
const chosen = form.querySelector('[name=role][checked]');
const role = chosen ? chosen.getAttribute('value') : 'editor';
form.querySelectorAll('[name=role]').forEach((r) => r.toggleAttribute('checked', r.getAttribute('value') === 'editor'));
```

- **同一組靠 `name`**：上游把「同一個 root（文件或 shadow root）裡同名的 `<jelly-radio>`」當一組，與外層的 `<jelly-radio-group>` 無關，所以同一頁不同用途的單選要用不同的 `name`。
- `<jelly-radio-group>` 只負責 `label`（顯示成群組標題並成為可存取名稱）、`direction`、`size` 與 `role="radiogroup"`；它**沒有** `value`、`name`，也不發 `change`。選取狀態在被選的 `<jelly-radio checked>`，`change` 由被選的單選發出。
- 使用者點選（或方向鍵、空白鍵）會清掉同組其他單選的 `checked`；程式設 `checked` 不會清別人，也不發事件，要自己清。
- 「勾選卡片」（邀請角色、匯出格式、輪替方式）保留卡片版面：卡片內放 `<jelly-radio>`，卡片的其他地方用 click 委派成「選取這張卡的單選」，讓點整張卡片等同點選。

### 對話框 `<jelly-dialog>` 與 `Jelly.dialog()`

```html
<jelly-dialog id="batchSheet" class="dialog-wide" label="批次產生">
    <div class="modal-header"><h2>批次產生</h2></div>
    <form id="bf">…<div class="modal-footer"><button type="submit" class="btn btn-primary">產生</button></div></form>
</jelly-dialog>
```

```js
Jelly.dialog($('batchSheet'), { opener: $('batchOpen'), focus: '#bPrefix' });   // 打開
Jelly.closeDialog($('batchSheet'));                                            // 關閉（有退場動畫）
$('batchSheet').addEventListener('close', () => { /* 清理；Esc、背景、✕ 關閉也會到這裡 */ });
```

- 打開一律用屬性：`Jelly.dialog(el)` 等 `jelly.js` 定義好元素後 `setAttribute('open', '')`；伺服器輸出的 `<jelly-dialog>` 不帶 `open`。也可以直接 `removeAttribute('open')` 立刻關閉（沒有動畫）。不要呼叫 `.showModal()`（只在升級後存在）。
- `Jelly.dialog(el, { opener, focus, locked })`：`opener` 是關閉後焦點要回去的元素（預設是打開當下的焦點），`focus` 是打開後要聚焦的元素或選擇器（預設是對話框本體），`locked: true` 讓對話框不理 Esc、點背景，也隱藏右上 ✕，只能由程式關閉（備用碼「我已保存備用碼」之前不能關）。處理中要暫時鎖住，可以直接切換 `data-jelly-locked` 屬性。對已經開著的對話框再呼叫一次（例如換內容）會保留原本的 opener。
- `dismissOnBackdrop: false`：點背景不關，Esc 與右上 ✕ 照常（與 `locked` 不同：`locked` 連 Esc 與 ✕ 都擋）。用在**輸入或產出後不能因誤點而消失**的對話框：一次性金鑰、還原草稿提示、編輯器的匯出／快捷鍵、主題客製化、外掛上傳、系統頁的表單對話框。預設（不給或給 `true`）點背景關閉，用在短的確認或單一表單（折扣碼批次、選單新增與設定、留言回覆、使用者、個人帳號身分確認）。
- 點背景的判定：只有按下與放開都在背景（`.wrap` 本身）才算點背景。從對話框內容拖到背景才放開不會關（`layout.html` 攔下上游的 click），所以不必在各頁另外處理拖曳選字。
- **Esc 只關最上層**：上游每個開著的對話框都在 `document` 上聽 Esc，巢狀時（對話框裡再開 `Jelly.confirm`）會一次全關。`layout.html` 維護開啟堆疊，在 `window` 的捕獲階段攔下 Esc（`preventDefault`）再只關最上層，所以頁面自己的 Esc 處理請先檢查 `event.defaultPrevented`。
- 打開時對話框會被移到 `<body>`、鎖捲動、外面設成 `inert`。所以**表單要放在對話框裡面**，對話框裡的樣式不能依賴祖先選擇器（例如 `.accounts-page …`），改用 `#userDialog …` 或對話框自己的 class。寬度用 `--jelly-dialog-width`（`.dialog-wide` 是 40rem），內距用 `--jelly-dialog-padding`。
- 可存取名稱：給 `label` 屬性，或讓第一個 `<h1>`／`<h2>`／`<h3>` 當名稱。右上 ✕ 的 `aria-label` 由 `layout.html` 依介面語言改成「關閉」／Close。
- 沒有 Enter 處理、沒有確認／取消的概念；表單照原本的 `<form>` 送出。對話框開著時外面的通知看得到但點不到。

### 確認 `Jelly.confirm()`

```js
if (!await Jelly.confirm({ title: '刪除「夏季特賣」？', message: '只有沒用過的折扣碼可以刪除。', confirmLabel: '刪除', cancelLabel: '取消', tone: 'danger' })) return;
```

`tone: 'danger'` 時初始焦點在取消鈕；`requireText` 要輸入同樣的字才能確認。Esc、取消、點背景、✕ 都回傳 `false`；開在一般對話框上時 Esc 只關確認框，焦點回到觸發的按鈕。不要用原生 `confirm()`、`alert()`、`prompt()`。

### 通知 `Jelly.toast()`

```js
Jelly.toast(T.copied, { kind: 'success' });
Jelly.toast(T.saveFailed, { kind: 'error', detail: err.error });
```

成功用 `kind: 'success'`、失敗用 `kind: 'error'`；訊息文字一律走 i18n 鍵（`{{.T.*}}` 或頁面的 `T`），細節放 `detail`。完整參數見下方〈Toast（通知條）〉。

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

#### 5. Modal（一般對話框）
```html
{{template "modal" dict 
    "id" "myModal" 
    "title" "對話框標題"
    "content" "對話框內容"
}}
```
輸出 `<jelly-dialog id label>`，以 `Jelly.dialog(document.getElementById('myModal'))` 打開；寫法見上方〈對話框〉。

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

#### 16. Tabs（標籤頁，導覽用；選值或篩選用 `<jelly-segmented>`）
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

後台不用原生 `alert()`：操作結果一律 `Jelly.toast`，成功 `kind: 'success'`、失敗 `kind: 'error'`。

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

