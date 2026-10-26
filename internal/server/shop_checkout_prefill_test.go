package server

import (
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

func shopCheckoutPrefillInput(t *testing.T, bodyHTML, inputID string) string {
	t.Helper()
	inputPattern := regexp.MustCompile(`<input\b[^>]*\bid="` + regexp.QuoteMeta(inputID) + `"[^>]*>`)
	input := inputPattern.FindString(bodyHTML)
	if input == "" {
		t.Fatalf("checkout input %q was not rendered", inputID)
	}
	valuePattern := regexp.MustCompile(`\bvalue="([^"]*)"`)
	match := valuePattern.FindStringSubmatch(input)
	if len(match) != 2 {
		return ""
	}
	return html.UnescapeString(match[1])
}

func shopCheckoutPrefillNoStore(t *testing.T, response shopResponse, label string) {
	t.Helper()
	cacheControl := strings.ToLower(response.header.Get("Cache-Control"))
	if !strings.Contains(cacheControl, "no-store") {
		t.Errorf("%s Cache-Control = %q, want no-store", label, response.header.Get("Cache-Control"))
	}
}

func shopCheckoutPrefillActivateTheme(t *testing.T, h *accessHarness) {
	t.Helper()
	tm := theme.NewManager("web/themes")
	if err := tm.Activate("default"); err != nil {
		t.Fatalf("activate default theme for checkout prefill: %v", err)
	}
	e := echo.New()
	RegisterRoutes(e, tm, nil)
	h.echo = e
}

// Browser-level acceptance for the async frontend companion must additionally
// hold the real addresses/invoice-profile GETs open while the member interacts:
// a touched (even emptied) address field stays untouched, touching any invoice
// control suppresses that profile's whole prefill, submit suppresses late fills,
// and failed GETs leave fields manually editable. Those timing assertions need
// a rendered browser and are intentionally not inferred from this HTTP test.
func TestShopCheckoutPrefillUsesOnlyAuthenticatedOwnDefaultsAndNoStore(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	shopCheckoutPrefillActivateTheme(t, h)
	p := shopSeedProduct(t, h, "checkout-prefill-item", 12000, 10)
	v := shopVariantRows(t, h, p.ID)[0]

	customer := h.user("customer", "checkout-prefill-customer")
	customer.DisplayName = "Checkout Prefill Buyer"
	customer.Status = "active"
	if err := h.db.Save(&customer).Error; err != nil {
		t.Fatalf("save active customer profile: %v", err)
	}
	staff := h.user("admin", "checkout-prefill-staff")
	staff.DisplayName = "Checkout Prefill Staff"
	staff.Status = "active"
	if err := h.db.Save(&staff).Error; err != nil {
		t.Fatalf("save active staff profile: %v", err)
	}
	other := h.user("customer", "checkout-prefill-other")
	other.DisplayName = "Do Not Leak Other Buyer"
	if err := h.db.Save(&other).Error; err != nil {
		t.Fatalf("save other customer profile: %v", err)
	}

	customerAddress := core.Address{UserID: customer.ID, Label: "default", Recipient: "Customer Default Recipient", Phone: "0911222333", Postcode: "100", City: "台北市", Address: "Customer Test Road 1", IsDefault: true}
	staffAddress := core.Address{UserID: staff.ID, Label: "default", Recipient: "Staff Default Recipient", Phone: "0922333444", Postcode: "200", City: "新北市", Address: "Staff Test Road 2", IsDefault: true}
	otherAddress := core.Address{UserID: other.ID, Label: "private", Recipient: "Other Private Recipient", Phone: "0933444555", Postcode: "300", City: "桃園市", Address: "Other Private Road 3", IsDefault: true}
	for _, address := range []*core.Address{&customerAddress, &staffAddress, &otherAddress} {
		if err := h.db.Create(address).Error; err != nil {
			t.Fatalf("create checkout prefill address: %v", err)
		}
	}
	customerInvoice := core.InvoiceProfile{UserID: customer.ID, Kind: "tax_id", TaxID: "12345675", CompanyName: "Customer Company", IsDefault: true}
	staffInvoice := core.InvoiceProfile{UserID: staff.ID, Kind: "personal_carrier", CarrierCode: "/ABC1234", IsDefault: true}
	otherInvoice := core.InvoiceProfile{UserID: other.ID, Kind: "donation", DonationCode: "1234567", IsDefault: true}
	if other.Email == "" || otherAddress.Recipient == "" || otherInvoice.DonationCode == "" {
		t.Fatal("other-customer leak fixture must contain non-empty sensitive values")
	}
	for _, profile := range []*core.InvoiceProfile{&customerInvoice, &staffInvoice, &otherInvoice} {
		if err := h.db.Create(profile).Error; err != nil {
			t.Fatalf("create checkout prefill invoice profile: %v", err)
		}
	}

	for _, tc := range []struct {
		name    string
		user    core.User
		address core.Address
		invoice core.InvoiceProfile
	}{
		{name: "active customer", user: customer, address: customerAddress, invoice: customerInvoice},
		{name: "active staff", user: staff, address: staffAddress, invoice: staffInvoice},
	} {
		t.Run(tc.name, func(t *testing.T) {
			browser := newShopBrowser(h)
			browser.token = h.token(tc.user)
			shopAddToCart(t, browser, v.ID, 1)

			page := browser.get("/checkout")
			shopWantCode(t, page, "authenticated GET /checkout", http.StatusOK)
			shopCheckoutPrefillNoStore(t, page, tc.name+" checkout page")
			if !strings.Contains(page.body, `data-member="1"`) {
				t.Errorf("%s checkout page did not expose its authenticated-member marker", tc.name)
			}
			if got := shopCheckoutPrefillInput(t, page.body, "co-email"); got != tc.user.Email {
				t.Errorf("%s checkout email = %q, want own email %q", tc.name, got, tc.user.Email)
			}
			if got := shopCheckoutPrefillInput(t, page.body, "co-name"); got != "" {
				t.Errorf("%s checkout recipient = %q, want blank until asynchronous address prefill", tc.name, got)
			}
			if got := shopCheckoutPrefillInput(t, page.body, "co-phone"); got != "" {
				t.Errorf("%s checkout phone = %q, want blank until asynchronous address prefill", tc.name, got)
			}
			for _, otherSensitiveValue := range []string{other.Email, otherAddress.Recipient, otherInvoice.DonationCode} {
				if strings.Contains(page.body, otherSensitiveValue) {
					t.Errorf("%s checkout page leaked another customer's saved value %q", tc.name, otherSensitiveValue)
				}
			}

			addressesResponse := browser.get("/api/account/addresses")
			shopWantCode(t, addressesResponse, "authenticated GET /api/account/addresses", http.StatusOK)
			var addresses []core.Address
			shopDecode(t, addressesResponse, "authenticated checkout address defaults", &addresses)
			if len(addresses) != 1 || addresses[0].ID != tc.address.ID || addresses[0].UserID != tc.user.ID {
				t.Errorf("%s address API returned rows outside its own defaults: %+v", tc.name, addresses)
			}
			invoicesResponse := browser.get("/api/account/invoice-profiles")
			shopWantCode(t, invoicesResponse, "authenticated GET /api/account/invoice-profiles", http.StatusOK)
			var invoices []core.InvoiceProfile
			shopDecode(t, invoicesResponse, "authenticated checkout invoice defaults", &invoices)
			if len(invoices) != 1 || invoices[0].ID != tc.invoice.ID || invoices[0].UserID != tc.user.ID {
				t.Errorf("%s invoice API returned rows outside its own defaults: %+v", tc.name, invoices)
			}
		})
	}

	guest := newShopBrowser(h)
	shopAddToCart(t, guest, v.ID, 1)
	guestPage := guest.get("/checkout")
	shopWantCode(t, guestPage, "anonymous GET /checkout", http.StatusOK)
	shopCheckoutPrefillNoStore(t, guestPage, "anonymous checkout page")
	if strings.Contains(guestPage.body, `data-member="1"`) || strings.Contains(guestPage.body, customer.Email) || strings.Contains(guestPage.body, customerAddress.Recipient) || strings.Contains(guestPage.body, customerAddress.Phone) {
		t.Error("anonymous checkout page included authenticated customer prefill")
	}
	for _, inputID := range []string{"co-email", "co-phone", "co-name", "co-postcode", "co-city", "co-address", "co-carrier", "co-taxid", "co-company", "co-donation"} {
		if got := shopCheckoutPrefillInput(t, guestPage.body, inputID); got != "" {
			t.Errorf("anonymous checkout %s = %q, want blank", inputID, got)
		}
	}
	for _, endpoint := range []string{"/api/account/addresses", "/api/account/invoice-profiles"} {
		shopWantCode(t, guest.get(endpoint), "anonymous GET "+endpoint, http.StatusUnauthorized)
	}
}
