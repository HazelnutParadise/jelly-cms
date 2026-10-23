# 07 — 顧客可以在前台購物：商店、分類、商品頁、購物車、結帳、付款、訂單查詢、會員中心；站長可以管折扣碼與訂單

Epic：商店
User Story：訪客能瀏覽商店與分類、看商品、加入購物車、用折扣碼、填寫收件與發票資料、以信用卡／ATM／超商代碼或轉帳付款，付款後收到確認並能查詢訂單；會員能在會員中心看訂單進度、取消未出貨訂單、管理地址與發票設定；站長能管折扣碼、處理訂單（出貨、退款、備註）並在設定裡設運送、金流與稅。
Blocked by：04 的分類與留言模式（已合併）、06 的 `internal/notify`、`user_invites`、`RequirePermission`（草稿 PR #24；本工作單在同一條分支上接續時以該契約為準）。
Status：draft（本工作單只交付設計板補齊、契約、骨架與失敗的驗收測試；實作由另一批代理依本文件完成）
設計稿：`design/project/Shop、ShopCategory、Product、Cart、Checkout、OrderDone、Account、OrderLookup、Coupons、OrderDetail、Products、ProductEditor、Orders、Settings.dc.html`；本工作單先補：商品分類管理（`Taxonomy` 板加「商品分類」分段）、`Settings` 板的「運送」分頁與金流遮罩／連線測試／通知網址、`Coupons` 板的買 X 送 Y 欄位與側欄「折扣碼」入口、`OrderDetail` 板的庫存時間軸改成「下單時保留」、訂單編號統一為 `JL-YYYY-NNNN`、`ProductEditor` 板的規格、原價、低庫存門檻、分類、運送旗標、SEO。

## 流程與驗收

- [ ] 訪客在 `/shop` 與 `/shop/c/<slug>` 看到商品卡（圖、名稱、售價、原價劃線、「加入」「售完」「剩 N」）、分類與數量、價格區間／只看有貨／排序篩選、每頁 12 件、超出頁數 404；分類頁有麵包屑、子分類 chip、特性篩選與作用中篩選摘要。
- [ ] 訪客在 `/shop/p/<slug>` 看到規格選擇、庫存狀態、售價與原價、短描述、三行信任資訊（配送／付款／鑑賞期）、Markdown 長描述、成分、一起買；草稿、下架、回收桶商品 404；頁面輸出 JSON-LD Product。
- [ ] 訪客的購物車存在伺服器，以 HttpOnly Cookie 的隨機權杖識別（只存雜湊）；每次讀取重新計價；數量夾在庫存內並回報 `issues`；售完的項目保留在列表但不計入總額；登入時合併訪客購物車。
- [ ] 訪客能套用與移除折扣碼：百分比、固定金額、免運、買 X 送 Y；範圍（全店／分類／商品）、最低消費、起訖、總次數、每人次數、只給會員、可否與免運碼疊加；總額不為負、折扣不超過小計。
- [ ] 訪客在 `/checkout` 填聯絡資料（Email、手機、是否建立帳號）、運送方式（宅配、自取；超商取貨顯示「稍後開放」）、收件人與地址、付款方式（信用卡／ATM／超商代碼走 ECPay、銀行轉帳人工確認；沙盒只在非正式環境）、發票（手機條碼、統編、捐贈，格式驗證）、同意條款；送出後建立訂單並導向付款或顯示轉帳說明；金額全部由伺服器計算，請求裡的任何價格欄位一律忽略；同一 `idempotency_key` 回同一筆訂單。
- [ ] 下單時保留庫存（信用卡 30 分鐘、ATM／超商代碼 72 小時、轉帳 72 小時）；逾期未付自動釋放並釋放折扣碼次數；兩個人同時買最後一件只有一個成功、庫存不為負；庫存異動都進帳本。
- [ ] 付款通知只接受簽章正確、金額與幣別相符的請求；重複通知只認一次；回傳頁只顯示狀態、不改訂單；付款失敗保留訂單可重試或換付款方式，不直接取消。
- [ ] 訪客在 `/checkout/done/<number>` 看到成功（編號、付款方式、預計到貨、品項、總額、下載收據、重寄確認信）或失敗（重新付款、換付款方式、保留期限）狀態。
- [ ] 訪客在 `/orders/lookup` 輸入訂單編號與 Email，不論是否符合都回同一個訊息；符合時寄 15 分鐘有效的查詢連結，連結只存雜湊；查詢與重寄都有速率限制。
- [ ] 下單的 Email 會成為 `customer` 角色的帳號（沒勾建立帳號時是未啟用的休眠帳號，只用來串訂單）；勾了建立帳號會收到設定密碼的邀請連結；`customer` 進不了 `/admin`。
- [ ] 會員在 `/account/orders` 看到訂單分段計數、時間軸（已付款、備貨中、已出貨＋物流、已送達）、下載收據、聯絡客服、取消（只限未出貨）、再買一次；`/account/addresses`、`/account/invoices` 能管理地址與發票設定；員工進 `/account` 會被導向後台個人帳號。
- [ ] 站長在 `/admin/orders` 看到 KPI 列（30 天營收、訂單數、待處理、平均客單）、分段與搜尋；在 `/admin/orders/<id>` 看到明細、時間軸、內部備註、顧客卡、付款面板，能標記已出貨（物流與單號）、完成、取消、退款（全額或部分、是否回補庫存）、人工確認收款、修改地址、列印出貨單；狀態只能照狀態機走。
- [ ] 站長在 `/admin/coupons` 看到進行中／排程／已結束分段、搜尋、本月影響；能建立、編輯、停用、刪除（未使用才能刪）、批次產生一人一碼、在結帳頁預覽。
- [ ] 站長在商品編輯頁管理規格、原價、低庫存門檻、商品分類、運送旗標（需要運送、冷藏）、SEO；商品列表有搜尋、狀態分段、SKU、低庫存標示、分頁；價格顯示正確（NT$120 不再顯示成 12,000）。
- [ ] 站長在設定的「商店」看到幣別、稅、運送方式與免運門檻、付款方式對應、金流金鑰（遮罩顯示、連線測試、通知網址）、收據賣方資料、轉帳說明。
- [ ] 所有前台商店頁面與後台頁面照設計板，手機 390px 可用，不從外部網址載入任何資源。

## 錯誤與邊界

價格與金額一律以「分」（int64）儲存，顯示與金流換算只經 `internal/money`。訂單編號 `JL-<年>-<四位流水>`，每年歸零，後台與顧客端都用同一個；金流交易號另用 20 碼英數 `merchant_trade_no`。狀態機：pending → paid｜cancelled｜expired；paid → shipped｜cancelled（含退款）；shipped → completed｜refunded；completed → refunded；refunded、cancelled、expired 為終態；每次轉移寫 `order_events`。表單與 API 錯誤：欄位錯誤 400（附欄位對照）、庫存不足 409 `{code:"stock"}`、購物車已變動 409 `{code:"cart_changed"}`、折扣碼無效 422、超量 429、非法狀態轉移 409 `{allowed:[...]}`、非本人訂單 404。所有權杖只存 SHA-256；金流金鑰不回傳明文。沒有 SMTP 時顧客信件進待送表，查詢連結永遠不回給匿名呼叫者。

## 契約（骨架與測試共同依據）

### 資料（`internal/data/migrations/0008` 起，與 06 同一個執行器）

| 表 | 主要欄位 |
|---|---|
| product_categories、product_category_links | `id, parent_id, name, slug unique, description, cover, position, hidden`；`(product_id, category_id)` |
| products（ALTER） | `compare_at_price, low_stock_threshold default 5, requires_shipping default true, cold_chain default false, attributes jsonb, related_ids jsonb, sold_count, seo_title, seo_description, kind physical|digital` |
| product_variants | `id, product_id, name, sku, price, compare_at_price, stock, position, status`；遷移為每個商品補一個名稱為空的預設規格 |
| stock_movements | `id, variant_id, order_id null, delta, reason reserve|release|restock|adjust, created_at`（只增不改） |
| carts、cart_items | `id, token_hash unique, user_id null, coupon_code, expires_at, updated_at`；`(cart_id, variant_id) unique, qty` |
| orders（ALTER） | `number unique, user_id → nullable, email, phone, currency, subtotal, discount_total, shipping_total, tax_total, coupon_code, coupon_id, shipping_method, shipping jsonb, invoice jsonb, payment_provider, payment_method, lookup_token_hash, idempotency_key unique, expires_at, paid_at, shipped_at, completed_at, cancelled_at, carrier, tracking_no, refunded_total, internal_note, locale` |
| order_items（ALTER） | `variant_id, title, variant_name, sku, image, unit_price, line_total, product_id → nullable ON DELETE SET NULL` |
| order_events | `id, order_id, at, actor_user_id null, kind, from_status, to_status, message, meta jsonb` |
| order_counters | `year PK, seq` |
| addresses、invoice_profiles、invoices | 見設計板 Account；`invoices` 先建表不用（電子發票留待後續） |
| coupons、coupon_targets、coupon_batches、coupon_redemptions | `code unique lower, label, kind percent|fixed|free_shipping|bogo, value, buy_qty, get_qty, min_subtotal, scope all|category|product, usage_limit, used_count, per_user_limit, starts_at, ends_at, stackable_with_free_shipping, members_only, batch_id, bound_email, status`；`(coupon_id, order_id) unique, released_at` |
| payments、payment_events | `order_id, provider, method, merchant_trade_no unique, amount, currency, status, provider_trade_no, masked_info jsonb, paid_at`；`provider, dedupe_key unique, verified, payload` |
| order_access_tokens | `order_id, token_hash unique, kind lookup|view, expires_at, used_at, created_ip_masked` |
| stock_alerts | 建表，功能留待後續 |

### 新套件與核心函式

- `internal/money`：`Format(minor int64, currency string) string`（`NT$120`、千分位）、`FromInput(s string) (int64, error)`（整數元 → 分）、`ToGateway(minor int64) (int64, error)`（分 → 元；非整數元回錯誤）。
- `internal/core/order_state.go`：`OrderService.Transition(tx, orderID uint, from, to string, actor *uint, note string) error`（compare-and-set，寫 `order_events`）；`AllowedTransitions(status) []string`。
- `internal/core/stock.go`：`Reserve(tx, variantID, qty, orderID)`、`Release(tx, orderID)`、`Restock(tx, orderID)`，全部寫 `stock_movements`，庫存不可為負（DB check）。
- `internal/core/coupon.go`：`Evaluate(cart, coupon, user) (Discount, error)`、`Redeem(tx, couponID, orderID, userID *uint, email string) error`（原子 `used_count` 遞增）、`ReleaseRedemption(tx, orderID)`。
- `internal/core/customer.go`：`EnsureCustomer(tx, email, phone string) (core.User, error)`（找不到就建 `role=customer, status=invited, provider=local`；找到就沿用，絕不改角色、密碼、狀態）。
- `internal/payment`：`Gateway` 介面加 `Capabilities() {Methods []string; Offline bool}`、`Query`／`Refund` 不支援時回 `ErrNotSupported`；新提供者 `manual_transfer`（離線，說明文字來自選項）與 `sandbox`（只在 `payment_sandbox_enabled=true` 且 `APP_ENV != production`；`/checkout/sandbox/<trade_no>` 頁面可按成功／失敗／ATM 待繳，回呼以每站獨立金鑰 HMAC-SHA256 簽章）；`Finalize(tx, provider string, cb VerifiedCallback) error` 以 `merchant_trade_no` 鎖列、比對金額幣別、`payment_events` 去重、走狀態機；通知路由改為 `POST /api/payment/:gateway/notify`（`/callback` 保留別名，只收 POST）。

### 公開路由與 API（全部在 `/:slug` 之前註冊；`shop`、`cart`、`checkout`、`orders`、`account` 加進保留別名）

| 路由 | 模板／回應 |
|---|---|
| `GET /shop`、`/shop/c/:slug`（`page, sort=new|price_asc|price_desc|popular, price=lt300|300-800|gt800, instock=1, attr=`） | `shop.html`、`shop_category.html`：`Products, Categories（含數量）, Term, Children, Breadcrumbs, Filters, Page, TotalPages, Total` |
| `GET /shop/p/:slug` | `product.html`：`Product, Variants, Category, Related, Trust（選項）, JSONLD` |
| `GET /cart`、`GET /checkout`（空車 302 `/cart`）、`GET /checkout/done/:number`（需本人 session 或 `?t=` 權杖）、`GET /checkout/sandbox/:trade_no` | `cart.html`、`checkout.html`、`order_done.html`、`sandbox_pay.html` |
| `GET /orders/lookup`、`GET /orders/view?t=`（換成 30 分鐘、限該訂單的 Cookie 後 302 `/orders/view/:number`） | `order_lookup.html`、`order_view.html` |
| `GET /account`（customer → `/account/orders`；員工 → `/admin/account?tab=identities`）、`/account/orders`、`/account/orders/:number`、`/account/addresses`、`/account/invoices`、`/account/security`、`/account/notifications` | `account_*.html`（帳號與密碼、通知分頁套 06 的 `/api/account/*`） |
| `GET /api/cart` | `{items:[{variant_id, product_id, title, variant_name, image, unit_price, qty, line_total, available, max_qty}], issues:[{variant_id, code}], subtotal, discount, shipping, shipping_gap_to_free, total, currency, coupon, count}` |
| `POST /api/cart/items {variant_id, qty}`、`PATCH /api/cart/items/:variant_id {qty}`、`DELETE /api/cart/items/:variant_id`、`POST /api/cart/coupon {code}`、`DELETE /api/cart/coupon` | 回完整購物車；qty 1–99 並夾在庫存內 |
| `GET /api/shop/shipping-methods`、`GET /api/shop/payment-methods` | 只列啟用的 |
| `POST /api/checkout` | 請求 `{email, phone, create_account, shipping:{method, name, postcode, city, address}, payment:{method}, invoice:{kind, carrier_code, tax_id, company_name, donation_code}, coupon_code, accept_terms, idempotency_key, website}`；201 `{order:{number, status, total, currency}, redirect_url, payment:{provider, method, kind:"redirect|offline|none", instructions}, access_token}` |
| `POST /api/checkout/retry {number, access, method}`、`GET /api/orders/:number/status`、`POST /api/orders/lookup {number, email, website}`（永遠 202 同一內容）、`POST /api/orders/:number/resend`（每小時 3 次） | |
| `POST /api/payment/:gateway/notify`、`GET /api/payment/:gateway/return` | 通知改狀態；回傳頁只顯示 |
| 會員：`GET /api/account/orders?status=&page=`、`GET /api/account/orders/:number`、`POST /api/account/orders/:number/cancel`、`POST /api/account/orders/:number/reorder`、`GET|POST /api/account/addresses`、`PUT|DELETE /api/account/addresses/:id`、`GET|POST /api/account/invoice-profiles`、`PUT|DELETE .../:id` | 只看自己的，別人的 404 |

### 管理 API（admin；GET 可用 06 的 `orders_read` 權杖範本）

| 路由 | 規則 |
|---|---|
| `GET /api/admin/products?status=&q=&page=&per_page=`、`POST|PUT` 以型別化輸入（`variants[]`、`categories[]`、`images[]`、`compare_at_price`…，忽略 `id`、`deleted_at`，價格庫存不可為負）、`POST /api/admin/products/:id/duplicate`、`/api/admin/product-categories` CRUD | |
| `GET /api/admin/orders?status=&q=&from=&to=&page=` → `{items, total, counts, kpi:{revenue_30d, orders_30d, pending, avg_order}}`、`GET /api/admin/orders/:id`（含 items、timeline、payment、customer.history_count）、`PUT /api/admin/orders/:id {status, carrier, tracking_no, note}`（狀態機，非法 409 `{allowed}`）、`POST .../notes`、`PATCH .../address`、`POST .../refund {amount?, reason, restock}`（可部分、冪等）、`POST .../mark-paid`（只限轉帳） | |
| `GET /api/admin/coupons?status=active|scheduled|ended&q=` → `{items, counts, impact:{orders, discount_total, revenue}}`、`POST`、`GET|PATCH|DELETE /:id`（用過的只能停用）、`POST /generate {prefix, size, template}`、`POST /preview {code, cart}` | |
| `GET|PUT /api/admin/shop/settings`（金鑰遮罩 `****1234`，部分金鑰不能啟用）、`POST /api/admin/shop/payment/:gateway/test` | GET 永不寫入 |

頁面：`web/admin/orders.html` 重寫、新增 `order_detail.html`、`coupons.html`、`settings.html` 的商店分頁、`products.html` 與 `product_editor.html` 補齊；側欄商店區加「折扣碼」（admin）。

### 選項鍵

`shop_currency`（TWD）、`shop_tax_mode`（included）、`shop_tax_rate`（5）、`shop_shipping_methods`（JSON：id、label、fee、free_over、eta_days、requires_address、enabled；預設宅配 80、自取 0）、`shop_free_shipping_over`、`shop_pickup_note`、`shop_stock_hold_minutes_card`（30）、`shop_stock_hold_hours_offline`（72）、`shop_failed_order_hold_hours`（24）、`shop_low_stock_default`（5）、`shop_order_prefix`（JL）、`shop_terms_page_id`、`shop_refund_window_days`（7）、`payment_methods`（method → provider）、既有 `payment_ecpay_*`、`payment_newebpay_*`、`payment_sandbox_enabled`、`shop_manual_transfer_note`、`shop_invoice_seller`、`shop_contact_email`。

### 通知（接 06 的 `internal/notify`）

`Recipients` 加 `Emails []string` 給訪客。站長：`order.new`（付款成功或離線下單）、`order.payment_failed`、`order.refunded`（新種類，偏好沿用付款失敗那一列）。顧客（不受偏好影響）：`order.confirmation`（附收據連結）、`order.shipped`、`order.lookup_link`、`order.offline_instructions`、`customer.set_password`。全部走 `notification_deliveries` 與 `Sender`。

## 我先決定了這些

- 金額以「分」儲存，顯示與金流換算集中在 `internal/money`；含稅價，收據列 5%。
- 金流先 ECPay＋銀行轉帳（人工確認）＋測試用沙盒；NewebPay 轉接器保留但本工作單只做到不會回假成功；LINE Pay 之後。
- 電子發票先只收資料、出可列印收據；`invoices` 表先建不接 API。
- 商品分類獨立於文章分類，管理介面放在分類與標籤頁的「商品分類」分段。
- 運送先做宅配與自取；超商取貨在畫面上顯示稍後開放，門市選擇器之後接。
- 庫存在下單時保留，依付款方式設定保留時間；OrderDetail 板的時間軸文案一併改。
- 沒勾建立帳號的訪客仍建立休眠的 `customer` 帳號串訂單；站長可在使用者頁刪除，訂單以快照保留、`user_id` 設空。
- 訪客的每人折扣次數只以小寫 Email 比對，不處理 Gmail 的點與加號。
- CSV 匯入匯出、數位下載、到貨通知、訂閱制留待後續。

## 分段實作與驗收

每段先由測試代理照本文件建立失敗案例（已隨本工作單交付），實作代理依契約實作，主代理審查後以真實伺服器（沙盒金流）與 Playwright 驗收。

1. E0 設計板：商品分類分段、設定的運送分頁與金流遮罩、折扣碼的買 X 送 Y 與側欄入口、OrderDetail 時間軸與訂單編號、ProductEditor 補欄位；canvas 發佈。
2. E1 金額、遷移、`customer` 角色、`EnsureCustomer`、`/admin` 員工閘（與 06 共用）。測試：`internal/money`、`internal/core/order_state_test.go`、`internal/data` 遷移重跑。
3. E2 後台型錄：商品型別化輸入、規格、分類、列表與編輯頁。測試檔 `products_admin_integration_test.go`。
4. E3 前台型錄：`/shop`、分類頁、商品頁、JSON-LD。測試檔 `shop_catalog_integration_test.go`。
5. E4 購物車與折扣碼：伺服器購物車、計價、`Evaluate`／`Redeem` 與併發限制。測試檔 `cart_integration_test.go`、`coupons_integration_test.go`。
6. E5 結帳與訂單：`POST /api/checkout`、庫存保留與逾期釋放、狀態機、訂單編號。測試檔 `checkout_integration_test.go`、`orders_state_integration_test.go`。
7. E6 付款：沙盒、轉帳、`Finalize`、ECPay 簽章驗證與測試向量、通知種類。測試檔 `payment_notify_integration_test.go`。
8. E7 會員中心與查詢：`/account/*`、查詢連結、地址與發票設定。測試檔 `account_orders_integration_test.go`、`order_lookup_integration_test.go`。
9. E8 後台訂單、折扣碼、設定頁與驗收：真實伺服器走完訪客沙盒購買 → 查詢連結 → 會員訂單列表 → 後台出貨與退款。
