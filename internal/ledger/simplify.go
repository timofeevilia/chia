// Package ledger contains the pure accounting logic that sits on top of postings.
package ledger

import "sort"

// Balance is a member's net position: positive is owed money, negative owes money.
type Balance struct {
	Member string
	Amount int64
}

// Transfer is a suggested payment.
type Transfer struct {
	From, To string
	Amount   int64
}

// Simplify returns payments that bring every balance to zero. It repeatedly matches
// the largest debtor with the largest creditor, which needs at most n-1 payments.
// (The true minimum is NP-hard to find; this bound is what users care about.)
// Balances must sum to zero, which the ledger guarantees.
func Simplify(balances []Balance) []Transfer {
	var debtors, creditors []Balance
	for _, b := range balances {
		switch {
		case b.Amount < 0:
			debtors = append(debtors, Balance{b.Member, -b.Amount})
		case b.Amount > 0:
			creditors = append(creditors, b)
		}
	}
	// Sort by amount descending, then by member for a deterministic result.
	order := func(s []Balance) {
		sort.Slice(s, func(i, j int) bool {
			if s[i].Amount != s[j].Amount {
				return s[i].Amount > s[j].Amount
			}
			return s[i].Member < s[j].Member
		})
	}
	transfers := []Transfer{}
	for len(debtors) > 0 && len(creditors) > 0 {
		order(debtors)
		order(creditors)
		d, c := &debtors[0], &creditors[0]
		amt := min(d.Amount, c.Amount)
		transfers = append(transfers, Transfer{From: d.Member, To: c.Member, Amount: amt})
		d.Amount -= amt
		c.Amount -= amt
		if d.Amount == 0 {
			debtors = debtors[1:]
		}
		if c.Amount == 0 {
			creditors = creditors[1:]
		}
	}
	return transfers
}
