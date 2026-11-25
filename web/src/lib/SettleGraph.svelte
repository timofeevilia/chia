<script lang="ts">
	// Draws who-owes-whom as a small directed graph and animates between the
	// raw debts and the simplified set of payments.
	import { prefersReducedMotion } from 'svelte/motion';
	import { draw, fade } from 'svelte/transition';
	import type { Edge } from './split.ts';

	let {
		members,
		before,
		after,
		format,
		labels
	}: {
		members: { id: string; name: string }[];
		before: Edge[];
		after: Edge[];
		format: (minor: number) => string;
		labels: { before: string; after: string; summary: string };
	} = $props();

	let mode = $state<'before' | 'after'>('before');
	const edges = $derived(mode === 'before' ? before : after);
	const ms = $derived(prefersReducedMotion.current ? 0 : 550);

	const W = 340;
	const H = 300;
	const R = 100; // radius of the ring the people sit on
	const NODE = 22;

	const pos = $derived(
		new Map(
			members.map((m, i) => {
				const a = (2 * Math.PI * i) / members.length - Math.PI / 2;
				return [m.id, { x: W / 2 + R * Math.cos(a), y: H / 2 + R * Math.sin(a), a }];
			})
		)
	);
	const max = $derived(Math.max(1, ...before.map((e) => e.amount), ...after.map((e) => e.amount)));

	function geometry(e: Edge) {
		const p = pos.get(e.from);
		const q = pos.get(e.to);
		if (!p || !q) return null;
		const dx = q.x - p.x;
		const dy = q.y - p.y;
		const len = Math.hypot(dx, dy) || 1;
		const ux = dx / len;
		const uy = dy / len;
		const width = 1.5 + (4.5 * e.amount) / max;
		const head = 6 + width;
		// Labels sit nearer the debtor, so crossing lines do not stack their amounts.
		// Start and end on the circle edges; stop short to leave room for the arrowhead.
		const x1 = p.x + ux * (NODE + 3);
		const y1 = p.y + uy * (NODE + 3);
		const x2 = q.x - ux * (NODE + 4 + head);
		const y2 = q.y - uy * (NODE + 4 + head);
		const tipX = q.x - ux * (NODE + 4);
		const tipY = q.y - uy * (NODE + 4);
		const arrow = `${tipX},${tipY} ${x2 - uy * head * 0.55},${y2 + ux * head * 0.55} ${x2 + uy * head * 0.55},${y2 - ux * head * 0.55}`;
		return { d: `M${x1},${y1}L${x2},${y2}`, width, arrow, lx: x1 + (x2 - x1) * 0.36, ly: y1 + (y2 - y1) * 0.36 };
	}

	const initials = (name: string) => name.trim().slice(0, 2);
</script>

<div class="seg" role="group">
	<button class="sm" aria-pressed={mode === 'before'} onclick={() => (mode = 'before')}>
		{labels.before} · {before.length}
	</button>
	<button class="sm" aria-pressed={mode === 'after'} onclick={() => (mode = 'after')}>
		{labels.after} · {after.length}
	</button>
</div>

<svg viewBox="0 0 {W} {H}" role="img" aria-label={labels.summary}>
	{#each edges as e (mode + e.from + e.to)}
		{@const g = geometry(e)}
		{#if g}
			<g class="edge">
				<path d={g.d} stroke-width={g.width} in:draw={{ duration: ms }} out:fade={{ duration: ms / 3 }} />
				<polygon points={g.arrow} in:fade={{ duration: ms / 2, delay: ms * 0.8 }} out:fade={{ duration: ms / 3 }} />
				<g in:fade={{ duration: ms / 2, delay: ms / 2 }} out:fade={{ duration: ms / 3 }}>
					<text class="amount" x={g.lx} y={g.ly} dy="4">{format(e.amount)}</text>
				</g>
			</g>
		{/if}
	{/each}
	{#each members as m (m.id)}
		{@const p = pos.get(m.id)}
		{#if p}
			<circle class="node" cx={p.x} cy={p.y} r={NODE} />
			<text class="initials" x={p.x} y={p.y} dy="5">{initials(m.name)}</text>
			<text class="name" x={p.x + Math.cos(p.a) * (NODE + 10)} y={p.y + Math.sin(p.a) * (NODE + 16)} dy="4"
				style:text-anchor={Math.abs(Math.cos(p.a)) < 0.3 ? 'middle' : Math.cos(p.a) > 0 ? 'start' : 'end'}>{m.name}</text>
		{/if}
	{/each}
</svg>
<p class="small muted">{labels.summary}</p>

<style>
	.seg {
		display: flex;
		gap: 6px;
		margin-bottom: 4px;
	}
	.seg button[aria-pressed='true'] {
		background: var(--brand-soft);
		border-color: var(--brand);
	}
	svg {
		display: block;
		width: 100%;
		max-width: 420px;
		margin: 0 auto;
		overflow: visible;
	}
	path {
		fill: none;
		stroke: var(--brand);
		stroke-linecap: round;
		opacity: 0.75;
	}
	polygon {
		fill: var(--brand);
	}
	text {
		font-size: 11px;
		text-anchor: middle;
		fill: var(--text);
		font-variant-numeric: tabular-nums;
	}
	.amount {
		/* Outline drawn under the glyphs keeps amounts readable over the lines. */
		paint-order: stroke;
		stroke: var(--surface);
		stroke-width: 4px;
		stroke-linejoin: round;
	}
	.node {
		fill: var(--brand-soft);
		stroke: var(--brand);
		stroke-width: 1.5;
	}
	.initials {
		font-size: 13px;
		font-weight: 700;
	}
	.name {
		font-size: 12px;
		fill: var(--muted);
	}
	p {
		margin: 4px 0 0;
		text-align: center;
	}
</style>
