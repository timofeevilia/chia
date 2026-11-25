// Builds a ready-made sample group through the public API, so a visitor can
// explore balances, the ledger and the settlement diagram without typing.
import { api } from './api.ts';

export async function createDemoGroup(onProgress: (done: number, total: number) => void): Promise<string> {
	const total = 8;
	let done = 0;
	const step = () => onProgress(++done, total);

	const g = await api.createGroup({
		name: 'Da Nang weekend (demo)',
		base_currency: 'SGD',
		members: ['An', 'Binh', 'Mei', 'Wei']
	});
	step();
	const [an, binh, mei, wei] = g.members.map((m) => m.id);
	const all = [an, binh, mei, wei];
	const equal = (ids: string[]) => ids.map((member_id) => ({ member_id, weight: 1 }));

	const expenses = [
		{ description: 'Seafood dinner', amount_minor: 2_400_000, currency: 'VND', paid_by: an, shares: equal(all) },
		{ description: 'Airport transfer', amount_minor: 3800, currency: 'SGD', paid_by: mei, shares: equal(all) },
		{ description: 'Homestay, 2 nights', amount_minor: 18000, currency: 'USD', paid_by: wei, shares: equal(all) },
		{ description: 'Coffee and banh mi', amount_minor: 320_000, currency: 'VND', paid_by: binh, shares: equal([an, binh, mei]) },
		{
			description: 'Ba Na Hills tickets (Wei brought a friend)',
			amount_minor: 3_600_000,
			currency: 'VND',
			paid_by: an,
			shares: [...equal([an, binh, mei]), { member_id: wei, weight: 2 }]
		}
	];
	// One after another, so the activity list keeps this order.
	for (const e of expenses) {
		await api.createExpense(g.id, e, crypto.randomUUID());
		step();
	}

	// Placeholder payment details: valid in format, but no real account or number.
	await api.updateMember(g.id, an, { payout: { method: 'vietqr', bank_bin: '970436', account: '0000000000' } });
	step();
	await api.updateMember(g.id, mei, { payout: { method: 'paynow', proxy_type: 'mobile', proxy_value: '+6500000000' } });
	step();
	return g.id;
}
