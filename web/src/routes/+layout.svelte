<script lang="ts">
	import '../app.css';
	import favicon from '#lib/assets/favicon.svg';
	import { API_URL } from '#lib/api.ts';
	import { i18n, setLocale, t } from '#lib/i18n.svelte.ts';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	$effect(() => {
		document.documentElement.lang = i18n.locale;
	});
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
	<title>Chia – split bills across currencies</title>
	<meta name="description" content="Split shared expenses across currencies and settle with a VietQR or PayNow code." />
</svelte:head>

<header>
	<div class="bar row">
		<a class="logo grow" href="/">Chia</a>
		<div class="lang" role="group" aria-label="Language">
			<button class="sm" aria-pressed={i18n.locale === 'en'} onclick={() => setLocale('en')}>EN</button>
			<button class="sm" aria-pressed={i18n.locale === 'vi'} onclick={() => setLocale('vi')}>VI</button>
		</div>
	</div>
</header>

<main>
	{@render children()}
</main>

<footer class="bar small muted">
	<p>{t('disclaimer')}</p>
	<p>
		<a href="{API_URL}/docs">{t('apiDocs')}</a> ·
		<a href="https://github.com/timofeevilia/chia">{t('source')}</a>
	</p>
</footer>

<style>
	header {
		padding: 12px 0;
		margin-bottom: 20px;
	}
	.logo {
		font-weight: 800;
		font-size: 1.35rem;
		letter-spacing: -0.03em;
		text-decoration: none;
		color: var(--brand);
	}
	.lang {
		display: flex;
		gap: 4px;
	}
	.lang button[aria-pressed='true'] {
		background: var(--brand-soft);
		border-color: var(--brand);
	}
	footer {
		padding-top: 12px;
		padding-bottom: 32px;
	}
</style>
