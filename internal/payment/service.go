package payment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"gorm.io/gorm"
)

// Service manages payment gateways
type Service struct {
	gateways       map[string]PaymentGateway
	defaultGateway PaymentGateway
	mu             sync.RWMutex
	db             *gorm.DB
	appURL         string // For building return/notify URLs
	reloadErr      error
}

// NewService creates a new payment service
func NewService(db *gorm.DB) *Service {
	service := &Service{
		gateways: make(map[string]PaymentGateway),
		db:       db,
		appURL:   os.Getenv("APP_URL"),
	}
	if service.appURL == "" {
		service.appURL = "http://localhost:8080"
	}

	_ = service.ReloadGateways()
	return service
}

// ReloadGateways reloads gateway configurations from database
func (s *Service) ReloadGateways() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		s.reloadErr = errors.New("payment: database is not initialized")
		return s.reloadErr
	}
	gateways := make(map[string]PaymentGateway)
	var defaultGateway PaymentGateway

	// Load ECPay config from database
	ecpayConfig, err := s.loadECPayConfigFromDB()
	if err != nil {
		s.reloadErr = err
		return err
	}
	if ecpayConfig != nil {
		ecpay := NewECPayGateway(ecpayConfig)
		gateways["ecpay"] = ecpay
		if defaultGateway == nil {
			defaultGateway = ecpay
		}
	}

	// NewebPay remains available as a configured adapter only after its
	// callback parser is hardened; ticket 07 intentionally does not route it.
	manualNote, _, err := s.option("shop_manual_transfer_note")
	if err != nil {
		s.reloadErr = err
		return err
	}
	manual := NewManualTransferGateway(manualNote)
	gateways["manual_transfer"] = manual
	if defaultGateway == nil {
		defaultGateway = manual
	}

	sandboxEnabled, _, err := s.option("payment_sandbox_enabled")
	if err != nil {
		s.reloadErr = err
		return err
	}
	if strings.EqualFold(strings.TrimSpace(sandboxEnabled), "true") && !strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production") {
		key, err := s.ensureSandboxSecret()
		if err != nil {
			s.reloadErr = err
			return err
		}
		sandbox := NewSandboxGateway(key)
		gateways["sandbox"] = sandbox
		if defaultGateway == nil {
			defaultGateway = sandbox
		}
	}

	s.gateways = gateways
	s.defaultGateway = defaultGateway
	s.reloadErr = nil
	return nil
}

// GetGateway returns a payment gateway by name
func (s *Service) GetGateway(name string) (PaymentGateway, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.reloadErr != nil {
		return nil, fmt.Errorf("payment: gateway configuration unavailable: %w", s.reloadErr)
	}

	if name == "" {
		if s.defaultGateway == nil {
			return nil, fmt.Errorf("no default gateway configured")
		}
		return s.defaultGateway, nil
	}

	gateway, ok := s.gateways[name]
	if !ok {
		return nil, fmt.Errorf("gateway %s not found", name)
	}
	return gateway, nil
}

// GetDefaultGateway returns the default payment gateway
func (s *Service) GetDefaultGateway() PaymentGateway {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.defaultGateway
}

// loadECPayConfigFromDB loads ECPay configuration from database
func (s *Service) loadECPayConfigFromDB() (*ECPayConfig, error) {
	enabled, found, err := s.option("payment_ecpay_enabled")
	if err != nil || !found || !strings.EqualFold(enabled, "true") {
		return nil, err
	}
	merchant, merchantFound, err := s.option("payment_ecpay_merchant_id")
	if err != nil {
		return nil, err
	}
	hashKey, hashKeyFound, err := s.option("payment_ecpay_hash_key")
	if err != nil {
		return nil, err
	}
	hashIV, hashIVFound, err := s.option("payment_ecpay_hash_iv")
	if err != nil {
		return nil, err
	}
	if !merchantFound || !hashKeyFound || !hashIVFound || merchant == "" || hashKey == "" || hashIV == "" {
		return nil, errors.New("payment: ECPay is enabled but its credentials are incomplete")
	}
	testMode, _, err := s.option("payment_ecpay_test_mode")
	if err != nil {
		return nil, err
	}
	return &ECPayConfig{
		MerchantID:    merchant,
		HashKey:       hashKey,
		HashIV:        hashIV,
		IsTestMode:    strings.EqualFold(testMode, "true"),
		ReturnURL:     s.appURL + "/api/payment/ecpay/notify",
		NotifyURL:     s.appURL + "/api/payment/ecpay/notify",
		ClientBackURL: s.appURL + "/api/payment/ecpay/return",
	}, nil
}

// loadNewebPayConfigFromDB loads NewebPay configuration from database
func (s *Service) loadNewebPayConfigFromDB() (*NewebPayConfig, error) {
	return nil, nil
}

// SaveECPayConfig saves ECPay configuration to database
func (s *Service) SaveECPayConfig(config *ECPayConfig, enabled bool) error {
	if config == nil {
		return errors.New("payment: missing ECPay configuration")
	}
	if enabled && (strings.TrimSpace(config.MerchantID) == "" || strings.TrimSpace(config.HashKey) == "" || strings.TrimSpace(config.HashIV) == "") {
		return errors.New("payment: ECPay credentials are required before enabling it")
	}
	settings := map[string]string{
		"payment_ecpay_enabled":     fmt.Sprintf("%v", enabled),
		"payment_ecpay_merchant_id": config.MerchantID,
		"payment_ecpay_hash_key":    config.HashKey,
		"payment_ecpay_hash_iv":     config.HashIV,
		"payment_ecpay_test_mode":   fmt.Sprintf("%v", config.IsTestMode),
	}

	if err := s.saveProviderOptions(settings); err != nil {
		return err
	}

	return s.ReloadGateways()
}

// SaveNewebPayConfig saves NewebPay configuration to database
func (s *Service) SaveNewebPayConfig(config *NewebPayConfig, enabled bool) error {
	if config == nil {
		return errors.New("payment: missing NewebPay configuration")
	}
	if enabled && (strings.TrimSpace(config.MerchantID) == "" || strings.TrimSpace(config.HashKey) == "" || strings.TrimSpace(config.HashIV) == "") {
		return errors.New("payment: NewebPay credentials are required before enabling it")
	}
	settings := map[string]string{
		"payment_newebpay_enabled":     fmt.Sprintf("%v", enabled),
		"payment_newebpay_merchant_id": config.MerchantID,
		"payment_newebpay_hash_key":    config.HashKey,
		"payment_newebpay_hash_iv":     config.HashIV,
		"payment_newebpay_test_mode":   fmt.Sprintf("%v", config.IsTestMode),
	}

	if err := s.saveProviderOptions(settings); err != nil {
		return err
	}

	return s.ReloadGateways()
}

func (s *Service) saveProviderOptions(settings map[string]string) error {
	if s.db == nil {
		return errors.New("payment: database is not initialized")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		for key, value := range settings {
			if strings.HasSuffix(key, "_hash_key") || strings.HasSuffix(key, "_hash_iv") {
				// Masked readback values are acknowledgements, not replacement keys.
				if strings.HasPrefix(value, "****") {
					continue
				}
				if value == "" {
					var current core.Option
					err := tx.First(&current, "key = ?", key).Error
					if err == nil {
						continue
					}
					if !errors.Is(err, gorm.ErrRecordNotFound) {
						return err
					}
				}
			}
			if err := tx.Exec(`INSERT INTO options (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, value).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// GetPaymentConfigs returns all payment gateway configurations
func (s *Service) GetPaymentConfigs() map[string]interface{} {
	result := make(map[string]interface{})
	if s.db == nil {
		return result
	}

	// ECPay config
	ecpayEnabled := false
	if value, found, err := s.option("payment_ecpay_enabled"); err == nil && found {
		ecpayEnabled = strings.EqualFold(value, "true")
	}

	ecpayConfig := make(map[string]string)
	if ecpayEnabled {
		keys := []string{"payment_ecpay_merchant_id", "payment_ecpay_hash_key", "payment_ecpay_hash_iv", "payment_ecpay_test_mode"}
		for _, key := range keys {
			if value, found, err := s.option(key); err == nil && found {
				if strings.HasSuffix(key, "_hash_key") || strings.HasSuffix(key, "_hash_iv") {
					ecpayConfig[key] = maskSecret(value)
				} else {
					ecpayConfig[key] = value
				}
			}
		}
	}
	result["ecpay"] = map[string]interface{}{
		"enabled": ecpayEnabled,
		"config":  ecpayConfig,
	}

	// NewebPay config
	newebpayEnabled := false
	if value, found, err := s.option("payment_newebpay_enabled"); err == nil && found {
		newebpayEnabled = strings.EqualFold(value, "true")
	}

	newebpayConfig := make(map[string]string)
	if newebpayEnabled {
		keys := []string{"payment_newebpay_merchant_id", "payment_newebpay_hash_key", "payment_newebpay_hash_iv", "payment_newebpay_test_mode"}
		for _, key := range keys {
			if value, found, err := s.option(key); err == nil && found {
				if strings.HasSuffix(key, "_hash_key") || strings.HasSuffix(key, "_hash_iv") {
					newebpayConfig[key] = maskSecret(value)
				} else {
					newebpayConfig[key] = value
				}
			}
		}
	}
	result["newebpay"] = map[string]interface{}{
		"enabled": newebpayEnabled,
		"config":  newebpayConfig,
	}

	return result
}

// CreatePayment creates a payment order using the specified gateway
func (s *Service) CreatePayment(ctx context.Context, gatewayName string, req CreateOrderRequest) (*CreateOrderResponse, error) {
	gateway, err := s.GetGateway(gatewayName)
	if err != nil {
		return nil, err
	}

	return gateway.CreateOrder(ctx, req)
}

// HandleCallback handles payment callback from gateway
func (s *Service) HandleCallback(ctx context.Context, gatewayName string, data map[string]interface{}) (*CallbackData, error) {
	gateway, err := s.GetGateway(gatewayName)
	if err != nil {
		return nil, err
	}

	return gateway.VerifyCallback(ctx, data)
}

func (s *Service) option(key string) (string, bool, error) {
	if s.db == nil {
		return "", false, errors.New("payment: database is not initialized")
	}
	var option core.Option
	err := s.db.Where("key = ?", key).First(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return option.Value, true, nil
}

func (s *Service) ensureSandboxSecret() (string, error) {
	value, found, err := s.option("payment_sandbox_secret")
	if err != nil {
		return "", err
	}
	if found {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 32 {
			return "", errors.New("payment: sandbox signing key must be 32 bytes of hex")
		}
		return value, nil
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("payment: generate sandbox signing key: %w", err)
	}
	value = hex.EncodeToString(secret)
	if err := s.db.Create(&core.Option{Key: "payment_sandbox_secret", Value: value}).Error; err != nil {
		// A concurrent request may have generated the site's key first. Read
		// that value and use it; never replace a key that may sign callbacks.
		stored, ok, readErr := s.option("payment_sandbox_secret")
		if readErr == nil && ok {
			decoded, decodeErr := hex.DecodeString(stored)
			if decodeErr == nil && len(decoded) == 32 {
				return stored, nil
			}
		}
		return "", fmt.Errorf("payment: persist sandbox signing key: %w", err)
	}
	return value, nil
}

func maskSecret(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 4 {
		return "****" + secret
	}
	return "****" + secret[len(secret)-4:]
}
