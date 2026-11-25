import { describe, expect, it } from 'vitest';
import type { Transaction } from './api.ts';
import { allocate, pairwiseDebts } from './split.ts';

describe('allocate', () => {
	it('matches the server rule', () => {
		expect(allocate(100, [1, 1, 1])).toEqual([34, 33, 33]);
		expect(allocate(1000, [2, 1, 1])).toEqual([500, 250, 250]);
		expect(allocate(5000, [1, 1, 1])).toEqual([1667, 1667, 1666]);
	});
	it('always conserves the total', () => {
		for (let i = 1; i < 500; i++) {
			const weights = [1 + (i % 7), 1 + (i % 3), 2, 1 + (i % 5)];
			const total = i * 7919;
			expect(allocate(total, weights).reduce((a, b) => a + b, 0)).toBe(total);
		}
	});
});

function tx(id: string, postings: [string, number][], extra: Partial<Transaction> = {}): Transaction {
	return {
		id, kind: 'expense', description: '', original_amount_minor: 1, original_currency: 'SGD',
		fx_rate: '1', base_amount_minor: 1, created_at: '', ...extra,
		postings: postings.map(([member_id, amount_minor]) => ({ member_id, amount_minor }))
	};
}

describe('pairwiseDebts', () => {
	it('nets debts within each pair and ignores cancelled entries', () => {
		const edges = pairwiseDebts([
			tx('1', [['an', 300], ['an', -100], ['binh', -100], ['chi', -100]]),
			tx('2', [['binh', 60], ['an', -30], ['binh', -30]]),
			tx('3', [['chi', 900], ['an', -900]], { reversed_by: '4' }),
			tx('4', [['chi', -900], ['an', 900]], { kind: 'reversal', reverses: '3' }),
			tx('5', [['chi', 40], ['an', -40]], { kind: 'settlement' })
		]);
		expect(edges).toEqual([
			{ from: 'binh', to: 'an', amount: 70 },
			{ from: 'chi', to: 'an', amount: 60 }
		]);
	});
});
