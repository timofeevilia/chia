package ledger

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestSimplifyExample(t *testing.T) {
	got := Simplify([]Balance{{"an", 700}, {"binh", -200}, {"chi", -500}})
	want := []Transfer{{"chi", "an", 500}, {"binh", "an", 200}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v want %v", got, want)
	}
	if n := len(Simplify([]Balance{{"a", 0}, {"b", 0}})); n != 0 {
		t.Errorf("settled group produced %d transfers", n)
	}
}

// Applying the suggested transfers must zero every balance in at most n-1 payments.
func TestSimplifySettlesEveryone(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 2000; i++ {
		n := 2 + rng.Intn(12)
		bal := make([]Balance, n)
		var sum int64
		for j := 0; j < n-1; j++ {
			bal[j] = Balance{fmt.Sprint("m", j), rng.Int63n(2_000_000) - 1_000_000}
			sum += bal[j].Amount
		}
		bal[n-1] = Balance{fmt.Sprint("m", n-1), -sum}

		net := map[string]int64{}
		for _, b := range bal {
			net[b.Member] = b.Amount
		}
		transfers := Simplify(bal)
		if len(transfers) > n-1 {
			t.Fatalf("%d transfers for %d members", len(transfers), n)
		}
		for _, tr := range transfers {
			if tr.Amount <= 0 {
				t.Fatalf("non-positive transfer %v", tr)
			}
			net[tr.From] += tr.Amount
			net[tr.To] -= tr.Amount
		}
		for m, v := range net {
			if v != 0 {
				t.Fatalf("member %s left with %d", m, v)
			}
		}
	}
}
