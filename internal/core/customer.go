package core

import "gorm.io/gorm"

// EnsureCustomer finds the user with this email, or creates one with
// role=customer, status=invited and provider=local when none exists. A user
// that already exists is returned unchanged: its role, password and status are
// never modified here.
//
// The customer account is dormant (status invited, no password) when the guest
// did not tick "create account"; it only links orders to the email.
//
// Skeleton: not implemented yet (ticket 07, E5). The status field arrives with
// ticket 06; until then this stub only exists for the call sites.
func EnsureCustomer(tx *gorm.DB, email, phone string) (User, error) {
	return User{}, ErrNotImplemented
}
