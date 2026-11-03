// Lightweight admin-UI i18n. Catalog lookup lives in `catalog.ts` so tests
// can run without Svelte stores. This module owns the reactive locale store
// and the `$t` / `tr` helpers used by the admin SPA.

import { derived, get, writable } from 'svelte/store';
import {
	type AdminLocale,
	STORAGE_KEY,
	resolveInitialLocale,
	translate,
	weekdayName as weekdayNameFromCatalog
} from './catalog';

export {
	ADMIN_LOCALES,
	type AdminLocale,
	STORAGE_KEY,
	isAdminLocale,
	matchAdminLocale,
	resolveInitialLocale,
	translate
} from './catalog';

function readStoredLocale(): string | null {
	try {
		return localStorage.getItem(STORAGE_KEY);
	} catch {
		return null;
	}
}

function browserLanguages(): readonly string[] {
	if (typeof navigator === 'undefined') return [];
	if (navigator.languages?.length) return navigator.languages;
	return navigator.language ? [navigator.language] : [];
}

function applyDocumentLang(code: AdminLocale) {
	if (typeof document !== 'undefined') {
		document.documentElement.lang = code;
	}
}

const initial = resolveInitialLocale(readStoredLocale(), browserLanguages());
applyDocumentLang(initial);

export const locale = writable<AdminLocale>(initial);

locale.subscribe((code) => {
	applyDocumentLang(code);
	try {
		localStorage.setItem(STORAGE_KEY, code);
	} catch {
		// Private mode / blocked storage — the in-memory store still works.
	}
});

export function setLocale(code: AdminLocale) {
	locale.set(code);
}

/** BCP-47 tag for Intl (`en`, `fr`). */
export function localeTag(code: AdminLocale = get(locale)): string {
	return code;
}

/** Reactive translator for templates: `{$t('nav.home')}`. */
export const t = derived(
	locale,
	($locale) =>
		(key: string, vars?: Record<string, string | number>) =>
			translate($locale, key, vars)
);

/** Non-reactive lookup for event handlers and toasts. */
export function tr(key: string, vars?: Record<string, string | number>): string {
	return translate(get(locale), key, vars);
}

export { WEEKDAY_KEYS } from './catalog';

export function weekdayName(dayIndex: number, translateFn: (key: string) => string = tr): string {
	return weekdayNameFromCatalog(dayIndex, translateFn);
}
