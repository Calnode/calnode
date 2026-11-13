import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	server: {
		proxy: {
			'/v1': 'http://localhost:3010',
			'/book': 'http://localhost:3010',
			'/t': 'http://localhost:3010',
			'/manage': 'http://localhost:3010',
			'/u': 'http://localhost:3010',
			'/team': 'http://localhost:3010',
			'/booking.css': 'http://localhost:3010',
			'/embed.js': 'http://localhost:3010'
		}
	}
});
