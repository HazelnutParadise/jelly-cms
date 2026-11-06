package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
)

// Ticket 27: v1 product and order lists share the admin query functions but
// answer the paged envelope without the admin screen statistics.

// integrationOrderLine is one line of an order in GET /api/v1/orders.
type integrationOrderLine struct {
	Title       string `json:"title"`
	VariantName string `json:"variant_name"`
	Quantity    int    `json:"quantity"`
}

// integrationOrderSummary is the shape of an order inside GET /api/v1/orders
// (ticket 27, 名詞「訂單摘要」). It carries no qty or total duplicate keys.
type integrationOrderSummary struct {
	ID              uint                   `json:"id"`
	Number          string                 `json:"number"`
	Email           string                 `json:"email"`
	Status          string                 `json:"status"`
	TotalAmount     int64                  `json:"total_amount"`
	Currency        string                 `json:"currency"`
	PaymentProvider string                 `json:"payment_provider"`
	PaymentMethod   string                 `json:"payment_method"`
	Shipping        json.RawMessage        `json:"shipping"`
	Items           []integrationOrderLine `json:"items"`
	CreatedAt       time.Time              `json:"created_at"`
}

// integrationOrderSummaryFields lists the JSON keys of integrationOrderSummary
// in struct order; the OpenAPI schema is generated from it.
func integrationOrderSummaryFields() []string {
	return integrationJSONFields(integrationOrderSummary{})
}

func newIntegrationOrderSummary(order core.Order) integrationOrderSummary {
	lines := make([]integrationOrderLine, 0, len(order.Items))
	for _, item := range order.Items {
		lines = append(lines, integrationOrderLine{Title: item.Title, VariantName: item.VariantName, Quantity: item.Quantity})
	}
	return integrationOrderSummary{
		ID: order.ID, Number: order.Number, Email: order.Email, Status: order.Status, TotalAmount: order.TotalAmount,
		Currency: order.Currency, PaymentProvider: order.PaymentProvider, PaymentMethod: order.PaymentMethod,
		Shipping: integrationRawOrNull(order.Shipping), Items: lines, CreatedAt: order.CreatedAt,
	}
}

// integrationListProducts answers {"items", "total", "page", "per_page"}; items
// keep the admin product view shape, counts is not included.
func integrationListProducts(c echo.Context) error {
	p, perr := integrationPageParams(c)
	if perr != nil {
		return perr.write(c)
	}
	status, q := c.QueryParam("status"), strings.TrimSpace(c.QueryParam("q"))
	if !validAdminProductStatus(status) {
		return writeAPIError(c, http.StatusBadRequest, "bad_request", "invalid status", map[string]any{"param": "status"})
	}
	items, total, err := queryAdminProducts(status, q, p)
	if err != nil {
		return writeAPIError(c, http.StatusInternalServerError, "internal_error", "Unable to load products", nil)
	}
	return c.JSON(http.StatusOK, integrationPaged(items, total, p))
}

// integrationListOrders answers {"items", "total", "page", "per_page"} with
// integrationOrderSummary items; counts and kpi are not included.
func integrationListOrders(c echo.Context) error {
	p, perr := integrationPageParams(c)
	if perr != nil {
		return perr.write(c)
	}
	if data.DB == nil {
		return writeAPIError(c, http.StatusServiceUnavailable, "service_unavailable", "Service unavailable", nil)
	}
	f := adminOrderFiltersFromQuery(c)
	if param, message := adminOrderFilterProblem(f); param != "" {
		return writeAPIError(c, http.StatusBadRequest, "bad_request", message, map[string]any{"param": param})
	}
	orders, total, err := queryAdminOrders(c.Request().Context(), f, p)
	if err != nil {
		return writeAPIError(c, http.StatusInternalServerError, "internal_error", "Unable to list orders", nil)
	}
	items := make([]integrationOrderSummary, 0, len(orders))
	for _, order := range orders {
		items = append(items, newIntegrationOrderSummary(order))
	}
	return c.JSON(http.StatusOK, integrationPaged(items, total, p))
}
