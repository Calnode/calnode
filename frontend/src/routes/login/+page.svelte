<script lang="ts">
	import { page } from '$app/stores';

	const errorMessages: Record<string, string> = {
		state: 'Login failed: invalid session state. Please try again.',
		denied: 'You denied Google access. Sign in is required to use the admin.',
		oauth: 'Google OAuth error. Please try again.',
		userinfo: 'Could not fetch your Google profile. Please try again.',
		no_account: 'No Calnode account found for your Google email. Contact your admin.',
		session: 'Could not create a session. Please try again.'
	};

	$: errorKey = $page.url.searchParams.get('error') ?? '';
	$: errorMsg = errorKey ? (errorMessages[errorKey] ?? 'An error occurred. Please try again.') : '';
</script>

<svelte:head><title>Sign in — Calnode</title></svelte:head>

<div class="login-wrap">
	<div class="login-card">
		<div class="brand">
			<span class="brand-icon">⚡</span>
			<span class="brand-name">Calnode</span>
		</div>

		<h1>Admin sign in</h1>
		<p class="subtitle">Sign in with your Google account to manage your workspace.</p>

		{#if errorMsg}
			<div class="error-msg">{errorMsg}</div>
		{/if}

		<a href="/v1/auth/login" class="google-btn">
			<svg width="18" height="18" viewBox="0 0 48 48" aria-hidden="true">
				<path fill="#EA4335" d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z"/>
				<path fill="#4285F4" d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z"/>
				<path fill="#FBBC05" d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z"/>
				<path fill="#34A853" d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.18 1.48-4.97 2.29-8.16 2.29-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z"/>
			</svg>
			Sign in with Google
		</a>
	</div>
</div>

<style>
	.login-wrap {
		min-height: 100vh;
		display: flex;
		align-items: center;
		justify-content: center;
		background: var(--bg);
		padding: 24px;
	}

	.login-card {
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: 10px;
		padding: 40px 36px;
		width: 100%;
		max-width: 380px;
		text-align: center;
	}

	.brand {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 8px;
		margin-bottom: 24px;
	}
	.brand-icon { font-size: 24px; }
	.brand-name { font-size: 22px; font-weight: 700; color: var(--text); letter-spacing: -0.03em; }

	h1 { margin: 0 0 6px; font-size: 18px; font-weight: 600; }
	.subtitle { margin: 0 0 24px; color: var(--text-muted); font-size: 13px; }

	.google-btn {
		display: inline-flex;
		align-items: center;
		gap: 10px;
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: 6px;
		padding: 10px 20px;
		font-size: 14px;
		font-weight: 500;
		color: var(--text);
		text-decoration: none;
		cursor: pointer;
		transition: background 0.15s, box-shadow 0.15s;
		width: 100%;
		justify-content: center;
	}
	.google-btn:hover { background: var(--bg); box-shadow: 0 1px 3px rgba(0,0,0,0.1); text-decoration: none; }
</style>
