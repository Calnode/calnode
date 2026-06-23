<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';

	type AuthStatus = {
		claimed: boolean;
		email_login: boolean;
		providers: string[];
		smtp_configured: boolean;
	};

	let status = $state<AuthStatus | null>(null);
	let email = $state('');
	let password = $state('');
	let submitting = $state(false);
	let loginError = $state('');
	let magicSubmitting = $state(false);
	let magicMessage = $state('');

	const oauthErrorMessages: Record<string, string> = {
		state: 'Login failed: invalid session state. Please try again.',
		denied: 'You denied access. Sign in is required to use the admin.',
		oauth: 'OAuth error. Please try again.',
		userinfo: 'Could not fetch your profile. Please try again.',
		no_account: 'No Calnode account found for your email. Contact your admin.',
		archived: 'Your account has been archived. If you think this is an error, please contact your workspace admin.',
		session: 'Could not create a session. Please try again.',
		link: 'This login link is invalid or has expired. Request a new one below.'
	};

	const errorKey = $derived($page.url.searchParams.get('error') ?? '');
	const oauthError = $derived(errorKey ? (oauthErrorMessages[errorKey] ?? 'An error occurred. Please try again.') : '');

	onMount(async () => {
		const res = await fetch('/v1/auth/status');
		if (res.ok) {
			status = await res.json();
			if (!status?.claimed) {
				window.location.href = '/admin/claim';
			}
		}
	});

	async function loginEmail(e: SubmitEvent) {
		e.preventDefault();
		submitting = true;
		loginError = '';
		try {
			const res = await fetch('/v1/auth/login/email', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ email: email.trim().toLowerCase(), password })
			});
			if (res.ok) {
				window.location.href = '/admin';
			} else {
				const data = await res.json().catch(() => ({}));
				loginError = data.error || 'Login failed. Please try again.';
			}
		} finally {
			submitting = false;
		}
	}

	async function sendMagicLink() {
		magicSubmitting = true;
		loginError = '';
		try {
			const res = await fetch('/v1/auth/magic-link/request', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ email: email.trim().toLowerCase() })
			});
			const data = await res.json().catch(() => ({}));
			magicMessage = data.message || 'If an account with that email exists, a login link is on its way.';
		} catch {
			magicMessage = 'If an account with that email exists, a login link is on its way.';
		} finally {
			magicSubmitting = false;
		}
	}

	const showGoogle = $derived(status?.providers?.includes('google') ?? false);
	const showMicrosoft = $derived(status?.providers?.includes('microsoft') ?? false);
	const showEmail = $derived(status?.email_login ?? false);
	const showForgot = $derived(status?.smtp_configured ?? false);
	const showMagic = $derived(status?.smtp_configured ?? false);
	const showDivider = $derived((showGoogle || showMicrosoft) && showEmail);
</script>

<svelte:head><title>Sign in — Calnode</title></svelte:head>

<div class="flex min-h-screen items-center justify-center bg-muted/30 p-6">
	<div class="w-full max-w-sm">
		<div class="mb-8 text-center">
			<div class="mb-3 flex justify-center">
				<svg viewBox="0 0 27 31" width="36" xmlns="http://www.w3.org/2000/svg">
					<rect width="27" height="31" rx="4" fill="#6366f1"/>
					<path fill="white" d="m 3.043653,30.614292 c -1.04707,-0.32444 -1.94939,-1.09611 -2.51053002,-2.14706 l -0.28493,-0.53364 -0.0338,-10.26902 c -0.0333,-10.1001 -0.0296,-10.28026 0.22157,-10.95165 C 0.93698298,5.3738426 2.254043,4.3160626 3.592343,4.1779326 l 0.66734,-0.069 0.0442,1.54722 c 0.0411,1.4322294 0.069,1.5846294 0.37758,2.0507194 0.43775,0.66128 1.06374,1.03377 1.8697695,1.11256 0.83411,0.0815 1.60889,-0.30288 2.1454309,-1.06449 0.35775,-0.50781 0.37448,-0.59108 0.41656,-2.0715194 l 0.0439,-1.54245 h 4.3226446 4.32263 l 0.0491,1.45985 c 0.0578,1.7152194 0.26715,2.2526994 1.10313,2.8320394 0.44147,0.30594 0.6462,0.36331 1.30282,0.36509 0.6673,0.002 0.85479,-0.0513 1.30717,-0.3706 0.83953,-0.59252 1.03808,-1.10942 1.09117,-2.8410394 l 0.0452,-1.47436 0.61114,0.0588 c 0.88513,0.085 1.92322,0.66148 2.52855,1.40407 0.98071,1.2030494 0.9433,0.7026404 0.90509,12.1120594 l -0.0339,10.12241 -0.34547,0.70339 c -0.37943,0.77247 -1.03179,1.43013 -1.85424,1.86928 l -0.53363,0.28492 -10.31215,0.0218 c -5.6716955,0.0121 -10.451945,-0.0215 -10.622765,-0.0745 z m 10.486245,-7.12509 c 0.41676,-0.42858 2.30921,-2.34577 4.20548,-4.26044 3.89443,-3.93222 3.79896,-3.77881 2.93494,-4.71617 -0.86333,-0.9366 -0.70987,-1.03489 -4.7574,3.04728 -2.01816,2.03542 -3.63135,3.56753 -3.70704,3.52074 -0.0737,-0.0455 -0.86549,-0.83379 -1.759495,-1.7516 -1.7365396,-1.78282 -2.1646795,-2.10404 -2.5380305,-1.90423 -0.40259,0.21546 -1.13741,1.12099 -1.13741,1.40162 0,0.18848 0.79327,1.06899 2.5741409,2.85723 3.0861346,3.09893 2.9741146,3.05059 4.1848146,1.80557 z"/>
				</svg>
			</div>
			<h1 class="text-xl font-semibold tracking-tight">Sign in to Calnode</h1>
		</div>

		{#if oauthError}
			<div class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{oauthError}</div>
		{/if}
		{#if loginError}
			<div class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{loginError}</div>
		{/if}

		{#if status === null}
			<div class="text-center text-sm text-muted-foreground">Loading…</div>
		{:else}
			{#if showGoogle}
				<Button variant="outline" class="h-11 w-full" onclick={() => window.location.href = '/v1/auth/login'}>
					<svg width="16" height="16" viewBox="0 0 48 48" aria-hidden="true" class="mr-2">
						<path fill="#EA4335" d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z"/>
						<path fill="#4285F4" d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z"/>
						<path fill="#FBBC05" d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z"/>
						<path fill="#34A853" d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.18 1.48-4.97 2.29-8.16 2.29-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z"/>
					</svg>
					Sign in with Google
				</Button>
			{/if}

			{#if showMicrosoft}
				<Button variant="outline" class="h-11 w-full {showGoogle ? 'mt-3' : ''}" onclick={() => window.location.href = '/v1/auth/microsoft/login'}>
					<svg width="16" height="16" viewBox="0 0 23 23" aria-hidden="true" class="mr-2">
						<path fill="#F25022" d="M1 1h10v10H1z"/>
						<path fill="#7FBA00" d="M12 1h10v10H12z"/>
						<path fill="#00A4EF" d="M1 12h10v10H1z"/>
						<path fill="#FFB900" d="M12 12h10v10H12z"/>
					</svg>
					Sign in with Microsoft
				</Button>
			{/if}

			{#if showDivider}
				<div class="my-4 flex items-center gap-3 text-xs text-muted-foreground">
					<div class="h-px flex-1 bg-border"></div>
					or
					<div class="h-px flex-1 bg-border"></div>
				</div>
			{/if}

			{#if showEmail}
				<form onsubmit={loginEmail} class="space-y-4">
					<div class="space-y-1.5">
						<Label for="email">Email</Label>
						<Input id="email" type="email" autocomplete="email" bind:value={email} required />
					</div>
					<div class="space-y-1.5">
						<div class="flex items-center justify-between">
							<Label for="password">Password</Label>
							{#if showForgot}
								<a href="/admin/forgot-password" class="text-xs text-muted-foreground hover:underline">Forgot password?</a>
							{/if}
						</div>
						<Input id="password" type="password" autocomplete="current-password" bind:value={password} required />
					</div>
					<Button type="submit" class="h-11 w-full" disabled={submitting}>
						{submitting ? 'Signing in…' : 'Sign in'}
					</Button>
				</form>
			{/if}

			{#if showMagic}
				{#if showGoogle || showMicrosoft || showEmail}
					<div class="my-4 flex items-center gap-3 text-xs text-muted-foreground">
						<div class="h-px flex-1 bg-border"></div>
						or
						<div class="h-px flex-1 bg-border"></div>
					</div>
				{/if}
				{#if magicMessage}
					<div class="rounded-md bg-green-50 px-3 py-2.5 text-sm text-green-700">{magicMessage}</div>
				{:else}
					<form onsubmit={(e) => { e.preventDefault(); sendMagicLink(); }} class="space-y-3">
						{#if !showEmail}
							<div class="space-y-1.5">
								<Label for="magic-email">Email</Label>
								<Input id="magic-email" type="email" autocomplete="email" bind:value={email} required />
							</div>
						{/if}
						<Button type="submit" variant="outline" class="h-11 w-full" disabled={magicSubmitting}>
							{magicSubmitting ? 'Sending…' : 'Email me a login link'}
						</Button>
						<p class="text-center text-xs text-muted-foreground">A one-time sign-in link, no password needed.</p>
					</form>
				{/if}
			{/if}

			{#if !showGoogle && !showMicrosoft && !showEmail && !showMagic}
				<p class="text-center text-sm text-muted-foreground">No login methods are configured. Contact your administrator.</p>
			{/if}
		{/if}
	</div>
</div>
