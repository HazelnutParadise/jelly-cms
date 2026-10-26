package payment

import (
	"context"
	"testing"
)

func TestECPayOfficialCreateCheckMacValue(t *testing.T) {
	g := NewECPayGateway(&ECPayConfig{HashKey: "pwFHCqoQZGmho4w6", HashIV: "EkRm7iFT261dpevs"})
	got := g.generateCheckMacValue(map[string]string{
		"MerchantID":        "3002607",
		"MerchantTradeNo":   "ecpay20230312153023",
		"MerchantTradeDate": "2023/03/12 15:30:23",
		"PaymentType":       "aio",
		"TotalAmount":       "30000",
		"TradeDesc":         "促銷方案",
		"ItemName":          "Apple iphone 15",
		"ReturnURL":         "https://www.ecpay.com.tw/receive.php",
		"ChoosePayment":     "ALL",
		"EncryptType":       "1",
	})
	const want = "6C51C9E6888DE861FD62FB1DD17029FC742634498FD813DC43D4243B5685B840"
	if got != want {
		t.Fatalf("official ECPay create-order CheckMacValue = %s, want %s", got, want)
	}
}

func TestECPayCheckMacValueMixedCaseKeysAndEncoding(t *testing.T) {
	g := NewECPayGateway(&ECPayConfig{HashKey: "test-key", HashIV: "test-iv"})
	got := g.generateCheckMacValue(map[string]string{
		"beta":  "two words",
		"Alpha": "~!*()+&",
	})
	const want = "EFEED4F6042EBD6B5DC9D2A56638F0CCDE1AA1FCCA2A65196ACE6EF2F1F85153"
	if got != want {
		t.Fatalf("mixed-case ECPay CheckMacValue = %s, want %s", got, want)
	}
}

func TestECPayOfficialCallbackVectorConvertsAmountToMinorUnits(t *testing.T) {
	g := NewECPayGateway(&ECPayConfig{HashKey: "pwFHCqoQZGmho4w6", HashIV: "EkRm7iFT261dpevs"})
	data := map[string]interface{}{
		"CustomField1": "", "CustomField2": "", "CustomField3": "", "CustomField4": "",
		"MerchantID": "3002607", "MerchantTradeNo": "D9RMXNrihUYM", "PaymentDate": "2024/12/31 12:26:09",
		"PaymentType": "Credit_CreditCard", "PaymentTypeChargeFee": "10", "RtnCode": "1", "RtnMsg": "交易成功",
		"SimulatePaid": "0", "StoreID": "", "TradeAmt": "402", "TradeDate": "2024/12/31 12:25:43",
		"TradeNo":       "2412311225437371",
		"CheckMacValue": "85D927637935683EA756CDEF76498FEB9F5D098A7A1AC4F0CB3B3609A9D4116A",
	}
	cb, err := g.VerifyCallback(context.Background(), data)
	if err != nil {
		t.Fatalf("VerifyCallback(official response): %v", err)
	}
	if cb.Amount != 40200 || cb.Currency != "TWD" || cb.Status != PaymentStatusPaid {
		t.Errorf("callback amount/currency/status = %d/%s/%s, want 40200/TWD/paid", cb.Amount, cb.Currency, cb.Status)
	}
}

func TestECPayCallbackRejectsInvalidSignatureAndAmount(t *testing.T) {
	g := NewECPayGateway(&ECPayConfig{HashKey: "pwFHCqoQZGmho4w6", HashIV: "EkRm7iFT261dpevs"})
	base := map[string]interface{}{
		"MerchantID": "3002607", "MerchantTradeNo": "D9RMXNrihUYM", "RtnCode": "1", "TradeAmt": "402",
		"TradeNo": "2412311225437371", "PaymentDate": "2024/12/31 12:26:09", "SimulatePaid": "0",
	}
	valid := func(overrides map[string]interface{}) map[string]interface{} {
		data := make(map[string]interface{}, len(base)+len(overrides)+1)
		for k, v := range base {
			data[k] = v
		}
		for k, v := range overrides {
			data[k] = v
		}
		strings := make(map[string]string, len(data))
		for k, v := range data {
			strings[k] = v.(string)
		}
		data["CheckMacValue"] = g.generateCheckMacValue(strings)
		return data
	}
	for _, tc := range []struct {
		name string
		data map[string]interface{}
	}{
		{"wrong signature", func() map[string]interface{} { d := valid(nil); d["CheckMacValue"] = "bad"; return d }()},
		{"missing signature", func() map[string]interface{} { d := valid(nil); delete(d, "CheckMacValue"); return d }()},
		{"missing amount", func() map[string]interface{} {
			d := valid(nil)
			delete(d, "TradeAmt")
			d["CheckMacValue"] = g.generateCheckMacValue(map[string]string{"MerchantID": "3002607", "MerchantTradeNo": "D9RMXNrihUYM", "RtnCode": "1", "TradeNo": "2412311225437371", "PaymentDate": "2024/12/31 12:26:09", "SimulatePaid": "0"})
			return d
		}()},
		{"invalid amount", func() map[string]interface{} { return valid(map[string]interface{}{"TradeAmt": "402x"}) }()},
		{"overflow amount", func() map[string]interface{} { return valid(map[string]interface{}{"TradeAmt": "92233720368547759"}) }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := g.VerifyCallback(context.Background(), tc.data); err == nil {
				t.Errorf("VerifyCallback accepted %s", tc.name)
			}
		})
	}
}

func TestECPayCallbackAcceptsMaximumWholeAmountWithoutOverflow(t *testing.T) {
	g := NewECPayGateway(&ECPayConfig{HashKey: "pwFHCqoQZGmho4w6", HashIV: "EkRm7iFT261dpevs"})
	data := map[string]interface{}{
		"MerchantID": "3002607", "MerchantTradeNo": "D9RMXNrihUYM", "RtnCode": "1",
		"TradeAmt": "92233720368547758", "TradeNo": "2412311225437371",
		"PaymentDate": "2024/12/31 12:26:09", "SimulatePaid": "0",
	}
	values := make(map[string]string, len(data))
	for key, value := range data {
		values[key] = value.(string)
	}
	data["CheckMacValue"] = g.generateCheckMacValue(values)

	cb, err := g.VerifyCallback(context.Background(), data)
	if err != nil {
		t.Fatalf("VerifyCallback(maximum non-overflowing amount): %v", err)
	}
	const want int64 = 9223372036854775800
	if cb.Amount != want || cb.Currency != "TWD" || cb.Status != PaymentStatusPaid {
		t.Errorf("callback amount/currency/status = %d/%s/%s, want %d/TWD/paid", cb.Amount, cb.Currency, cb.Status, want)
	}
}

func TestECPayNonFinalAndSimulatedCallbacksRemainPending(t *testing.T) {
	g := NewECPayGateway(&ECPayConfig{HashKey: "key", HashIV: "iv"})
	for _, tc := range []struct{ name, rtnCode, paymentType, simulate string }{
		{"ATM number issued", "2", "ATM_TAISHIN", "0"},
		{"CVS number issued", "10100073", "CVS", "0"},
		{"simulated paid", "1", "Credit_CreditCard", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := map[string]interface{}{
				"MerchantID": "3002607", "MerchantTradeNo": "D9RMXNrihUYM", "RtnCode": tc.rtnCode,
				"TradeAmt": "402", "TradeNo": "2412311225437371", "PaymentDate": "2024/12/31 12:26:09",
				"PaymentType": tc.paymentType, "SimulatePaid": tc.simulate,
			}
			values := make(map[string]string, len(data))
			for k, v := range data {
				values[k] = v.(string)
			}
			data["CheckMacValue"] = g.generateCheckMacValue(values)
			cb, err := g.VerifyCallback(context.Background(), data)
			if err != nil {
				t.Fatalf("VerifyCallback: %v", err)
			}
			if cb.Status != PaymentStatusPending {
				t.Errorf("status = %q, want pending", cb.Status)
			}
		})
	}
}

func TestECPayUnsupportedQueryAndRefundDoNotReportSuccess(t *testing.T) {
	g := NewECPayGateway(&ECPayConfig{})
	if result, err := g.QueryOrder(context.Background(), "trade"); err != ErrNotSupported || result != nil {
		t.Errorf("QueryOrder returned result=%+v err=%v, want nil and ErrNotSupported", result, err)
	}
	if result, err := g.Refund(context.Background(), RefundRequest{}); err != ErrNotSupported || result != nil {
		t.Errorf("Refund returned result=%+v err=%v, want nil and ErrNotSupported", result, err)
	}
}
