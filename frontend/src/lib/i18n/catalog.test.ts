import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import en from './en.json' with { type: 'json' };
import fr from './fr.json' with { type: 'json' };
import {
	isAdminLocale,
	matchAdminLocale,
	pluralForm,
	resolveInitialLocale,
	translate,
	translatePlural,
	translatePluralPair
} from './catalog.ts';

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
		assert.deepEqual(frKeys, enKeys);
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
		assert.deepEqual(mismatches, []);
	});

	test('catalogs do not bake "(s)" plurals', () => {
		const baked: string[] = [];
		for (const [key, value] of Object.entries({ ...flatten(en), ...flatten(fr) })) {
			if (/\w\(s\)/.test(value) && !value.includes('http(s)')) baked.push(key);
		}
		assert.deepEqual(baked, []);
	});
});

describe('translate', () => {
	test('returns the French string when the key exists', () => {
		assert.equal(translate('fr', 'layout.nav.eventTypes'), 'Types de rendez-vous');
		assert.equal(translate('fr', 'layout.nav.settings'), 'Paramètres');
	});

	test('falls back to the key when missing from every catalog', () => {
		assert.ok(translate('fr', 'common.save'));
		assert.equal(translate('fr', 'does.not.exist'), 'does.not.exist');
	});

	test('interpolates {placeholders}', () => {
		assert.equal(translate('en', 'bookings.filter.upcoming', { count: 3 }), 'Upcoming (3)');
		assert.match(translate('fr', 'bookings.filter.upcoming', { count: 3 }), /3/);
	});

	test('leaves unknown placeholders intact', () => {
		assert.equal(translate('en', 'bookings.filter.upcoming', {}), 'Upcoming ({count})');
	});
});

describe('locale matching', () => {
	test('isAdminLocale only accepts shipped codes', () => {
		assert.equal(isAdminLocale('en'), true);
		assert.equal(isAdminLocale('fr'), true);
		assert.equal(isAdminLocale('de'), false);
		assert.equal(isAdminLocale('fr-CA'), false);
	});

	test('matchAdminLocale maps regional tags onto the primary language', () => {
		assert.equal(matchAdminLocale('fr-CA'), 'fr');
		assert.equal(matchAdminLocale('fr-FR'), 'fr');
		assert.equal(matchAdminLocale('en-GB'), 'en');
		assert.equal(matchAdminLocale('de-DE'), null);
	});

	test('stored preference wins over the browser', () => {
		assert.equal(resolveInitialLocale('fr', ['en-US', 'en']), 'fr');
		assert.equal(resolveInitialLocale('en', ['fr-FR']), 'en');
	});

	test('browser language is used when nothing is stored', () => {
		assert.equal(resolveInitialLocale(null, ['fr-CA', 'en']), 'fr');
		assert.equal(resolveInitialLocale(null, ['de-DE', 'fr']), 'fr');
		assert.equal(resolveInitialLocale(null, ['de-DE', 'ja']), 'en');
	});

	test('unknown stored value falls through to the browser, then English', () => {
		assert.equal(resolveInitialLocale('pt-BR', ['fr']), 'fr');
		assert.equal(resolveInitialLocale('nope', ['ja']), 'en');
	});
});

describe('pluralForm', () => {
	test('English: 1 is one; 0 and >1 are other', () => {
		assert.equal(pluralForm('en', 1), 'one');
		assert.equal(pluralForm('en', 0), 'other');
		assert.equal(pluralForm('en', 2), 'other');
	});

	test('French: 0 and 1 are one; >1 is other (Intl.PluralRules)', () => {
		assert.equal(pluralForm('fr', 0), 'one');
		assert.equal(pluralForm('fr', 1), 'one');
		assert.equal(pluralForm('fr', 2), 'other');
	});
});

describe('translatePlural', () => {
	test('picks singular and plural member counts per locale', () => {
		assert.equal(translatePlural('en', 'teams.memberCount', 1), '1 member');
		assert.equal(translatePlural('en', 'teams.memberCount', 0), '0 members');
		assert.equal(translatePlural('en', 'teams.memberCount', 3), '3 members');
		assert.equal(translatePlural('fr', 'teams.memberCount', 1), '1 membre');
		assert.equal(translatePlural('fr', 'teams.memberCount', 0), '0 membre');
		assert.equal(translatePlural('fr', 'teams.memberCount', 3), '3 membres');
	});

	test('translate() with {count} selects .one / .other', () => {
		assert.equal(translate('en', 'teams.memberCount', { count: 1 }), '1 member');
		assert.equal(translate('fr', 'recordings.toast.deleteAll', { count: 2 }), '2 enregistrements supprimés.');
	});

	test('translatePluralPair selects both counts', () => {
		assert.equal(
			translatePluralPair('fr', 'recordings.toast.deletePartial', 1, 2, { deleted: 1, failed: 2 }),
			"1 supprimé ; 2 n'ont pas pu l'être."
		);
		assert.equal(
			translatePluralPair('fr', 'recordings.toast.deletePartial', 2, 1, { deleted: 2, failed: 1 }),
			"2 supprimés ; 1 n'a pas pu l'être."
		);
	});
});
