import { afterEach, expect, test } from 'vitest';
import { page } from 'vitest/browser';
import { cleanup, render } from 'vitest-browser-svelte';
import VisibilitySelect from './VisibilitySelect.svelte';
import { setLocale } from '$lib/i18n';

afterEach(cleanup);

test('visibility defaults to Public and explains each selection', async () => {
	setLocale('en');
	await render(VisibilitySelect);
	const control = page.getByRole('button', { name: 'Visibility' });
	await expect.element(control).toHaveTextContent('Public');
	await expect.element(page.getByText('Listed in public directories and bookable by anyone with the link.')).toBeVisible();

	for (const [label, hint] of [
		['Unlisted', 'Hidden from public directories. Anyone with the link can book; no sign-in is required.'],
		['Private', 'Hidden from public directories and unavailable through the public booking link.'],
		['Public', 'Listed in public directories and bookable by anyone with the link.']
	]) {
		await control.click();
		await page.getByRole('option', { name: label, exact: true }).click();
		await expect.element(control).toHaveTextContent(label);
		await expect.element(page.getByText(hint, { exact: true })).toBeVisible();
	}
});

test('an existing Unlisted value is displayed on load', async () => {
	setLocale('en');
	await render(VisibilitySelect, { props: { value: 'unlisted' } });
	await expect.element(page.getByRole('button', { name: 'Visibility' })).toHaveTextContent('Unlisted');
});
