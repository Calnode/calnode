<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type ZoomSettings } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';

	let loading = $state(true);
	let saving = $state(false);

	let settings = $state<ZoomSettings | null>(null);
	let clientID = $state('');
	let clientSecret = $state('');

	const redirectURI = $derived(settings?.redirect_uri || '');

	onMount(async () => {
		try {
			settings = await api.get<ZoomSettings>('/v1/settings/zoom');
			clientID = settings.client_id;
		} catch (e: any) {
			toast.error(e.message || 'Could not load Zoom settings');
		} finally {
			loading = false;
		}
	});

	async function save() {
		saving = true;
		try {
			const body: Record<string, unknown> = { client_id: clientID };
			if (clientSecret) body.client_secret = clientSecret;
			settings = await api.patch<ZoomSettings>('/v1/settings/zoom', body);
			clientSecret = '';
			toast.success('Saved — each host can now connect Zoom from the Calendar page');
		} catch (e: any) {
			toast.error(e.message || 'Could not save Zoom settings');
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

		{#if !settings?.configured}
		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-4 text-sm font-semibold">Setup instructions</h2>
			<ol class="space-y-4 text-sm">
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">1</span>
					<div>
						Go to the <a href="https://marketplace.zoom.us" target="_blank" rel="noopener noreferrer" class="font-medium text-primary underline">Zoom App Marketplace</a>
						and sign in with your Zoom account. Click <span class="font-medium">Develop → Build App</span>.
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">2</span>
					<div>
						Choose a <span class="font-medium">General App</span> (User-managed OAuth). You can keep it
						unpublished — your own team's Zoom users can still connect.
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">3</span>
					<div>
						Under <span class="font-medium">OAuth</span>, set the <span class="font-medium">Redirect URL for OAuth</span>
						(and add it to the allow list) to:
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{redirectURI}</code>
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">4</span>
					<div>
						Under <span class="font-medium">Scopes</span>, add <code class="rounded bg-muted px-1">meeting:write</code>
						(create/update/delete meetings on the user's behalf).
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">5</span>
					<div>
						Copy the <span class="font-medium">Client ID</span> and <span class="font-medium">Client Secret</span>
						from the app's Basic Information, paste them below, and save.
					</div>
				</li>
			</ol>
		</div>
		{/if}

		<div class="rounded-lg border bg-card p-6">
			<div class="mb-4 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">Zoom</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">
						Lets each host connect their own Zoom account so Zoom-located bookings get a real
						meeting link minted automatically.
					</p>
				</div>
				{#if settings !== null}
					<span class="flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium {settings.configured ? 'bg-green-50 text-green-700' : 'bg-amber-50 text-amber-700'}">
						<span class="h-1.5 w-1.5 rounded-full {settings.configured ? 'bg-green-500' : 'bg-amber-400'}"></span>
						{settings.configured ? 'Configured' : 'Not configured'}
					</span>
				{/if}
			</div>

			<div class="space-y-3">
				<div class="space-y-1.5">
					<Label for="z-client-id">Client ID</Label>
					<Input id="z-client-id" type="text" placeholder="abcdEFGhij-KLmnOPqrS" bind:value={clientID} />
				</div>
				<div class="space-y-1.5">
					<Label for="z-client-secret">Client Secret</Label>
					<Input id="z-client-secret" type="password"
						placeholder={settings?.client_secret_set ? '•••••••• (stored)' : 'Enter client secret'}
						bind:value={clientSecret} />
					{#if settings?.client_secret_set && !clientSecret}
						<p class="text-xs text-muted-foreground">Stored — leave blank to keep it.</p>
					{/if}
				</div>
			</div>

			{#if settings?.configured}
				<div class="mt-5 border-t pt-4">
					<p class="text-xs font-medium text-muted-foreground">Redirect URL</p>
					<p class="mt-0.5 text-xs text-muted-foreground">
						This must be registered on your Zoom app (OAuth → Redirect URL + OAuth allow list).
					</p>
					<code class="mt-2 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{redirectURI}</code>
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
