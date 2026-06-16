<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type APIKey } from '$lib/api';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import * as Tooltip from '$lib/components/ui/tooltip';

	let items: APIKey[] = $state([]);
	let loading = $state(true);
	let error = $state('');
	let showCreate = $state(false);
	let newName = $state('');
	let creating = $state(false);
	let createError = $state('');
	let newKey = $state('');
	let revokeOpen = $state(false);
	let revokeTarget = $state<{ id: string; name: string } | null>(null);

	async function load() {
		try {
			const res = await api.get<{ items: APIKey[] }>('/v1/api-keys');
			items = res.items;
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	onMount(load);

	async function create() {
		createError = '';
		if (!newName.trim()) { createError = 'Name is required.'; return; }
		creating = true;
		try {
			const res = await api.post<{ key: string }>('/v1/api-keys', { name: newName.trim() });
			newKey = res.key;
			newName = '';
			showCreate = false;
			await load();
		} catch (e: any) {
			createError = e.message;
		} finally {
			creating = false;
		}
	}

	function revoke(id: string, name: string) {
		revokeTarget = { id, name };
		revokeOpen = true;
	}

	async function doRevoke() {
		if (!revokeTarget) return;
		try {
			await api.del(`/v1/api-keys/${revokeTarget.id}`);
			await load();
		} catch (e: any) {
			error = e.message;
		}
	}

	function fmtDate(iso: string) {
		return new Date(iso).toLocaleDateString(undefined, { dateStyle: 'medium' });
	}

	function copyKey() {
		navigator.clipboard.writeText(newKey).catch(() => {});
	}
</script>

<ConfirmDialog
	bind:open={revokeOpen}
	title="Revoke API key?"
	description={revokeTarget ? `Revoke "${revokeTarget.name}"? Any integrations using it will stop working immediately.` : ''}
	confirmText="Revoke"
	destructive
	onConfirm={doRevoke}
/>

<svelte:head><title>API Keys — Calnode</title></svelte:head>

<div class="mb-8 flex items-center justify-between">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">API Keys</h1>
		<p class="mt-1 text-sm text-muted-foreground">Authenticate CLI tools and integrations.</p>
	</div>
	<Button onclick={() => { showCreate = !showCreate; createError = ''; newKey = ''; }}>
		{showCreate ? 'Cancel' : 'New key'}
	</Button>
</div>

{#if newKey}
	<div class="mb-6 rounded-lg border border-green-200 bg-green-50 p-4">
		<p class="mb-2 text-sm font-medium text-green-800">Key created — copy it now. It won't be shown again.</p>
		<div class="mb-3 rounded-md border bg-white px-3 py-2 font-mono text-xs text-foreground break-all">{newKey}</div>
		<Button variant="outline" size="sm" onclick={copyKey}>
			Copy to clipboard
		</Button>
	</div>
{/if}

{#if showCreate}
	<div class="mb-6 rounded-lg border bg-card p-6">
		<h2 class="mb-4 text-sm font-semibold">New API key</h2>
		{#if createError}<p class="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{createError}</p>{/if}
		<div class="mb-4 max-w-sm space-y-1.5">
			<Label for="key-name">Key name</Label>
			<Input
				id="key-name"
				bind:value={newName}
				placeholder="e.g. CI/CD pipeline"
			/>
		</div>
		<Button onclick={create} disabled={creating}>
			{creating ? 'Creating…' : 'Create key'}
		</Button>
	</div>
{/if}

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else if items.length === 0}
	<div class="rounded-lg border border-dashed bg-card p-12 text-center">
		<p class="text-sm font-medium">No API keys</p>
		<p class="mt-1 text-sm text-muted-foreground">Create a key to authenticate CLI tools and integrations.</p>
	</div>
{:else}
	<div class="rounded-lg border bg-card overflow-hidden">
		<table class="w-full text-sm">
			<thead>
				<tr class="border-b">
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Name</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Created</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Last used</th>
					<th class="px-4 pb-3 pt-3"></th>
				</tr>
			</thead>
			<tbody class="divide-y">
				<Tooltip.Provider>
					{#each items as k}
						<tr class="transition-colors hover:bg-muted/30">
							<td class="px-4 py-3 font-medium">{k.name}</td>
							<td class="px-4 py-3 text-muted-foreground">{fmtDate(k.created_at)}</td>
							<td class="px-4 py-3 text-muted-foreground">
								{#if k.last_used_at}{fmtDate(k.last_used_at)}{:else}Never{/if}
							</td>
							<td class="px-4 py-3 text-right">
								<Tooltip.Root>
									<Tooltip.Trigger class={buttonVariants({ variant: 'ghost', size: 'icon' })} onclick={() => revoke(k.id, k.name)}>
										<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
									</Tooltip.Trigger>
									<Tooltip.Content>Revoke key</Tooltip.Content>
								</Tooltip.Root>
							</td>
						</tr>
					{/each}
				</Tooltip.Provider>
			</tbody>
		</table>
	</div>
{/if}
