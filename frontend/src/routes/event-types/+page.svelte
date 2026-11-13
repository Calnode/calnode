<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type EventType, type SingleUseLink, type CreateSingleUseLinkPayload } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { t, tr } from '$lib/i18n';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Badge } from '$lib/components/ui/badge';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { toast } from 'svelte-sonner';

	let items: EventType[] = $state([]);
	let loading = $state(true);

	let creating = $state(false);
	let deleteOpen = $state(false);
	let deleteSlug = $state('');
	// Slug currently being duplicated, so the row's button can't be double-fired into two
	// copies while the request is in flight.
	let duplicating = $state('');

	// Admins can switch to every event type on the instance (?scope=all), the same way
	// the bookings page offers "All bookings". Their default list stays their own.
	const isAdmin = $derived($currentUser?.is_admin ?? false);
	let scope = $state<'mine' | 'all'>('mine');

	let filter = $state<'active' | 'archived'>('active');
	const visible = $derived(items.filter((et) => (filter === 'archived' ? !!et.archived : !et.archived)));
	const archivedCount = $derived(items.filter((et) => et.archived).length);
	const activeCount = $derived(items.length - archivedCount);
	const filterEmptyWord = $derived(
		(filter === 'archived'
			? $t('eventTypes.filter.archived', { count: archivedCount })
			: $t('eventTypes.filter.active', { count: activeCount })
		).replace(/\s*\(.*\)$/, '').trim().toLowerCase()
	);

	async function load() {
		try {
			const res = await api.get<{ items: EventType[] }>(scope === 'all' && isAdmin ? '/v1/event-types?scope=all' : '/v1/event-types');
			items = res.items;
		} catch (e: any) {
			toast.error(e.message || tr('eventTypes.toast.errors.load'));
		} finally {
			loading = false;
		}
	}

	onMount(load);

	async function setScope(s: 'mine' | 'all') {
		if (scope === s) return;
		scope = s;
		await load();
	}

	function ownerLabel(et: EventType) {
		return et.owned ? $t('eventTypes.owner.you') : et.owner_name || et.owner_email || $t('common.emDash');
	}

	async function createDirect() {
		if (creating) return;
		creating = true;
		try {
			const suffix = Math.random().toString(36).slice(2, 6);
			const created = await api.post<EventType>('/v1/event-types', {
				slug: `new-meeting-${suffix}`,
				name: 'New Meeting',
				duration_minutes: 30,
				is_active: true
			});
			window.location.href = `/admin/event-types/${created.slug}`;
		} catch (e: any) {
			toast.error(e.message || tr('eventTypes.toast.errors.create'));
			creating = false;
		}
	}

	async function saveActive(et: EventType, newActive: boolean) {
		try {
			await api.patch(`/v1/event-types/${et.slug}`, { is_active: newActive });
		} catch (e: any) {
			et.is_active = !newActive; // revert optimistic update
			toast.error(e.message || tr('eventTypes.toast.errors.status'));
		}
	}

	async function archive(et: EventType, archived: boolean) {
		try {
			await api.patch(`/v1/event-types/${et.slug}`, { archived });
			toast.success(archived ? tr('eventTypes.toast.archived') : tr('eventTypes.toast.restored'));
			await load();
		} catch (e: any) {
			toast.error(e.message || tr('eventTypes.toast.errors.update'));
		}
	}

	// The copy is created inactive, so the toast names the new slug: nothing appears on a
	// booking page until the operator edits it and switches it on.
	async function duplicateEventType(et: EventType) {
		if (duplicating) return;
		duplicating = et.slug;
		try {
			const copy = await api.post<EventType>(`/v1/event-types/${et.slug}/duplicate`);
			toast.success(tr('eventTypes.toast.duplicated', { slug: copy.slug }));
			await load();
		} catch (e: any) {
			toast.error(e.message || tr('eventTypes.toast.errors.duplicate'));
		} finally {
			duplicating = '';
		}
	}

	function del(slug: string) {
		deleteSlug = slug;
		deleteOpen = true;
	}

	async function doDelete() {
		try {
			await api.del(`/v1/event-types/${deleteSlug}`);
			await load();
		} catch (e: any) {
			toast.error(e.message || tr('eventTypes.toast.errors.delete'));
		}
	}

	function bookLink(slug: string) {
		return `${window.location.origin}/book/${slug}`;
	}

	// ── Single-use ephemeral links ─────────────────────────────────────────────
	let linkModalOpen = $state(false);
	let linkEventType = $state<EventType | null>(null);
	let singleUseLinks = $state<SingleUseLink[]>([]);
	let linksLoading = $state(false);
	let mintingId = $state('');
	let revokingToken = $state('');

	function resolveSingleUseUrl(link: SingleUseLink) {
		if (link.url && link.url.startsWith('http')) return link.url;
		return `${window.location.origin}/t/${link.token}`;
	}

	async function copyText(text: string, msg = '1-time link copied to clipboard') {
		try {
			await navigator.clipboard.writeText(text);
			toast.success(msg);
		} catch {
			toast.error('Unable to copy automatically — please copy the link manually.');
		}
	}

	async function mintAndCopyOneTimeLink(et: EventType) {
		if (mintingId) return;
		mintingId = et.id;
		try {
			const created = await api.post<SingleUseLink>(
				`/v1/event-types/${et.id}/single-use-links`,
				{}
			);
			await copyText(resolveSingleUseUrl(created), '1-time link copied to clipboard');
			if (linkModalOpen && linkEventType?.id === et.id) {
				singleUseLinks = [created, ...singleUseLinks];
			}
		} catch (e: any) {
			toast.error(e.message || 'Failed to create 1-time link');
		} finally {
			mintingId = '';
		}
	}

	async function openSingleUseDrawer(et: EventType) {
		linkEventType = et;
		linkModalOpen = true;
		await loadSingleUseLinks(et.id);
	}

	async function loadSingleUseLinks(eventTypeId: string) {
		linksLoading = true;
		try {
			const res = await api.get<{ single_use_links: SingleUseLink[] }>(
				`/v1/event-types/${eventTypeId}/single-use-links`
			);
			singleUseLinks = res.single_use_links ?? [];
		} catch (e: any) {
			toast.error(e.message || 'Failed to load 1-time links');
			singleUseLinks = [];
		} finally {
			linksLoading = false;
		}
	}

	async function revokeSingleUseLink(token: string) {
		if (revokingToken) return;
		revokingToken = token;
		try {
			await api.del(`/v1/single-use-links/${token}`);
			singleUseLinks = singleUseLinks.filter((l) => l.token !== token);
			toast.success('1-time link revoked');
		} catch (e: any) {
			toast.error(e.message || 'Failed to revoke link');
		} finally {
			revokingToken = '';
		}
	}

	function linkStatus(link: SingleUseLink): 'used' | 'expired' | 'active' {
		if (link.used_at) return 'used';
		if (link.expires_at && new Date(link.expires_at).getTime() <= Date.now()) return 'expired';
		return 'active';
	}
</script>

<ConfirmDialog
	bind:open={deleteOpen}
	title={$t('eventTypes.confirm.delete.title')}
	description={$t('eventTypes.confirm.delete.description')}
	confirmText={$t('common.delete')}
	destructive
	onConfirm={doDelete}
/>

<svelte:head><title>{$t('eventTypes.headTitle')}</title></svelte:head>

<div class="mb-8 flex items-center justify-between">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">{$t('eventTypes.title')}</h1>
		<p class="mt-1 text-sm text-muted-foreground">
			{scope === 'all' ? $t('eventTypes.subtitle.all') : $t('eventTypes.subtitle.mine')}
		</p>
	</div>
	<div class="flex shrink-0 items-center gap-2">
		{#if isAdmin}
			<div class="inline-flex rounded-md border p-0.5">
				<button
					class="rounded px-3 py-1 text-sm font-medium transition-colors {scope === 'mine' ? 'bg-secondary text-secondary-foreground' : 'text-muted-foreground hover:text-foreground'}"
					onclick={() => setScope('mine')}
				>{$t('eventTypes.scope.mine')}</button>
				<button
					class="rounded px-3 py-1 text-sm font-medium transition-colors {scope === 'all' ? 'bg-secondary text-secondary-foreground' : 'text-muted-foreground hover:text-foreground'}"
					onclick={() => setScope('all')}
				>{$t('eventTypes.scope.all')}</button>
			</div>
		{/if}
		<Button onclick={createDirect} disabled={creating}>
			{creating ? 'Creating...' : '+ New event type'}
		</Button>
	</div>
</div>

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">{$t('common.loading')}</p>
{:else if items.length === 0}
	<div class="rounded-lg border border-dashed bg-card p-12 text-center">
		<p class="text-sm font-medium">{$t('eventTypes.empty.title')}</p>
		<p class="mt-1 text-sm text-muted-foreground">{$t('eventTypes.empty.description')}</p>
	</div>
{:else}
	<div class="mb-4 inline-flex rounded-md border p-0.5 text-sm">
		<button type="button" class="rounded px-3 py-1 transition-colors {filter === 'active' ? 'bg-muted font-medium' : 'text-muted-foreground hover:text-foreground'}" onclick={() => (filter = 'active')}>{$t('eventTypes.filter.active', { count: activeCount })}</button>
		<button type="button" class="rounded px-3 py-1 transition-colors {filter === 'archived' ? 'bg-muted font-medium' : 'text-muted-foreground hover:text-foreground'}" onclick={() => (filter = 'archived')}>{$t('eventTypes.filter.archived', { count: archivedCount })}</button>
	</div>
	{#if visible.length === 0}
		<div class="rounded-lg border border-dashed bg-card p-12 text-center">
			<p class="text-sm text-muted-foreground">{$t('eventTypes.filter.none', { filter: filterEmptyWord })}</p>
		</div>
	{:else}
	<div class="rounded-lg border bg-card overflow-hidden">
		<table class="w-full text-sm">
			<thead>
				<tr class="border-b">
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('eventTypes.table.name')}</th>
					{#if scope === 'all'}<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('eventTypes.table.owner')}</th>{/if}
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('eventTypes.table.duration')}</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('eventTypes.table.bookingLink')}</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('eventTypes.table.active')}</th>
					<th class="px-4 pb-3 pt-3"></th>
				</tr>
			</thead>
			<tbody class="divide-y">
				{#each visible as et}
					<tr class="transition-colors hover:bg-muted/30">
						<td class="px-4 py-3">
							<div class="flex items-center gap-2">
								<span class="font-medium">{et.name}</span>
								{#if et.owned === false && scope === 'mine'}
									<Badge variant="secondary" class="text-[10px]">{$t('eventTypes.badge.youHost')}</Badge>
								{/if}
							</div>
							<div class="text-xs text-muted-foreground">{et.slug}</div>
						</td>
						{#if scope === 'all'}<td class="px-4 py-3 text-muted-foreground">{ownerLabel(et)}</td>{/if}
						<td class="px-4 py-3 text-muted-foreground">{$t('common.minutesShort', { n: et.duration_minutes })}</td>
						<td class="px-4 py-3">
							<div class="flex items-center gap-1.5">
								<Tooltip.Provider>
									<Tooltip.Root>
										<Tooltip.Trigger
											class={buttonVariants({ variant: 'ghost', size: 'icon' })}
											onclick={() => window.open(bookLink(et.slug), '_blank')}
										>
											<!-- External link icon -->
											<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/></svg>
										</Tooltip.Trigger>
										<Tooltip.Content>{bookLink(et.slug)}</Tooltip.Content>
									</Tooltip.Root>
								</Tooltip.Provider>
								<Button
									variant="outline"
									size="sm"
									class="h-7 px-2.5 text-xs"
									onclick={() => mintAndCopyOneTimeLink(et)}
									disabled={!et.is_active || !!et.archived || mintingId === et.id}
								>
									{mintingId === et.id ? 'Copying...' : '1-Time Link'}
								</Button>
								<Button
									variant="ghost"
									size="sm"
									class="h-7 px-2 text-xs text-muted-foreground"
									onclick={() => openSingleUseDrawer(et)}
									title="Manage 1-time links"
								>
									Manage
								</Button>
							</div>
						</td>
						<td class="px-4 py-3">
							<Switch bind:checked={et.is_active} onCheckedChange={(v) => saveActive(et, v)} disabled={et.can_edit === false || et.archived} />
						</td>
						<td class="px-4 py-3">
							<Tooltip.Provider>
								<div class="flex items-center justify-end gap-1">
									<Tooltip.Root>
										<Tooltip.Trigger
											class={buttonVariants({ variant: 'ghost', size: 'icon' })}
											onclick={() => window.location.href = '/admin/event-types/' + et.slug}
										>
											<!-- Gear/Settings icon -->
											<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>
										</Tooltip.Trigger>
										<Tooltip.Content>{$t('eventTypes.tooltip.settings')}</Tooltip.Content>
									</Tooltip.Root>

									{#if et.can_edit !== false}
										<Tooltip.Root>
											<Tooltip.Trigger
												class={buttonVariants({ variant: 'ghost', size: 'icon' })}
												onclick={() => duplicateEventType(et)}
												disabled={duplicating === et.slug}
											>
												<!-- Copy icon -->
												<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="8" y="8" width="14" height="14" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>
											</Tooltip.Trigger>
											<Tooltip.Content>{$t('eventTypes.tooltip.duplicate')}</Tooltip.Content>
										</Tooltip.Root>
										<Tooltip.Root>
											<Tooltip.Trigger
												class={buttonVariants({ variant: 'ghost', size: 'icon' })}
												onclick={() => archive(et, !et.archived)}
											>
												{#if et.archived}
													<!-- Restore icon -->
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="4" width="20" height="5" rx="1"/><path d="M4 9v9a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9"/><path d="M12 12v6"/><path d="M9 15l3-3 3 3"/></svg>
												{:else}
													<!-- Archive box icon -->
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="4" width="20" height="5" rx="1"/><path d="M4 9v9a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9"/><path d="M10 13h4"/></svg>
												{/if}
											</Tooltip.Trigger>
											<Tooltip.Content>{et.archived ? $t('eventTypes.tooltip.restore') : $t('eventTypes.tooltip.archive')}</Tooltip.Content>
										</Tooltip.Root>
										<Tooltip.Root>
											<Tooltip.Trigger
												class={buttonVariants({ variant: 'ghost', size: 'icon' })}
												onclick={() => del(et.slug)}
											>
												<!-- Trash icon -->
												<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
											</Tooltip.Trigger>
											<Tooltip.Content>{$t('eventTypes.tooltip.delete')}</Tooltip.Content>
										</Tooltip.Root>
									{/if}
								</div>
							</Tooltip.Provider>
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
	{/if}
{/if}

<Dialog.Root bind:open={linkModalOpen}>
	<Dialog.Content class="max-w-lg">
		<Dialog.Header>
			<Dialog.Title>1-Time Links{linkEventType ? ` — ${linkEventType.name}` : ''}</Dialog.Title>
			<Dialog.Description>
				Ephemeral links expire automatically after a single booking. Append query params like <code class="rounded bg-muted px-1 py-0.5 text-xs">?name=...&amp;email=...</code> externally if needed.
			</Dialog.Description>
		</Dialog.Header>

		<div class="space-y-4 py-2">
			{#if linkEventType}
				<Button class="w-full" onclick={() => linkEventType && mintAndCopyOneTimeLink(linkEventType)} disabled={!!mintingId}>
					{mintingId ? 'Generating...' : 'Generate & Copy New 1-Time Link'}
				</Button>
			{/if}

			<div class="border-t pt-3">
				<p class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Existing 1-Time Links</p>
				{#if linksLoading}
					<p class="py-3 text-xs text-muted-foreground">Loading links...</p>
				{:else if singleUseLinks.length === 0}
					<p class="py-3 text-xs text-muted-foreground">No 1-time links created for this event type yet.</p>
				{:else}
					<div class="max-h-64 space-y-2 overflow-y-auto pr-1">
						{#each singleUseLinks as link (link.id)}
							{@const st = linkStatus(link)}
							<div class="flex items-center justify-between gap-2 rounded-md border bg-muted/20 px-3 py-2 text-xs">
								<div class="min-w-0 flex-1">
									<div class="flex items-center gap-1.5">
										<Badge variant={st === 'active' ? 'default' : 'secondary'} class="text-[10px] capitalize">
											{st}
										</Badge>
										<span class="truncate font-mono text-[11px] text-muted-foreground">/t/{link.token}</span>
									</div>
								</div>
								<div class="flex shrink-0 items-center gap-1">
									{#if st === 'active'}
										<Button variant="ghost" size="sm" class="h-7 px-2 text-xs" onclick={() => copyText(resolveSingleUseUrl(link))}>
											Copy
										</Button>
									{/if}
									{#if !link.used_at}
										<Button
											variant="ghost"
											size="sm"
											class="h-7 px-2 text-xs text-destructive hover:text-destructive"
											disabled={revokingToken === link.token}
											onclick={() => revokeSingleUseLink(link.token)}
										>
											Revoke
										</Button>
									{/if}
								</div>
							</div>
						{/each}
					</div>
				{/if}
			</div>
		</div>
	</Dialog.Content>
</Dialog.Root>
