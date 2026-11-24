// Thin typed client. All types come from the OpenAPI document (api.gen.ts),
// so the web app cannot drift from the API contract unnoticed.
import type { components } from './api.gen';

type S = components['schemas'];
export type Group = S['Group'];
export type Member = S['Member'];
export type Payout = S['Payout'];
export type Transaction = S['Transaction'];
export type Balances = S['Balances'];
export type Transfer = S['Transfer'];
export type PaymentQr = S['PaymentQr'];
export type Currency = S['Currency'];
export type FxRate = S['FxRate'];

export const API_URL: string = (import.meta.env.VITE_API_URL ?? 'http://localhost:8080').replace(/\/$/, '');

export class ApiError extends Error {
	constructor(
		public status: number,
		public code: string,
		message: string
	) {
		super(message);
	}
}

async function request<T>(method: string, path: string, body?: unknown, headers: Record<string, string> = {}): Promise<T> {
	let res: Response;
	try {
		res = await fetch(API_URL + path, {
			method,
			headers: body === undefined ? headers : { 'Content-Type': 'application/json', ...headers },
			body: body === undefined ? undefined : JSON.stringify(body)
		});
	} catch {
		throw new ApiError(0, 'network', 'Network error');
	}
	const data = await res.json().catch(() => null);
	if (!res.ok) {
		throw new ApiError(res.status, data?.code ?? 'unknown', data?.message ?? res.statusText);
	}
	return data as T;
}

const g = (id: string) => `/v1/groups/${id}`;

export const api = {
	currencies: () => request<Currency[]>('GET', '/v1/currencies'),
	fx: (from: string, to: string) => request<FxRate>('GET', `/v1/fx?from=${from}&to=${to}`),
	createGroup: (body: S['CreateGroupRequest']) => request<Group>('POST', '/v1/groups', body),
	group: (id: string) => request<Group>('GET', g(id)),
	balances: (id: string) => request<Balances>('GET', g(id) + '/balances'),
	transactions: (id: string, cursor?: string) =>
		request<S['TransactionPage']>('GET', g(id) + '/transactions?limit=30' + (cursor ? `&cursor=${cursor}` : '')),
	addMember: (id: string, name: string) => request<Member>('POST', g(id) + '/members', { name }),
	updateMember: (id: string, memberId: string, body: S['UpdateMemberRequest']) =>
		request<Member>('PATCH', `${g(id)}/members/${memberId}`, body),
	// The caller supplies the idempotency key and reuses it when retrying, so a
	// flaky connection can never record the same expense twice.
	createExpense: (id: string, body: S['CreateExpenseRequest'], key: string) =>
		request<Transaction>('POST', g(id) + '/expenses', body, { 'Idempotency-Key': key }),
	createSettlement: (id: string, body: S['CreateSettlementRequest'], key: string) =>
		request<Transaction>('POST', g(id) + '/settlements', body, { 'Idempotency-Key': key }),
	reverse: (id: string, txId: string) => request<Transaction>('POST', `${g(id)}/transactions/${txId}/reversal`),
	paymentQr: (id: string, memberId: string, amountMinor: number) =>
		request<PaymentQr>('GET', `${g(id)}/members/${memberId}/payment-qr?amount_minor=${amountMinor}`),
	eventsUrl: (id: string) => API_URL + g(id) + '/events'
};
