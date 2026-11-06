package server

import (
	"github.com/labstack/echo/v4"
)

// Ticket 27: v1 product and order lists share the admin query functions but
// answer the paged envelope without the admin screen statistics.

// integrationOrderSummary is the shape of an order inside GET /api/v1/orders
// (ticket 27, 名詞「訂單摘要」). It carries no qty or total duplicate keys.
type integrationOrderSummary struct {
	// Fields are filled in by the implementation.
}

// integrationOrderSummaryFields lists the JSON keys of integrationOrderSummary
// in struct order; the OpenAPI schema is generated from it.
func integrationOrderSummaryFields() []string {
	panic("ticket 27: integrationOrderSummaryFields not implemented")
}

// integrationListProducts answers {"items", "total", "page", "per_page"}; items
// keep the admin product view shape, counts is not included.
func integrationListProducts(c echo.Context) error {
	panic("ticket 27: integrationListProducts not implemented")
}

// integrationListOrders answers {"items", "total", "page", "per_page"} with
// integrationOrderSummary items; counts and kpi are not included.
func integrationListOrders(c echo.Context) error {
	panic("ticket 27: integrationListOrders not implemented")
}
