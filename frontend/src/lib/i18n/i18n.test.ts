import { describe, expect, test } from 'vitest';
import en from './en.json';
import fr from './fr.json';
import {
	isAdminLocale,
	matchAdminLocale,
	pluralForm,
	resolveInitialLocale,
	translate,
	translatePlural,
	translatePluralPair
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

	test('catalogs do not bake "(s)" plurals', () => {
		const baked: string[] = [];
		for (const [key, value] of Object.entries({ ...flatten(en), ...flatten(fr) })) {
			if (/\w\(s\)/.test(value) && !value.includes('http(s)')) baked.push(key);
		}
		expect(baked).toEqual([]);
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

describe('pluralForm', () => {
	test('English: 1 is one; 0 and >1 are other', () => {
		expect(pluralForm('en', 1)).toBe('one');
		expect(pluralForm('en', 0)).toBe('other');
		expect(pluralForm('en', 2)).toBe('other');
	});

	test('French: 0 and 1 are one; >1 is other (Intl.PluralRules)', () => {
		expect(pluralForm('fr', 0)).toBe('one');
		expect(pluralForm('fr', 1)).toBe('one');
		expect(pluralForm('fr', 2)).toBe('other');
	});
});

describe('translatePlural', () => {
	test('picks singular and plural member counts per locale', () => {
		expect(translatePlural('en', 'teams.memberCount', 1)).toBe('1 member');
		expect(translatePlural('en', 'teams.memberCount', 0)).toBe('0 members');
		expect(translatePlural('fr', 'teams.memberCount', 0)).toBe('0 membre');
		expect(translatePlural('fr', 'teams.memberCount', 3)).toBe('3 membres');
	});

	test('translate() with {count} selects .one / .other', () => {
		expect(translate('en', 'teams.memberCount', { count: 1 })).toBe('1 member');
		expect(translate('fr', 'recordings.toast.deleteAll', { count: 2 })).toBe(
			'2 enregistrements supprimés.'
		);
	});

	test('translatePluralPair selects both counts', () => {
		expect(
			translatePluralPair('fr', 'recordings.toast.deletePartial', 1, 2, { deleted: 1, failed: 2 })
		).toBe("1 supprimé ; 2 n'ont pas pu l'être.");
	});
});
