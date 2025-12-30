package payment

import (
	"context"
	"fmt"
	"os"
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

	service.ReloadGateways()
	return service
}

// ReloadGateways reloads gateway configurations from database
func (s *Service) ReloadGateways() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.gateways = make(map[string]PaymentGateway)
	s.defaultGateway = nil

	// Load ECPay config from database
	if ecpayConfig := s.loadECPayConfigFromDB(); ecpayConfig != nil {
		ecpay := NewECPayGateway(ecpayConfig)
		s.gateways["ecpay"] = ecpay
		if s.defaultGateway == nil {
			s.defaultGateway = ecpay
		}
	}

	// Load NewebPay config from database
	if newebpayConfig := s.loadNewebPayConfigFromDB(); newebpayConfig != nil {
		newebpay := NewNewebPayGateway(newebpayConfig)
		s.gateways["newebpay"] = newebpay
		if s.defaultGateway == nil {
			s.defaultGateway = newebpay
		}
	}
}

// GetGateway returns a payment gateway by name
func (s *Service) GetGateway(name string) (PaymentGateway, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

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
	return s.defaultGateway
}

// loadECPayConfigFromDB loads ECPay configuration from database
func (s *Service) loadECPayConfigFromDB() *ECPayConfig {
	var opt core.Option
	if err := s.db.Where("key = ?", "payment_ecpay_enabled").First(&opt).Error; err != nil {
		return nil
	}
	if opt.Value != "true" {
		return nil
	}

	config := &ECPayConfig{}

	// Load merchant ID
	if err := s.db.Where("key = ?", "payment_ecpay_merchant_id").First(&opt).Error; err != nil {
		return nil
	}
	config.MerchantID = opt.Value

	// Load hash key
	if err := s.db.Where("key = ?", "payment_ecpay_hash_key").First(&opt).Error; err != nil {
		return nil
	}
	config.HashKey = opt.Value

	// Load hash IV
	if err := s.db.Where("key = ?", "payment_ecpay_hash_iv").First(&opt).Error; err != nil {
		return nil
	}
	config.HashIV = opt.Value

	// Load test mode
	if err := s.db.Where("key = ?", "payment_ecpay_test_mode").First(&opt).Error; err == nil {
		config.IsTestMode = opt.Value == "true"
	}

	// Build URLs
	config.ReturnURL = s.appURL + "/api/payment/ecpay/return"
	config.NotifyURL = s.appURL + "/api/payment/ecpay/callback"

	return config
}

// loadNewebPayConfigFromDB loads NewebPay configuration from database
func (s *Service) loadNewebPayConfigFromDB() *NewebPayConfig {
	var opt core.Option
	if err := s.db.Where("key = ?", "payment_newebpay_enabled").First(&opt).Error; err != nil {
		return nil
	}
	if opt.Value != "true" {
		return nil
	}

	config := &NewebPayConfig{}

	// Load merchant ID
	if err := s.db.Where("key = ?", "payment_newebpay_merchant_id").First(&opt).Error; err != nil {
		return nil
	}
	config.MerchantID = opt.Value

	// Load hash key
	if err := s.db.Where("key = ?", "payment_newebpay_hash_key").First(&opt).Error; err != nil {
		return nil
	}
	config.HashKey = opt.Value

	// Load hash IV
	if err := s.db.Where("key = ?", "payment_newebpay_hash_iv").First(&opt).Error; err != nil {
		return nil
	}
	config.HashIV = opt.Value

	// Load test mode
	if err := s.db.Where("key = ?", "payment_newebpay_test_mode").First(&opt).Error; err == nil {
		config.IsTestMode = opt.Value == "true"
	}

	// Build URLs
	config.ReturnURL = s.appURL + "/api/payment/newebpay/return"
	config.NotifyURL = s.appURL + "/api/payment/newebpay/callback"

	return config
}

// SaveECPayConfig saves ECPay configuration to database
func (s *Service) SaveECPayConfig(config *ECPayConfig, enabled bool) error {
	settings := map[string]string{
		"payment_ecpay_enabled":     fmt.Sprintf("%v", enabled),
		"payment_ecpay_merchant_id": config.MerchantID,
		"payment_ecpay_hash_key":    config.HashKey,
		"payment_ecpay_hash_iv":     config.HashIV,
		"payment_ecpay_test_mode":   fmt.Sprintf("%v", config.IsTestMode),
	}

	for k, v := range settings {
		var opt core.Option
		result := s.db.Where("key = ?", k).First(&opt)
		if result.Error != nil {
			opt = core.Option{Key: k, Value: v}
		} else {
			opt.Value = v
		}
		if err := s.db.Save(&opt).Error; err != nil {
			return err
		}
	}

	// Reload gateways
	s.ReloadGateways()
	return nil
}

// SaveNewebPayConfig saves NewebPay configuration to database
func (s *Service) SaveNewebPayConfig(config *NewebPayConfig, enabled bool) error {
	settings := map[string]string{
		"payment_newebpay_enabled":     fmt.Sprintf("%v", enabled),
		"payment_newebpay_merchant_id": config.MerchantID,
		"payment_newebpay_hash_key":    config.HashKey,
		"payment_newebpay_hash_iv":     config.HashIV,
		"payment_newebpay_test_mode":   fmt.Sprintf("%v", config.IsTestMode),
	}

	for k, v := range settings {
		var opt core.Option
		result := s.db.Where("key = ?", k).First(&opt)
		if result.Error != nil {
			opt = core.Option{Key: k, Value: v}
		} else {
			opt.Value = v
		}
		if err := s.db.Save(&opt).Error; err != nil {
			return err
		}
	}

	// Reload gateways
	s.ReloadGateways()
	return nil
}

// GetPaymentConfigs returns all payment gateway configurations
func (s *Service) GetPaymentConfigs() map[string]interface{} {
	result := make(map[string]interface{})

	// ECPay config
	var opt core.Option
	ecpayEnabled := false
	if err := s.db.Where("key = ?", "payment_ecpay_enabled").First(&opt).Error; err == nil {
		ecpayEnabled = opt.Value == "true"
	}

	ecpayConfig := make(map[string]string)
	if ecpayEnabled {
		keys := []string{"payment_ecpay_merchant_id", "payment_ecpay_hash_key", "payment_ecpay_hash_iv", "payment_ecpay_test_mode"}
		for _, key := range keys {
			if err := s.db.Where("key = ?", key).First(&opt).Error; err == nil {
				ecpayConfig[key] = opt.Value
			}
		}
	}
	result["ecpay"] = map[string]interface{}{
		"enabled": ecpayEnabled,
		"config":  ecpayConfig,
	}

	// NewebPay config
	newebpayEnabled := false
	if err := s.db.Where("key = ?", "payment_newebpay_enabled").First(&opt).Error; err == nil {
		newebpayEnabled = opt.Value == "true"
	}

	newebpayConfig := make(map[string]string)
	if newebpayEnabled {
		keys := []string{"payment_newebpay_merchant_id", "payment_newebpay_hash_key", "payment_newebpay_hash_iv", "payment_newebpay_test_mode"}
		for _, key := range keys {
			if err := s.db.Where("key = ?", key).First(&opt).Error; err == nil {
				newebpayConfig[key] = opt.Value
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
