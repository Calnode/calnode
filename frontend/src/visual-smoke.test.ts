import { describe, test, expect, afterEach } from 'vitest';
import { render, cleanup } from 'vitest-browser-svelte';
import { today, getLocalTimeZone } from '@internationalized/date';
import Switch from '$lib/components/ui/switch/switch.svelte';
import Checkbox from '$lib/components/ui/checkbox/checkbox.svelte';
import Calendar from '$lib/components/ui/calendar/calendar.svelte';

// Resolve a CSS value (e.g. `var(--primary)`) to the browser's computed color
// string, so comparisons are exact and color-space-agnostic (same engine, same
// serialization) rather than fragile literal matching.
function resolveColor(value: string): string {
	const probe = document.createElement('div');
	probe.style.backgroundColor = value;
	document.body.appendChild(probe);
	const c = getComputedStyle(probe).backgroundColor;
	probe.remove();
	return c;
}

afterEach(cleanup);

// These tests guard the silent bug class where a shadcn-svelte component renders
// with NO state styling because its custom Tailwind variant (data-checked,
// data-selected, …) was never declared in app.css. Logic/state stays correct, so
// only a real-browser computed-style assertion catches it. See TESTING.md.
describe('shadcn component state styling', () => {
	test('Switch: checked = --primary fill, unchecked = --input', async () => {
		const { container } = await render(Switch, { props: { checked: true } });
		const on = container.querySelector('[data-slot="switch"]')!;
		expect(getComputedStyle(on).backgroundColor).toBe(resolveColor('var(--primary)'));

		cleanup();
		const { container: c2 } = await render(Switch, { props: { checked: false } });
		const off = c2.querySelector('[data-slot="switch"]')!;
		const offBg = getComputedStyle(off).backgroundColor;
		expect(offBg).toBe(resolveColor('var(--input)'));
		expect(offBg).not.toBe(resolveColor('var(--primary)'));
	});

	test('Switch: thumb translates when checked', async () => {
		const { container } = await render(Switch, { props: { checked: true } });
		const thumb = container.querySelector('[data-slot="switch-thumb"]')!;
		const t = getComputedStyle(thumb).translate;
		// data-checked:translate-x-… must apply; unstyled would be "none"/"0px".
		expect(t).not.toBe('none');
		expect(t).not.toBe('0px');
		expect(t).not.toBe('');
	});

	test('Checkbox: checked = --primary fill', async () => {
		const { container } = await render(Checkbox, { props: { checked: true } });
		const cb = container.querySelector('[data-slot="checkbox"]')!;
		expect(getComputedStyle(cb).backgroundColor).toBe(resolveColor('var(--primary)'));
	});

	// NB: query the Day element ([data-bits-day]) — the styling classes live there,
	// while the parent Cell (<td>) also carries data-selected/data-today.
	test('Calendar: today cell is underlined (data-today)', async () => {
		const { container } = await render(Calendar, {});
		const todayCell = container.querySelector('[data-bits-day][data-today]')!;
		expect(todayCell).toBeTruthy();
		expect(getComputedStyle(todayCell).textDecorationLine).toContain('underline');
	});

	test('Calendar: selected day = --primary fill (data-selected)', async () => {
		const { container } = await render(Calendar, {
			props: { value: today(getLocalTimeZone()) }
		});
		const selected = container.querySelector('[data-bits-day][data-selected]')!;
		expect(selected).toBeTruthy();
		expect(getComputedStyle(selected).backgroundColor).toBe(resolveColor('var(--primary)'));
	});
});
