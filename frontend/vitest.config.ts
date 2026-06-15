import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';
import { playwright } from '@vitest/browser-playwright';
import { fileURLToPath } from 'node:url';

// Standalone Vitest config (NOT the app's vite.config.ts — we use the bare
// svelte() plugin instead of sveltekit() so components mount in isolation, with
// the Tailwind plugin active so app.css compiles real utilities into the test
// page. This is what lets us assert *computed styles* in a real browser, which
// is the only thing that catches a missing/wrong @custom-variant.
export default defineConfig({
	plugins: [tailwindcss(), svelte()],
	resolve: {
		alias: {
			$lib: fileURLToPath(new URL('./src/lib', import.meta.url))
		}
	},
	test: {
		setupFiles: ['./src/test-setup.ts'],
		include: ['src/**/*.{test,spec}.{js,ts}'],
		browser: {
			enabled: true,
			provider: playwright(),
			headless: true,
			instances: [{ browser: 'chromium' }]
		}
	}
});
