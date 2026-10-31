package server

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

type shopAPIDocOperation struct {
	method, path, summary, description string
	security                           bool
	params                             []any
	body                               any
	contentType                        string
	responses                          map[string]any
}

func shopDocObject(required []string, properties map[string]any) map[string]any {
	result := map[string]any{"type": "object", "properties": properties}
	if required != nil {
		result["required"] = required
	}
	return result
}
func shopDocString(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}
func shopDocInt(description string, minimum any) map[string]any {
	s := map[string]any{"type": "integer", "format": "int64", "description": description}
	if minimum != nil {
		s["minimum"] = minimum
	}
	return s
}
func shopDocRef(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}
func shopDocParam(name, in, description string, schema any) map[string]any {
	return map[string]any{"name": name, "in": in, "required": in == "path", "description": description, "schema": schema}
}

func shopDocOperations() []shopAPIDocOperation {
	pathID := func(name string) any {
		return shopDocParam(name, "path", "資源識別值。", map[string]any{"type": "integer", "format": "int64", "minimum": 1})
	}
	strPath := func(name, desc string) any {
		return shopDocParam(name, "path", desc, map[string]any{"type": "string", "minLength": 1})
	}
	query := func(name, desc string, schema any) any { return shopDocParam(name, "query", desc, schema) }
	json := "application/json"
	form := "application/x-www-form-urlencoded"
	return []shopAPIDocOperation{
		{method: "GET", path: "/api/cart", summary: "取得目前購物車", description: "匿名購物車由 HttpOnly jelly_cart cookie 識別。重新依目前商品價格計價。sold-out 項目留在 items 但不計價。free_shipping_over 是全店門檻（分，0 停用）；requires_shipping 只計有貨且需配送的商品。coupon.free_shipping 與配送方式門檻也會影響 shipping。回傳 subtotal、discount、shipping、shipping_gap_to_free、total、tax_total、rounding_adjustment、currency、coupon、count。", responses: shopDocResponses("ShopCart", "購物車快照；所有金額為分。")},
		{method: "POST", path: "/api/cart/items", summary: "加入購物車品項", description: "JSON variant_id 必須為正整數，qty 為 1–99；伺服器以目前價格及庫存計算，數量會夾在庫存內。來源檢查失敗回 403；無效欄位回 400；庫存或品項限制回 409。", body: shopDocRef("CartItemInput"), contentType: json, responses: shopDocResponsesWithErrors("ShopCart", "更新後購物車。", "200", "400", "403", "404", "409", "429", "500")},
		{method: "PATCH", path: "/api/cart/items/{variant_id}", summary: "修改購物車數量", description: "以變體 ID 更新 qty（1–99），伺服器重新檢查庫存並計價。無效 ID 或欄位回 400/404，庫存衝突回 409。需同來源請求。", params: []any{pathID("variant_id")}, body: shopDocRef("CartQtyInput"), contentType: json, responses: shopDocResponses("ShopCart", "更新後購物車。")},
		{method: "DELETE", path: "/api/cart/items/{variant_id}", summary: "移除購物車品項", description: "由目前瀏覽器的購物車移除指定變體；需同來源請求。成功回 204，沒有回應本文。", params: []any{pathID("variant_id")}, responses: shopDocNoContent("204", "已移除，沒有回應本文。")},
		{method: "POST", path: "/api/cart/coupon", summary: "套用折扣碼", description: "接受 JSON code；折扣資格、使用次數、會員條件和範圍由伺服器檢查。無效折扣回 422 JSON 錯誤；不信任客戶端金額。需同來源請求；頻率限制回 429。", body: shopDocRef("CartCouponInput"), contentType: json, responses: shopDocResponsesWithErrors("ShopCart", "含折扣計算的購物車。", "200", "400", "409", "422", "429", "500")},
		{method: "DELETE", path: "/api/cart/coupon", summary: "移除折扣碼", description: "清除目前購物車折扣碼。成功回 204，沒有回應本文。", responses: shopDocNoContent("204", "已移除，沒有回應本文。")},
		{method: "GET", path: "/api/shop/shipping-methods", summary: "列出可用配送方式", description: "唯讀回傳目前啟用的配送選項及免運條件，不寫入設定。", responses: shopDocResponses("ShippingMethods", "啟用的配送方法陣列。")},
		{method: "GET", path: "/api/shop/payment-methods", summary: "列出可用付款方式", description: "唯讀回傳目前環境可用的付款方法；正式環境不提供沙盒方式。", responses: shopDocResponses("PaymentMethods", "啟用且可用的付款方法陣列。")},
		{method: "POST", path: "/api/checkout", summary: "建立訂單", description: "JSON 必填 email、phone、invoice、accept_terms、idempotency_key；shipping 與 payment 依商品、配送方式和總額條件判斷。website 為 honeypot。伺服器重新計價並鎖定庫存、原子保留庫存及套用折扣。總額為零時可省略付款方式；正額訂單須選有效的啟用付款方式。新訂單回 201；相同購物車憑證及冪等鍵重送會重播原回應並回 200；金額、購物車或鍵值不符回 409。需同來源；欄位錯誤 400、庫存衝突 409、頻率限制 429。", body: shopDocRef("CheckoutInput"), contentType: json, responses: shopCheckoutResponses()},
		{method: "POST", path: "/api/checkout/retry", summary: "重試尚未完成的付款", description: "JSON number、access、method。access 是訂單用途權杖；不接受只靠訂單編號授權。僅允許符合狀態與重試條件的訂單。Cookie 寫入要求同來源證明（Origin/Referer/Sec-Fetch-Site 或 X-Jelly-CSRF: 1）。失效權杖回 404，狀態衝突回 409。", body: shopDocRef("CheckoutRetryInput"), contentType: json, responses: shopDocResponses("CheckoutRetryResponse", "新的付款嘗試。")},
		{method: "POST", path: "/api/checkout/sandbox/{trade_no}", summary: "模擬沙盒付款結果", description: "僅在 payment_sandbox_enabled=true 且 APP_ENV 非 production 時可用。JSON status 只能是 paid、failed 或 pending；只供測試，不是真實付款。需同來源請求及有效交易權杖；無效交易回 404，錯誤狀態回 400。成功只回傳 redirect_url。", params: []any{strPath("trade_no", "20 碼英數 merchant_trade_no。")}, body: shopDocRef("SandboxResultInput"), contentType: json, responses: shopDocResponsesWithErrors("SandboxResult", "付款模擬結果。", "200", "400", "403", "404", "500")},
		{method: "GET", path: "/api/orders/{number}/status", summary: "查詢訂單狀態", description: "訂單編號本身不是授權。需有效 access/t 用途權杖或該訂單會員 session；無權限或不存在一律 404。只回傳狀態、訂單編號與付款狀態，不回訂單明細。", params: []any{strPath("number", "訂單編號。"), query("access", "訂單用途權杖。", map[string]any{"type": "string"}), query("t", "相容用訂單用途權杖。", map[string]any{"type": "string"})}, responses: shopDocResponses("OrderStatus", "訂單狀態摘要。")},
		{method: "POST", path: "/api/orders/lookup", summary: "寄送訂單查詢連結", description: "JSON number、email、website。對存在與不存在的配對均回相同 202 訊息；honeypot 不寄信。連結權杖短效且一次性。依 IP 與訂單節流，超限回 429。", body: shopDocRef("OrderLookupInput"), contentType: json, responses: shopDocResponses("AcceptedMessage", "固定 202 回覆。", "202")},
		{method: "POST", path: "/api/orders/{number}/resend", summary: "重寄訂單資料", description: "需有效訂單用途權杖或會員 session；訂單編號不構成授權。每筆訂單與來源 IP 每小時最多三次。需同來源請求。無權限回 404、超限回 429。", params: []any{strPath("number", "訂單編號。")}, body: shopDocRef("OrderResendInput"), contentType: json, responses: shopDocResponses("ResendAccepted", "受理回覆。")},
		{method: "POST", path: "/api/payment/{gateway}/notify", summary: "接收金流通知", description: "接受供應商 application/x-www-form-urlencoded 通知；共用 parser 也接受 JSON。伺服器依 gateway 驗證簽章、金額、幣別與交易，再以冪等交易更新訂單；不可由瀏覽器當作付款證明。", params: []any{strPath("gateway", "目前支援的已設定金流代碼。")}, body: map[string]any{"type": "object", "description": "金流供應商定義欄位；欄位名稱依供應商不同。", "additionalProperties": map[string]any{"type": "string"}}, contentType: form, responses: shopDocResponses("PaymentNotify", "金流協定要求的純文字 ACK。")},
		{method: "POST", path: "/api/payment/{gateway}/callback", summary: "金流通知相容路徑", description: "與 /notify 相同，接受 form-urlencoded 或 JSON 通知並驗證供應商簽章、金額與幣別後才更新狀態。", params: []any{strPath("gateway", "目前支援的已設定金流代碼。")}, body: map[string]any{"type": "object", "description": "金流供應商定義欄位；欄位名稱依供應商不同。", "additionalProperties": map[string]any{"type": "string"}}, contentType: form, responses: shopDocResponses("PaymentNotify", "金流協定要求的純文字 ACK。")},
		{method: "GET", path: "/api/payment/{gateway}/return", summary: "返回結帳完成頁", description: "依 MerchantTradeNo、merchant_trade_no 或 trade_no 查詢付款嘗試，找到時以 302 導向本站 /checkout/done/{number}。找不到交易或資料庫不可用時回空的 404／503；query 不會更新付款狀態。", params: []any{strPath("gateway", "路由參數；目前 handler 不依此值選擇供應商。")}, responses: map[string]any{"302": map[string]any{"description": "導向本站訂單完成頁。", "headers": map[string]any{"Location": map[string]any{"schema": map[string]any{"type": "string"}}}}, "404": shopDocNoContent("404", "找不到對應付款嘗試。")["404"], "503": shopDocNoContent("503", "資料庫不可用。")["503"]}},
		{method: "GET", path: "/api/account/orders", summary: "列出會員訂單", description: "需要有效會員登入；只列登入者自己的訂單。status 可篩選狀態，page 為頁碼。", security: true, params: []any{query("status", "訂單狀態篩選。", map[string]any{"type": "string"}), query("page", "頁碼。", map[string]any{"type": "integer", "minimum": 1})}, responses: shopDocResponses("MemberOrders", "本人訂單分頁及狀態計數。")},
		{method: "GET", path: "/api/account/orders/{number}", summary: "讀取會員訂單明細", description: "需要有效會員登入；只能讀取本人訂單，其他訂單回 404。", security: true, params: []any{strPath("number", "訂單編號。")}, responses: shopDocResponses("MemberOrder", "本人訂單與商品快照。")},
		{method: "POST", path: "/api/account/orders/{number}/cancel", summary: "取消尚可取消的訂單", description: "需要有效會員登入且只能操作本人訂單。依訂單狀態機檢查；不能取消時回 409。Cookie 寫入需同來源證明。", security: true, params: []any{strPath("number", "訂單編號。")}, responses: shopDocResponses("MemberCancel", "回傳 status: cancelled 或 refund_requested。")},
		{method: "POST", path: "/api/account/orders/{number}/reorder", summary: "將訂單商品加入購物車", description: "需要有效會員登入；只取本人歷史訂單商品，重新依目前商品與庫存計價，不能沿用歷史價格。Cookie 寫入需同來源證明。回傳 unavailable 變體 ID 陣列。", security: true, params: []any{strPath("number", "訂單編號。")}, responses: shopDocResponses("ReorderResult", "無法加入購物車的變體 ID。")},
		{method: "GET", path: "/api/account/addresses", summary: "列出會員地址", description: "需要有效會員登入，只回傳本人儲存的地址。", security: true, responses: shopDocResponses("AddressList", "本人地址清單。")},
		{method: "POST", path: "/api/account/addresses", summary: "新增會員地址", description: "需要有效會員登入。需提供地址欄位；同來源 Cookie 請求須通過 CSRF 檢查。", security: true, body: shopDocRef("AddressInput"), contentType: json, responses: shopDocResponses("Address", "新增的本人地址。", "201")},
		{method: "PUT", path: "/api/account/addresses/{id}", summary: "更新會員地址", description: "需要有效會員登入，只能更新本人地址；他人資源回 404。", security: true, params: []any{pathID("id")}, body: shopDocRef("AddressInput"), contentType: json, responses: shopDocResponses("Address", "更新後本人地址。")},
		{method: "DELETE", path: "/api/account/addresses/{id}", summary: "刪除會員地址", description: "需要有效會員登入，只能刪除本人地址；他人資源回 404。成功回 204，沒有回應本文。", security: true, params: []any{pathID("id")}, responses: shopDocNoContent("204", "已刪除，沒有回應本文。")},
		{method: "GET", path: "/api/account/invoice-profiles", summary: "列出會員發票設定", description: "需要有效會員登入，只回傳本人發票設定。", security: true, responses: shopDocResponses("InvoiceProfileList", "本人發票設定清單。")},
		{method: "POST", path: "/api/account/invoice-profiles", summary: "新增會員發票設定", description: "需要有效會員登入，發票類型與載具／統編欄位依規則驗證；Cookie 寫入需同來源證明。", security: true, body: shopDocRef("InvoiceProfileInput"), contentType: json, responses: shopDocResponses("InvoiceProfile", "新增的本人發票設定。", "201")},
		{method: "PUT", path: "/api/account/invoice-profiles/{id}", summary: "更新會員發票設定", description: "需要有效會員登入，只能更新本人設定；其他使用者資源回 404。", security: true, params: []any{pathID("id")}, body: shopDocRef("InvoiceProfileInput"), contentType: json, responses: shopDocResponses("InvoiceProfile", "更新後本人發票設定。")},
		{method: "DELETE", path: "/api/account/invoice-profiles/{id}", summary: "刪除會員發票設定", description: "需要有效會員登入，只能刪除本人設定；其他使用者資源回 404。成功回 204，沒有回應本文。", security: true, params: []any{pathID("id")}, responses: shopDocNoContent("204", "已刪除，沒有回應本文。")},
		shopAdminDoc("GET", "/api/admin/products", "列出商品", "ProductListQuery", "ProductList", "可用 status/q/page/per_page 篩選；需 admin 角色。"),
		shopAdminDoc("POST", "/api/admin/products", "建立商品", "ProductInput", "Product", "型別化商品、規格、價格與分類輸入；成功回 200。頂層 price 字串以元輸入，非負整數以分輸入；variants[] 存在時以規格的 SKU 與庫存為準。需 admin 角色。"),
		shopAdminDoc("GET", "/api/admin/products/{id}", "讀取商品", "", "Product", "需 admin 角色。"),
		shopAdminDoc("PUT", "/api/admin/products/{id}", "更新商品", "ProductInput", "Product", "只能更新指定商品；頂層 price 字串以元輸入、非負整數以分輸入；variants[] 存在時以規格的 SKU 與庫存為準。未提供 variants[] 時，legacy SKU/stock 只適用於單一空名稱預設規格，多規格或非預設規格回 409。需 admin 角色。"),
		shopAdminDoc("DELETE", "/api/admin/products/{id}", "刪除商品", "", "DeletedMessage", "軟刪除商品；訂單商品快照仍保留。需 admin 角色。"),
		shopAdminDoc("POST", "/api/admin/products/{id}/duplicate", "複製商品", "", "Product", "建立商品副本；需 admin 角色。"),
		shopAdminDoc("GET", "/api/admin/product-categories", "列出商品分類", "", "CategoryList", "需 admin 角色。"),
		shopAdminDoc("POST", "/api/admin/product-categories", "建立商品分類", "CategoryCreateInput", "Category", "需 admin 角色。"),
		shopAdminDoc("PUT", "/api/admin/product-categories/{id}", "更新商品分類", "CategoryUpdateInput", "Category", "需 admin 角色。"),
		shopAdminDoc("DELETE", "/api/admin/product-categories/{id}", "刪除商品分類", "", "DeletedMessage", "有子分類或商品使用時回 409；成功回 message=deleted。需 admin 角色。"),
		shopAdminDoc("GET", "/api/admin/orders", "列出訂單", "AdminOrderQuery", "AdminOrderList", "可依 status/q/email/date/from/to/page/per_page 篩選；需 admin 或 orders_read 權限。"),
		shopAdminDoc("GET", "/api/admin/orders/{id}", "讀取訂單明細", "", "AdminOrder", "回傳平鋪訂單欄位，並含 payment、customer、items、timeline、snapshot 與 allowed；需 admin 角色。"),
		shopAdminDoc("PUT", "/api/admin/orders/{id}", "更新訂單狀態與物流", "AdminOrderUpdate", "AdminMutationResult", "依訂單狀態機驗證；成功回 status=ok；需 admin 角色。"),
		shopAdminDoc("POST", "/api/admin/orders/{id}/notes", "新增訂單備註", "OrderNoteInput", "AdminMutationResult", "body 或 note 欄位可提供備註；成功回 status=ok；需 admin 角色。"),
		shopAdminDoc("PATCH", "/api/admin/orders/{id}/address", "更新訂單地址", "OrderAddressInput", "AdminMutationResult", "只接受 recipient、name、phone、postcode、city、address；成功回 status=ok；需 admin 角色。"),
		shopAdminDoc("POST", "/api/admin/orders/{id}/refund", "建立退款", "RefundInput", "AdminMutationResult", "amount 可省略或為正整數分；可用 Idempotency-Key 標頭或本文 idempotency_key；成功回 status=ok。"),
		shopAdminDoc("POST", "/api/admin/orders/{id}/mark-paid", "標記人工轉帳已付款", "", "AdminMutationResult", "沒有 JSON 本文；僅接受待付款銀行轉帳訂單；成功回 status=ok。"),
		shopAdminDoc("GET", "/api/admin/coupons", "列出折扣碼", "CouponListQuery", "CouponList", "可依 status/q 篩選；需 admin 角色。"),
		shopAdminDoc("POST", "/api/admin/coupons", "建立折扣碼", "CouponInput", "Coupon", "折扣金額保持分精度，百分比與使用限制由伺服器驗證；成功回 200；需 admin 角色。"),
		shopAdminDoc("GET", "/api/admin/coupons/{id}", "讀取折扣碼", "", "CouponDetail", "回傳 coupon 與 targets；需 admin 角色。"),
		shopAdminDoc("PATCH", "/api/admin/coupons/{id}", "更新折扣碼", "CouponInput", "Coupon", "已使用折扣碼只可停用；需 admin 角色。"),
		shopAdminDoc("DELETE", "/api/admin/coupons/{id}", "刪除折扣碼", "", "AdminMutationResult", "已使用折扣碼不可刪除；成功回 status=ok。需 admin 角色。"),
		shopAdminDoc("POST", "/api/admin/coupons/generate", "批次產生折扣碼", "CouponGenerateInput", "CouponGenerateResult", "需 admin 角色。"),
		shopAdminDoc("POST", "/api/admin/coupons/preview", "預覽折扣碼", "CouponPreviewInput", "CouponPreview", "只試算，不建立訂單或消耗次數；需 admin 角色。"),
		shopAdminDoc("GET", "/api/admin/shop/settings", "讀取商店設定", "", "ShopSettings", "敏感值只回遮罩內容，不回傳秘密原文；需 admin 角色。"),
		shopAdminDoc("PUT", "/api/admin/shop/settings", "更新商店設定", "ShopSettingsInput", "AdminMutationResult", "輸入欄位依各設定型別驗證，成功只回 status=ok；需 admin 角色。"),
		shopAdminDoc("POST", "/api/admin/shop/payment/{gateway}/test", "測試金流連線", "", "PaymentTest", "僅回報目前沒有安全的供應商連線測試端點，不會呼叫供應商或執行真實收款。需要 admin 角色。"),
	}
}

func shopAdminDoc(method, path, summary, request, response, description string) shopAPIDocOperation {
	op := shopAPIDocOperation{method: method, path: path, summary: summary, description: description, security: true, responses: shopDocResponses(response, description)}
	for _, part := range strings.Split(path, "/") {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
			if name == "id" {
				op.params = append(op.params, shopDocParam(name, "path", "資源識別值。", map[string]any{"type": "integer", "format": "int64", "minimum": 1}))
			} else {
				op.params = append(op.params, shopDocParam(name, "path", "資源識別值。", map[string]any{"type": "string", "minLength": 1}))
			}
		}
	}
	if request != "" && method == http.MethodGet {
		if schema, ok := shopAPISchemas()[request].(map[string]any); ok {
			if properties, ok := schema["properties"].(map[string]any); ok {
				for name, property := range properties {
					op.params = append(op.params, shopDocParam(name, "query", "選填篩選條件。", property))
				}
			}
		}
	} else if request != "" {
		op.body = shopDocRef(request)
		op.contentType = "application/json"
	}
	if code := shopAdminSuccessStatus(method, path); code != "" {
		op.responses = shopDocResponses(response, description, code)
	}
	op.responses["401"] = shopDocError("需要有效的管理員登入。")
	op.responses["403"] = shopDocError("目前帳號沒有執行此管理操作的權限。")
	op.responses["default"] = shopDocError("其他驗證或伺服器錯誤；回應本文使用 Error schema。")
	if method == http.MethodGet && path == "/api/admin/products" {
		op.responses["400"] = shopDocError("status、page 或 per_page 參數格式錯誤。")
	}
	if method == http.MethodPost && path == "/api/admin/products" {
		for _, status := range []string{"400", "409", "500"} {
			op.responses[status] = shopDocError("商品欄位驗證失敗；多規格商品需在 variants[] 提供 SKU 與庫存。")
		}
	}
	if method == http.MethodGet && path == "/api/admin/coupons/{id}" {
		op.responses["400"] = shopDocError("折扣碼 ID 格式錯誤。")
		op.responses["404"] = shopDocError("折扣碼不存在或 ID 無效。")
		op.responses["500"] = shopDocError("讀取折扣碼或適用目標失敗。")
	}
	if method == http.MethodPost && path == "/api/admin/coupons" {
		op.responses["400"] = shopDocError("JSON 格式或欄位驗證失敗。")
		op.responses["409"] = shopDocError("折扣碼重複。")
		op.responses["500"] = shopDocError("建立折扣碼失敗。")
	}
	if method == http.MethodPost && path == "/api/admin/product-categories" {
		for _, status := range []string{"400", "409", "500"} {
			op.responses[status] = shopDocError("分類欄位、網址代稱、父分類或資料庫驗證失敗。")
		}
	}
	if method == http.MethodPut && path == "/api/admin/product-categories/{id}" {
		for _, status := range []string{"400", "404", "409", "500"} {
			op.responses[status] = shopDocError("分類欄位、父分類循環或目標分類驗證失敗。")
		}
	}
	return op
}

func shopAdminSuccessStatus(method, path string) string {
	switch method + " " + path {
	case "POST /api/admin/products/{id}/duplicate", "POST /api/admin/product-categories", "POST /api/admin/coupons/generate":
		return "201"
	case "POST /api/admin/products":
		return "200"
	}
	return ""
}

func shopDocResponses(schema, description string, status ...string) map[string]any {
	code := "200"
	if len(status) > 0 {
		code = status[0]
	}
	success := map[string]any{"description": description, "content": map[string]any{"application/json": map[string]any{"schema": shopDocRef(schema)}}}
	if schema == "PaymentNotify" {
		success["content"] = map[string]any{"text/plain": map[string]any{"schema": map[string]any{"type": "string", "description": "供應商協定 ACK。"}}}
	}
	return map[string]any{code: success}
}
func shopDocResponsesWithErrors(schema, description string, successStatus string, errorStatuses ...string) map[string]any {
	responses := shopDocResponses(schema, description, successStatus)
	for _, status := range errorStatuses {
		responses[status] = shopDocError("此狀態碼由端點實際驗證流程回傳。")
	}
	return responses
}
func shopCheckoutResponses() map[string]any {
	responses := shopDocResponses("CheckoutResponse", "建立新訂單。", "201")
	responses["200"] = map[string]any{"description": "相同冪等鍵與購物車憑證的重播回應。", "content": map[string]any{"application/json": map[string]any{"schema": shopDocRef("CheckoutResponse")}}}
	for _, status := range []string{"400", "403", "404", "409", "422", "429", "500"} {
		responses[status] = shopDocError("此狀態碼由結帳驗證、授權、庫存或訂單建立流程實際回傳。")
	}
	return responses
}
func shopDocNoContent(status, description string) map[string]any {
	return map[string]any{status: map[string]any{"description": description}}
}
func shopDocError(description string) map[string]any {
	return map[string]any{"description": description, "content": map[string]any{"application/json": map[string]any{"schema": shopDocRef("Error")}}}
}

func shopSettingDocProperties() map[string]any {
	stringValue := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	integerValue := func(description string) map[string]any {
		return map[string]any{"type": "integer", "minimum": 0, "description": description}
	}
	booleanValue := func(description string) map[string]any {
		return map[string]any{"type": "boolean", "description": description}
	}
	props := map[string]any{
		"shop_currency": stringValue("商店幣別，目前限定 TWD。"), "shop_tax_mode": stringValue("含稅模式，目前限定 included。"), "shop_tax_rate": map[string]any{"type": "number", "minimum": 0, "maximum": 100},
		"shop_shipping_methods":   map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"id", "label", "fee"}, "properties": map[string]any{"id": stringValue("配送方式 ID。"), "label": stringValue("名稱。"), "fee": integerValue("運費，分。"), "free_over": integerValue("免運門檻，分。"), "eta_days": integerValue("預計天數。"), "requires_address": booleanValue("是否需要地址。"), "enabled": booleanValue("是否啟用。")}}},
		"shop_free_shipping_over": integerValue("全店免運門檻，分。"), "shop_pickup_note": stringValue("取貨說明。"), "shop_stock_hold_minutes_card": integerValue("信用卡庫存保留分鐘。"), "shop_stock_hold_hours_offline": integerValue("離線付款庫存保留小時。"), "shop_failed_order_hold_hours": integerValue("付款失敗訂單保留小時。"), "shop_low_stock_default": integerValue("預設低庫存門檻。"), "shop_order_prefix": stringValue("訂單編號前綴。"), "shop_terms_page_id": integerValue("條款頁面 ID。"), "shop_refund_window_days": integerValue("退款期限天數。"),
		"payment_methods": map[string]any{"type": "object", "description": "付款方法設定的字串 map。", "additionalProperties": stringValue("付款方法欄位值。")}, "payment_ecpay_merchant_id": stringValue("ECPay 商店代號。"), "payment_ecpay_hash_key": stringValue("ECPay 簽章金鑰。"), "payment_ecpay_hash_iv": stringValue("ECPay 簽章向量。"), "payment_ecpay_enabled": booleanValue("是否啟用 ECPay。"), "payment_ecpay_test_mode": booleanValue("是否使用 ECPay 測試環境。"),
		"payment_newebpay_merchant_id": stringValue("NewebPay 商店代號。"), "payment_newebpay_hash_key": stringValue("NewebPay 簽章金鑰。"), "payment_newebpay_hash_iv": stringValue("NewebPay 簽章向量。"), "payment_newebpay_enabled": booleanValue("是否啟用 NewebPay。"), "payment_newebpay_test_mode": booleanValue("是否使用 NewebPay 測試環境。"), "payment_sandbox_enabled": booleanValue("是否啟用沙盒付款。"), "payment_sandbox_secret": stringValue("沙盒簽章密鑰。"), "shop_manual_transfer_note": stringValue("人工轉帳說明。"), "shop_invoice_seller": stringValue("發票賣方資訊。"), "shop_contact_email": stringValue("商店聯絡信箱。"),
	}
	for _, key := range []string{"payment_ecpay_hash_key", "payment_ecpay_hash_iv", "payment_newebpay_hash_key", "payment_newebpay_hash_iv", "payment_sandbox_secret"} {
		property := props[key].(map[string]any)
		property["writeOnly"] = true
		property["description"] = "只接受寫入；讀取時回傳遮罩值。"
	}
	return props
}

func shopSettingReadDocProperties() map[string]any {
	input := shopSettingDocProperties()
	props := make(map[string]any, len(input))
	for key, schema := range input {
		inputSchema := schema.(map[string]any)
		description := "設定值以字串回傳。"
		switch inputSchema["type"] {
		case "boolean":
			description = "布林設定以 true 或 false 字串回傳。"
		case "integer", "number":
			description = "數值設定以十進位字串回傳。"
		case "array":
			description = "配送方式設定以 JSON 字串回傳。"
		case "object":
			description = "付款方式設定以 JSON 字串回傳。"
		}
		property := map[string]any{"type": "string", "description": description}
		if inputSchema["writeOnly"] == true {
			property["description"] = "秘密設定以遮罩字串回傳，不會包含秘密原文。"
			property["readOnly"] = true
		}
		props[key] = property
	}
	return props
}

func shopAPISchemas() map[string]any {
	obj := func(req []string, props map[string]any) any { return shopDocObject(req, props) }
	str := func(d string) any { return shopDocString(d) }
	int := func(d string, min any) any { return shopDocInt(d, min) }
	array := func(ref string) any { return map[string]any{"type": "array", "items": shopDocRef(ref)} }
	schemas := map[string]any{
		"Error":              obj(nil, map[string]any{"error": str("錯誤說明；部分狀態轉換衝突只回 allowed。"), "code": map[string]any{"oneOf": []any{map[string]any{"type": "integer", "description": "Echo HTTP 錯誤回應的狀態碼。"}, map[string]any{"type": "string", "description": "商店 API 錯誤代碼，例如 coupon。"}}}, "field": str("欄位名稱（如適用）。"), "allowed": map[string]any{"type": "array", "items": str("狀態轉換衝突時允許的狀態。")}}),
		"CartItemInput":      obj([]string{"variant_id", "qty"}, map[string]any{"variant_id": int("商品變體 ID。", 1), "qty": map[string]any{"type": "integer", "minimum": 1, "maximum": 99}}),
		"CartQtyInput":       obj([]string{"qty"}, map[string]any{"qty": map[string]any{"type": "integer", "minimum": 1, "maximum": 99}}),
		"CartCouponInput":    obj([]string{"code"}, map[string]any{"code": str("折扣碼。")}),
		"CartLine":           obj([]string{"variant_id", "product_id", "qty", "unit_price", "line_total", "available", "max_qty"}, map[string]any{"variant_id": int("變體 ID。", 1), "product_id": int("商品 ID。", 1), "product_slug": str("商品網址代稱。"), "qty": int("數量。", 1), "unit_price": int("單價，分。", 0), "line_total": int("列小計，分。", 0), "available": map[string]any{"type": "boolean"}, "max_qty": int("目前可購買上限。", 0), "title": str("商品名稱快照。"), "variant_name": str("規格名稱。"), "image": str("圖片站內路徑。")}),
		"CartIssue":          obj([]string{"code"}, map[string]any{"variant_id": map[string]any{"type": "integer", "minimum": 0, "description": "相關變體 ID；0 表示整個購物車的折扣碼問題。"}, "code": str("庫存或品項問題代碼。")}),
		"ShopCart":           obj([]string{"items", "issues", "subtotal", "discount", "shipping", "shipping_gap_to_free", "free_shipping_over", "requires_shipping", "total", "tax_total", "rounding_adjustment", "currency", "coupon", "count"}, map[string]any{"items": array("CartLine"), "issues": array("CartIssue"), "subtotal": int("商品小計，分。", 0), "discount": int("折扣金額，分。", 0), "shipping": int("運費，分。", 0), "shipping_gap_to_free": int("達免運所需金額，分。", 0), "free_shipping_over": int("全店免運門檻，分；0 表示停用。", 0), "requires_shipping": map[string]any{"type": "boolean", "description": "是否有有貨且需實體配送的商品。"}, "total": int("伺服器計算並四捨五入至整元的總額，分。", 0), "tax_total": int("含稅額，分。", 0), "rounding_adjustment": map[string]any{"type": "integer", "minimum": -49, "maximum": 50, "description": "整元四捨五入調整，分。"}, "currency": map[string]any{"type": "string", "example": "TWD"}, "coupon": map[string]any{"type": "object", "nullable": true, "properties": map[string]any{"code": str("折扣碼。"), "label": str("折扣說明。"), "free_shipping": map[string]any{"type": "boolean"}}}, "count": int("購物車品項數量合計。", 0)}),
		"ShippingMethods":    map[string]any{"type": "array", "description": "已啟用配送方式。", "items": obj([]string{"id", "label", "fee", "free_over", "eta_days", "requires_address", "enabled"}, map[string]any{"id": str("方法代碼。"), "label": str("顯示名稱。"), "fee": int("運費，分。", 0), "free_over": int("此方法免運門檻，分。", 0), "eta_days": map[string]any{"type": "integer", "minimum": 0}, "requires_address": map[string]any{"type": "boolean"}, "enabled": map[string]any{"type": "boolean"}})},
		"PaymentMethods":     map[string]any{"type": "array", "description": "已啟用且此環境可使用的付款方法。", "items": obj([]string{"id", "method", "provider", "kind"}, map[string]any{"id": str("設定項目 ID。"), "method": str("付款方法代碼。"), "provider": str("供應商代碼。"), "kind": str("redirect 或 offline。")})},
		"CheckoutInput":      obj([]string{"email", "phone", "invoice", "accept_terms", "idempotency_key"}, map[string]any{"email": map[string]any{"type": "string", "format": "email"}, "phone": str("聯絡手機。"), "create_account": map[string]any{"type": "boolean"}, "shipping": obj(nil, map[string]any{"method": str("配送方法；需配送商品需選擇。"), "name": str("收件人。"), "postcode": str("郵遞區號。"), "city": str("縣市。"), "address": str("地址。"), "store_id": str("門市代碼（供支援方法使用）。"), "store_name": str("門市名稱。")}), "payment": obj(nil, map[string]any{"method": str("正額訂單需啟用付款方法；總額為零可省略或使用任何值，伺服器會規範為 none。")}), "invoice": obj([]string{"kind"}, map[string]any{"kind": str("發票類型。"), "carrier_code": str("手機條碼。"), "tax_id": str("統一編號。"), "company_name": str("公司名稱。"), "donation_code": str("捐贈碼。")}), "coupon_code": str("折扣碼。"), "accept_terms": map[string]any{"type": "boolean", "enum": []bool{true}}, "idempotency_key": map[string]any{"type": "string", "minLength": 1, "maxLength": 64, "pattern": "^[A-Za-z0-9_-]+$"}, "website": str("honeypot，需留空。")}),
		"CheckoutResponse":   obj([]string{"order", "redirect_url", "payment", "access_token"}, map[string]any{"order": obj([]string{"number", "status", "total", "tax_total", "rounding_adjustment", "currency"}, map[string]any{"number": str("訂單編號。"), "status": str("訂單狀態。"), "total": int("總額，分。", 0), "tax_total": int("稅額，分。", 0), "rounding_adjustment": map[string]any{"type": "integer", "minimum": -49, "maximum": 50, "description": "整元四捨五入調整，分。"}, "currency": str("幣別。")}), "redirect_url": str("本站內付款或訂單完成路徑。"), "payment": obj([]string{"provider", "method", "kind", "instructions"}, map[string]any{"provider": str("金流供應商；零額為 none。"), "method": str("付款方法；零額為 none。"), "kind": map[string]any{"type": "string", "enum": []string{"redirect", "offline", "none"}, "description": "redirect 為導向付款、offline 為離線付款、none 為零額訂單。"}, "instructions": str("離線付款指示。")}), "access_token": str("該訂單用途權杖。")}),
		"CheckoutRetryInput": obj([]string{"number", "method"}, map[string]any{"number": str("訂單編號。"), "access": str("訂單用途權杖，非會員訂單必須提供。"), "method": str("目前啟用的付款方法。")}), "CheckoutRetryResponse": obj([]string{"redirect_url", "payment"}, map[string]any{"redirect_url": str("本站內付款或完成頁網址。"), "payment": obj([]string{"provider", "method", "kind", "instructions"}, map[string]any{"provider": str("供應商。"), "method": str("付款方式。"), "kind": str("redirect 或 offline。"), "instructions": str("離線付款指示。")})}), "SandboxResultInput": obj([]string{"status"}, map[string]any{"status": map[string]any{"type": "string", "enum": []string{"paid", "failed", "pending"}}}),
		"SandboxResult": obj([]string{"redirect_url"}, map[string]any{"redirect_url": str("本站訂單完成頁網址。")}), "OrderStatus": obj([]string{"status", "number", "payment_status"}, map[string]any{"status": str("訂單狀態。"), "number": str("訂單編號。"), "payment_status": str("付款狀態；零額無付款嘗試為 none。")}),
		"OrderLookupInput": obj([]string{"number", "email"}, map[string]any{"number": str("訂單編號。"), "email": map[string]any{"type": "string", "format": "email"}, "website": str("honeypot，需留空。")}), "AcceptedMessage": obj([]string{"message"}, map[string]any{"message": str("無論訂單是否存在均為相同內容。")}), "OrderResendInput": obj(nil, map[string]any{"access": str("訂單用途權杖。"), "email": map[string]any{"type": "string", "format": "email"}}), "ResendAccepted": obj([]string{"status"}, map[string]any{"status": map[string]any{"type": "string", "enum": []string{"ok"}}}), "PaymentNotify": map[string]any{"type": "string", "description": "金流供應商協定 ACK 純文字。"}, "PaymentReturn": obj(nil, map[string]any{"status": str("僅供顯示，不能作為付款證明。")}),
		"MemberCancel": obj([]string{"status"}, map[string]any{"status": map[string]any{"type": "string", "enum": []string{"cancelled", "refund_requested"}}}), "ReorderResult": obj([]string{"unavailable"}, map[string]any{"unavailable": map[string]any{"type": "array", "items": int("無法加入的變體 ID。", 1)}}),
		"MemberOrders": obj([]string{"items", "counts", "status", "page", "total_pages"}, map[string]any{"items": map[string]any{"type": "array", "items": shopDocRef("MemberOrderSummary")}, "counts": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer"}}, "status": str("目前狀態篩選；未提供時為空字串。"), "page": int("目前頁碼。", 1), "total_pages": int("總頁數。", 1)}), "MemberOrderSummary": obj([]string{"Number", "CreatedAt", "Status", "Total", "ItemCount", "Summary", "Images"}, map[string]any{"Number": str("訂單編號。"), "CreatedAt": str("依網站時區格式化的建立時間。"), "Status": str("狀態。"), "Total": int("總額，分。", 0), "ItemCount": int("訂單品項數。", 0), "Summary": str("最多三項商品名稱摘要。"), "Images": map[string]any{"type": "array", "items": str("商品圖片站內路徑，最多三張。")}}), "MemberOrder": obj([]string{"order"}, map[string]any{"order": map[string]any{"type": "object", "description": "shopOrderTemplateView 使用 TitleCase 欄名。", "properties": map[string]any{"Number": str("訂單編號。"), "Status": str("狀態。"), "Total": int("總額，分。", 0), "Subtotal": int("商品小計，分。", 0), "DiscountTotal": int("折扣，分。", nil), "ShippingTotal": int("運費，分。", nil), "TaxTotal": int("稅額，分。", nil), "RoundingAdjustment": int("整元調整，分。", nil), "Currency": str("幣別。"), "Items": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"Title": str("商品快照名稱。"), "VariantName": str("規格名稱。"), "SKU": str("SKU。"), "Image": str("圖片站內路徑。"), "ProductSlug": str("商品網址代稱。"), "UnitPrice": int("單價，分。", 0), "LineTotal": int("列總額，分。", 0), "Qty": int("數量。", 1)}}}}}}),
		"AddressInput": obj([]string{"recipient", "phone", "address"}, map[string]any{"label": str("地址標籤。"), "recipient": str("收件人。"), "phone": str("聯絡電話。"), "postcode": str("郵遞區號。"), "city": str("縣市。"), "address": str("地址。"), "is_default": map[string]any{"type": "boolean"}}), "Address": obj([]string{"id", "user_id", "recipient", "phone", "postcode", "city", "address", "is_default"}, map[string]any{"id": int("地址 ID。", 1), "user_id": int("會員 ID。", 1), "label": str("地址標籤。"), "recipient": str("收件人。"), "phone": str("電話。"), "postcode": str("郵遞區號。"), "city": str("縣市。"), "address": str("地址。"), "is_default": map[string]any{"type": "boolean"}, "created_at": map[string]any{"type": "string", "format": "date-time"}, "updated_at": map[string]any{"type": "string", "format": "date-time"}}), "AddressList": map[string]any{"type": "array", "items": shopDocRef("Address")}, "InvoiceProfileInput": obj([]string{"kind"}, map[string]any{"kind": str("none、personal_carrier、tax_id 或 donation。"), "carrier_code": str("手機條碼。"), "tax_id": str("統一編號。"), "company_name": str("公司名稱。"), "donation_code": str("捐贈碼。"), "is_default": map[string]any{"type": "boolean"}}), "InvoiceProfile": obj([]string{"id", "kind"}, map[string]any{"id": int("設定 ID。", 1), "kind": str("發票類型。"), "carrier_code": str("手機條碼。"), "tax_id": str("統編。"), "company_name": str("公司名稱。"), "donation_code": str("捐贈碼。")}), "InvoiceProfileList": map[string]any{"type": "array", "items": shopDocRef("InvoiceProfile")}, "DeletedMessage": obj([]string{"message"}, map[string]any{"message": map[string]any{"type": "string", "enum": []string{"deleted"}}}),
		"Product": obj([]string{"id", "title", "slug", "price", "status"}, map[string]any{"id": int("商品 ID。", 1), "title": str("商品名稱。"), "slug": str("網址代稱。"), "description": str("商品描述。"), "price": int("商品價格，分。", 0), "sku": str("SKU。"), "stock": int("庫存數量。", 0), "images": map[string]any{"type": "array", "items": str("站內圖片路徑。")}, "status": str("狀態。"), "compare_at_price": int("比較價格，分。", 0), "low_stock_threshold": int("低庫存門檻。", 0), "requires_shipping": map[string]any{"type": "boolean"}, "cold_chain": map[string]any{"type": "boolean"}, "attributes": map[string]any{"type": "object", "additionalProperties": str("商品屬性。")}, "related_ids": map[string]any{"type": "array", "items": int("相關商品 ID。", 1)}, "sold_count": int("銷售數量。", 0), "seo_title": str("SEO 標題。"), "seo_description": str("SEO 說明。"), "kind": str("physical 或 digital。"), "variants": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"id": int("規格 ID。", 1), "product_id": int("商品 ID。", 1), "name": str("規格名稱。"), "sku": str("SKU。"), "price": int("規格價格，分。", 0), "compare_at_price": int("比較價格，分。", 0), "stock": int("庫存。", 0), "position": int("排序位置。", 0), "status": str("狀態。")}}}, "categories": map[string]any{"type": "array", "items": int("分類 ID。", 1)}}), "ProductInput": obj([]string{"title"}, map[string]any{"title": str("商品名稱。"), "slug": str("網址代稱。"), "description": str("商品描述。"), "price": map[string]any{"oneOf": []any{str("元字串，轉換為分。"), int("非負整數分。", 0)}, "nullable": true, "description": "字串以元輸入；非負十進位整數以分輸入。null 沿用空值語意。"}, "sku": str("頂層相容欄位；variants[] 存在時以規格 SKU 為準。"), "stock": int("非負整數庫存；variants[] 存在時以規格庫存為準。", 0), "kind": str("physical 或 digital。"), "status": str("商品狀態。"), "compare_at_price": str("元字串。"), "low_stock_threshold": int("低庫存門檻。", 0), "requires_shipping": map[string]any{"type": "boolean"}, "cold_chain": map[string]any{"type": "boolean"}, "images": map[string]any{"type": "array", "items": str("站內圖片路徑。")}, "categories": map[string]any{"type": "array", "items": int("分類 ID。", 1)}, "variants": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"id": int("規格 ID。", 0), "name": str("規格名稱。"), "sku": str("SKU。"), "price": str("元字串。"), "compare_at_price": str("元字串。"), "stock": int("庫存。", 0), "position": int("排序位置。", 0), "status": str("狀態。")}}}, "attributes": map[string]any{"type": "object", "additionalProperties": str("屬性字串。")}, "related_ids": map[string]any{"type": "array", "items": int("相關商品 ID。", 1)}, "seo_title": str("SEO 標題。"), "seo_description": str("SEO 說明。")}), "ProductListQuery": obj(nil, map[string]any{"status": str("狀態篩選。"), "q": str("搜尋字串。"), "page": int("頁碼。", 1), "per_page": int("每頁數量。", 1)}), "ProductList": obj([]string{"items", "total"}, map[string]any{"items": map[string]any{"type": "array", "items": shopDocRef("Product")}, "total": int("符合筆數。", 0)}),
		"Category":            obj([]string{"id", "name", "slug"}, map[string]any{"id": int("分類 ID。", 1), "name": str("名稱。"), "slug": str("網址代稱。"), "parent_id": map[string]any{"type": "integer", "minimum": 1, "nullable": true, "description": "父分類 ID；根分類為 null。"}, "description": str("分類說明。"), "cover": str("站內封面路徑。"), "position": int("排序位置。", 0), "hidden": map[string]any{"type": "boolean"}}),
		"CategoryCreateInput": obj([]string{"name"}, map[string]any{"name": str("分類名稱，必填。"), "slug": str("網址代稱；省略時由名稱產生。"), "parent_id": map[string]any{"type": "integer", "minimum": 1, "nullable": true, "description": "父分類 ID；null 建立根分類。"}, "description": str("分類說明。"), "cover": str("站內封面路徑。"), "position": int("排序位置，不得為負。", 0), "hidden": map[string]any{"type": "boolean"}}),
		"CategoryUpdateInput": obj(nil, map[string]any{"name": str("分類名稱；省略時保留原值。"), "slug": str("網址代稱；省略時保留原值。"), "parent_id": map[string]any{"type": "integer", "minimum": 1, "nullable": true, "description": "父分類 ID；null 設為根分類，省略時保留原值。"}, "description": str("分類說明。"), "cover": str("站內封面路徑。"), "position": int("排序位置，不得為負。", 0), "hidden": map[string]any{"type": "boolean"}}),
		"CategoryInput":       obj([]string{"name", "slug"}, map[string]any{"name": str("分類名稱。"), "slug": str("網址代稱。"), "parent_id": map[string]any{"type": "integer", "minimum": 1, "nullable": true}}), "CategoryList": map[string]any{"type": "array", "items": shopDocRef("Category")},
		"AdminOrderList": obj([]string{"items", "total", "counts", "kpi"}, map[string]any{"items": map[string]any{"type": "array", "items": shopDocRef("AdminOrderSummary")}, "total": int("總筆數。", 0), "counts": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer"}}, "kpi": map[string]any{"type": "object", "properties": map[string]any{"revenue_30d": int("近 30 日營收，分。", 0), "orders_30d": int("近 30 日訂單數。", 0), "pending": int("待處理數。", 0), "avg_order": int("平均訂單金額，分。", 0)}}}), "AdminOrder": obj([]string{"id", "number", "status", "total_amount", "items", "payment", "customer", "timeline", "snapshot", "allowed"}, map[string]any{"id": int("訂單 ID。", 1), "number": str("訂單編號。"), "status": str("狀態。"), "email": str("買家信箱。"), "phone": str("買家電話。"), "currency": str("幣別。"), "subtotal": int("小計，分。", 0), "discount_total": int("折扣，分。", 0), "shipping_total": int("運費，分。", 0), "tax_total": int("含稅額，分。", 0), "rounding_adjustment": int("整元調整，分。", nil), "total_amount": int("總額，分。", 0), "total": int("總額別名，分。", 0), "payment": map[string]any{"type": "object", "nullable": true}, "customer": map[string]any{"type": "object"}, "items": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}, "timeline": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}, "snapshot": map[string]any{"type": "object"}, "allowed": map[string]any{"type": "array", "items": str("允許狀態。")}}), "AdminOrderQuery": obj(nil, map[string]any{"status": str("訂單狀態或 all。"), "q": str("訂單編號、信箱、電話或配送資料。"), "email": str("買家信箱模糊搜尋。"), "date": str("單日 YYYY-MM-DD；未提供 from/to 時使用。"), "from": str("起始日期 YYYY-MM-DD。"), "to": str("結束日期 YYYY-MM-DD。"), "page": int("頁碼，預設 1。", 1), "per_page": int("每頁筆數，預設 20、上限 100。", 1)}), "AdminOrderSummary": obj(nil, map[string]any{"id": int("訂單 ID。", 1), "number": str("訂單編號。"), "email": str("信箱。"), "status": str("狀態。"), "total_amount": int("總額，分。", 0), "total": int("總額別名，分。", 0), "currency": str("幣別。"), "payment_provider": str("金流供應商。"), "payment_method": str("付款方式。"), "shipping": map[string]any{"type": "object"}, "items": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}, "created_at": map[string]any{"type": "string", "format": "date-time"}}), "AdminMutationResult": obj([]string{"status"}, map[string]any{"status": map[string]any{"type": "string", "enum": []string{"ok"}}}), "AdminOrderUpdate": obj([]string{"status"}, map[string]any{"status": str("目標狀態。"), "carrier": str("物流商。"), "tracking_no": str("追蹤碼。"), "note": str("內部備註。")}), "OrderNoteInput": obj(nil, map[string]any{"body": str("備註內容，優先使用。"), "note": str("相容備註欄位。")}), "OrderAddressInput": obj(nil, map[string]any{"recipient": str("收件人。"), "name": str("相容收件人。"), "phone": str("電話。"), "postcode": str("郵遞區號。"), "city": str("縣市。"), "address": str("地址。")}), "RefundInput": obj([]string{"reason"}, map[string]any{"amount": int("部分退款金額，分；省略為可退款餘額。", 1), "reason": str("退款原因。"), "restock": map[string]any{"type": "boolean"}, "idempotency_key": str("冪等鍵。")}),
		"Coupon": obj([]string{"id", "code", "kind", "value", "status"}, map[string]any{"id": int("折扣 ID。", 1), "code": str("折扣碼。"), "label": str("折扣說明。"), "kind": str("percent、fixed、free_shipping 或 bogo。"), "value": int("百分比或固定折扣分，依種類解讀。", 0), "buy_qty": int("BOGO 購買數量。", 0), "get_qty": int("BOGO 贈送數量。", 0), "min_subtotal": int("最低小計，分。", 0), "scope": str("all、category 或 product。"), "usage_limit": map[string]any{"type": "integer", "nullable": true}, "used_count": int("已使用次數。", 0), "per_user_limit": map[string]any{"type": "integer", "nullable": true}, "starts_at": map[string]any{"type": "string", "format": "date-time", "nullable": true}, "ends_at": map[string]any{"type": "string", "format": "date-time", "nullable": true}, "stackable_with_free_shipping": map[string]any{"type": "boolean"}, "members_only": map[string]any{"type": "boolean"}, "batch_id": map[string]any{"type": "integer", "nullable": true}, "bound_email": str("綁定信箱。"), "status": str("折扣碼狀態。"), "targets": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"target_type": str("category 或 product。"), "target_id": int("分類或商品 ID。", 1)}}}}), "CouponInput": obj([]string{"code", "kind", "value"}, map[string]any{"code": str("折扣碼。"), "label": str("折扣說明。"), "kind": str("percent、fixed、free_shipping 或 bogo。"), "value": int("折扣值，分或百分比。", 0), "buy_qty": int("BOGO 購買數量。", 0), "get_qty": int("BOGO 贈送數量。", 0), "min_subtotal": int("最低小計，分。", 0), "scope": str("all、category 或 product。"), "usage_limit": map[string]any{"type": "integer", "nullable": true}, "per_user_limit": map[string]any{"type": "integer", "nullable": true}, "starts_at": map[string]any{"type": "string", "format": "date-time", "nullable": true}, "ends_at": map[string]any{"type": "string", "format": "date-time", "nullable": true}, "stackable_with_free_shipping": map[string]any{"type": "boolean"}, "members_only": map[string]any{"type": "boolean"}, "bound_email": str("綁定信箱。"), "status": str("折扣碼狀態。"), "targets": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"type": str("category 或 product。"), "target_type": str("相容的目標種類。"), "target_id": int("目標 ID。", 1)}}}}), "CouponListQuery": obj(nil, map[string]any{"status": str("狀態篩選。"), "q": str("搜尋字串。")}), "CouponList": obj([]string{"items", "counts", "impact"}, map[string]any{"items": map[string]any{"type": "array", "items": shopDocRef("Coupon")}, "counts": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer"}}, "impact": map[string]any{"type": "object", "properties": map[string]any{"orders": int("使用訂單數。", 0), "discount_total": int("折扣合計，分。", 0), "revenue": int("營收，分。", 0)}}}), "CouponGenerateInput": obj([]string{"prefix", "size", "template"}, map[string]any{"prefix": str("代碼前綴。"), "size": int("產生數量。", 1), "template": map[string]any{"type": "object", "description": "折扣欄位範本物件。", "additionalProperties": true}}), "CouponGenerateResult": obj([]string{"batch_id", "count", "codes"}, map[string]any{"batch_id": int("折扣批次 ID。", 1), "count": int("建立數量。", 0), "codes": map[string]any{"type": "array", "items": str("已建立代碼。")}}), "CouponPreviewInput": obj([]string{"code", "cart"}, map[string]any{"code": str("折扣碼。"), "cart": obj([]string{"subtotal", "qty"}, map[string]any{"subtotal": int("商品小計，分。", 0), "qty": int("商品數量，1–10000。", 1), "email": str("買家信箱。"), "shipping": int("運費，分。", 0), "shipping_fee": int("相容運費欄位，分。", 0)})}), "CouponPreview": obj([]string{"amount", "discount", "free_shipping", "free_units", "label", "shipping", "total"}, map[string]any{"amount": int("折扣金額，分。", 0), "discount": int("折扣金額別名，分。", 0), "free_shipping": map[string]any{"type": "boolean"}, "free_units": int("BOGO 贈品件數。", 0), "label": str("折扣說明。"), "shipping": int("折扣後運費，分。", 0), "total": int("折扣後總額，分。", 0)}),
		"ShopSettings": obj(nil, shopSettingReadDocProperties()), "ShopSettingsInput": map[string]any{"type": "object", "properties": shopSettingDocProperties(), "additionalProperties": false}, "PaymentTest": obj([]string{"ok", "message", "checked_at"}, map[string]any{"ok": map[string]any{"type": "boolean", "enum": []bool{false}}, "message": str("目前沒有安全的供應商測試端點。"), "checked_at": map[string]any{"type": "string", "format": "date-time"}}),
	}
	schemas["CouponDetail"] = obj([]string{"coupon", "targets"}, map[string]any{
		"coupon":  shopDocRef("Coupon"),
		"targets": map[string]any{"type": "array", "items": obj([]string{"target_type", "target_id"}, map[string]any{"target_type": str("category 或 product。"), "target_id": int("分類或商品 ID。", 1)})},
	})
	return schemas
}

func shopDocOperationOpenAPI(op shopAPIDocOperation) map[string]any {
	result := map[string]any{"summary": op.summary, "description": op.description, "responses": op.responses}
	if op.security {
		result["security"] = []any{map[string]any{"BearerAuth": []any{}}, map[string]any{"JWTTokenCookie": []any{}}, map[string]any{"SessionCookie": []any{}}}
	}
	if len(op.params) > 0 {
		result["parameters"] = op.params
	}
	if op.body != nil {
		ct := op.contentType
		if ct == "" {
			ct = "application/json"
		}
		result["requestBody"] = map[string]any{"required": true, "content": map[string]any{ct: map[string]any{"schema": op.body}}}
	}
	return result
}
func shopDocOpenAPI() map[string]any {
	paths := map[string]any{}
	for _, op := range shopDocOperations() {
		item, _ := paths[op.path].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[op.path] = item
		}
		item[strings.ToLower(op.method)] = shopDocOperationOpenAPI(op)
	}
	return map[string]any{"openapi": "3.0.3", "info": map[string]any{"title": "Jelly CMS Shop API", "version": "1.0.0", "description": "商店、購物車、結帳、訂單、會員與管理 API 契約。金額 API 欄位均為分；管理介面商品價格輸入為元字串，讀取商品 price 則是分。瀏覽器 Cookie 寫入須符合同來源／CSRF 保護；金流 callback 需驗證簽章。文件不包含任何金鑰值。"}, "servers": []any{map[string]any{"url": "/", "description": "目前站台"}}, "paths": paths, "components": map[string]any{"securitySchemes": map[string]any{"BearerAuth": map[string]any{"type": "http", "scheme": "bearer", "bearerFormat": "JWT"}, "JWTTokenCookie": map[string]any{"type": "apiKey", "in": "cookie", "name": "jwt_token"}, "SessionCookie": map[string]any{"type": "apiKey", "in": "cookie", "name": "jelly-cms-session"}}, "schemas": shopAPISchemas()}}
}

func shopAPIOperationURL(method, path string) string {
	escaped := strings.ReplaceAll(path, "~", "~0")
	escaped = strings.ReplaceAll(escaped, "/", "~1")
	return "#/paths/" + escaped + "/" + strings.ToLower(method)
}
func shopAPIOverview(c echo.Context) error {
	ops := shopDocOperations()
	out := make([]map[string]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, map[string]string{"method": op.method, "path": op.path, "summary": op.summary, "documentation_url": shopAPIOperationURL(op.method, op.path)})
	}
	return c.JSON(http.StatusOK, map[string]any{"openapi_url": "/api/shop/openapi.json", "operations": out})
}
func shopAPIOpenAPI(c echo.Context) error { return c.JSON(http.StatusOK, shopDocOpenAPI()) }
