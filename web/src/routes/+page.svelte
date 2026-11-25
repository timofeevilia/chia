<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError, type Currency } from '#lib/api.ts';
	import { createDemoGroup } from '#lib/demo.ts';
	import { t } from '#lib/i18n.svelte.ts';
	import { loadRecent, rememberGroup } from '#lib/recent.ts';

	let name = $state('');
	let currency = $state('SGD');
	let members = $state<string[]>([]);
	let memberInput = $state('');
	let currencies = $state<Currency[]>([
		{ code: 'SGD', exponent: 2, name: 'Singapore Dollar' },
		{ code: 'VND', exponent: 0, name: 'Vietnamese Dong' }
	]);
	let busy = $state(false);
	let error = $state('');
	const recent = loadRecent();

	api.currencies().then((c) => (currencies = c)).catch(() => {});

	function addMember() {
		const n = memberInput.trim().replace(/\s+/g, ' ');
		if (n && !members.includes(n)) members.push(n);
		memberInput = '';
	}

	let demoProgress = $state('');

	async function demo() {
		busy = true;
		error = '';
		demoProgress = t('demoPreparing', { done: '0', total: '8' });
		try {
			const id = await createDemoGroup((done, total) => {
				demoProgress = t('demoPreparing', { done: String(done), total: String(total) });
			});
			await goto(`/g/${id}`);
		} catch (err) {
			const e = err as ApiError;
			error = e.status === 0 ? t('offline') : e.status === 429 ? t('rateLimited') : e.message || t('failed');
		} finally {
			busy = false;
			demoProgress = '';
		}
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		addMember();
		if (members.length < 2) {
			error = t('needTwo');
			return;
		}
		busy = true;
		error = '';
		try {
			const g = await api.createGroup({ name: name.trim(), base_currency: currency, members });
			rememberGroup(g.id, g.name);
			await goto(`/g/${g.id}`);
		} catch (err) {
			const e = err as ApiError;
			error = e.status === 0 ? t('offline') : e.status === 429 ? t('rateLimited') : e.message || t('failed');
		} finally {
			busy = false;
		}
	}
</script>

<section class="hero">
	<h1>{t('tagline')}</h1>
	<p class="muted">{t('intro')}</p>
	<div class="row wrap">
		<button class="primary" onclick={demo} disabled={busy}>{demoProgress || t('tryDemo')}</button>
		<span class="small muted">{t('demoHint')}</span>
	</div>
</section>

<form class="card" onsubmit={submit}>
	<h2>{t('createGroup')}</h2>
	<div class="row wrap">
		<div class="field grow">
			<label for="name">{t('groupName')}</label>
			<input id="name" bind:value={name} placeholder={t('groupNamePh')} maxlength="100" required />
		</div>
		<div class="field cur">
			<label for="cur">{t('baseCurrency')}</label>
			<select id="cur" bind:value={currency}>
				{#each currencies as c (c.code)}
					<option value={c.code}>{c.code} – {c.name}</option>
				{/each}
			</select>
		</div>
	</div>

	<div class="field">
		<label for="member">{t('members')}</label>
		<div class="row">
			<input
				id="member"
				class="grow"
				bind:value={memberInput}
				placeholder={t('memberNamePh')}
				maxlength="60"
				onkeydown={(e) => {
					if (e.key === 'Enter') {
						e.preventDefault();
						addMember();
					}
				}}
			/>
			<button type="button" onclick={addMember}>{t('add')}</button>
		</div>
	</div>
	{#if members.length}
		<div class="row wrap chips">
			{#each members as m, i (m)}
				<span class="chip">
					{m}
					<button type="button" aria-label="Remove {m}" onclick={() => members.splice(i, 1)}>×</button>
				</span>
			{/each}
		</div>
	{/if}

	<button class="primary" disabled={busy}>{t('create')}</button>
	<span class="small muted note">{t('noSignup')}</span>
	{#if error}<p class="error" role="alert">{error}</p>{/if}
</form>

{#if recent.length}
	<section class="card">
		<h2>{t('recent')}</h2>
		<ul class="list">
			{#each recent as r (r.id)}
				<li><a href="/g/{r.id}">{r.name}</a></li>
			{/each}
		</ul>
	</section>
{/if}

<section class="card">
	<h2>{t('howTitle')}</h2>
	<ol>
		<li>{t('how1')}</li>
		<li>{t('how2')}</li>
		<li>{t('how3')}</li>
	</ol>
</section>

<style>
	.hero {
		margin-bottom: 24px;
	}
	.cur {
		flex: 0 1 220px;
	}
	.chips {
		margin-bottom: 14px;
	}
	.note {
		margin-left: 10px;
	}
	ol {
		margin: 0;
		padding-left: 20px;
	}
	ol li {
		margin-bottom: 6px;
	}
</style>
