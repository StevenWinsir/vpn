// Package payment is the adapter for the external payment gateway described in
// 支付接入技术文档.md (handoff_payment_account.md 6). It builds signed submit URLs,
// queries order status and parses callbacks. It holds no business rules: whether a
// callback may fulfil an order is decided by the API layer.
package payment

import (
	"errors"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// MaxAmountCents bounds every amount handled here (10 million yuan).
const MaxAmountCents int64 = 1_000_000_000

var amountText = regexp.MustCompile(`^(0|[1-9][0-9]{0,7})(\.[0-9]{1,2})?$`)

// floatText is a plain JSON number without sign: digits, optional fraction and a small
// non-negative exponent. Hex, fractions and signs never reach big.Rat.
var floatText = regexp.MustCompile(`^[0-9]{1,10}(\.[0-9]{1,20})?([eE][0-9]{1,2})?$`)

var ErrAmount = errors.New("invalid amount")

// ParseAmount converts a decimal yuan string ("12", "12.5", "12.00") into cents
// without floating point. Signs, exponents, more than two decimals, leading zeros and
// values above MaxAmountCents are rejected.
func ParseAmount(text string) (int64, error) {
	if !amountText.MatchString(text) {
		return 0, ErrAmount
	}
	whole, fraction, _ := strings.Cut(text, ".")
	yuan, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, ErrAmount
	}
	fraction = (fraction + "00")[:2]
	cents, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return 0, ErrAmount
	}
	total := yuan*100 + cents
	if total > MaxAmountCents {
		return 0, ErrAmount
	}
	return total, nil
}

// FormatAmount renders cents as the gateway's money format: two decimals, no grouping.
func FormatAmount(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	fraction := strconv.FormatInt(cents%100, 10)
	if len(fraction) == 1 {
		fraction = "0" + fraction
	}
	return sign + strconv.FormatInt(cents/100, 10) + "." + fraction
}

// callbackEpsilon is how far a callback amount may sit from a whole cent and still be
// read as that cent: the gateway serialises money as a float (FAQ Q7), so 20.01 can
// arrive as 20.009999999999998.
var callbackEpsilon = big.NewRat(1, 1_000_000)

// ParseCallbackAmount converts a callback money value (a JSON number's text) into
// cents. Exact decimals with up to two places are taken as is; longer float
// renderings are rounded only when they lie within one millionth of a cent, so 20.01
// written as 20.009999999999998 is 2001 while 20.004 is rejected.
func ParseCallbackAmount(text string) (int64, error) {
	if cents, err := ParseAmount(text); err == nil {
		if cents <= 0 {
			return 0, ErrAmount
		}
		return cents, nil
	}
	if len(text) > 32 || !floatText.MatchString(text) {
		return 0, ErrAmount
	}
	value, ok := new(big.Rat).SetString(text)
	if !ok || value.Sign() <= 0 {
		return 0, ErrAmount
	}
	scaled := new(big.Rat).Mul(value, big.NewRat(100, 1))
	nearest := new(big.Int).Quo(new(big.Int).Add(new(big.Int).Mul(scaled.Num(), big.NewInt(2)), scaled.Denom()), new(big.Int).Mul(scaled.Denom(), big.NewInt(2)))
	diff := new(big.Rat).Sub(scaled, new(big.Rat).SetInt(nearest))
	if diff.Abs(diff).Cmp(callbackEpsilon) > 0 || !nearest.IsInt64() || nearest.Int64() <= 0 || nearest.Int64() > MaxAmountCents {
		return 0, ErrAmount
	}
	return nearest.Int64(), nil
}
