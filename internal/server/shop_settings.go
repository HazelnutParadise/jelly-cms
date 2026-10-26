package server

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

var shopSettingKeys = []string{
	"shop_currency", "shop_tax_mode", "shop_tax_rate", "shop_shipping_methods",
	"shop_free_shipping_over", "shop_pickup_note", "shop_stock_hold_minutes_card",
	"shop_stock_hold_hours_offline", "shop_failed_order_hold_hours", "shop_low_stock_default",
	"shop_order_prefix", "shop_terms_page_id", "shop_refund_window_days",
	"payment_methods", "payment_ecpay_merchant_id", "payment_ecpay_hash_key", "payment_ecpay_hash_iv",
	"payment_ecpay_enabled", "payment_ecpay_test_mode", "payment_newebpay_merchant_id", "payment_newebpay_hash_key", "payment_newebpay_hash_iv",
	"payment_newebpay_enabled", "payment_newebpay_test_mode", "payment_sandbox_enabled", "payment_sandbox_secret",
	"shop_manual_transfer_note", "shop_invoice_seller", "shop_contact_email",
}

var shopSecretKeys = map[string]bool{
	"payment_ecpay_hash_key": true, "payment_ecpay_hash_iv": true,
	"payment_newebpay_hash_key": true, "payment_newebpay_hash_iv": true,
	"payment_sandbox_secret": true,
}

var shopSettingKeySet = func() map[string]bool {
	keys := make(map[string]bool, len(shopSettingKeys))
	for _, key := range shopSettingKeys {
		keys[key] = true
	}
	return keys
}()

var errInvalidShopSetting = errors.New("invalid shop setting")

const shopSettingsAdvisoryLockKey int64 = 0x4a454c4c5953484f

func maskSecret(v string) string {
	if v == "" {
		return ""
	}
	if len(v) <= 4 {
		return "****"
	}
	return "****" + v[len(v)-4:]
}

func adminShopSettingsGet(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "service unavailable"})
	}
	var opts []core.Option
	if err := data.DB.WithContext(c.Request().Context()).Where("key IN ?", shopSettingKeys).Find(&opts).Error; err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "could not load shop settings"})
	}
	settings := make(map[string]string, len(opts))
	for _, opt := range opts {
		if !shopSettingKeySet[opt.Key] {
			continue
		}
		settings[opt.Key] = opt.Value
	}
	for key := range shopSecretKeys {
		if secret, ok := settings[key]; ok {
			settings[key] = maskSecret(secret)
		}
	}
	return c.JSON(http.StatusOK, settings)
}

func adminShopSettingsPut(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "service unavailable"})
	}
	var req map[string]json.RawMessage
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, 1<<20)
	decoder := json.NewDecoder(c.Request().Body)
	if err := decoder.Decode(&req); err != nil || req == nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}
	for key := range req {
		if !shopSettingKeySet[key] {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "unknown shop setting"})
		}
	}

	err := data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockShopSettings(tx); err != nil {
			return err
		}
		var opts []core.Option
		if err := tx.Where("key IN ?", shopSettingKeys).Find(&opts).Error; err != nil {
			return err
		}
		current := make(map[string]string, len(opts))
		for _, opt := range opts {
			current[opt.Key] = opt.Value
		}
		merged := make(map[string]string, len(current)+len(req))
		for key, value := range current {
			merged[key] = value
		}
		updates := make(map[string]string, len(req))
		for key, raw := range req {
			value, err := parseShopSetting(key, raw)
			if err != nil {
				return errInvalidShopSetting
			}
			if shopSecretKeys[key] && strings.Contains(value, "*") {
				if (value == maskSecret(current[key]) || strings.Trim(value, "*") == "") && current[key] != "" {
					continue // The UI returns the existing masked secret unchanged.
				}
				return errInvalidShopSetting
			}
			updates[key] = value
			merged[key] = value
		}
		if err := validateShopSettings(merged); err != nil {
			return errInvalidShopSetting
		}
		for key, value := range updates {
			opt := core.Option{Key: key}
			if err := tx.Where("key = ?", key).First(&opt).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				opt.Value = value
				if err := tx.Create(&opt).Error; err != nil {
					return err
				}
				continue
			}
			opt.Value = value
			if err := tx.Save(&opt).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errInvalidShopSetting) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid shop settings"})
	}
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "could not save shop settings"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func parseShopSetting(key string, raw json.RawMessage) (string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", errInvalidShopSetting
	}
	var value string
	switch key {
	case "shop_shipping_methods", "payment_methods":
		if !json.Valid(raw) {
			return "", errInvalidShopSetting
		}
		return string(raw), nil
	case "payment_ecpay_enabled", "payment_ecpay_test_mode", "payment_newebpay_enabled", "payment_newebpay_test_mode", "payment_sandbox_enabled":
		var enabled bool
		if err := json.Unmarshal(raw, &enabled); err != nil {
			return "", errInvalidShopSetting
		}
		if enabled {
			return "true", nil
		}
		return "false", nil
	case "shop_tax_rate", "shop_free_shipping_over", "shop_stock_hold_minutes_card", "shop_stock_hold_hours_offline", "shop_failed_order_hold_hours", "shop_low_stock_default", "shop_terms_page_id", "shop_refund_window_days":
		var number json.Number
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&number); err != nil {
			return "", errInvalidShopSetting
		}
		f, err := number.Float64()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || (key != "shop_tax_rate" && math.Trunc(f) != f) {
			return "", errInvalidShopSetting
		}
		if key == "shop_tax_rate" && f > 100 {
			return "", errInvalidShopSetting
		}
		return number.String(), nil
	default:
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", errInvalidShopSetting
		}
		value = strings.TrimSpace(value)
		if key == "shop_currency" && value != "TWD" {
			return "", errInvalidShopSetting
		}
		if key == "shop_tax_mode" && value != "included" {
			return "", errInvalidShopSetting
		}
		return value, nil
	}
}

func validateShopSettings(settings map[string]string) error {
	if raw := strings.TrimSpace(settings["shop_shipping_methods"]); raw != "" {
		var methods []shopShippingMethod
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&methods); err != nil {
			return errInvalidShopSetting
		}
		seen := make(map[string]bool, len(methods))
		for _, method := range methods {
			if strings.TrimSpace(method.ID) == "" || strings.TrimSpace(method.Label) == "" || method.Fee < 0 || method.FreeOver < 0 || method.ETADays < 0 || seen[method.ID] {
				return errInvalidShopSetting
			}
			seen[method.ID] = true
		}
	}
	if raw := strings.TrimSpace(settings["payment_methods"]); raw != "" {
		var methods map[string]string
		if err := json.Unmarshal([]byte(raw), &methods); err != nil || methods == nil {
			return errInvalidShopSetting
		}
		for method, provider := range methods {
			if method != "credit_card" && method != "atm" && method != "cvs" && method != "bank_transfer" {
				return errInvalidShopSetting
			}
			if provider != "ecpay" && provider != "sandbox" && provider != "manual_transfer" {
				return errInvalidShopSetting
			}
			if provider == "manual_transfer" && method != "bank_transfer" || provider == "ecpay" && method == "bank_transfer" || provider == "sandbox" && method == "bank_transfer" {
				return errInvalidShopSetting
			}
		}
	}
	if strings.EqualFold(settings["payment_ecpay_enabled"], "true") {
		for _, key := range []string{"payment_ecpay_merchant_id", "payment_ecpay_hash_key", "payment_ecpay_hash_iv"} {
			if strings.TrimSpace(settings[key]) == "" {
				return errInvalidShopSetting
			}
		}
	}
	if strings.EqualFold(settings["payment_sandbox_enabled"], "true") {
		secret, err := hex.DecodeString(strings.TrimSpace(settings["payment_sandbox_secret"]))
		if err != nil || len(secret) != 32 {
			return errInvalidShopSetting
		}
	}
	return nil
}

func lockShopSettings(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("shop settings database is unavailable")
	}
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	return tx.Exec("SELECT pg_advisory_xact_lock(?)", shopSettingsAdvisoryLockKey).Error
}

func adminShopPaymentTest(c echo.Context) error {
	// No gateway in this release exposes a harmless credentials-only connectivity
	// check. Never report success based only on non-empty locally stored keys.
	message := "payment provider test is not available without a safe provider validation endpoint"
	if c.Param("gateway") != "ecpay" && c.Param("gateway") != "newebpay" {
		message = "unsupported payment provider"
	}
	return c.JSON(http.StatusOK, map[string]any{"ok": false, "message": message, "checked_at": time.Now().UTC().Format(time.RFC3339)})
}
