// Client-side mirrors of two server rules, used only for previews and the
// settlement diagram. The server remains the source of truth.
import type { Transaction } from './api.ts';

/**
 * Largest-remainder allocation, the same rule the API applies: parts always
 * sum to `total`, leftover minor units go to the largest remainders first.
 */
export function allocate(total: number, weights: number[]): number[] {
	const sum = weights.reduce((a, b) => a + b, 0);
	if (sum <= 0 || total < 0) return weights.map(() => 0);
	const t = BigInt(total);
	const s = BigInt(sum);
	const parts = weights.map((w) => Number((t * BigInt(w)) / s));
	const rems = weights.map((w, i) => ({ i, r: (t * BigInt(w)) % s }));
	rems.sort((a, b) => (a.r === b.r ? a.i - b.i : a.r > b.r ? -1 : 1));
	let left = total - parts.reduce((a, b) => a + b, 0);
	for (let k = 0; left > 0; k++, left--) parts[rems[k].i]++;
	return parts;
}

export type Edge = { from: string; to: string; amount: number };

/**
 * Who owes whom before simplification: every debt between two people, netted
 * within the pair. Cancelled transactions are ignored.
 */
export function pairwiseDebts(txs: Transaction[]): Edge[] {
	const owed = new Map<string, number>();
	for (const tx of txs) {
		if (tx.kind === 'reversal' || tx.reversed_by) continue;
		const creditor = tx.postings.find((p) => p.amount_minor > 0)?.member_id;
		if (!creditor) continue;
		for (const p of tx.postings) {
			if (p.amount_minor >= 0 || p.member_id === creditor) continue;
			const [a, b] = [p.member_id, creditor];
			// Store each pair once, signed: positive means the smaller id owes the larger.
			const key = a < b ? `${a}|${b}` : `${b}|${a}`;
			const sign = a < b ? 1 : -1;
			owed.set(key, (owed.get(key) ?? 0) + sign * -p.amount_minor);
		}
	}
	const edges: Edge[] = [];
	for (const [key, v] of owed) {
		const [a, b] = key.split('|');
		if (v > 0) edges.push({ from: a, to: b, amount: v });
		if (v < 0) edges.push({ from: b, to: a, amount: -v });
	}
	return edges.sort((x, y) => y.amount - x.amount);
}
