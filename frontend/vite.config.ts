import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';
import tailwindcss from '@tailwindcss/vite';

const apiProxy = {
	target: 'http://localhost:3010',
	changeOrigin: false,
	configure: (proxy: any) => {
		proxy.on('proxyReq', (proxyReq: any, req: any) => {
			if (req.headers.host) {
				proxyReq.setHeader('host', req.headers.host);
			}
		});
	}
};

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	server: {
		proxy: {
			'/v1': apiProxy,
			'/book': apiProxy,
			'/t': apiProxy,
			'/manage': apiProxy,
			'/u': apiProxy,
			'/team': apiProxy,
			'/booking.css': apiProxy,
			'/embed.js': apiProxy
		}
	}
});
