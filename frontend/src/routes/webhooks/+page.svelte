<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Webhook, type WebhookDelivery } from '$lib/api';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Badge } from '$lib/components/ui/badge';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { t, tr } from '$lib/i18n';

	let items: Webhook[] = $state([]);
	let loading = $state(true);
	let error = $state('');
	let showCreate = $state(false);

	const allEvents = [
		'booking.created',
		'booking.cancelled',
		'booking.rescheduled',
		'booking.rsvp',
		'recording.completed',
		'transcript.ready',
		'notes.ready'
	];

	// Payload field catalog (keys must match the backend's webhook field keys).
	// `pii` flags personal data so the operator chooses consciously what leaves the system.
	type FieldDef = { key: string; label: string; pii?: boolean };
	const fieldGroups = $derived<{ group: string; pii?: boolean; fields: FieldDef[] }[]>([
		{ group: $t('webhooks.fieldGroup.booking'), fields: [
			{ key: 'id', label: $t('webhooks.fields.booking.id') },
			{ key: 'status', label: $t('webhooks.fields.booking.status') },
			{ key: 'start_at', label: $t('webhooks.fields.booking.start_at') },
			{ key: 'end_at', label: $t('webhooks.fields.booking.end_at') },
			{ key: 'created_at', label: $t('webhooks.fields.booking.created_at') },
			{ key: 'location_value', label: $t('webhooks.fields.booking.location_value') },
			{ key: 'cancellation_reason', label: $t('webhooks.fields.booking.cancellation_reason') },
			{ key: 'previous_start_at', label: $t('webhooks.fields.booking.previous_start_at') },
			{ key: 'previous_end_at', label: $t('webhooks.fields.booking.previous_end_at') },
			{ key: 'rsvp_status', label: $t('webhooks.fields.booking.rsvp_status') },
			{ key: 'invite_delivery', label: $t('webhooks.fields.booking.invite_delivery') },
		] },
		{ group: $t('webhooks.fieldGroup.payment'), fields: [
			{ key: 'payment_status', label: $t('webhooks.fields.payment.payment_status') },
			{ key: 'amount_paid_cents', label: $t('webhooks.fields.payment.amount_paid_cents') },
			{ key: 'amount_paid_currency', label: $t('webhooks.fields.payment.amount_paid_currency') },
		] },
		{ group: $t('webhooks.fieldGroup.eventType'), fields: [
			{ key: 'event_type_slug', label: $t('webhooks.fields.eventType.event_type_slug') },
			{ key: 'event_type_name', label: $t('webhooks.fields.eventType.event_type_name') },
		] },
		{ group: $t('webhooks.fieldGroup.host'), fields: [
			{ key: 'host_id', label: $t('webhooks.fields.host.host_id') },
			{ key: 'host_name', label: $t('webhooks.fields.host.host_name') },
			{ key: 'host_email', label: $t('webhooks.fields.host.host_email'), pii: true },
		] },
		{ group: $t('webhooks.fieldGroup.attendee'), pii: true, fields: [
			{ key: 'attendee_name', label: $t('webhooks.fields.attendee.attendee_name'), pii: true },
			{ key: 'attendee_email', label: $t('webhooks.fields.attendee.attendee_email'), pii: true },
			{ key: 'attendee_timezone', label: $t('webhooks.fields.attendee.attendee_timezone'), pii: true },
		] },
		{ group: $t('webhooks.fieldGroup.intake'), pii: true, fields: [
			{ key: 'answers', label: $t('webhooks.fields.intake.answers'), pii: true },
		] },
	]);
	// Keys are stable identifiers; only the labels above follow the locale.
	const allFieldKeys = [
		'id', 'status', 'start_at', 'end_at', 'created_at', 'location_value',
		'cancellation_reason', 'previous_start_at', 'previous_end_at', 'rsvp_status', 'invite_delivery',
		'payment_status', 'amount_paid_cents', 'amount_paid_currency',
		'event_type_slug', 'event_type_name',
		'host_id', 'host_name', 'host_email',
		'attendee_name', 'attendee_email', 'attendee_timezone',
		'answers'
	];

	let form = $state<{ url: string; events: string[]; fields: string[] }>({
		url: '', events: ['booking.created', 'booking.cancelled'], fields: [...allFieldKeys]
	});

	// Delivery log (lazy-loaded per webhook).
	let openDeliveries = $state<string | null>(null);
	let deliveries = $state<WebhookDelivery[]>([]);
	let deliveriesLoading = $state(false);
	let creating = $state(false);
	let createError = $state('');
	let deleteOpen = $state(false);
	let deleteId = $state('');

	async function load() {
		try {
			const res = await api.get<{ items: Webhook[] }>('/v1/webhooks');
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
		if (!form.url) { createError = tr('webhooks.validation.url'); return; }
		if (!form.url.startsWith('https://')) { createError = tr('webhooks.validation.https'); return; }
		if (form.events.length === 0) { createError = tr('webhooks.validation.events'); return; }
		creating = true;
		try {
			await api.post('/v1/webhooks', { url: form.url, events: form.events, fields: form.fields });
			form = { url: '', events: ['booking.created', 'booking.cancelled'], fields: [...allFieldKeys] };
			showCreate = false;
			await load();
		} catch (e: any) {
			createError = e.message;
		} finally {
			creating = false;
		}
	}

	function del(id: string) {
		deleteId = id;
		deleteOpen = true;
	}

	async function doDelete() {
		try {
			await api.del(`/v1/webhooks/${deleteId}`);
			await load();
		} catch (e: any) {
			error = e.message;
		}
	}

	function fmtDate(iso: string) {
		return new Date(iso).toLocaleDateString(undefined, { dateStyle: 'medium' });
	}

	function toggleEvent(ev: string) {
		if (form.events.includes(ev)) {
			form.events = form.events.filter((e) => e !== ev);
		} else {
			form.events = [...form.events, ev];
		}
	}

	function toggleField(key: string) {
		form.fields = form.fields.includes(key)
			? form.fields.filter((f) => f !== key)
			: [...form.fields, key];
	}

	async function toggleDeliveries(id: string) {
		if (openDeliveries === id) { openDeliveries = null; return; }
		openDeliveries = id;
		deliveries = [];
		deliveriesLoading = true;
		try {
			const res = await api.get<{ items: WebhookDelivery[] }>(`/v1/webhooks/${id}/deliveries`);
			deliveries = res.items ?? [];
		} catch (e: any) {
			error = e.message;
		} finally {
			deliveriesLoading = false;
		}
	}
</script>

<ConfirmDialog
	bind:open={deleteOpen}
	title={$t('webhooks.confirm.delete.title')}
	description={$t('webhooks.confirm.delete.description')}
	confirmText={$t('common.delete')}
	destructive
	onConfirm={doDelete}
/>

<svelte:head><title>{$t('webhooks.headTitle')}</title></svelte:head>

<div class="mb-8 flex items-center justify-between">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">{$t('webhooks.title')}</h1>
		<p class="mt-1 text-sm text-muted-foreground">{$t('webhooks.subtitle')}</p>
	</div>
	<Button onclick={() => { showCreate = !showCreate; createError = ''; }}>
		{showCreate ? $t('webhooks.new.toggleCancel') : $t('webhooks.new.toggleOpen')}
	</Button>
</div>

{#if showCreate}
	<div class="mb-6 rounded-lg border bg-card p-6">
		<h2 class="mb-4 text-sm font-semibold">{$t('webhooks.new.title')}</h2>
		{#if createError}<p class="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{createError}</p>{/if}

		<div class="mb-4 space-y-1.5">
			<Label for="wh-url">{$t('webhooks.new.urlLabel')}</Label>
			<Input
				id="wh-url"
				type="url"
				bind:value={form.url}
				placeholder={$t('webhooks.new.urlPlaceholder')}
			/>
		</div>

		<div class="mb-4 space-y-2">
			<p class="text-sm font-medium">{$t('webhooks.new.eventsTitle')}</p>
			{#each allEvents as ev}
				<label class="flex cursor-pointer items-center gap-2 font-mono text-sm">
					<Checkbox
						checked={form.events.includes(ev)}
						onCheckedChange={() => toggleEvent(ev)}
					/>
					<span>{ev}</span>
				</label>
			{/each}
		</div>

		<div class="mb-4 space-y-3">
			<p class="text-sm font-medium">{$t('webhooks.new.fieldsTitle')}</p>
			{#each fieldGroups as grp}
				<div class="space-y-1.5">
					<p class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
						{grp.group}{#if grp.pii}<span class="ml-1.5 font-normal normal-case text-amber-600">{$t('webhooks.new.fieldsPii')}</span>{/if}
					</p>
					<div class="grid grid-cols-2 gap-x-4 gap-y-1">
						{#each grp.fields as f}
							<label class="flex cursor-pointer items-center gap-2 font-mono text-sm">
								<Checkbox checked={form.fields.includes(f.key)} onCheckedChange={() => toggleField(f.key)} />
								<span>{f.label}{#if f.pii}<span class="ml-1 text-[10px] font-medium uppercase text-amber-600">{$t('webhooks.new.piiBadge')}</span>{/if}</span>
							</label>
						{/each}
					</div>
				</div>
			{/each}
		</div>

		<Button onclick={create} disabled={creating}>
			{creating ? $t('webhooks.new.creating') : $t('webhooks.new.create')}
		</Button>
	</div>
{/if}

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">{$t('common.loading')}</p>
{:else if items.length === 0}
	<div class="rounded-lg border border-dashed bg-card p-12 text-center">
		<p class="text-sm font-medium">{$t('webhooks.empty.title')}</p>
		<p class="mt-1 text-sm text-muted-foreground">{$t('webhooks.empty.description')}</p>
	</div>
{:else}
	<div class="rounded-lg border bg-card overflow-hidden">
		<table class="w-full text-sm">
			<thead>
				<tr class="border-b">
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('webhooks.table.url')}</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('webhooks.table.events')}</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('webhooks.table.fields')}</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('webhooks.table.status')}</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('webhooks.table.created')}</th>
					<th class="px-4 pb-3 pt-3"></th>
				</tr>
			</thead>
			<tbody class="divide-y">
				<Tooltip.Provider>
					{#each items as wh}
						<tr class="transition-colors hover:bg-muted/30">
							<td class="max-w-xs overflow-hidden text-ellipsis whitespace-nowrap px-4 py-3 font-mono text-xs">{wh.url}</td>
							<td class="px-4 py-3 text-xs text-muted-foreground">{(wh.events ?? []).join(', ')}</td>
							<td class="px-4 py-3 text-xs text-muted-foreground">{$t('webhooks.fieldsCount', { count: (wh.fields ?? []).length })}</td>
							<td class="px-4 py-3">
								{#if wh.is_active}
									<Badge class="bg-green-50 text-green-700 border-green-200">{$t('common.active')}</Badge>
								{:else}
									<Badge variant="secondary">{$t('common.inactive')}</Badge>
								{/if}
							</td>
							<td class="px-4 py-3 text-muted-foreground">{fmtDate(wh.created_at)}</td>
							<td class="px-4 py-3 text-right whitespace-nowrap">
								<Tooltip.Root>
									<Tooltip.Trigger class={buttonVariants({ variant: 'ghost', size: 'icon' })} onclick={() => toggleDeliveries(wh.id)}>
										<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/></svg>
									</Tooltip.Trigger>
									<Tooltip.Content>{openDeliveries === wh.id ? $t('webhooks.tooltip.hideDeliveries') : $t('webhooks.tooltip.deliveries')}</Tooltip.Content>
								</Tooltip.Root>
								<Tooltip.Root>
									<Tooltip.Trigger class={buttonVariants({ variant: 'ghost', size: 'icon' })} onclick={() => del(wh.id)}>
										<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
									</Tooltip.Trigger>
									<Tooltip.Content>{$t('webhooks.tooltip.delete')}</Tooltip.Content>
								</Tooltip.Root>
							</td>
						</tr>
						{#if openDeliveries === wh.id}
							<tr class="bg-muted/20">
								<td colspan="6" class="px-4 py-3">
									{#if deliveriesLoading}
										<p class="text-xs text-muted-foreground">{$t('webhooks.deliveries.loading')}</p>
									{:else if deliveries.length === 0}
										<p class="text-xs text-muted-foreground">{$t('webhooks.deliveries.empty')}</p>
									{:else}
										<table class="w-full text-xs">
											<thead>
												<tr class="text-left text-muted-foreground">
													<th class="py-1 pr-4 font-medium">{$t('webhooks.deliveries.event')}</th>
													<th class="py-1 pr-4 font-medium">{$t('webhooks.deliveries.status')}</th>
													<th class="py-1 pr-4 font-medium">{$t('webhooks.deliveries.http')}</th>
													<th class="py-1 pr-4 font-medium">{$t('webhooks.deliveries.attempts')}</th>
													<th class="py-1 font-medium">{$t('webhooks.deliveries.lastAttempt')}</th>
												</tr>
											</thead>
											<tbody class="divide-y divide-border/50">
												{#each deliveries as d}
													<tr>
														<td class="py-1 pr-4 font-mono">{d.event}</td>
														<td class="py-1 pr-4">
															<span class={d.status === 'delivered' ? 'text-green-700' : d.status === 'failed' ? 'text-destructive' : 'text-muted-foreground'}>{d.status}</span>
														</td>
														<td class="py-1 pr-4">{d.response_status ?? $t('common.emDash')}</td>
														<td class="py-1 pr-4">{d.attempt_count}</td>
														<td class="py-1 text-muted-foreground">{d.last_attempted_at ? new Date(d.last_attempted_at).toLocaleString() : $t('common.emDash')}</td>
													</tr>
												{/each}
											</tbody>
										</table>
									{/if}
								</td>
							</tr>
						{/if}
					{/each}
				</Tooltip.Provider>
			</tbody>
		</table>
	</div>
{/if}
