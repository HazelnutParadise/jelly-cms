package server

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

type shopShippingMethod struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	Fee             int64  `json:"fee"`
	FreeOver        int64  `json:"free_over"`
	ETADays         int    `json:"eta_days"`
	RequiresAddress bool   `json:"requires_address"`
	Enabled         bool   `json:"enabled"`
}

type shopPaymentMethod struct {
	ID       string `json:"id"`
	Method   string `json:"method"`
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
}

func shopShippingMethods() ([]shopShippingMethod, error) {
	return shopShippingMethodsWithDB(data.DB)
}

func shopShippingMethodsWithDB(db *gorm.DB) ([]shopShippingMethod, error) {
	raw, err := shopReadOptionWithDB(db, "shop_shipping_methods")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return []shopShippingMethod{
			{ID: "home", Label: "宅配", Fee: 8000, FreeOver: 0, ETADays: 3, RequiresAddress: true, Enabled: true},
			{ID: "pickup", Label: "自取", Fee: 0, FreeOver: 0, ETADays: 1, RequiresAddress: false, Enabled: true},
		}, nil
	}
	var methods []shopShippingMethod
	if err := json.Unmarshal([]byte(raw), &methods); err != nil {
		return nil, err
	}
	out := make([]shopShippingMethod, 0, len(methods))
	for _, method := range methods {
		if method.ID == "" || method.Fee < 0 || method.FreeOver < 0 || method.ETADays < 0 {
			continue
		}
		if method.Enabled {
			out = append(out, method)
		}
	}
	return out, nil
}

func shopFindShipping(methods []shopShippingMethod, id string) *shopShippingMethod {
	for i := range methods {
		if methods[i].ID == id {
			return &methods[i]
		}
	}
	return nil
}

func shopPaymentMethods() ([]shopPaymentMethod, error) {
	raw, err := shopReadOption("payment_methods")
	if err != nil {
		return nil, err
	}
	methods := make([]shopPaymentMethod, 0)
	production := strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production")
	sandboxRaw, err := shopReadOption("payment_sandbox_enabled")
	if err != nil {
		return nil, err
	}
	sandboxEnabled := strings.EqualFold(strings.TrimSpace(sandboxRaw), "true") && !production
	if raw == "" {
		return methods, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	tok, err := decoder.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, errors.New("invalid payment_methods option")
	}
	for decoder.More() {
		tok, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		method, ok := tok.(string)
		if !ok {
			return nil, errors.New("invalid payment method key")
		}
		var provider string
		if err := decoder.Decode(&provider); err != nil {
			return nil, err
		}
		provider = strings.TrimSpace(provider)
		if method == "" || provider == "" || (method != "credit_card" && method != "atm" && method != "cvs" && method != "bank_transfer") {
			continue
		}
		if provider == "sandbox" {
			if method == "bank_transfer" {
				continue
			}
			if !sandboxEnabled {
				continue
			}
			configured, err := shopSandboxKeyConfigured()
			if err != nil {
				return nil, err
			}
			if !configured {
				continue
			}
		}
		if provider == "manual_transfer" && method != "bank_transfer" {
			continue
		}
		if provider == "ecpay" {
			if method == "bank_transfer" {
				continue
			}
			ready, err := shopECPayReady()
			if err != nil {
				return nil, err
			}
			if !ready {
				continue
			}
		} else if provider != "sandbox" && provider != "manual_transfer" {
			continue
		}
		kind := "redirect"
		if provider == "manual_transfer" {
			kind = "offline"
		}
		methods = append(methods, shopPaymentMethod{ID: method, Method: method, Provider: provider, Kind: kind})
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("invalid payment_methods option")
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, errors.New("trailing payment_methods data")
		}
		return nil, err
	}
	return methods, nil
}

func shopSandboxKeyConfigured() (bool, error) {
	secret, err := shopReadOption("payment_sandbox_secret")
	if err != nil {
		return false, err
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(secret))
	return err == nil && len(decoded) == 32, nil
}

func shopECPayReady() (bool, error) {
	for _, key := range []string{"payment_ecpay_enabled", "payment_ecpay_merchant_id", "payment_ecpay_hash_key", "payment_ecpay_hash_iv"} {
		value, err := shopReadOption(key)
		if err != nil {
			return false, err
		}
		if key == "payment_ecpay_enabled" {
			if !strings.EqualFold(strings.TrimSpace(value), "true") {
				return false, nil
			}
		} else if strings.TrimSpace(value) == "" {
			return false, nil
		}
	}
	return true, nil
}

func shopShippingMethodsHandler(c echo.Context) error {
	methods, err := shopShippingMethods()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not load shipping methods")
	}
	return c.JSON(http.StatusOK, methods)
}

func shopPaymentMethodsHandler(c echo.Context) error {
	methods, err := shopPaymentMethods()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not load payment methods")
	}
	return c.JSON(http.StatusOK, methods)
}
