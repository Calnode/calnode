<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';

	let token = $derived($page.params.token);
	let inviteEmail = $state('');
	let valid = $state<boolean | null>(null);

	let name = $state('');
	let password = $state('');
	let timezone = $state(Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC');
	let submitting = $state(false);
	let error = $state('');

	onMount(async () => {
		const res = await fetch(`/v1/invites/${token}`);
		if (res.ok) {
			const data = await res.json();
			inviteEmail = data.email;
			valid = true;
		} else {
			valid = false;
		}
	});

	async function claim(e: SubmitEvent) {
		e.preventDefault();
		submitting = true;
		error = '';
		try {
			const res = await fetch(`/v1/invites/${token}/claim`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ name: name.trim(), password, timezone })
			});
			const data = await res.json().catch(() => ({}));
			if (res.ok) {
				window.location.href = '/admin';
			} else {
				error = data.error || 'Could not complete setup. Please try again.';
			}
		} finally {
			submitting = false;
		}
	}
</script>

<svelte:head><title>Accept invite — Calnode</title></svelte:head>

<div class="flex min-h-screen items-center justify-center bg-muted/30 p-6">
	<div class="w-full max-w-sm">
		<div class="mb-8 text-center">
			<div class="mb-3 flex justify-center">
				<svg viewBox="0 0 27 31" width="36" xmlns="http://www.w3.org/2000/svg">
					<rect width="27" height="31" rx="4" fill="#6366f1"/>
					<path fill="white" d="m 3.043653,30.614292 c -1.04707,-0.32444 -1.94939,-1.09611 -2.51053002,-2.14706 l -0.28493,-0.53364 -0.0338,-10.26902 c -0.0333,-10.1001 -0.0296,-10.28026 0.22157,-10.95165 C 0.93698298,5.3738426 2.254043,4.3160626 3.592343,4.1779326 l 0.66734,-0.069 0.0442,1.54722 c 0.0411,1.4322294 0.069,1.5846294 0.37758,2.0507194 0.43775,0.66128 1.06374,1.03377 1.8697695,1.11256 0.83411,0.0815 1.60889,-0.30288 2.1454309,-1.06449 0.35775,-0.50781 0.37448,-0.59108 0.41656,-2.0715194 l 0.0439,-1.54245 h 4.3226446 4.32263 l 0.0491,1.45985 c 0.0578,1.7152194 0.26715,2.2526994 1.10313,2.8320394 0.44147,0.30594 0.6462,0.36331 1.30282,0.36509 0.6673,0.002 0.85479,-0.0513 1.30717,-0.3706 0.83953,-0.59252 1.03808,-1.10942 1.09117,-2.8410394 l 0.0452,-1.47436 0.61114,0.0588 c 0.88513,0.085 1.92322,0.66148 2.52855,1.40407 0.98071,1.2030494 0.9433,0.7026404 0.90509,12.1120594 l -0.0339,10.12241 -0.34547,0.70339 c -0.37943,0.77247 -1.03179,1.43013 -1.85424,1.86928 l -0.53363,0.28492 -10.31215,0.0218 c -5.6716955,0.0121 -10.451945,-0.0215 -10.622765,-0.0745 z m 10.486245,-7.12509 c 0.41676,-0.42858 2.30921,-2.34577 4.20548,-4.26044 3.89443,-3.93222 3.79896,-3.77881 2.93494,-4.71617 -0.86333,-0.9366 -0.70987,-1.03489 -4.7574,3.04728 -2.01816,2.03542 -3.63135,3.56753 -3.70704,3.52074 -0.0737,-0.0455 -0.86549,-0.83379 -1.759495,-1.7516 -1.7365396,-1.78282 -2.1646795,-2.10404 -2.5380305,-1.90423 -0.40259,0.21546 -1.13741,1.12099 -1.13741,1.40162 0,0.18848 0.79327,1.06899 2.5741409,2.85723 3.0861346,3.09893 2.9741146,3.05059 4.1848146,1.80557 z"/>
				</svg>
			</div>
			<h1 class="text-xl font-semibold tracking-tight">You've been invited</h1>
		</div>

		{#if valid === null}
			<p class="text-center text-sm text-muted-foreground">Checking your invite…</p>
		{:else if valid === false}
			<div class="rounded-md bg-destructive/10 px-4 py-3 text-sm text-destructive text-center">
				This invite link has expired or already been used. Ask your admin to send a new one.
			</div>
		{:else}
			<div class="mb-5 rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground">
				This invite is for <span class="font-medium text-foreground">{inviteEmail}</span>.
				It cannot be used by anyone else.
			</div>

			{#if error}
				<div class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>
			{/if}

			<form onsubmit={claim} class="space-y-4">
				<div class="space-y-1.5">
					<Label for="name">Full name</Label>
					<Input id="name" type="text" autocomplete="name" bind:value={name} required />
				</div>
				<div class="space-y-1.5">
					<Label for="email-display">Email</Label>
					<Input id="email-display" type="email" value={inviteEmail} disabled />
				</div>
				<div class="space-y-1.5">
					<Label for="password">Password</Label>
					<Input id="password" type="password" autocomplete="new-password" bind:value={password} required minlength={8} />
					<p class="text-xs text-muted-foreground">Minimum 8 characters</p>
				</div>
				<Button type="submit" class="w-full" disabled={submitting}>
					{submitting ? 'Creating account…' : 'Create account'}
				</Button>
			</form>
		{/if}
	</div>
</div>
