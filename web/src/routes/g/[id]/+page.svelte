<script lang="ts">
	import { page } from '$app/state';
	import QRCode from 'qrcode';
	import { api, ApiError, type Balances, type Group, type Member, type PaymentQr, type Transaction, type Transfer } from '#lib/api.ts';
	import { VN_BANKS } from '#lib/banks.ts';
	import { fmtLocale, t, type Key } from '#lib/i18n.svelte.ts';
	import { exponentOf, formatAmount, parseAmount, shortRate } from '#lib/money.ts';
	import { rememberGroup } from '#lib/recent.ts';
	import SettleGraph from '#lib/SettleGraph.svelte';
	import { allocate, pairwiseDebts } from '#lib/split.ts';

	const groupId = $derived(page.params.id ?? '');

	let group = $state<Group | null>(null);
	let balances = $state<Balances | null>(null);
	let txs = $state<Transaction[]>([]);
	let nextCursor = $state<string | undefined>();
	// Full history (capped) for the settlement diagram; the activity list pages separately.
	let allTxs = $state<Transaction[]>([]);
	const rawDebts = $derived(pairwiseDebts(allTxs));
	let exponents = $state<Record<string, number>>({ VND: 0, JPY: 0, KRW: 0 });
	let currencyCodes = $state<string[]>(['SGD', 'VND', 'USD']);
	let loadError = $state<Key | ''>('');
	let toast = $state('');

	const nameOf = (id: string) => group?.members.find((m) => m.id === id)?.name ?? '?';
	const money = (minor: number, code: string) => formatAmount(minor, code, fmtLocale(), exponentOf(code, exponents));

	function errorText(err: unknown): string {
		const e = err as ApiError;
		if (e.status === 0) return t('offline');
		if (e.status === 429) return t('rateLimited');
		if (e.code === 'name_taken') return t('nameTaken');
		return e.message || t('failed');
	}

	async function load() {
		try {
			const [g, b, page1] = await Promise.all([api.group(groupId), api.balances(groupId), api.transactions(groupId)]);
			group = g;
			balances = b;
			txs = page1.items;
			nextCursor = page1.next_cursor;
			loadError = '';
			rememberGroup(g.id, g.name);
			let all = page1.items;
			let cursor = page1.next_cursor;
			for (let i = 0; cursor && i < 5; i++) {
				const p = await api.transactions(groupId, cursor, 100);
				all = all.concat(p.items);
				cursor = p.next_cursor;
			}
			allTxs = all;
		} catch (err) {
			const e = err as ApiError;
			if (!group) loadError = e.status === 404 || e.status === 400 ? 'notFound' : 'offline';
		}
	}

	async function loadMore() {
		if (!nextCursor) return;
		const p = await api.transactions(groupId, nextCursor);
		txs.push(...p.items);
		nextCursor = p.next_cursor;
	}

	// Initial load, then live updates: the API pushes an event whenever anyone
	// in the group changes something, and we simply reload.
	$effect(() => {
		const id = groupId;
		if (!id) return;
		load();
		api.currencies().then((list) => {
			exponents = Object.fromEntries(list.map((c) => [c.code, c.exponent]));
			currencyCodes = list.map((c) => c.code);
		}).catch(() => {});
		let timer: ReturnType<typeof setTimeout> | undefined;
		const es = new EventSource(api.eventsUrl(id));
		es.addEventListener('change', () => {
			clearTimeout(timer);
			timer = setTimeout(load, 150);
		});
		return () => {
			es.close();
			clearTimeout(timer);
		};
	});

	function flash(message: string) {
		toast = message;
		setTimeout(() => (toast = ''), 2500);
	}

	async function copyLink() {
		try {
			await navigator.clipboard.writeText(location.href);
			flash(t('copied'));
		} catch {
			prompt(t('copyLink'), location.href);
		}
	}

	// ---- add expense ----
	let desc = $state('');
	let amountText = $state('');
	let expCurrency = $state('');
	let paidBy = $state('');
	let splitIds = $state<string[]>([]);
	let unequal = $state(false);
	let weights = $state<Record<string, number>>({});
	const weightOf = (id: string) => (unequal ? (weights[id] ?? 1) : 1);
	// Live preview of each person's part, using the same rounding rule as the API.
	const preview = $derived.by(() => {
		const amount = parseAmount(amountText, exponentOf(expCurrency, exponents));
		const out: Record<string, number> = {};
		if (amount === null || splitIds.length === 0) return out;
		const parts = allocate(amount, splitIds.map(weightOf));
		splitIds.forEach((id, i) => (out[id] = parts[i]));
		return out;
	});
	function bump(id: string, by: number) {
		weights[id] = Math.min(99, Math.max(1, (weights[id] ?? 1) + by));
	}
	let rateText = $state('');
	let marketRate = $state('');
	let expError = $state('');
	let saving = $state(false);
	// One key per filled-in form: retrying after a network error reuses it, so
	// the expense cannot be recorded twice.
	let expenseKey = $state(crypto.randomUUID());

	$effect(() => {
		if (group && !expCurrency) {
			expCurrency = group.base_currency;
			paidBy = group.members[0]?.id ?? '';
			splitIds = group.members.map((m) => m.id);
		}
	});

	$effect(() => {
		const from = expCurrency;
		const to = group?.base_currency;
		marketRate = '';
		if (!from || !to || from === to) return;
		api.fx(from, to).then((r) => {
			if (expCurrency === from) marketRate = shortRate(r.rate);
		}).catch(() => {});
	});

	async function addExpense(e: SubmitEvent) {
		e.preventDefault();
		if (!group) return;
		const amount = parseAmount(amountText, exponentOf(expCurrency, exponents));
		if (amount === null) return void (expError = t('invalidAmount'));
		if (splitIds.length === 0) return void (expError = t('pickSomeone'));
		saving = true;
		expError = '';
		try {
			await api.createExpense(
				group.id,
				{
					description: desc.trim(),
					amount_minor: amount,
					currency: expCurrency,
					paid_by: paidBy,
					shares: splitIds.map((id) => ({ member_id: id, weight: weightOf(id) })),
					...(rateText.trim() && expCurrency !== group.base_currency ? { fx_rate: rateText.trim().replace(',', '.') } : {})
				},
				expenseKey
			);
			desc = '';
			amountText = '';
			rateText = '';
			expenseKey = crypto.randomUUID();
			await load();
		} catch (err) {
			expError = errorText(err);
		} finally {
			saving = false;
		}
	}

	async function undo(tx: Transaction) {
		if (!group || !confirm(t('undoConfirm'))) return;
		try {
			await api.reverse(group.id, tx.id);
			await load();
		} catch (err) {
			flash(errorText(err));
		}
	}

	// ---- settle ----
	let settling = $state('');

	async function markPaid(tr: Transfer) {
		if (!group || settling) return;
		settling = tr.from + tr.to;
		try {
			await api.createSettlement(group.id, { from: tr.from, to: tr.to, amount_minor: tr.amount_minor }, crypto.randomUUID());
			await load();
		} catch (err) {
			flash(errorText(err));
		} finally {
			settling = '';
		}
	}

	let qrDialog = $state<HTMLDialogElement>();
	let qrTransfer = $state<Transfer | null>(null);
	let qr = $state<PaymentQr | null>(null);
	let qrImage = $state('');
	let qrError = $state('');

	async function openQr(tr: Transfer) {
		if (!group) return;
		qrTransfer = tr;
		qr = null;
		qrImage = '';
		qrError = '';
		qrDialog?.showModal();
		try {
			const res = await api.paymentQr(group.id, tr.to, tr.amount_minor);
			qrImage = await QRCode.toDataURL(res.payload, { errorCorrectionLevel: 'M', margin: 2, width: 560 });
			qr = res;
		} catch (err) {
			const e = err as ApiError;
			qrError = e.code === 'payout_not_set' ? t('qrNoPayout', { name: nameOf(tr.to) }) : errorText(err);
		}
	}

	async function paidFromQr() {
		const tr = qrTransfer;
		qrDialog?.close();
		if (tr) await markPaid(tr);
	}

	// ---- members ----
	let newMember = $state('');
	let memberError = $state('');
	let editing = $state<Member | null>(null);
	let method = $state<'vietqr' | 'paynow'>('vietqr');
	let bankBin = $state(VN_BANKS[0].bin);
	let account = $state('');
	let proxyType = $state<'mobile' | 'uen'>('mobile');
	let proxyValue = $state('');
	let payoutError = $state('');

	async function addMember(e: SubmitEvent) {
		e.preventDefault();
		if (!group || !newMember.trim()) return;
		memberError = '';
		try {
			await api.addMember(group.id, newMember.trim());
			newMember = '';
			await load();
		} catch (err) {
			memberError = errorText(err);
		}
	}

	function editPayout(m: Member) {
		editing = m;
		payoutError = '';
		method = m.payout?.method ?? (group?.base_currency === 'SGD' ? 'paynow' : 'vietqr');
		bankBin = m.payout?.bank_bin ?? VN_BANKS[0].bin;
		account = m.payout?.account ?? '';
		proxyType = m.payout?.proxy_type ?? 'mobile';
		proxyValue = m.payout?.proxy_value ?? '+65';
	}

	async function savePayout(e: SubmitEvent) {
		e.preventDefault();
		if (!group || !editing) return;
		const payout =
			method === 'vietqr'
				? { method, bank_bin: bankBin, account: account.replace(/\s/g, '') }
				: { method, proxy_type: proxyType, proxy_value: proxyValue.replace(/\s/g, '').toUpperCase() };
		try {
			await api.updateMember(group.id, editing.id, { payout });
			editing = null;
			await load();
		} catch (err) {
			payoutError = (err as ApiError).status === 400 ? t('invalidPayout') : errorText(err);
		}
	}

	function payoutLabel(m: Member): string {
		const p = m.payout;
		if (!p) return t('payoutNone');
		if (p.method === 'vietqr') {
			const bank = VN_BANKS.find((b) => b.bin === p.bank_bin)?.name ?? p.bank_bin;
			return `VietQR · ${bank} · ${p.account}`;
		}
		return `PayNow · ${p.proxy_value}`;
	}

	function when(iso: string): string {
		return new Intl.DateTimeFormat(fmtLocale(), { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' }).format(new Date(iso));
	}

	const payer = (tx: Transaction) => tx.postings.find((p) => p.amount_minor > 0)?.member_id ?? '';
	const receiver = (tx: Transaction) => tx.postings.find((p) => p.amount_minor < 0)?.member_id ?? '';
</script>

{#if loadError}
	<div class="card">
		<p>{t(loadError)}</p>
		{#if loadError === 'offline'}<button onclick={load}>{t('retry')}</button>{/if}
	</div>
{:else if !group || !balances}
	<p class="muted">{t('waking')}</p>
{:else}
	<div class="row wrap head">
		<div class="grow">
			<h1>{group.name}</h1>
			<p class="muted small">{group.members.length} {t('members').toLowerCase()} · {group.base_currency}</p>
		</div>
		<button onclick={copyLink}>{t('copyLink')}</button>
	</div>

	<section class="card" aria-labelledby="h-bal">
		<h2 id="h-bal">{t('balances')}</h2>
		<ul class="list">
			{#each balances.balances as b (b.member_id)}
				<li class="row">
					<span class="grow">{nameOf(b.member_id)}</span>
					{#if b.amount_minor > 0}
						<span class="small muted">{t('getsBack')}</span>
						<strong class="num pos">{money(b.amount_minor, balances.currency)}</strong>
					{:else if b.amount_minor < 0}
						<span class="small muted">{t('owes')}</span>
						<strong class="num neg">{money(-b.amount_minor, balances.currency)}</strong>
					{:else}
						<span class="small muted">{t('settled')}</span>
					{/if}
				</li>
			{/each}
		</ul>
	</section>

	<section class="card" aria-labelledby="h-settle">
		<h2 id="h-settle">{t('toSettle')}</h2>
		{#if balances.transfers.length === 0}
			<p class="muted">{t('allSettled')}</p>
		{:else}
			<ul class="list">
				{#each balances.transfers as tr (tr.from + tr.to)}
					<li>
						<div class="row">
							<span class="grow"><strong>{nameOf(tr.from)}</strong> {t('pays')} <strong>{nameOf(tr.to)}</strong></span>
							<strong class="num">{money(tr.amount_minor, balances.currency)}</strong>
						</div>
						<div class="row actions">
							<button class="sm primary" onclick={() => openQr(tr)}>{t('payQr')}</button>
							<button class="sm" disabled={settling === tr.from + tr.to} onclick={() => markPaid(tr)}>{t('markPaid')}</button>
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</section>

	{#if group.members.length >= 3 && rawDebts.length > 0}
		<section class="card" aria-labelledby="h-graph">
			<h2 id="h-graph">{t('graphTitle')}</h2>
			<SettleGraph
				members={group.members}
				before={rawDebts}
				after={balances.transfers.map((x) => ({ from: x.from, to: x.to, amount: x.amount_minor }))}
				format={(minor) => money(minor, balances?.currency ?? '')}
				labels={{
					before: t('graphBefore'),
					after: t('graphAfter'),
					summary: t('graphSummary', { before: String(rawDebts.length), after: String(balances.transfers.length) })
				}}
			/>
		</section>
	{/if}

	<form class="card" onsubmit={addExpense} aria-labelledby="h-add">
		<h2 id="h-add">{t('addExpense')}</h2>
		<div class="field">
			<label for="desc">{t('description')}</label>
			<input id="desc" bind:value={desc} placeholder={t('descriptionPh')} maxlength="200" required />
		</div>
		<div class="row wrap">
			<div class="field grow">
				<label for="amount">{t('amount')}</label>
				<input id="amount" bind:value={amountText} inputmode="decimal" autocomplete="off" required />
			</div>
			<div class="field narrow">
				<label for="ecur">{t('currency')}</label>
				<select id="ecur" bind:value={expCurrency}>
					{#each currencyCodes as c (c)}<option>{c}</option>{/each}
				</select>
			</div>
			<div class="field grow">
				<label for="payer">{t('paidBy')}</label>
				<select id="payer" bind:value={paidBy}>
					{#each group.members as m (m.id)}<option value={m.id}>{m.name}</option>{/each}
				</select>
			</div>
		</div>
		{#if expCurrency !== group.base_currency}
			<div class="field">
				<label for="rate">{t('rate')}</label>
				<input id="rate" bind:value={rateText} inputmode="decimal" autocomplete="off" placeholder={marketRate} />
				<p class="small muted hint">{t('rateHint', { from: expCurrency, to: group.base_currency, rate: marketRate || '…' })}</p>
			</div>
		{/if}
		<fieldset>
			<legend>{t('splitBetween')}</legend>
			<ul class="list split">
				{#each group.members as m (m.id)}
					{@const on = splitIds.includes(m.id)}
					<li class="row">
						<label class="pick grow"><input type="checkbox" value={m.id} bind:group={splitIds} /> {m.name}</label>
						{#if unequal && on}
							<span class="stepper">
								<button type="button" class="sm" aria-label={t('fewer', { name: m.name })} onclick={() => bump(m.id, -1)}>−</button>
								<span class="num" aria-live="polite">{weightOf(m.id)}</span>
								<button type="button" class="sm" aria-label={t('more', { name: m.name })} onclick={() => bump(m.id, 1)}>+</button>
							</span>
						{/if}
						{#if on && preview[m.id] !== undefined}
							<span class="num small muted part">{money(preview[m.id], expCurrency)}</span>
						{/if}
					</li>
				{/each}
			</ul>
			<label class="pick toggle"><input type="checkbox" bind:checked={unequal} /> {t('unequal')}</label>
		</fieldset>
		<button class="primary" disabled={saving}>{saving ? t('saving') : t('save')}</button>
		{#if expError}<p class="error" role="alert">{expError}</p>{/if}
	</form>

	<section class="card" aria-labelledby="h-act">
		<h2 id="h-act">{t('activity')}</h2>
		{#if txs.length === 0}
			<p class="muted">{t('noActivity')}</p>
		{:else}
			<ul class="list">
				{#each txs as tx (tx.id)}
					<li class:void={tx.reversed_by || tx.kind === 'reversal'}>
						<div class="row">
							<div class="grow">
								{#if tx.kind === 'expense'}
									<div class="title">{tx.description}</div>
									<div class="small muted">{nameOf(payer(tx))} {t('paid')} · {when(tx.created_at)}</div>
								{:else if tx.kind === 'settlement'}
									<div class="title">{nameOf(payer(tx))} {t('paidTo')} {nameOf(receiver(tx))}</div>
									<div class="small muted">{when(tx.created_at)}</div>
								{:else}
									<div class="title">{t('cancellation')}: {tx.description}</div>
									<div class="small muted">{when(tx.created_at)}</div>
								{/if}
							</div>
							<div class="amt">
								<strong class="num">{money(tx.original_amount_minor, tx.original_currency)}</strong>
								{#if tx.original_currency !== group.base_currency}
									<div class="small muted num">{money(tx.base_amount_minor, group.base_currency)}</div>
								{/if}
							</div>
						</div>
						<div class="row meta">
							{#if tx.reversed_by}
								<span class="small muted">{t('cancelled')}</span>
							{:else if tx.kind !== 'reversal'}
								<button class="ghost sm" onclick={() => undo(tx)}>{t('undo')}</button>
							{/if}
						</div>
						<details class="ledger">
							<summary class="small">{t('ledger')}</summary>
							<table class="small">
								<tbody>
									{#each tx.postings as p, i (i)}
										<tr>
											<td>{nameOf(p.member_id)}</td>
											<td class="num" class:pos={p.amount_minor > 0} class:neg={p.amount_minor < 0}>
												{p.amount_minor > 0 ? '+' : '−'}{money(Math.abs(p.amount_minor), group.base_currency)}
											</td>
										</tr>
									{/each}
								</tbody>
								<tfoot>
									<tr>
										<td>{t('ledgerSum')}</td>
										<td class="num">{money(tx.postings.reduce((a, p) => a + p.amount_minor, 0), group.base_currency)}</td>
									</tr>
								</tfoot>
							</table>
							{#if tx.original_currency !== group.base_currency}
								<p class="small muted">{t('rateLocked', { from: tx.original_currency, rate: shortRate(tx.fx_rate), to: group.base_currency })}</p>
							{/if}
							<p class="small muted id">{tx.id}</p>
						</details>
					</li>
				{/each}
			</ul>
			{#if nextCursor}<button class="more" onclick={loadMore}>{t('loadMore')}</button>{/if}
		{/if}
	</section>

	<section class="card" aria-labelledby="h-mem">
		<h2 id="h-mem">{t('members')}</h2>
		<ul class="list">
			{#each group.members as m (m.id)}
				<li>
					<div class="row">
						<div class="grow">
							<div class="title">{m.name}</div>
							<div class="small muted">{payoutLabel(m)}</div>
						</div>
						<button class="sm" onclick={() => editPayout(m)}>{m.payout ? t('edit') : t('payoutSet')}</button>
					</div>
					{#if editing?.id === m.id}
						<form class="payout" onsubmit={savePayout}>
							<div class="field">
								<label for="method">{t('method')}</label>
								<select id="method" bind:value={method}>
									<option value="vietqr">VietQR (Vietnam)</option>
									<option value="paynow">PayNow (Singapore)</option>
								</select>
							</div>
							{#if method === 'vietqr'}
								<div class="field">
									<label for="bank">{t('bank')}</label>
									<select id="bank" bind:value={bankBin}>
										{#each VN_BANKS as b (b.bin)}<option value={b.bin}>{b.name}</option>{/each}
									</select>
								</div>
								<div class="field">
									<label for="acct">{t('account')}</label>
									<input id="acct" bind:value={account} inputmode="numeric" autocomplete="off" required />
								</div>
							{:else}
								<div class="field">
									<label for="ptype">{t('proxyType')}</label>
									<select id="ptype" bind:value={proxyType}>
										<option value="mobile">{t('mobile')}</option>
										<option value="uen">{t('uen')}</option>
									</select>
								</div>
								<div class="field">
									<label for="pval">{proxyType === 'mobile' ? t('mobile') : t('uen')}</label>
									<input id="pval" type={proxyType === 'mobile' ? 'tel' : 'text'} bind:value={proxyValue} placeholder={proxyType === 'mobile' ? '+6591234567' : '201912345K'} autocomplete="off" required />
								</div>
							{/if}
							<div class="row">
								<button class="primary sm">{t('saveDetails')}</button>
								<button type="button" class="sm" onclick={() => (editing = null)}>{t('cancel')}</button>
							</div>
							{#if payoutError}<p class="error" role="alert">{payoutError}</p>{/if}
						</form>
					{/if}
				</li>
			{/each}
		</ul>
		<form class="row addm" onsubmit={addMember}>
			<input class="grow" bind:value={newMember} placeholder={t('memberNamePh')} aria-label={t('addMember')} maxlength="60" />
			<button>{t('add')}</button>
		</form>
		{#if memberError}<p class="error" role="alert">{memberError}</p>{/if}
	</section>
{/if}

<dialog bind:this={qrDialog} aria-labelledby="h-qr">
	{#if qrTransfer}
		<h2 id="h-qr">{t('qrTitle', { name: nameOf(qrTransfer.to) })}</h2>
		{#if qrError}
			<p>{qrError}</p>
		{:else if qr}
			<img src={qrImage} alt="QR" width="280" height="280" />
			<p class="big num">{money(qr.amount_minor, qr.currency)}</p>
			{#if qr.fx_rate && group}
				<p class="small muted">{t('qrConverted', { from: group.base_currency, rate: shortRate(qr.fx_rate), to: qr.currency })}</p>
			{/if}
			<p class="small muted">{t('qrScan')}</p>
		{:else}
			<p class="muted">…</p>
		{/if}
		<div class="row">
			{#if qr}<button class="primary grow" onclick={paidFromQr}>{t('qrPaid')}</button>{/if}
			<button class="grow" onclick={() => qrDialog?.close()}>{t('close')}</button>
		</div>
	{/if}
</dialog>

{#if toast}<div class="toast" role="status">{toast}</div>{/if}

<style>
	.head {
		margin-bottom: 16px;
		align-items: flex-start;
	}
	.head h1 {
		margin-bottom: 2px;
		overflow-wrap: anywhere;
	}
	.head p {
		margin: 0;
	}
	.actions {
		margin-top: 8px;
	}
	.narrow {
		flex: 0 0 110px;
	}
	.hint {
		margin: 4px 0 0;
	}
	fieldset {
		border: 0;
		padding: 0;
		margin: 0 0 16px;
	}
	legend {
		font-size: 0.875rem;
		font-weight: 600;
		padding: 0;
		margin-bottom: 6px;
	}
	.split > li {
		padding: 4px 0;
		border: 0;
		min-height: 44px;
	}
	.pick {
		display: flex;
		align-items: center;
		gap: 8px;
		font-weight: 400;
		font-size: 1rem;
		margin: 0;
		min-height: 40px;
		cursor: pointer;
	}
	.pick input {
		width: 18px;
		height: 18px;
		accent-color: var(--brand);
	}
	.toggle {
		margin-top: 6px;
		color: var(--muted);
	}
	.stepper {
		display: inline-flex;
		align-items: center;
		gap: 8px;
	}
	.stepper button {
		width: 36px;
		padding: 0;
	}
	.part {
		min-width: 92px;
		text-align: right;
	}
	.meta {
		min-height: 0;
	}
	.ledger summary {
		cursor: pointer;
		color: var(--muted);
		padding: 4px 0;
	}
	.ledger table {
		width: 100%;
		border-collapse: collapse;
		margin: 6px 0;
	}
	.ledger td {
		padding: 3px 0;
	}
	.ledger td.num {
		text-align: right;
	}
	.ledger tfoot td {
		border-top: 1px solid var(--line);
		font-weight: 600;
	}
	.ledger p {
		margin: 2px 0;
	}
	.ledger .id {
		font-family: ui-monospace, monospace;
		font-size: 0.75rem;
		overflow-wrap: anywhere;
	}
	.title {
		font-weight: 600;
		overflow-wrap: anywhere;
	}
	.amt {
		text-align: right;
	}
	li.void > .row .title,
	li.void > .row .amt {
		text-decoration: line-through;
		opacity: 0.55;
	}
	.more {
		margin-top: 12px;
		width: 100%;
	}
	.payout {
		margin-top: 12px;
		padding: 14px;
		background: var(--bg);
		border-radius: 10px;
	}
	.addm {
		margin-top: 14px;
	}
	dialog {
		width: min(360px, calc(100vw - 32px));
		border: 1px solid var(--line);
		border-radius: var(--radius);
		background: var(--surface);
		color: var(--text);
		padding: 20px;
		text-align: center;
	}
	dialog::backdrop {
		background: rgb(0 0 0 / 0.5);
	}
	dialog img {
		width: 100%;
		max-width: 280px;
		height: auto;
		border-radius: 8px;
		background: #fff;
	}
	.big {
		font-size: 1.5rem;
		font-weight: 700;
		margin: 8px 0 4px;
	}
	.toast {
		position: fixed;
		left: 50%;
		bottom: 24px;
		transform: translateX(-50%);
		background: var(--text);
		color: var(--bg);
		padding: 10px 18px;
		border-radius: 999px;
		font-size: 0.9rem;
		max-width: calc(100vw - 32px);
	}
</style>
