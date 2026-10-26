package server

import (
	"errors"
	"math"

	"github.com/HazelnutParadise/jelly-cms/internal/money"
)

func shopCalculateTotal(subtotal, shipping, discount int64) (total, adjustment int64, err error) {
	if subtotal < 0 || shipping < 0 || discount < 0 || discount > subtotal {
		return 0, 0, errors.New("invalid shop amount")
	}
	net := subtotal - discount
	if net > math.MaxInt64-shipping {
		return 0, 0, errors.New("shop amount overflow")
	}
	unrounded := net + shipping
	total, err = money.RoundToWhole(unrounded)
	if err != nil {
		return 0, 0, err
	}
	return total, total - unrounded, nil
}
