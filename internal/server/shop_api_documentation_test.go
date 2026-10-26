package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

const (
	shopAPIOverviewPath = "/api/shop"
	shopAPIOpenAPIPath  = "/api/shop/openapi.json"
)

type shopDocumentedOperation struct {
	Method           string `json:"method"`
	Path             string `json:"path"`
	Summary          string `json:"summary"`
	DocumentationURL string `json:"documentation_url"`
}

type shopDocumentationHarness struct {
	echo *echo.Echo
}

// This fixture registers only route owners needed by the shop API contract.
// It avoids opening a database while still deriving coverage from Echo's actual routes.
func newShopDocumentationHarness() *shopDocumentationHarness {
	e := echo.New()
	auth.RegisterRoutes(e)
	RegisterPaymentRoutes(e)
	RegisterEcommerceRoutes(e)
	registerShopRoutes(e, nil)
	registerShopAdminRoutes(e, func(c echo.Context, _, _ string) error { return c.NoContent(http.StatusOK) })
	return &shopDocumentationHarness{echo: e}
}

func (h *shopDocumentationHarness) get(path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return rec
}

func shopExpectedDocumentedOperations() []string {
	return []string{
		"GET /api/cart", "POST /api/cart/items", "PATCH /api/cart/items/{variant_id}", "DELETE /api/cart/items/{variant_id}",
		"POST /api/cart/coupon", "DELETE /api/cart/coupon", "GET /api/shop/shipping-methods", "GET /api/shop/payment-methods",
		"POST /api/checkout", "POST /api/checkout/retry", "POST /api/checkout/sandbox/{trade_no}",
		"GET /api/orders/{number}/status", "POST /api/orders/lookup", "POST /api/orders/{number}/resend",
		"POST /api/payment/{gateway}/notify", "POST /api/payment/{gateway}/callback", "GET /api/payment/{gateway}/return",
		"GET /api/account/orders", "GET /api/account/orders/{number}", "POST /api/account/orders/{number}/cancel", "POST /api/account/orders/{number}/reorder",
		"GET /api/account/addresses", "POST /api/account/addresses", "PUT /api/account/addresses/{id}", "DELETE /api/account/addresses/{id}",
		"GET /api/account/invoice-profiles", "POST /api/account/invoice-profiles", "PUT /api/account/invoice-profiles/{id}", "DELETE /api/account/invoice-profiles/{id}",
		"GET /api/admin/products", "POST /api/admin/products", "GET /api/admin/products/{id}", "PUT /api/admin/products/{id}", "DELETE /api/admin/products/{id}", "POST /api/admin/products/{id}/duplicate",
		"GET /api/admin/product-categories", "POST /api/admin/product-categories", "PUT /api/admin/product-categories/{id}", "DELETE /api/admin/product-categories/{id}",
		"GET /api/admin/orders", "GET /api/admin/orders/{id}", "PUT /api/admin/orders/{id}", "POST /api/admin/orders/{id}/notes", "PATCH /api/admin/orders/{id}/address", "POST /api/admin/orders/{id}/refund", "POST /api/admin/orders/{id}/mark-paid",
		"GET /api/admin/coupons", "POST /api/admin/coupons", "GET /api/admin/coupons/{id}", "PATCH /api/admin/coupons/{id}", "DELETE /api/admin/coupons/{id}", "POST /api/admin/coupons/generate", "POST /api/admin/coupons/preview",
		"GET /api/admin/shop/settings", "PUT /api/admin/shop/settings", "POST /api/admin/shop/payment/{gateway}/test",
	}
}

func shopRouteOperationKey(method, path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, ":") {
			segments[i] = "{" + strings.TrimPrefix(segment, ":") + "}"
		}
	}
	return strings.ToUpper(method) + " " + strings.Join(segments, "/")
}

func shopExpectedOpenAPIOperationURL(method, path string) string {
	escaped := strings.ReplaceAll(path, "~", "~0")
	escaped = strings.ReplaceAll(escaped, "/", "~1")
	return "#/paths/" + escaped + "/" + strings.ToLower(method)
}

func shopDocJSONTag(field reflect.StructField) string {
	name := strings.Split(field.Tag.Get("json"), ",")[0]
	if name == "" {
		return field.Name
	}
	return name
}

func shopDocObjectProperties(t *testing.T, schemas map[string]any, name string) map[string]any {
	t.Helper()
	schema, _ := schemas[name].(map[string]any)
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		t.Fatalf("schema %s has no properties object", name)
	}
	return properties
}

func shopDocResponseSchema(t *testing.T, operation map[string]any, status string, schemas map[string]any) map[string]any {
	t.Helper()
	responses, _ := operation["responses"].(map[string]any)
	response, _ := responses[status].(map[string]any)
	content, _ := response["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	schema, _ := media["schema"].(map[string]any)
	if ref, ok := schema["$ref"].(string); ok && strings.HasPrefix(ref, "#/components/schemas/") {
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		resolved, _ := schemas[name].(map[string]any)
		if resolved == nil {
			t.Fatalf("response references missing schema %q", name)
		}
		return resolved
	}
	if schema == nil {
		t.Fatalf("response %s has no JSON schema", status)
	}
	return schema
}

func shopDocContainsInvalidOpenAPI30Null(node any) bool {
	switch value := node.(type) {
	case map[string]any:
		if value["type"] == "null" {
			return true
		}
		for key, child := range value {
			if key == "required" && child == nil {
				return true
			}
			if shopDocContainsInvalidOpenAPI30Null(child) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if shopDocContainsInvalidOpenAPI30Null(child) {
				return true
			}
		}
	}
	return false
}

func containsAny(values []any, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func shopDocJSONKind(kind reflect.Kind) string {
	switch kind {
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.String:
		return "string"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	default:
		return ""
	}
}

func shopDocAssertReflectField(t *testing.T, properties map[string]any, source reflect.Type, fieldName string) {
	t.Helper()
	field, ok := source.FieldByName(fieldName)
	if !ok {
		t.Fatalf("source type %s has no field %s", source, fieldName)
	}
	propertyName := shopDocJSONTag(field)
	property, _ := properties[propertyName].(map[string]any)
	if property == nil {
		t.Errorf("OpenAPI schema omitted source field %s.%s (JSON %q)", source, fieldName, propertyName)
		return
	}
	wantType := shopDocJSONKind(field.Type.Kind())
	if property["type"] != wantType {
		t.Errorf("schema field %q type=%v, want %q from %s.%s", propertyName, property["type"], wantType, source, fieldName)
	}
}

func assertProductSchema(t *testing.T, schemas map[string]any) {
	t.Helper()
	input := shopDocObjectProperties(t, schemas, "ProductInput")
	output := shopDocObjectProperties(t, schemas, "Product")
	shopDocAssertReflectField(t, output, reflect.TypeOf(core.Product{}), "Price")
	inputPrice, _ := input["price"].(map[string]any)
	outputPrice, _ := output["price"].(map[string]any)
	priceChoices, _ := inputPrice["oneOf"].([]any)
	if len(priceChoices) != 2 {
		t.Errorf("ProductInput.price must have exactly string-dollar and integer-cent choices, got %v", inputPrice["oneOf"])
	}
	var stringPrice, integerPrice map[string]any
	for _, raw := range priceChoices {
		choice, _ := raw.(map[string]any)
		switch choice["type"] {
		case "string":
			stringPrice = choice
		case "integer":
			integerPrice = choice
		default:
			t.Errorf("ProductInput.price has unsupported oneOf branch %v", choice)
		}
	}
	if stringPrice == nil || integerPrice == nil {
		t.Errorf("ProductInput.price must include one string and one integer branch, got %v", priceChoices)
	}
	if inputPrice["nullable"] != true {
		t.Errorf("ProductInput.price nullable=%v, want true to preserve null-as-empty semantics", inputPrice["nullable"])
	}
	if integerPrice != nil {
		if integerPrice["minimum"] != float64(0) || integerPrice["format"] != "int64" {
			t.Errorf("ProductInput.price integer branch must be non-negative int64, got %v", integerPrice)
		}
		if description, _ := integerPrice["description"].(string); !strings.Contains(description, "分") {
			t.Errorf("ProductInput.price integer branch must describe its minor-unit input, got %q", description)
		}
	}
	if stringPrice != nil {
		if description, _ := stringPrice["description"].(string); !strings.Contains(description, "元") {
			t.Errorf("ProductInput.price string branch must describe its major-unit input, got %q", description)
		}
	}
	if description, _ := inputPrice["description"].(string); !strings.Contains(description, "元") || !strings.Contains(description, "分") {
		t.Errorf("ProductInput.price description must explain both input units, got %q", description)
	}
	if outputPrice["type"] != "integer" {
		t.Errorf("Product.price response schema type=%v, want integer minor units", outputPrice["type"])
	}
	if description, _ := outputPrice["description"].(string); !strings.Contains(description, "分") {
		t.Errorf("Product.price response description must identify minor units, got %q", description)
	}
	comparePrice, _ := input["compare_at_price"].(map[string]any)
	compareDescription, _ := comparePrice["description"].(string)
	if comparePrice["type"] != "string" || !strings.Contains(compareDescription, "元") {
		t.Errorf("ProductInput.compare_at_price must remain a major-unit string, got %v", comparePrice)
	}
	variants, _ := input["variants"].(map[string]any)
	variantItems, _ := variants["items"].(map[string]any)
	variantProperties, _ := variantItems["properties"].(map[string]any)
	for _, field := range []string{"price", "compare_at_price"} {
		property, _ := variantProperties[field].(map[string]any)
		description, _ := property["description"].(string)
		if property["type"] != "string" || !strings.Contains(description, "元") {
			t.Errorf("ProductInput.variants.%s must remain a major-unit string, got %v", field, property)
		}
	}
}

func assertAddressSchema(t *testing.T, schemas map[string]any) {
	t.Helper()
	input := shopDocObjectProperties(t, schemas, "AddressInput")
	output := shopDocObjectProperties(t, schemas, "Address")
	for _, field := range []string{"Label", "Recipient", "Phone", "Postcode", "City", "Address", "IsDefault"} {
		shopDocAssertReflectField(t, input, reflect.TypeOf(shopAddressInput{}), field)
	}
	for _, field := range []string{"Label", "Recipient", "Phone", "Postcode", "City", "Address", "IsDefault"} {
		shopDocAssertReflectField(t, output, reflect.TypeOf(core.Address{}), field)
	}
	for _, oldField := range []string{"name"} {
		if input[oldField] != nil {
			t.Errorf("AddressInput must match shopAddressInput JSON tags and must not document obsolete %q", oldField)
		}
	}
}

func assertCouponSchema(t *testing.T, schemas map[string]any) {
	t.Helper()
	input := shopDocObjectProperties(t, schemas, "CouponInput")
	output := shopDocObjectProperties(t, schemas, "Coupon")
	for _, field := range []string{"Value", "Kind", "BuyQty", "GetQty"} {
		shopDocAssertReflectField(t, input, reflect.TypeOf(core.Coupon{}), field)
		shopDocAssertReflectField(t, output, reflect.TypeOf(core.Coupon{}), field)
	}
	for _, legacy := range []string{"amount", "percent"} {
		if input[legacy] != nil || output[legacy] != nil {
			t.Errorf("coupon schema must use core.Coupon JSON fields value/kind/buy_qty/get_qty, not %q", legacy)
		}
	}
	generate := shopDocObjectProperties(t, schemas, "CouponGenerateInput")
	template, _ := generate["template"].(map[string]any)
	if template["type"] != "object" {
		t.Errorf("CouponGenerateInput.template type=%v, want object", template["type"])
	}
}

func assertShopSettingsSchema(t *testing.T, schemas map[string]any, operationAt func(path, method string) map[string]any) {
	t.Helper()
	settings, _ := schemas["ShopSettingsInput"].(map[string]any)
	inputProperties, _ := settings["properties"].(map[string]any)
	if settings["type"] != "object" || settings["additionalProperties"] != false {
		t.Fatalf("ShopSettingsInput must be an object that rejects unknown setting keys")
	}
	wantInputTypes := map[string]string{
		"shop_currency": "string", "shop_tax_mode": "string", "shop_tax_rate": "number", "shop_shipping_methods": "array",
		"shop_free_shipping_over": "integer", "shop_pickup_note": "string", "shop_stock_hold_minutes_card": "integer",
		"shop_stock_hold_hours_offline": "integer", "shop_failed_order_hold_hours": "integer", "shop_low_stock_default": "integer",
		"shop_order_prefix": "string", "shop_terms_page_id": "integer", "shop_refund_window_days": "integer", "payment_methods": "object",
		"payment_ecpay_merchant_id": "string", "payment_ecpay_hash_key": "string", "payment_ecpay_hash_iv": "string",
		"payment_ecpay_enabled": "boolean", "payment_ecpay_test_mode": "boolean", "payment_newebpay_merchant_id": "string",
		"payment_newebpay_hash_key": "string", "payment_newebpay_hash_iv": "string", "payment_newebpay_enabled": "boolean",
		"payment_newebpay_test_mode": "boolean", "payment_sandbox_enabled": "boolean", "payment_sandbox_secret": "string",
		"shop_manual_transfer_note": "string", "shop_invoice_seller": "string", "shop_contact_email": "string",
	}
	if len(inputProperties) != len(wantInputTypes) {
		t.Errorf("ShopSettingsInput property count=%d, want exact supported-key count %d", len(inputProperties), len(wantInputTypes))
	}
	if len(shopSettingKeys) != len(wantInputTypes) {
		t.Errorf("shop settings handler supports %d keys, test contract lists %d", len(shopSettingKeys), len(wantInputTypes))
	}
	for _, key := range shopSettingKeys {
		if _, ok := wantInputTypes[key]; !ok {
			t.Errorf("handler accepts undocumented shop setting key %q", key)
		}
	}
	for key, wantType := range wantInputTypes {
		property, _ := inputProperties[key].(map[string]any)
		if property == nil {
			t.Errorf("ShopSettingsInput omitted supported key %q", key)
			continue
		}
		if property["type"] != wantType {
			t.Errorf("ShopSettingsInput.%s type=%v, want %s", key, property["type"], wantType)
		}
		if _, ok := shopSettingKeySet[key]; !ok {
			t.Errorf("documented shop setting %q is not accepted by the handler", key)
		}
	}
	readProperties, _ := schemas["ShopSettings"].(map[string]any)
	readProperties, _ = readProperties["properties"].(map[string]any)
	if len(readProperties) != len(wantInputTypes) {
		t.Errorf("ShopSettings GET property count=%d, want %d", len(readProperties), len(wantInputTypes))
	}
	secretKeys := map[string]bool{"payment_ecpay_hash_key": true, "payment_ecpay_hash_iv": true, "payment_newebpay_hash_key": true, "payment_newebpay_hash_iv": true, "payment_sandbox_secret": true}
	for key := range wantInputTypes {
		readProperty, _ := readProperties[key].(map[string]any)
		if readProperty == nil || readProperty["type"] != "string" {
			t.Errorf("ShopSettings GET property %q must be a string", key)
		}
		inputProperty, _ := inputProperties[key].(map[string]any)
		if secretKeys[key] && inputProperty["writeOnly"] != true {
			t.Errorf("ShopSettingsInput.%s must be marked writeOnly", key)
		}
		if secretKeys[key] && readProperty["readOnly"] != true {
			t.Errorf("ShopSettings.%s must state that GET returns only the masked value", key)
		}
	}
	put := operationAt("/api/admin/shop/settings", "put")
	putResponse := shopDocResponseSchema(t, put, "200", schemas)
	status, _ := putResponse["properties"].(map[string]any)
	statusValue, _ := status["status"].(map[string]any)
	if !containsAny(asAnySlice(statusValue["enum"]), "ok") {
		t.Error("PUT /api/admin/shop/settings success schema must document {status: ok}")
	}
}

func asAnySlice(value any) []any {
	slice, _ := value.([]any)
	return slice
}

func assertDeleteNoContent(t *testing.T, operationAt func(path, method string) map[string]any) {
	t.Helper()
	for _, path := range []string{"/api/cart/items/{variant_id}", "/api/cart/coupon"} {
		operation := operationAt(path, "delete")
		responses, _ := operation["responses"].(map[string]any)
		response, _ := responses["204"].(map[string]any)
		if response == nil {
			t.Errorf("DELETE %s must document its 204 no-content success", path)
			continue
		}
		if response["content"] != nil {
			t.Errorf("DELETE %s 204 response must not claim a body", path)
		}
	}
}

func assertAdminOrderResponseSchemas(t *testing.T, schemas map[string]any, operationAt func(path, method string) map[string]any) {
	t.Helper()
	adminDetail := shopDocResponseSchema(t, operationAt("/api/admin/orders/{id}", "get"), "200", schemas)
	adminProperties, _ := adminDetail["properties"].(map[string]any)
	for _, field := range []string{"id", "number", "status", "payment", "customer", "items", "timeline", "snapshot", "allowed"} {
		if adminProperties[field] == nil {
			t.Errorf("admin order detail response schema omitted actual top-level field %q", field)
		}
	}
	member := shopDocResponseSchema(t, operationAt("/api/account/orders/{number}", "get"), "200", schemas)
	memberProperties, _ := member["properties"].(map[string]any)
	order, _ := memberProperties["order"].(map[string]any)
	orderProperties, _ := order["properties"].(map[string]any)
	if order == nil {
		t.Error("member order API response must wrap its Go order view under order")
	} else {
		items, _ := orderProperties["Items"].(map[string]any)
		item, _ := items["items"].(map[string]any)
		itemProperties, _ := item["properties"].(map[string]any)
		if orderProperties["Number"] == nil || itemProperties["Title"] == nil {
			t.Error("member order view schema must preserve Go TitleCase fields such as Number and Items[].Title")
		}
	}
	for _, operation := range []struct{ path, method string }{
		{"/api/admin/orders/{id}", "put"}, {"/api/admin/orders/{id}/notes", "post"},
		{"/api/admin/orders/{id}/address", "patch"}, {"/api/admin/orders/{id}/refund", "post"},
		{"/api/admin/orders/{id}/mark-paid", "post"},
	} {
		response := shopDocResponseSchema(t, operationAt(operation.path, operation.method), "200", schemas)
		status, _ := response["properties"].(map[string]any)
		if status["status"] == nil {
			t.Errorf("%s %s must document the {status: ok} response", strings.ToUpper(operation.method), operation.path)
		}
	}
}

func TestShopAPIDocumentationOverviewAndOpenAPICoverRegisteredRoutes(t *testing.T) {
	h := newShopDocumentationHarness()

	registered := map[string]bool{}
	want := append([]string(nil), shopExpectedDocumentedOperations()...)
	wantedRoutes := make(map[string]bool, len(want))
	for _, operation := range want {
		wantedRoutes[operation] = true
	}
	for _, route := range h.echo.Routes() {
		if strings.HasPrefix(route.Path, "/api/") && !strings.HasPrefix(route.Path, "/api/auth") && route.Path != shopAPIOverviewPath && route.Path != shopAPIOpenAPIPath {
			key := shopRouteOperationKey(route.Method, route.Path)
			if wantedRoutes[key] {
				registered[key] = true
			}
		}
	}
	gotRoutes := make([]string, 0, len(registered))
	for operation := range registered {
		gotRoutes = append(gotRoutes, operation)
	}
	sort.Strings(want)
	sort.Strings(gotRoutes)
	if !reflect.DeepEqual(gotRoutes, want) {
		t.Fatalf("shop route fixture scope changed: registered=%v, expected=%v", gotRoutes, want)
	}

	indexResponse := h.get(shopAPIOverviewPath)
	if indexResponse.Code != http.StatusOK {
		t.Fatalf("GET %s returned %d, want 200; body=%s", shopAPIOverviewPath, indexResponse.Code, indexResponse.Body.String())
	}
	var index struct {
		OpenAPIURL string                    `json:"openapi_url"`
		Operations []shopDocumentedOperation `json:"operations"`
	}
	if err := json.Unmarshal(indexResponse.Body.Bytes(), &index); err != nil {
		t.Fatalf("decode shop API overview: %v", err)
	}
	if index.OpenAPIURL != shopAPIOpenAPIPath {
		t.Errorf("openapi_url=%q, want %q", index.OpenAPIURL, shopAPIOpenAPIPath)
	}
	documented := make(map[string]shopDocumentedOperation, len(index.Operations))
	for _, operation := range index.Operations {
		key := operation.Method + " " + operation.Path
		if operation.Method != strings.ToUpper(operation.Method) || operation.Summary == "" || operation.DocumentationURL == "" {
			t.Errorf("overview operation needs uppercase method, summary, and documentation link: %+v", operation)
		}
		if _, duplicate := documented[key]; duplicate {
			t.Errorf("overview repeats operation %q", key)
		}
		documented[key] = operation
	}
	for _, key := range want {
		if _, ok := documented[key]; !ok {
			t.Errorf("shop API overview omitted registered route %q", key)
		}
	}
	for key := range documented {
		if !registered[key] {
			t.Errorf("shop API overview lists route outside this registered scope: %q", key)
		}
	}

	openAPIResponse := h.get(index.OpenAPIURL)
	if openAPIResponse.Code != http.StatusOK {
		t.Fatalf("GET %s returned %d, want 200; body=%s", index.OpenAPIURL, openAPIResponse.Code, openAPIResponse.Body.String())
	}
	var document map[string]any
	if err := json.Unmarshal(openAPIResponse.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode shop OpenAPI document: %v", err)
	}
	version, _ := document["openapi"].(string)
	if !strings.HasPrefix(version, "3.") {
		t.Errorf("OpenAPI version=%q, want 3.x", version)
	}
	if shopDocContainsInvalidOpenAPI30Null(document) {
		t.Error("OpenAPI 3.0 document contains type:null or a null required list; use nullable:true where needed")
	}
	paths, _ := document["paths"].(map[string]any)
	for _, key := range want {
		parts := strings.SplitN(key, " ", 2)
		method, path := strings.ToLower(parts[0]), parts[1]
		item, _ := paths[path].(map[string]any)
		if _, ok := item[method]; !ok {
			t.Errorf("OpenAPI omitted %s %s", method, path)
		}
		if operation, ok := documented[key]; ok && operation.DocumentationURL != shopExpectedOpenAPIOperationURL(parts[0], path) {
			t.Errorf("%s documentation_url=%q, want %q", key, operation.DocumentationURL, shopExpectedOpenAPIOperationURL(parts[0], path))
		}
	}
	if _, included := paths["/api/auth/me"]; included {
		t.Error("shop OpenAPI must not absorb existing auth API documentation")
	}
}

func TestShopAPIDocumentationContractsForSchemasAndSecurity(t *testing.T) {
	h := newShopDocumentationHarness()
	indexResponse := h.get(shopAPIOverviewPath)
	if indexResponse.Code != http.StatusOK {
		t.Fatalf("GET %s returned %d, want 200; body=%s", shopAPIOverviewPath, indexResponse.Code, indexResponse.Body.String())
	}
	var index struct {
		OpenAPIURL string `json:"openapi_url"`
	}
	if err := json.Unmarshal(indexResponse.Body.Bytes(), &index); err != nil || index.OpenAPIURL == "" {
		t.Fatalf("invalid shop overview: %v; body=%s", err, indexResponse.Body.String())
	}
	response := h.get(index.OpenAPIURL)
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s returned %d, want 200", index.OpenAPIURL, response.Code)
	}
	var document map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode OpenAPI document: %v", err)
	}
	paths, _ := document["paths"].(map[string]any)
	components, _ := document["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	operationAt := func(path, method string) map[string]any {
		t.Helper()
		item, _ := paths[path].(map[string]any)
		operation, _ := item[method].(map[string]any)
		if operation == nil {
			t.Fatalf("missing OpenAPI operation %s %s", method, path)
		}
		return operation
	}
	checkout := operationAt("/api/checkout", "post")
	body, _ := checkout["requestBody"].(map[string]any)
	if body["required"] != true {
		t.Error("POST /api/checkout requestBody must be required")
	}
	checkoutInput := shopDocObjectProperties(t, schemas, "CheckoutInput")
	paymentInput, _ := checkoutInput["payment"].(map[string]any)
	if paymentInput == nil {
		t.Fatal("CheckoutInput.payment must be documented")
	}
	shippingInput, _ := checkoutInput["shipping"].(map[string]any)
	shippingProperties, _ := shippingInput["properties"].(map[string]any)
	for _, field := range []string{"store_id", "store_name"} {
		if shippingProperties[field] == nil {
			t.Errorf("CheckoutInput.shipping omitted supported shipping field %q", field)
		}
	}
	checkoutResponse := shopDocObjectProperties(t, schemas, "CheckoutResponse")
	paymentResponse, _ := checkoutResponse["payment"].(map[string]any)
	paymentResponseProps, _ := paymentResponse["properties"].(map[string]any)
	kindSchema, _ := paymentResponseProps["kind"].(map[string]any)
	kinds, _ := kindSchema["enum"].([]any)
	if !containsAny(kinds, "none") {
		t.Error("CheckoutResponse.payment.kind must include the zero-total payment kind none")
	}
	checkoutDescription, _ := checkout["description"].(string)
	if !strings.Contains(checkoutDescription, "0") || !strings.Contains(strings.ToLower(checkoutDescription), "payment") {
		t.Error("checkout documentation must explain the zero-total payment behavior")
	}
	for _, key := range []string{"GET /api/cart", "GET /api/shop/shipping-methods", "GET /api/shop/payment-methods", "POST /api/orders/lookup", "GET /api/orders/{number}/status"} {
		parts := strings.SplitN(key, " ", 2)
		if security, exists := operationAt(parts[1], strings.ToLower(parts[0]))["security"]; exists && security != nil {
			t.Errorf("public operation %s should not require authentication", key)
		}
	}
	for _, key := range []string{"GET /api/account/orders", "GET /api/account/addresses", "GET /api/account/invoice-profiles", "GET /api/admin/orders", "GET /api/admin/products"} {
		parts := strings.SplitN(key, " ", 2)
		if _, exists := operationAt(parts[1], strings.ToLower(parts[0]))["security"]; !exists {
			t.Errorf("protected operation %s must document security", key)
		}
	}
	assertPositivePathID := func(key string) {
		t.Helper()
		parts := strings.SplitN(key, " ", 2)
		params, _ := operationAt(parts[1], strings.ToLower(parts[0]))["parameters"].([]any)
		for _, raw := range params {
			parameter, _ := raw.(map[string]any)
			schema, _ := parameter["schema"].(map[string]any)
			if parameter["in"] == "path" && parameter["required"] == true && schema["type"] == "integer" && schema["minimum"] == float64(1) {
				return
			}
		}
		t.Errorf("%s must document a required positive integer path parameter", key)
	}
	for _, key := range []string{"PATCH /api/cart/items/{variant_id}", "PUT /api/account/addresses/{id}", "GET /api/admin/products/{id}", "GET /api/admin/orders/{id}"} {
		assertPositivePathID(key)
	}
	productCreateResponses, _ := operationAt("/api/admin/products", "post")["responses"].(map[string]any)
	if productCreateResponses["200"] == nil {
		t.Error("POST /api/admin/products must document its successful 200 response")
	}
	if productCreateResponses["201"] != nil {
		t.Error("POST /api/admin/products must not document a 201 response")
	}
	for _, key := range []string{"POST /api/checkout", "POST /api/account/addresses", "PUT /api/account/addresses/{id}", "POST /api/admin/products", "PUT /api/admin/orders/{id}"} {
		parts := strings.SplitN(key, " ", 2)
		operation := operationAt(parts[1], strings.ToLower(parts[0]))
		requestBody, _ := operation["requestBody"].(map[string]any)
		content, _ := requestBody["content"].(map[string]any)
		jsonContent, _ := content["application/json"].(map[string]any)
		if requestBody["required"] != true || jsonContent["schema"] == nil {
			t.Errorf("%s must document its required JSON request schema", key)
		}
	}
	cartSchema := shopDocObjectProperties(t, schemas, "ShopCart")
	for _, field := range []string{"shipping_gap_to_free", "free_shipping_over", "requires_shipping", "rounding_adjustment"} {
		if cartSchema[field] == nil {
			t.Errorf("ShopCart schema omitted field %q", field)
		}
	}
	if schemas["Error"] == nil {
		t.Error("OpenAPI components must define the shared error schema")
	}
	for _, key := range []string{"POST /api/cart/items", "POST /api/checkout", "GET /api/admin/products"} {
		parts := strings.SplitN(key, " ", 2)
		responses, _ := operationAt(parts[1], strings.ToLower(parts[0]))["responses"].(map[string]any)
		response, _ := responses["400"].(map[string]any)
		content, _ := response["content"].(map[string]any)
		media, _ := content["application/json"].(map[string]any)
		errorSchema, _ := media["schema"].(map[string]any)
		if errorSchema["$ref"] != "#/components/schemas/Error" {
			t.Errorf("%s 400 response must reference shared Error schema", key)
		}
	}
	lookup := operationAt("/api/orders/lookup", "post")
	lookupDescription, _ := lookup["description"].(string)
	if !strings.Contains(lookupDescription, "202") || !strings.Contains(lookupDescription, "429") {
		t.Error("public order lookup must document its fixed accepted response and throttling outcome")
	}
	checkoutDescriptionLower := strings.ToLower(checkoutDescription)
	if !strings.Contains(checkoutDescriptionLower, "honeypot") || (!strings.Contains(checkoutDescriptionLower, "csrf") && !strings.Contains(checkoutDescription, "同來源")) {
		t.Error("checkout documentation must describe bot-field and same-origin write protection")
	}
	sandbox, _ := operationAt("/api/checkout/sandbox/{trade_no}", "post")["description"].(string)
	if !strings.Contains(strings.ToLower(sandbox), "非 production") && !strings.Contains(strings.ToLower(sandbox), "non-production") {
		t.Error("sandbox checkout operation must state it is limited to non-production environments")
	}
	assertProductSchema(t, schemas)
	assertAddressSchema(t, schemas)
	assertCouponSchema(t, schemas)
	assertShopSettingsSchema(t, schemas, operationAt)
	assertDeleteNoContent(t, operationAt)
	assertAdminOrderResponseSchemas(t, schemas, operationAt)
	encoded, _ := json.Marshal(document)
	for _, sensitive := range []string{
		"session-secret-fixture-value-8249e0d2",
		"jwt-secret-fixture-value-1ac2d359",
		"ecpay-hash-key-fixture-value-47b1cd80",
		"newebpay-hash-iv-fixture-value-9f8a1b77",
		"sandbox-secret-fixture-value-5eab2763",
	} {
		if strings.Contains(string(encoded), sensitive) {
			t.Errorf("shop OpenAPI document contains forbidden secret value %q", sensitive)
		}
	}
}
