package money

import (
	"math/big"
	"math/rand"
	"testing"
)

func cur(t *testing.T, code string) Currency {
	t.Helper()
	c, ok := Lookup(code)
	if !ok {
		t.Fatalf("unknown currency %s", code)
	}
	return c
}

func TestConvert(t *testing.T) {
	cases := []struct {
		amount   int64
		from, to string
		rate     string
		want     int64
	}{
		{1234, "SGD", "SGD", "1", 1234},
		{1000, "SGD", "VND", "19450.5", 194505},   // 10.00 SGD -> 194 505 VND
		{500000, "VND", "SGD", "0.0000514", 2570}, // 500 000 VND -> 25.70 SGD
		{1, "VND", "SGD", "0.0000514", 0},         // rounds to nothing
		{25, "SGD", "JPY", "110", 28},             // 0.25 SGD * 110 = 27.5 -> half-even 28
		{5, "SGD", "JPY", "50", 2},                // 2.5 -> half-even 2
		{15, "SGD", "JPY", "50", 8},               // 7.5 -> half-even 8
		{100, "USD", "SGD", "1.3456", 135},        // 134.56 -> 135
	}
	for _, c := range cases {
		r, err := ParseRate(c.rate)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Convert(c.amount, cur(t, c.from), cur(t, c.to), r)
		if err != nil || got != c.want {
			t.Errorf("Convert(%d %s->%s @%s) = %d, %v; want %d", c.amount, c.from, c.to, c.rate, got, err, c.want)
		}
	}
}

func TestParseRateRejects(t *testing.T) {
	for _, s := range []string{"", "0", "-1", "1e5", "1/3", "abc", "+2", " "} {
		if _, err := ParseRate(s); err == nil {
			t.Errorf("ParseRate(%q) accepted", s)
		}
	}
}

func TestFormatRate(t *testing.T) {
	r, _ := ParseRate("19450.5000")
	if got := FormatRate(r); got != "19450.5" {
		t.Errorf("got %s", got)
	}
	r, _ = ParseRate("2")
	if got := FormatRate(r); got != "2" {
		t.Errorf("got %s", got)
	}
}

func TestAllocate(t *testing.T) {
	got, _ := Allocate(100, []int64{1, 1, 1})
	if got[0] != 34 || got[1] != 33 || got[2] != 33 {
		t.Errorf("equal split = %v", got)
	}
	got, _ = Allocate(1000, []int64{2, 1, 1})
	if got[0] != 500 || got[1] != 250 || got[2] != 250 {
		t.Errorf("weighted split = %v", got)
	}
	if _, err := Allocate(10, []int64{0, 0}); err == nil {
		t.Error("zero weights accepted")
	}
}

// The allocation must never create or destroy a minor unit, whatever the inputs.
func TestAllocateConservesTotal(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		total := rng.Int63n(MaxAmount)
		weights := make([]int64, 1+rng.Intn(20))
		for j := range weights {
			weights[j] = 1 + rng.Int63n(1000)
		}
		parts, err := Allocate(total, weights)
		if err != nil {
			t.Fatal(err)
		}
		var sum int64
		for _, p := range parts {
			if p < 0 {
				t.Fatalf("negative part %d", p)
			}
			sum += p
		}
		if sum != total {
			t.Fatalf("total %d weights %v: parts sum to %d", total, weights, sum)
		}
	}
}

func TestFormat(t *testing.T) {
	if got := Format(1205, cur(t, "SGD")); got != "12.05" {
		t.Errorf("got %s", got)
	}
	if got := Format(50000, cur(t, "VND")); got != "50000" {
		t.Errorf("got %s", got)
	}
}

// A stored (quantized) rate must reproduce the converted amount on its own.
func TestQuantizedRateIsReproducible(t *testing.T) {
	raw, _ := ParseRate("1.3")
	raw.Quo(raw, mustRate(t, "25400")) // SGD per VND, a repeating decimal
	q, err := QuantizeRate(raw)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := Convert(1_000_000_000, cur(t, "VND"), cur(t, "SGD"), q)
	again, _ := ParseRate(FormatRate(q))
	b, _ := Convert(1_000_000_000, cur(t, "VND"), cur(t, "SGD"), again)
	if a != b {
		t.Fatalf("stored rate gives %d, original gave %d", b, a)
	}
}

func mustRate(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, err := ParseRate(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
