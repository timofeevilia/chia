// Money helpers. Amounts travel as integer minor units; parsing works on the
// digits the user typed and never goes through floating point.

/** Fallback exponents until the list from the API has loaded. */
export const EXPONENTS: Record<string, number> = { VND: 0, JPY: 0, KRW: 0 };

export function exponentOf(code: string, table: Record<string, number> = EXPONENTS): number {
	return table[code] ?? 2;
}

/**
 * Parses user input such as "12.50", "12,5" or "1 250 000" into minor units.
 * Returns null when the text is not a positive amount the currency can express.
 */
export function parseAmount(input: string, exponent: number): number | null {
	let s = input.trim().replace(/[\s _']/g, '');
	if (s === '') return null;
	// Treat the last "." or "," as the decimal separator when it is followed by
	// at most `exponent` digits; any other separators are thousands grouping.
	const m = s.match(/^(.*)[.,](\d*)$/);
	let whole = s;
	let frac = '';
	if (m && exponent > 0 && m[2].length <= exponent) {
		whole = m[1];
		frac = m[2];
	}
	whole = whole.replace(/[.,]/g, '');
	if (!/^\d+$/.test(whole || '0') || !/^\d*$/.test(frac)) return null;
	const minor = Number((whole || '0') + frac.padEnd(exponent, '0'));
	if (!Number.isSafeInteger(minor) || minor <= 0 || minor > 1_000_000_000_000) return null;
	return minor;
}

export function formatAmount(minor: number, code: string, locale: string, exponent = exponentOf(code)): string {
	const value = minor / 10 ** exponent;
	try {
		return new Intl.NumberFormat(locale, {
			style: 'currency',
			currency: code,
			minimumFractionDigits: exponent,
			maximumFractionDigits: exponent
		}).format(value);
	} catch {
		return `${value.toFixed(exponent)} ${code}`;
	}
}

/** Shortens a long decimal rate to about six significant digits for display. */
export function shortRate(rate: string): string {
	const n = Number(rate);
	if (!Number.isFinite(n) || n <= 0) return rate;
	return String(Number(n.toPrecision(6)));
}
