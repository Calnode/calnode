<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type OAuthConnection } from '$lib/api';
	import { buttonVariants } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import * as Tooltip from '$lib/components/ui/tooltip';

	let items: OAuthConnection[] = $state([]);
	let loading = $state(true);
	let error = $state('');
	let revokeOpen = $state(false);
	let revokeTarget = $state<{ id: string; name: string } | null>(null);

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

	onMount(load);

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

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else if items.length === 0}
	<div class="rounded-lg border border-dashed bg-card p-12 text-center">
		<p class="text-sm font-medium">No connected apps</p>
		<p class="mx-auto mt-1 max-w-md text-sm text-muted-foreground">
			Add this workspace as a connector in an MCP-capable app (e.g. Claude) using its URL,
			then sign in to authorize it. Approved apps appear here.
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
