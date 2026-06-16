<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type EventType } from '$lib/api';
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

	let form = $state({ slug: '', name: '', description: '', duration_minutes: 30 });
	let creating = $state(false);
	let deleteOpen = $state(false);
	let deleteSlug = $state('');

	async function load() {
		try {
			const res = await api.get<{ items: EventType[] }>('/v1/event-types');
			items = res.items;
		} catch (e: any) {
			toast.error(e.message || 'Could not load event types');
		} finally {
			loading = false;
		}
	}

	onMount(load);

	async function create() {
		if (!form.slug || !form.name || !form.duration_minutes) {
			toast.error('Slug, name, and duration are required.');
			return;
		}
		creating = true;
		try {
			await api.post('/v1/event-types', {
				slug: form.slug,
				name: form.name,
				description: form.description || undefined,
				duration_minutes: Number(form.duration_minutes)
			});
			form = { slug: '', name: '', description: '', duration_minutes: 30 };
			showCreate = false;
			await load();
		} catch (e: any) {
			toast.error(e.message || 'Could not create event type');
		} finally {
			creating = false;
		}
	}

	async function saveActive(et: EventType, newActive: boolean) {
		try {
			await api.patch(`/v1/event-types/${et.slug}`, { is_active: newActive });
		} catch (e: any) {
			et.is_active = !newActive; // revert optimistic update
			toast.error(e.message || 'Could not update status');
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
			toast.error(e.message || 'Could not delete event type');
		}
	}

	function bookLink(slug: string) {
		return `${window.location.origin}/book/${slug}`;
	}
</script>

<ConfirmDialog
	bind:open={deleteOpen}
	title="Delete event type?"
	description="This will permanently remove the event type and its booking link. Existing bookings are not affected."
	confirmText="Delete"
	destructive
	onConfirm={doDelete}
/>

<svelte:head><title>Event Types — Calnode</title></svelte:head>

<div class="mb-8 flex items-center justify-between">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">Event Types</h1>
		<p class="mt-1 text-sm text-muted-foreground">Manage the types of meetings people can book with you.</p>
	</div>
	<Button onclick={() => { showCreate = !showCreate; }}>
		{showCreate ? 'Cancel' : 'New event type'}
	</Button>
</div>

{#if showCreate}
	<div class="mb-6 rounded-lg border bg-card p-6">
		<h2 class="mb-4 text-sm font-semibold">New event type</h2>
		<div class="mb-4 grid grid-cols-2 gap-4">
			<div class="space-y-1.5">
				<Label for="et-name">Name</Label>
				<Input id="et-name" bind:value={form.name} placeholder="30-Minute Call" />
			</div>
			<div class="space-y-1.5">
				<Label for="et-slug">Slug (URL)</Label>
				<Input id="et-slug" bind:value={form.slug} placeholder="30-min-call" />
			</div>
			<div class="space-y-1.5">
				<Label for="et-dur">Duration (minutes)</Label>
				<Input id="et-dur" type="number" min="5" bind:value={form.duration_minutes} />
			</div>
			<div class="space-y-1.5">
				<Label for="et-desc">Description (optional)</Label>
				<Input id="et-desc" bind:value={form.description} placeholder="Brief description…" />
			</div>
		</div>
		<Button onclick={create} disabled={creating}>
			{creating ? 'Creating…' : 'Create event type'}
		</Button>
	</div>
{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else if items.length === 0}
	<div class="rounded-lg border border-dashed bg-card p-12 text-center">
		<p class="text-sm font-medium">No event types yet</p>
		<p class="mt-1 text-sm text-muted-foreground">Create your first event type to start accepting bookings.</p>
	</div>
{:else}
	<div class="rounded-lg border bg-card overflow-hidden">
		<table class="w-full text-sm">
			<thead>
				<tr class="border-b">
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Name</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Duration</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Booking link</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Active</th>
					<th class="px-4 pb-3 pt-3"></th>
				</tr>
			</thead>
			<tbody class="divide-y">
				{#each items as et}
					<tr class="transition-colors hover:bg-muted/30">
						<td class="px-4 py-3">
							<div class="font-medium">{et.name}</div>
							<div class="text-xs text-muted-foreground">{et.slug}</div>
						</td>
						<td class="px-4 py-3 text-muted-foreground">{et.duration_minutes} min</td>
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
							<Switch bind:checked={et.is_active} onCheckedChange={(v) => saveActive(et, v)} />
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
										<Tooltip.Content>Settings</Tooltip.Content>
									</Tooltip.Root>

									<Tooltip.Root>
										<Tooltip.Trigger
											class={buttonVariants({ variant: 'ghost', size: 'icon' })}
											onclick={() => del(et.slug)}
										>
											<!-- Trash icon -->
											<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
										</Tooltip.Trigger>
										<Tooltip.Content>Delete</Tooltip.Content>
									</Tooltip.Root>
								</div>
							</Tooltip.Provider>
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
{/if}
