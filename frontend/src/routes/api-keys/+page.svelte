<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type APIKey } from '$lib/api';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { t, tr, localeTag } from '$lib/i18n';

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
		if (!newName.trim()) { createError = tr('apiKeys.validation.name'); return; }
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
		return new Date(iso).toLocaleDateString(localeTag(), { dateStyle: 'medium' });
	}

	function copyKey() {
		navigator.clipboard.writeText(newKey).catch(() => {});
	}
</script>

<ConfirmDialog
	bind:open={revokeOpen}
	title={$t('apiKeys.confirm.title')}
	description={revokeTarget ? $t('apiKeys.confirm.description', { name: revokeTarget.name }) : ''}
	confirmText={$t('apiKeys.confirm.confirm')}
	destructive
	onConfirm={doRevoke}
/>

<svelte:head><title>{$t('apiKeys.headTitle')}</title></svelte:head>

<div class="mb-8 flex items-center justify-between">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">{$t('apiKeys.title')}</h1>
		<p class="mt-1 text-sm text-muted-foreground">{$t('apiKeys.subtitle')}</p>
	</div>
	<Button onclick={() => { showCreate = !showCreate; createError = ''; newKey = ''; }}>
		{showCreate ? $t('apiKeys.new.toggleCancel') : $t('apiKeys.new.toggleOpen')}
	</Button>
</div>

{#if newKey}
	<div class="mb-6 rounded-lg border border-green-200 bg-green-50 p-4">
		<p class="mb-2 text-sm font-medium text-green-800">{$t('apiKeys.created.banner')}</p>
		<div class="mb-3 rounded-md border bg-white px-3 py-2 font-mono text-xs text-foreground break-all">{newKey}</div>
		<Button variant="outline" size="sm" onclick={copyKey}>
			{$t('apiKeys.copyToClipboard')}
		</Button>
	</div>
{/if}

{#if showCreate}
	<div class="mb-6 rounded-lg border bg-card p-6">
		<h2 class="mb-4 text-sm font-semibold">{$t('apiKeys.new.title')}</h2>
		{#if createError}<p class="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{createError}</p>{/if}
		<div class="mb-4 max-w-sm space-y-1.5">
			<Label for="key-name">{$t('apiKeys.new.nameLabel')}</Label>
			<Input
				id="key-name"
				bind:value={newName}
				placeholder={$t('apiKeys.new.namePlaceholder')}
			/>
		</div>
		<Button onclick={create} disabled={creating}>
			{creating ? $t('apiKeys.new.creating') : $t('apiKeys.new.create')}
		</Button>
	</div>
{/if}

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">{$t('common.loading')}</p>
{:else if items.length === 0}
	<div class="rounded-lg border border-dashed bg-card p-12 text-center">
		<p class="text-sm font-medium">{$t('apiKeys.empty.title')}</p>
		<p class="mt-1 text-sm text-muted-foreground">{$t('apiKeys.empty.description')}</p>
	</div>
{:else}
	<div class="rounded-lg border bg-card overflow-hidden">
		<table class="w-full text-sm">
			<thead>
				<tr class="border-b">
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('apiKeys.table.name')}</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('apiKeys.table.created')}</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('apiKeys.table.lastUsed')}</th>
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
								{#if k.last_used_at}{fmtDate(k.last_used_at)}{:else}{$t('common.never')}{/if}
							</td>
							<td class="px-4 py-3 text-right">
								<Tooltip.Root>
									<Tooltip.Trigger class={buttonVariants({ variant: 'ghost', size: 'icon' })} onclick={() => revoke(k.id, k.name)}>
										<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
									</Tooltip.Trigger>
									<Tooltip.Content>{$t('apiKeys.tooltip.revoke')}</Tooltip.Content>
								</Tooltip.Root>
							</td>
						</tr>
					{/each}
				</Tooltip.Provider>
			</tbody>
		</table>
	</div>
{/if}
