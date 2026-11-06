<script lang="ts">
	import VisibilitySelect from '$lib/components/event-types/VisibilitySelect.svelte';
	import { onMount } from 'svelte';
	import { api, type EventType } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { t, tr } from '$lib/i18n';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { Badge } from '$lib/components/ui/badge';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { toast } from 'svelte-sonner';

	let items: EventType[] = $state([]);
	let loading = $state(true);
	let showCreate = $state(false);

	let form = $state({ slug: '', name: '', description: '', duration_minutes: 30, visibility: 'public' as EventType['visibility'] });
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

	async function create() {
		if (!form.slug || !form.name || !form.duration_minutes) {
			toast.error(tr('eventTypes.toast.validation'));
			return;
		}
		creating = true;
		try {
			await api.post('/v1/event-types', {
				slug: form.slug,
				name: form.name,
				description: form.description || undefined,
				duration_minutes: Number(form.duration_minutes),
				visibility: form.visibility
			});
			form = { slug: '', name: '', description: '', duration_minutes: 30, visibility: 'public' as EventType['visibility'] };
			showCreate = false;
			await load();
		} catch (e: any) {
			toast.error(e.message || tr('eventTypes.toast.errors.create'));
		} finally {
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
		<Button onclick={() => { showCreate = !showCreate; }}>
			{showCreate ? $t('eventTypes.new.toggleCancel') : $t('eventTypes.new.toggleOpen')}
		</Button>
	</div>
</div>

{#if showCreate}
	<div class="mb-6 rounded-lg border bg-card p-6">
		<h2 class="mb-4 text-sm font-semibold">{$t('eventTypes.new.title')}</h2>
		<div class="mb-4 grid grid-cols-2 gap-4">
			<div class="space-y-1.5">
				<Label for="et-name">{$t('eventTypes.new.name')}</Label>
				<Input id="et-name" bind:value={form.name} placeholder={$t('eventTypes.new.namePlaceholder')} />
			</div>
			<div class="space-y-1.5">
				<Label for="et-slug">{$t('eventTypes.new.slug')}</Label>
				<Input id="et-slug" bind:value={form.slug} placeholder={$t('eventTypes.new.slugPlaceholder')} />
			</div>
			<div class="space-y-1.5">
				<Label for="et-dur">{$t('eventTypes.new.duration')}</Label>
				<Input id="et-dur" type="number" min="5" bind:value={form.duration_minutes} />
			</div>
			<div class="space-y-1.5">
				<Label for="et-desc">{$t('eventTypes.new.description')}</Label>
				<Input id="et-desc" bind:value={form.description} placeholder={$t('eventTypes.new.descriptionPlaceholder')} />
			</div>
			<VisibilitySelect bind:value={form.visibility} />
		</div>
		<Button onclick={create} disabled={creating}>
			{creating ? $t('eventTypes.new.creating') : $t('eventTypes.new.create')}
		</Button>
	</div>
{/if}

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
								<Badge variant="outline">{$t(`eventTypes.visibility.${et.visibility}`)}</Badge>
								{#if et.owned === false && scope === 'mine'}
									<Badge variant="secondary" class="text-[10px]">{$t('eventTypes.badge.youHost')}</Badge>
								{/if}
							</div>
							<div class="text-xs text-muted-foreground">{et.slug}</div>
						</td>
						{#if scope === 'all'}<td class="px-4 py-3 text-muted-foreground">{ownerLabel(et)}</td>{/if}
						<td class="px-4 py-3 text-muted-foreground">{$t('common.minutesShort', { n: et.duration_minutes })}</td>
						<td class="px-4 py-3">
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
