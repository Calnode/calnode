<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type GoogleSettings } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';

	let loading = $state(true);
	let saving = $state(false);

	let googleSettings: GoogleSettings | null = $state(null);
	let clientID = $state('');
	let clientSecret = $state('');

	// Host the server builds its OAuth redirect URIs from. Prefer the server's
	// configured base_url so the displayed URIs match exactly what we send to
	// Google; fall back to the current origin if it's somehow blank.
	const redirectBase = $derived(
		googleSettings?.base_url || (typeof window !== 'undefined' ? window.location.origin : '')
	);
	const isLocal = $derived(redirectBase.includes('localhost') || redirectBase.includes('127.0.0.1'));

	onMount(async () => {
		try {
			googleSettings = await api.get<GoogleSettings>('/v1/settings/google');
			clientID = googleSettings.client_id;
		} catch (e: any) {
			toast.error(e.message || 'Could not load Google settings');
		} finally {
			loading = false;
		}
	});

	async function save() {
		saving = true;
		try {
			const body: Record<string, unknown> = { client_id: clientID };
			if (clientSecret) body.client_secret = clientSecret;
			googleSettings = await api.patch<GoogleSettings>('/v1/settings/google', body);
			clientSecret = '';
			toast.success('Saved — go to Calendar to connect your account');
		} catch (e: any) {
			toast.error(e.message || 'Could not save Google settings');
		} finally {
			saving = false;
		}
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !saving)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">Admin access required.</p>
{:else}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else}
	<div class="max-w-lg space-y-4">

		{#if !googleSettings?.configured}
		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-4 text-sm font-semibold">Setup instructions</h2>
			<ol class="space-y-4 text-sm">
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">1</span>
					<div>
						Go to <a href="https://console.cloud.google.com" target="_blank" rel="noopener noreferrer" class="font-medium text-primary underline">console.cloud.google.com</a>.
						If you don't have a project, create one — any name works.
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">2</span>
					<div>
						Go to <span class="font-medium">APIs &amp; Services → Library</span>, search for
						<span class="font-medium">Google Calendar API</span>, and enable it.
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">3</span>
					<div>
						Go to <span class="font-medium">APIs &amp; Services → OAuth consent screen</span>.
						Choose <span class="font-medium">External</span> (or Internal if you have Google Workspace).
						Fill in the app name and your email, then save.
						<p class="mt-1.5 text-xs text-muted-foreground">
							While the app is in <span class="font-medium">Testing</span>, only Google accounts added as test users can connect, and Google caps this at 100 users. Once your team members are ready to connect their calendars, click <span class="font-medium">Publish app</span> to lift the limit (Internal / Workspace apps have no cap). See
							<a href="https://support.google.com/cloud/answer/15549945" target="_blank" rel="noopener noreferrer" class="text-primary underline">Google's guide</a>.
						</p>
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">4</span>
					<div>
						Go to <span class="font-medium">Credentials → Create Credentials → OAuth client ID</span>.
						Set application type to <span class="font-medium">Web application</span>.
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">5</span>
					<div>
						Under <span class="font-medium">Authorised redirect URIs</span>, add both:
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{redirectBase}/v1/calendar/callback</code>
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{redirectBase}/v1/auth/callback</code>
						Click <span class="font-medium">Create</span>. Copy the Client ID and Client Secret shown.
						{#if !isLocal}
							<p class="mt-1.5 text-xs text-muted-foreground">
								If you also run Calnode locally, add the
								<code class="rounded bg-muted px-1">http://localhost:3000/…</code> variants of both URIs too.
							</p>
						{/if}
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">6</span>
					<div>Paste them into the form below and save.</div>
				</li>
			</ol>
		</div>
		{/if}

		<div class="rounded-lg border bg-card p-6">
			<div class="mb-4 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">Google OAuth</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">Enables Google sign-in and Google Calendar integration.</p>
				</div>
				{#if googleSettings !== null}
					<span class="flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium {googleSettings.configured ? 'bg-green-50 text-green-700' : 'bg-amber-50 text-amber-700'}">
						<span class="h-1.5 w-1.5 rounded-full {googleSettings.configured ? 'bg-green-500' : 'bg-amber-400'}"></span>
						{googleSettings.configured ? 'Configured' : 'Not configured'}
					</span>
				{/if}
			</div>

			<div class="space-y-3">
				<div class="space-y-1.5">
					<Label for="g-client-id">Client ID</Label>
					<Input id="g-client-id" type="text" placeholder="123456789-abc.apps.googleusercontent.com" bind:value={clientID} />
				</div>
				<div class="space-y-1.5">
					<Label for="g-client-secret">Client Secret</Label>
					<Input id="g-client-secret" type="password"
						placeholder={googleSettings?.client_secret_set ? '•••••••• (stored)' : 'Enter client secret'}
						bind:value={clientSecret} />
					{#if googleSettings?.client_secret_set && !clientSecret}
						<p class="text-xs text-muted-foreground">Stored — leave blank to keep it.</p>
					{/if}
				</div>
			</div>

			{#if googleSettings?.configured}
				<div class="mt-5 border-t pt-4">
					<p class="text-xs font-medium text-muted-foreground">Authorised redirect URIs</p>
					<p class="mt-0.5 text-xs text-muted-foreground">
						These must be registered on your OAuth client in Google Cloud (Credentials → your client → Authorised redirect URIs).
					</p>
					<code class="mt-2 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{redirectBase}/v1/calendar/callback</code>
					<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{redirectBase}/v1/auth/callback</code>
					{#if !isLocal}
						<p class="mt-1.5 text-xs text-muted-foreground">
							If you also run Calnode locally, add the
							<code class="rounded bg-muted px-1">http://localhost:3000/…</code> variants too.
						</p>
					{/if}
				</div>
			{/if}

			<div class="mt-5">
				<Button onclick={save} disabled={saving}>
					{saving ? 'Saving…' : 'Save'}
				</Button>
			</div>
		</div>
	</div>
{/if}

{/if}
