package notify

import (
	"encoding/json"
	"net/mail"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"gorm.io/gorm"
)

// Ticket 07 shop notification kinds
const (
	KindOrderConfirmation        = "order.confirmation"
	KindOrderShipped             = "order.shipped"
	KindOrderLookupLink          = "order.lookup_link"
	KindOrderOfflineInstructions = "order.offline_instructions"
	KindCustomerSetPassword      = "customer.set_password"
	KindOrderRefunded            = "order.refunded"
)

func init() {
	registerShopKinds()
}

func registerShopKinds() {
	kinds[KindOrderConfirmation] = KindSpec{Kind: KindOrderConfirmation, Category: CategoryOrders, Preference: ""}
	kinds[KindOrderShipped] = KindSpec{Kind: KindOrderShipped, Category: CategoryOrders, Preference: ""}
	kinds[KindOrderLookupLink] = KindSpec{Kind: KindOrderLookupLink, Category: CategoryOrders, Preference: ""}
	kinds[KindOrderOfflineInstructions] = KindSpec{Kind: KindOrderOfflineInstructions, Category: CategoryOrders, Preference: ""}
	kinds[KindCustomerSetPassword] = KindSpec{Kind: KindCustomerSetPassword, Category: CategorySecurity, Preference: ""}
	kinds[KindOrderRefunded] = KindSpec{Kind: KindOrderRefunded, Category: CategoryOrders, Preference: KindOrderPaymentFailed}
}

// isRequiredEmailKind returns true if email must be sent regardless of user preferences
func isRequiredEmailKind(kind string) bool {
	switch kind {
	case KindOrderConfirmation, KindOrderShipped, KindOrderLookupLink, KindOrderOfflineInstructions, KindCustomerSetPassword:
		return true
	default:
		return false
	}
}

func normalizeEmail(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	addr, err := mail.ParseAddress(s)
	if err != nil {
		return "", err
	}
	return strings.ToLower(addr.Address), nil
}

func dedupeNormalizedEmails(emails []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(emails))
	for _, e := range emails {
		ne, err := normalizeEmail(e)
		if err != nil || ne == "" {
			continue
		}
		if _, ok := seen[ne]; ok {
			continue
		}
		seen[ne] = struct{}{}
		result = append(result, ne)
	}
	return result
}

// queueGuestEmails queues email deliveries for guest addresses with no user notification row.
// All errors are returned.
func queueGuestEmails(tx *gorm.DB, kind string, params map[string]any, emails []string, now time.Time) error {
	emails = dedupeNormalizedEmails(emails)
	if len(emails) == 0 {
		return nil
	}
	var paramsJSON []byte
	var err error
	if params == nil {
		paramsJSON = []byte("{}")
	} else {
		paramsJSON, err = json.Marshal(params)
		if err != nil {
			return err
		}
	}
	for _, addr := range emails {
		if err := tx.Model(&core.NotificationDelivery{}).Create(map[string]interface{}{
			"notification_id": nil,
			"kind":            kind,
			"to_address":      addr,
			"channel":         "email",
			"status":          "pending",
			"attempts":        0,
			"next_attempt_at": now,
			"error":           "",
			"params":          paramsJSON,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func queueRequiredCustomerEmail(tx *gorm.DB, user core.User, kind string, params []byte, held bool, now time.Time) error {
	status := "pending"
	if held {
		status = "held"
	}
	if err := tx.Model(&core.NotificationDelivery{}).Create(map[string]interface{}{
		"notification_id": nil,
		"kind":            kind,
		"to_address":      user.Email,
		"channel":         "email",
		"status":          status,
		"attempts":        0,
		"next_attempt_at": now,
		"error":           "",
		"params":          params,
	}).Error; err != nil {
		return err
	}
	return nil
}
