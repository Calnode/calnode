import { describe, expect, test } from 'vitest';
import en from './en.json';
import fr from './fr.json';
import {
	isAdminLocale,
	matchAdminLocale,
	resolveInitialLocale,
	translate
} from './catalog';

function flatten(obj: unknown, prefix = ''): Record<string, string> {
	const out: Record<string, string> = {};
	if (obj && typeof obj === 'object') {
		for (const [k, v] of Object.entries(obj as Record<string, unknown>)) {
			const path = prefix ? `${prefix}.${k}` : k;
			if (v && typeof v === 'object') Object.assign(out, flatten(v, path));
			else if (typeof v === 'string') out[path] = v;
		}
	}
	return out;
}

describe('admin i18n catalogs', () => {
	test('French has the same keys as English', () => {
		const enKeys = Object.keys(flatten(en)).sort();
		const frKeys = Object.keys(flatten(fr)).sort();
		expect(frKeys).toEqual(enKeys);
	});

	test('placeholders match between English and French', () => {
		const ph = /\{(\w+)\}/g;
		const names = (s: string) => new Set([...s.matchAll(ph)].map((m) => m[1]));
		const enFlat = flatten(en);
		const frFlat = flatten(fr);
		const mismatches: string[] = [];
		for (const [key, value] of Object.entries(enFlat)) {
			const a = names(value);
			const b = names(frFlat[key] ?? '');
			if (a.size !== b.size || [...a].some((n) => !b.has(n))) {
				mismatches.push(key);
			}
		}
		expect(mismatches).toEqual([]);
	});
});

describe('translate', () => {
	test('returns the French string when the key exists', () => {
		expect(translate('fr', 'layout.nav.eventTypes')).toBe('Types de rendez-vous');
		expect(translate('fr', 'layout.nav.settings')).toBe('Paramètres');
	});

	test('falls back to English when a key is missing from French', () => {
		expect(translate('fr', 'common.save')).toBeTruthy();
		// A key that only exists conceptually: unknown key returns the key.
		expect(translate('fr', 'does.not.exist')).toBe('does.not.exist');
	});

	test('interpolates {placeholders}', () => {
		expect(translate('en', 'bookings.filter.upcoming', { count: 3 })).toBe('Upcoming (3)');
		expect(translate('fr', 'bookings.filter.upcoming', { count: 3 })).toContain('3');
	});

	test('leaves unknown placeholders intact', () => {
		expect(translate('en', 'bookings.filter.upcoming', {})).toBe('Upcoming ({count})');
	});
});

describe('locale matching', () => {
	test('isAdminLocale only accepts shipped codes', () => {
		expect(isAdminLocale('en')).toBe(true);
		expect(isAdminLocale('fr')).toBe(true);
		expect(isAdminLocale('de')).toBe(false);
		expect(isAdminLocale('fr-CA')).toBe(false);
	});

	test('matchAdminLocale maps regional tags onto the primary language', () => {
		expect(matchAdminLocale('fr-CA')).toBe('fr');
		expect(matchAdminLocale('fr-FR')).toBe('fr');
		expect(matchAdminLocale('en-GB')).toBe('en');
		expect(matchAdminLocale('de-DE')).toBe(null);
	});

	test('stored preference wins over the browser', () => {
		expect(resolveInitialLocale('fr', ['en-US', 'en'])).toBe('fr');
		expect(resolveInitialLocale('en', ['fr-FR'])).toBe('en');
	});

	test('browser language is used when nothing is stored', () => {
		expect(resolveInitialLocale(null, ['fr-CA', 'en'])).toBe('fr');
		expect(resolveInitialLocale(null, ['de-DE', 'fr'])).toBe('fr');
		expect(resolveInitialLocale(null, ['de-DE', 'ja'])).toBe('en');
	});

	test('unknown stored value falls through to the browser, then English', () => {
		expect(resolveInitialLocale('pt-BR', ['fr'])).toBe('fr');
		expect(resolveInitialLocale('nope', ['ja'])).toBe('en');
	});
});
