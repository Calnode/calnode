import { describe, expect, test } from 'vitest';
import en from './en.json';
import fr from './fr.json';
import {
	isAdminLocale,
	matchAdminLocale,
	pluralForm,
	resolveInitialLocale,
	splitLinked,
	translate,
	translatePlural,
	translatePluralPair
} from './catalog';

const LINKED_SENTENCES: Record<string, string[]> = {
	'calendar.noProvider.body': ['google'],
	'settings.google.setup.step1': ['console'],
	'settings.google.setup.step3Testing': ['guide'],
	'settings.payments.setup.step1': ['dashboard'],
	'settings.storage.setup.step1': ['r2', 'b2', 's3'],
	'settings.video.setup.step1': ['cloud', 'selfHosted'],
	'settings.video.webhook.step1': ['docs'],
	'settings.video.notetaker.deepgram.hint': ['deepgram'],
	'settings.zoom.setup.step1': ['marketplace'],
	'settings.zoom.setup.step2': ['videoDocs'],
	'settings.zoom.multiMemberWarning': ['zoomDocs'],
	'eventTypeEditor.invite.hint.calnode': ['email'],
	'eventTypeEditor.invite.emailNotSetup': ['email'],
	'eventTypeEditor.price.hint': ['stripe'],
	'eventTypeEditor.notifications.smtpNotConfigured': ['smtp']
};

const LINK_TEXT_KEYS = [
	'calendar.noProvider.googleLink',
	'settings.google.setup.console',
	'settings.google.setup.guide',
	'settings.payments.setup.dashboard',
	'settings.storage.setup.r2',
	'settings.storage.setup.b2',
	'settings.storage.setup.s3',
	'settings.video.setup.cloud',
	'settings.video.setup.selfHosted',
	'settings.video.webhook.docs',
	'settings.video.notetaker.deepgram.console',
	'settings.zoom.setup.marketplace',
	'settings.zoom.setup.videoDocs',
	'settings.zoom.zoomDocs',
	'eventTypeEditor.invite.settingsEmailLink',
	'eventTypeEditor.price.stripeLink',
	'eventTypeEditor.notifications.smtpNotConfiguredLink'
];

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

	test('linked sentences keep the same {placeholders} in both locales', () => {
		const ph = /\{(\w+)\}/g;
		const names = (s: string) => [...s.matchAll(ph)].map((m) => m[1]);
		const enFlat = flatten(en);
		const frFlat = flatten(fr);
		const missing: string[] = [];
		for (const [key, expected] of Object.entries(LINKED_SENTENCES)) {
			for (const catalog of [enFlat, frFlat]) {
				const got = names(catalog[key] ?? '');
				if (expected.some((n) => !got.includes(n))) missing.push(key);
			}
		}
		expect(missing).toEqual([]);
	});

	test('link-text keys exist and are plain text', () => {
		const enFlat = flatten(en);
		const frFlat = flatten(fr);
		const bad: string[] = [];
		for (const key of LINK_TEXT_KEYS) {
			for (const catalog of [enFlat, frFlat]) {
				const value = catalog[key] ?? '';
				if (!value || /\{(\w+)\}/.test(value) || /<\/?a[\s>]/.test(value)) bad.push(key);
			}
		}
		expect(bad).toEqual([]);
	});

	test('catalogs do not embed HTML anchors', () => {
		const html: string[] = [];
		for (const [key, value] of Object.entries({ ...flatten(en), ...flatten(fr) })) {
			if (/<\/?a[\s>]/i.test(value) || /href\s*=/.test(value)) html.push(key);
		}
		expect(html).toEqual([]);
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

describe('splitLinked', () => {
	test('splits named placeholders and leaves others intact', () => {
		expect(splitLinked('Go to {console}. See {guide}.', ['console', 'guide'])).toEqual([
			{ type: 'text', value: 'Go to ' },
			{ type: 'link', name: 'console' },
			{ type: 'text', value: '. See ' },
			{ type: 'link', name: 'guide' },
			{ type: 'text', value: '.' }
		]);
		expect(splitLinked('Hello {name}, open {email}.', ['email'])).toEqual([
			{ type: 'text', value: 'Hello {name}, open ' },
			{ type: 'link', name: 'email' },
			{ type: 'text', value: '.' }
		]);
	});

	test('returns the whole string when no names are given', () => {
		expect(splitLinked('See {docs}.')).toEqual([{ type: 'text', value: 'See {docs}.' }]);
		expect(splitLinked('')).toEqual([]);
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
