// Store-free catalog helpers. English is the source of truth; missing keys
// (and unknown locales) fall back to the English catalog, then to the key.
//
// Adding a locale: copy en.json to <code>.json, translate the values (keep
// every key and any {placeholders}), then register the import in `catalogs`
// and the display name in ADMIN_LOCALES. No other code change is required.
// Booking-page locales (internal/i18n/locales) are a separate system.

import en from './en.json' with { type: 'json' };
import fr from './fr.json' with { type: 'json' };

export const ADMIN_LOCALES = [
	{ code: 'en', name: 'English' },
	{ code: 'fr', name: 'Français' }
] as const;

export type AdminLocale = (typeof ADMIN_LOCALES)[number]['code'];

export const STORAGE_KEY = 'calnode-admin-locale';

type Catalog = Record<string, unknown>;

const catalogs: Record<AdminLocale, Catalog> = { en, fr };

const localeSet = new Set<string>(ADMIN_LOCALES.map((l) => l.code));

export function isAdminLocale(value: string | null | undefined): value is AdminLocale {
	return !!value && localeSet.has(value);
}

/** Map a BCP-47 tag onto a shipped admin locale (`fr-CA` → `fr`, `en-GB` → `en`). */
export function matchAdminLocale(tag: string): AdminLocale | null {
	const lower = tag.trim().toLowerCase();
	if (!lower) return null;
	const primary = lower.split('-')[0];
	if (isAdminLocale(primary)) return primary;
	return null;
}

export function resolveInitialLocale(
	stored: string | null = null,
	navigatorLanguages: readonly string[] = []
): AdminLocale {
	if (isAdminLocale(stored)) return stored;
	if (stored) {
		const fromStored = matchAdminLocale(stored);
		if (fromStored) return fromStored;
	}
	for (const tag of navigatorLanguages) {
		const hit = matchAdminLocale(tag);
		if (hit) return hit;
	}
	return 'en';
}

function lookup(catalog: Catalog, key: string): string | undefined {
	let cur: unknown = catalog;
	for (const part of key.split('.')) {
		if (cur && typeof cur === 'object' && part in (cur as object)) {
			cur = (cur as Record<string, unknown>)[part];
		} else {
			return undefined;
		}
	}
	return typeof cur === 'string' ? cur : undefined;
}

function interpolate(template: string, vars?: Record<string, string | number>): string {
	if (!vars) return template;
	return template.replace(/\{(\w+)\}/g, (match, name: string) =>
		Object.prototype.hasOwnProperty.call(vars, name) ? String(vars[name]) : match
	);
}

export function translate(
	code: AdminLocale,
	key: string,
	vars?: Record<string, string | number>
): string {
	const hit = lookup(catalogs[code], key) ?? lookup(catalogs.en, key) ?? key;
	return interpolate(hit, vars);
}

export const WEEKDAY_KEYS = [
	'sunday',
	'monday',
	'tuesday',
	'wednesday',
	'thursday',
	'friday',
	'saturday'
] as const;

export function weekdayName(dayIndex: number, translateFn: (key: string) => string): string {
	const key = WEEKDAY_KEYS[((dayIndex % 7) + 7) % 7];
	return translateFn(`availability.days.${key}`);
}
