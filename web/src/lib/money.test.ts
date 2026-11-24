import { describe, expect, it } from 'vitest';
import { formatAmount, parseAmount, shortRate } from './money';

describe('parseAmount', () => {
	it('parses decimals for two-decimal currencies', () => {
		expect(parseAmount('12.50', 2)).toBe(1250);
		expect(parseAmount('12,5', 2)).toBe(1250);
		expect(parseAmount('12', 2)).toBe(1200);
		expect(parseAmount('0.07', 2)).toBe(7);
		expect(parseAmount('1,234.56', 2)).toBe(123456);
	});
	it('avoids floating point error', () => {
		expect(parseAmount('0.29', 2)).toBe(29); // 0.29 * 100 === 28.999999999999996
		expect(parseAmount('1.15', 2)).toBe(115);
	});
	it('treats separators as grouping for zero-decimal currencies', () => {
		expect(parseAmount('1.250.000', 0)).toBe(1250000);
		expect(parseAmount('1,250,000', 0)).toBe(1250000);
		expect(parseAmount('50 000', 0)).toBe(50000);
	});
	it('rejects invalid input', () => {
		for (const bad of ['', 'abc', '-5', '0', '1e5', '12.345.6x']) {
			expect(parseAmount(bad, 2)).toBeNull();
		}
	});
});

describe('formatAmount', () => {
	it('uses the currency exponent', () => {
		expect(formatAmount(1250, 'SGD', 'en-SG', 2)).toContain('12.50');
		expect(formatAmount(50000, 'VND', 'en-SG', 0)).toMatch(/50,000/);
	});
});

describe('shortRate', () => {
	it('keeps six significant digits', () => {
		expect(shortRate('19538.461538461538461538')).toBe('19538.5');
		expect(shortRate('0.000051181102362204')).toBe('0.0000511811');
		expect(shortRate('1')).toBe('1');
	});
});
