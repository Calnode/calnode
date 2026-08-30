<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type OAuthConnection } from '$lib/api';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { toast } from 'svelte-sonner';

	let items: OAuthConnection[] = $state([]);
	let loading = $state(true);
	let error = $state('');
	let revokeOpen = $state(false);
	let revokeTarget = $state<{ id: string; name: string } | null>(null);

	// Read from the browser rather than a config value: this is whatever host the admin
	// actually reached us on, which is the one an external app needs to be able to resolve.
	// A configured BASE_URL can be stale or internal; the address in the URL bar cannot.
	let origin = $state('');
	const mcpUrl = $derived(origin ? `${origin}/mcp` : '');

	let copied = $state(false);
	let copyTimer: ReturnType<typeof setTimeout> | null = null;

	async function copyMcpUrl() {
		if (!mcpUrl) return;
		try {
			await navigator.clipboard.writeText(mcpUrl);
			copied = true;
			if (copyTimer !== null) clearTimeout(copyTimer);
			copyTimer = setTimeout(() => (copied = false), 2000);
		} catch {
			// Clipboard access is blocked outside a secure context, and on a self-hosted
			// instance served over plain http that is the normal case - so say what to do
			// rather than just failing.
			toast.error('Could not copy. Select the URL and copy it manually.');
		}
	}

	async function load() {
		try {
			const res = await api.get<{ items: OAuthConnection[] }>('/v1/oauth/connections');
			items = res.items;
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		origin = window.location.origin;
		load();
	});

	function revoke(id: string, name: string) {
		revokeTarget = { id, name };
		revokeOpen = true;
	}

	async function doRevoke() {
		if (!revokeTarget) return;
		try {
			await api.del(`/v1/oauth/connections/${revokeTarget.id}`);
			await load();
		} catch (e: any) {
			error = e.message;
		}
	}

	function fmtDate(iso: string) {
		return new Date(iso).toLocaleDateString(undefined, { dateStyle: 'medium' });
	}
</script>

<ConfirmDialog
	bind:open={revokeOpen}
	title="Disconnect app?"
	description={revokeTarget
		? `Disconnect "${revokeTarget.name}"? It will immediately lose access to your scheduling tools and must reconnect to regain it.`
		: ''}
	confirmText="Disconnect"
	destructive
	onConfirm={doRevoke}
/>

<svelte:head><title>Connected apps — Calnode</title></svelte:head>

<div class="mb-8">
	<h1 class="text-2xl font-semibold tracking-tight">Connected apps</h1>
	<p class="mt-1 text-sm text-muted-foreground">
		AI agents and other apps you've connected to your scheduling tools (MCP) via sign-in.
	</p>
</div>

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}

<!-- Shown whether or not anything is connected: the URL is what you need to add the SECOND
     app too, and it used to vanish the moment the first one appeared. -->
<div class="mb-6 rounded-lg border bg-card p-5">
	<h2 class="text-sm font-semibold">Connect an app</h2>
	<p class="mt-1 text-sm text-muted-foreground">
		Add this URL as a custom connector in any MCP-capable app, then sign in when it asks.
		The app appears below once you approve it.
	</p>

	<div class="mt-3 flex items-center gap-2">
		<Input
			readonly
			value={mcpUrl}
			aria-label="MCP connector URL"
			onclick={(e) => e.currentTarget.select()}
			class="min-w-0 flex-1 bg-muted/40 font-mono"
		/>
		<Button variant="outline" onclick={copyMcpUrl} disabled={!mcpUrl}>
			{copied ? 'Copied' : 'Copy'}
		</Button>
	</div>

	<p class="mt-3 text-xs text-muted-foreground">
		In Claude: <span class="font-medium">Settings → Connectors → Add custom connector</span>,
		paste the URL, then sign in with your Calnode account to authorize it. Access is scoped to
		your own role, and you can revoke it here at any time.
	</p>
</div>

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else if items.length === 0}
	<div class="rounded-lg border border-dashed bg-card p-12 text-center">
		<p class="text-sm font-medium">No connected apps</p>
		<p class="mx-auto mt-1 max-w-md text-sm text-muted-foreground">
			Use the URL above to add Calnode to an MCP-capable app. Once you approve it, it shows up here.
		</p>
	</div>
{:else}
	<div class="overflow-hidden rounded-lg border bg-card">
		<table class="w-full text-sm">
			<thead>
				<tr class="border-b">
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">App</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Connected</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Last used</th>
					<th class="px-4 pb-3 pt-3"></th>
				</tr>
			</thead>
			<tbody class="divide-y">
				<Tooltip.Provider>
					{#each items as c}
						<tr class="transition-colors hover:bg-muted/30">
							<td class="px-4 py-3 font-medium">{c.client_name}</td>
							<td class="px-4 py-3 text-muted-foreground">{fmtDate(c.created_at)}</td>
							<td class="px-4 py-3 text-muted-foreground">
								{#if c.last_used_at}{fmtDate(c.last_used_at)}{:else}Never{/if}
							</td>
							<td class="px-4 py-3 text-right">
								<Tooltip.Root>
									<Tooltip.Trigger
										class={buttonVariants({ variant: 'ghost', size: 'icon' })}
										onclick={() => revoke(c.id, c.client_name)}
									>
										<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
									</Tooltip.Trigger>
									<Tooltip.Content>Disconnect</Tooltip.Content>
								</Tooltip.Root>
							</td>
						</tr>
					{/each}
				</Tooltip.Provider>
			</tbody>
		</table>
	</div>
{/if}
