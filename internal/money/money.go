// Package money holds the integer arithmetic every amount in Chia goes through.
// Amounts are int64 minor units; conversions use exact rationals and round once.
package money

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Currency describes an ISO 4217 currency.
type Currency struct {
	Code     string
	Exponent int // decimal places of the minor unit
	Name     string
}

var currencies = []Currency{
	{"AUD", 2, "Australian Dollar"},
	{"CNY", 2, "Chinese Yuan"},
	{"EUR", 2, "Euro"},
	{"GBP", 2, "Pound Sterling"},
	{"HKD", 2, "Hong Kong Dollar"},
	{"IDR", 2, "Indonesian Rupiah"},
	{"JPY", 0, "Japanese Yen"},
	{"KRW", 0, "South Korean Won"},
	{"MYR", 2, "Malaysian Ringgit"},
	{"PHP", 2, "Philippine Peso"},
	{"SGD", 2, "Singapore Dollar"},
	{"THB", 2, "Thai Baht"},
	{"USD", 2, "US Dollar"},
	{"VND", 0, "Vietnamese Dong"},
}

var byCode = func() map[string]Currency {
	m := make(map[string]Currency, len(currencies))
	for _, c := range currencies {
		m[c.Code] = c
	}
	return m
}()

// Currencies returns the supported currencies sorted by code.
func Currencies() []Currency { return append([]Currency(nil), currencies...) }

// Lookup finds a currency by its upper-case code.
func Lookup(code string) (Currency, bool) {
	c, ok := byCode[code]
	return c, ok
}

// MaxAmount bounds a single amount so sums of many postings cannot overflow int64.
const MaxAmount = int64(1_000_000_000_000_000)

var (
	ErrInvalidRate = errors.New("money: rate must be a positive decimal")
	ErrOverflow    = errors.New("money: amount out of range")
	ErrNoShares    = errors.New("money: at least one positive weight is required")
)

// ParseRate parses a positive decimal string such as "19450.5" exactly.
func ParseRate(s string) (*big.Rat, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 40 || strings.ContainsAny(s, "eE/_+- ") {
		return nil, ErrInvalidRate
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok || r.Sign() <= 0 {
		return nil, ErrInvalidRate
	}
	return r, nil
}

// RateScale is the number of decimal places a stored rate keeps.
const RateScale = 18

// QuantizeRate rounds a rate to RateScale places. Conversions use the quantized
// value, so the rate stored with a transaction reproduces its amount exactly.
func QuantizeRate(r *big.Rat) (*big.Rat, error) {
	q, ok := new(big.Rat).SetString(r.FloatString(RateScale))
	if !ok || q.Sign() <= 0 {
		return nil, ErrInvalidRate
	}
	return q, nil
}

// FormatRate renders a rate with up to RateScale decimal places and no trailing zeros.
func FormatRate(r *big.Rat) string {
	s := r.FloatString(RateScale)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// Convert turns amount (minor units of from) into minor units of to, where rate is
// units of to per one unit of from in major units. It rounds half to even, the
// convention that does not drift when many conversions are summed.
func Convert(amount int64, from, to Currency, rate *big.Rat) (int64, error) {
	if rate == nil || rate.Sign() <= 0 {
		return 0, ErrInvalidRate
	}
	v := new(big.Rat).SetInt64(amount)
	v.Mul(v, rate)
	// Move between minor-unit scales: multiply by 10^(to.Exponent - from.Exponent).
	if d := to.Exponent - from.Exponent; d > 0 {
		v.Mul(v, new(big.Rat).SetInt(pow10(d)))
	} else if d < 0 {
		v.Quo(v, new(big.Rat).SetInt(pow10(-d)))
	}
	n := roundHalfEven(v)
	if !n.IsInt64() || abs(n.Int64()) > MaxAmount {
		return 0, ErrOverflow
	}
	return n.Int64(), nil
}

// Allocate divides total between weights so that the parts sum to total exactly.
// Each part gets floor(total*w/sum); the leftover units go to the parts with the
// largest remainders, earlier positions winning ties (largest-remainder method).
func Allocate(total int64, weights []int64) ([]int64, error) {
	if total < 0 {
		return nil, ErrOverflow
	}
	var sum int64
	for _, w := range weights {
		if w < 0 {
			return nil, ErrNoShares
		}
		sum += w
	}
	if sum <= 0 {
		return nil, ErrNoShares
	}
	type rem struct {
		idx int
		r   *big.Int
	}
	parts := make([]int64, len(weights))
	rems := make([]rem, len(weights))
	bigSum := big.NewInt(sum)
	left := total
	for i, w := range weights {
		p := new(big.Int).Mul(big.NewInt(total), big.NewInt(w))
		q, r := new(big.Int).QuoRem(p, bigSum, new(big.Int))
		parts[i] = q.Int64()
		left -= parts[i]
		rems[i] = rem{i, r}
	}
	sort.SliceStable(rems, func(a, b int) bool { return rems[a].r.Cmp(rems[b].r) > 0 })
	for i := 0; left > 0; i++ {
		parts[rems[i].idx]++
		left--
	}
	return parts, nil
}

// Format renders minor units as a plain decimal in major units, e.g. 1234 SGD -> "12.34".
// This is the form EMVCo QR payloads expect.
func Format(amount int64, c Currency) string {
	if c.Exponent == 0 {
		return fmt.Sprintf("%d", amount)
	}
	div := pow10(c.Exponent).Int64()
	return fmt.Sprintf("%d.%0*d", amount/div, c.Exponent, amount%div)
}

func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func roundHalfEven(v *big.Rat) *big.Int {
	q, r := new(big.Int).QuoRem(v.Num(), v.Denom(), new(big.Int))
	// v is non-negative here; compare 2r with the denominator.
	twice := new(big.Int).Lsh(r, 1)
	switch twice.Cmp(v.Denom()) {
	case 1:
		q.Add(q, big.NewInt(1))
	case 0:
		if q.Bit(0) == 1 {
			q.Add(q, big.NewInt(1))
		}
	}
	return q
}
