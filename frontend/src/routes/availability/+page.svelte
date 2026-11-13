<script lang="ts">
	import { onMount } from 'svelte';
	import {api, type AvailabilityRule, type AvailabilityOverride, type EventType} from '$lib/api';
	import { prefs, fmtDate, weekDays } from '$lib/prefs';
	import { t, tr, localeTag } from '$lib/i18n';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { Badge } from '$lib/components/ui/badge';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import * as Select from '$lib/components/ui/select';
	import { DatePicker } from '$lib/components/ui/date-picker';

	const DAY_NAMES = $derived(weekDays($t));

	const TIME_SLOTS: string[] = [];
	for (let h = 0; h < 24; h++) {
		for (const m of [0, 30]) {
			TIME_SLOTS.push(`${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`);
		}
	}

	function fmtTime(slot: string) {
		const [hh, mm] = slot.split(':').map(Number);
		const date = new Date(2000, 0, 1, hh, mm);
		if ($prefs.time_format === '24h') {
			return date.toLocaleTimeString(localeTag(), { hour: '2-digit', minute: '2-digit', hour12: false });
		}
		return date.toLocaleTimeString(localeTag(), {
			hour: 'numeric',
			minute: mm === 0 ? undefined : '2-digit',
			hour12: true
		});
	}

	// ── Weekly rules — 7-day model ────────────────────────────────────────────────
	type DayBlock = { id: string; event_type_id: string, start_time: string; end_time: string; saving: boolean; error: string }
	type DayState = { day_of_week: number; blocks: DayBlock[]; error: string }

	let days: DayState[] = $state(
		Array.from({ length: 7 }, (_, i) => ({ day_of_week: i, blocks: [], error: '' }))
	);

	let rulesLoading = $state(true);
	let rulesError = $state('');

	let orderedDays = $derived([...days].sort((a, b) => {
		const ws = $prefs.week_start ?? 1;
		return ((a.day_of_week - ws + 7) % 7) - ((b.day_of_week - ws + 7) % 7);
	}));

	async function loadRules() {
		rulesError = '';
		rulesLoading = true;
		try {
			const res = await api.get<{ items: AvailabilityRule[] }>('/v1/availability-rules');
			for (const day of days) { day.blocks = []; day.error = ''; }
			for (const r of (res.items ?? [])) {
				const day = days[r.day_of_week];
				if (day) day.blocks.push({ id: r.id, event_type_id: r.event_type_id ?? 'null', start_time: r.start_time, end_time: r.end_time, saving: false, error: '' });
			}
			for (const day of days) {
				day.blocks.sort((a, b) => a.start_time.localeCompare(b.start_time));
			}
		} catch (e: any) {
			rulesError = e.message;
		} finally {
			rulesLoading = false;
		}
	}

	async function addBlock(day: DayState) {
		day.blocks.push({ id: '', event_type_id: 'null', start_time: '09:00', end_time: '17:00', saving: true, error: '' });
		// Operate on the element AS STORED in the reactive array (a proxy), not the literal we
		// pushed — Svelte 5 proxies pushed objects, so mutating the local literal wouldn't update
		// the UI (the "saving…" stuck-until-refresh bug).
		const block = day.blocks[day.blocks.length - 1];
		try {
			const r = await api.post<AvailabilityRule>('/v1/availability-rules', {
				day_of_week: day.day_of_week,
				start_time: '09:00',
				end_time: '17:00'
			});
			block.id = r.id;
		} catch (e: any) {
			const idx = day.blocks.indexOf(block);
			if (idx !== -1) day.blocks.splice(idx, 1);
			day.error = e.message;
		} finally {
			block.saving = false;
		}
	}

	async function updateBlock(block: DayBlock) {
		if (!block.id) return;
		if (block.start_time >= block.end_time) {
			block.error = tr('availability.blockError.endAfterStart');
			return;
		}
		block.error = '';
		block.saving = true;
		try {
			await api.patch(`/v1/availability-rules/${block.id}`, {
				event_type_id: block.event_type_id === "null" ? null : block.event_type_id,
				start_time: block.start_time,
				end_time: block.end_time
			});
		} catch (e: any) {
			block.error = e.message;
		} finally {
			block.saving = false;
		}
	}

	async function removeBlock(day: DayState, block: DayBlock) {
		if (!block.id) return;
		block.saving = true;
		try {
			await api.del(`/v1/availability-rules/${block.id}`);
			const idx = day.blocks.indexOf(block);
			if (idx !== -1) day.blocks.splice(idx, 1);
		} catch (e: any) {
			block.error = e.message;
			block.saving = false;
		}
	}

	// ── Date overrides ────────────────────────────────────────────────────────────
	let overrides: AvailabilityOverride[] = $state([]);
	let overridesLoading = $state(true);
	let overridesError = $state('');

	type OverrideReason = 'day_off' | 'out_of_office' | 'custom_hours';
	const REASON_LABELS = $derived<Record<OverrideReason, string>>({
		day_off: $t('availability.override.type.dayOff'),
		out_of_office: $t('availability.override.type.outOfOffice'),
		custom_hours: $t('availability.override.type.customHours')
	});

	let ovForm = $state({ date: '', end_date: '', reason: 'day_off' as OverrideReason, start_time: '09:00', end_time: '17:00' });
	let addingOv = $state(false);
	let ovAddError = $state('');

	let deleteOvOpen = $state(false);
	let deleteOvId = $state('');

	let deleteGroupId = $state('');
	let deleteGroupOpen = $state(false);

	// Collapse per-date rows that share a group_id (a multi-day span) into one entry.
	type OvEntry =
		| { kind: 'single'; ov: AvailabilityOverride }
		| { kind: 'span'; group_id: string; reason: OverrideReason; start: string; end: string; days: number };
	const overrideEntries = $derived.by((): OvEntry[] => {
		const groups = new Map<string, AvailabilityOverride[]>();
		const entries: OvEntry[] = [];
		for (const ov of overrides) {
			if (ov.group_id) {
				const arr = groups.get(ov.group_id) ?? [];
				arr.push(ov);
				groups.set(ov.group_id, arr);
			} else {
				entries.push({ kind: 'single', ov });
			}
		}
		for (const [gid, arr] of groups) {
			const dates = arr.map((o) => o.date).sort();
			entries.push({
				kind: 'span',
				group_id: gid,
				reason: (arr[0].reason ?? 'out_of_office') as OverrideReason,
				start: dates[0],
				end: dates[dates.length - 1],
				days: dates.length
			});
		}
		return entries.sort((a, b) =>
			(a.kind === 'single' ? a.ov.date : a.start).localeCompare(b.kind === 'single' ? b.ov.date : b.start)
		);
	});

	async function loadOverrides() {
		overridesError = '';
		try {
			const res = await api.get<{ items: AvailabilityOverride[] }>('/v1/availability-overrides');
			overrides = (res.items ?? []).sort((a, b) => a.date.localeCompare(b.date));
		} catch (e: any) {
			overridesError = e.message;
		} finally {
			overridesLoading = false;
		}
	}

	// Dates whose custom-hours blocks overlap: they merge into one window at slot
	// time, so flag them rather than silently showing two rows that act as one.
	const overlappingOverrideDates = $derived.by((): string[] => {
		const byDate = new Map<string, AvailabilityOverride[]>();
		for (const ov of overrides) {
			if (ov.reason !== 'custom_hours' || !ov.start_time || !ov.end_time) continue;
			const arr = byDate.get(ov.date) ?? [];
			arr.push(ov);
			byDate.set(ov.date, arr);
		}
		const out: string[] = [];
		for (const [date, blocks] of byDate) {
			const sorted = [...blocks].sort((a, b) => (a.start_time ?? '').localeCompare(b.start_time ?? ''));
			for (let i = 1; i < sorted.length; i++) {
				if ((sorted[i].start_time ?? '') < (sorted[i - 1].end_time ?? '')) {
					out.push(date);
					break;
				}
			}
		}
		return out;
	});



	async function addOverride() {
		ovAddError = '';
		if (!ovForm.date) { ovAddError = tr('availability.validation.dateRequired'); return; }
		if (ovForm.reason === 'custom_hours' && (!ovForm.start_time || !ovForm.end_time)) {
			ovAddError = tr('availability.validation.customHoursTimes'); return;
		}
		if (ovForm.reason === 'custom_hours' && ovForm.start_time >= ovForm.end_time) {
			ovAddError = tr('availability.validation.endAfterStart'); return;
		}
		if (ovForm.end_date && ovForm.end_date < ovForm.date) {
			ovAddError = tr('availability.validation.endDate'); return;
		}
		addingOv = true;
		try {
			const isRange = ovForm.reason !== 'custom_hours' && !!ovForm.end_date && ovForm.end_date > ovForm.date;
			await api.post('/v1/availability-overrides', {
				date: ovForm.date,
				reason: ovForm.reason,
				...(ovForm.reason === 'custom_hours'
					? { start_time: ovForm.start_time, end_time: ovForm.end_time }
					: {}),
				...(isRange ? { end_date: ovForm.end_date } : {})
			});
			ovForm = { date: '', end_date: '', reason: 'day_off', start_time: '09:00', end_time: '17:00' };
			await loadOverrides();
		} catch (e: any) {
			ovAddError = e.message;
		} finally {
			addingOv = false;
		}
	}

	function deleteOverride(id: string) {
		deleteOvId = id;
		deleteOvOpen = true;
	}

	async function doDeleteOverride() {
		try {
			await api.del(`/v1/availability-overrides/${deleteOvId}`);
			await loadOverrides();
		} catch (e: any) {
			overridesError = e.message;
		}
	}

	function deleteGroup(groupId: string) {
		deleteGroupId = groupId;
		deleteGroupOpen = true;
	}

	async function doDeleteGroup() {
		try {
			await api.del(`/v1/availability-overrides/group/${deleteGroupId}`);
			await loadOverrides();
		} catch (e: any) {
			overridesError = e.message;
		}
	}

	let eventTypes = $state<{id: string; name: string}[]>([])
	async function loadEventTypes() {
		try {
			const res = await api.get<{ items: EventType[] }>('/v1/event-types');
			eventTypes = (res.items ?? []).map((et) => ({
				id: et.id,
				name: et.name,
			}))
		} catch (e: any) {

		}
	}

	onMount(() => {
		loadEventTypes()
		loadRules();
		loadOverrides();
	});

	const REASON_OPTIONS = $derived<{ value: OverrideReason; label: string }[]>([
		{ value: 'day_off', label: REASON_LABELS.day_off },
		{ value: 'out_of_office', label: REASON_LABELS.out_of_office },
		{ value: 'custom_hours', label: REASON_LABELS.custom_hours },
	]);

	// Time-of-day options for the start/end selects, formatted per the user's prefs.
	let timeOptions = $derived(TIME_SLOTS.map((t) => ({ value: t, label: fmtTime(t) })));
	function timeLabel(t: string) { return fmtTime(t); }
	function eventName(id: string | null) {
		if (id === null) {
			return {id: null, name: "None"}
		}
		return eventTypes.find((et => et.id == id))
	}
</script>

<ConfirmDialog
	bind:open={deleteOvOpen}
	title={$t('availability.confirmRemoveOverride.title')}
	description={$t('availability.confirmRemoveOverride.description')}
	confirmText={$t('common.remove')}
	destructive
	onConfirm={doDeleteOverride}
/>

<ConfirmDialog
	bind:open={deleteGroupOpen}
	title={$t('availability.confirmRemoveSpan.title')}
	description={$t('availability.confirmRemoveSpan.description')}
	confirmText={$t('common.remove')}
	destructive
	onConfirm={doDeleteGroup}
/>

<svelte:head><title>{$t('availability.headTitle')}</title></svelte:head>

<div class="mb-8">
	<h1 class="text-2xl font-semibold tracking-tight">{$t('availability.title')}</h1>
	<p class="mt-1 text-sm text-muted-foreground">{$t('availability.subtitle')}</p>
</div>

<!-- Weekly Hours -->
<div class="mb-8">
	<h2 class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground">{$t('availability.weeklyHours')}</h2>

	{#if rulesError}<p class="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{rulesError}</p>{/if}

	{#if rulesLoading}
		<p class="py-4 text-sm text-muted-foreground">{$t('common.loading')}</p>
	{:else}
		<div class="rounded-lg border bg-card">
			<Tooltip.Provider>
				{#each orderedDays as day, i}
					<div class="flex gap-4 px-4 py-3 {i > 0 ? 'border-t' : ''}">
						<!-- Day name -->
						<div class="w-24 shrink-0 pt-1.5 text-sm {day.blocks.length === 0 ? 'font-normal text-muted-foreground' : 'font-medium'}">
							{DAY_NAMES[day.day_of_week]}
						</div>

						<!-- Blocks -->
						<div class="flex-1 space-y-2">
							{#if day.error}
								<p class="text-xs text-destructive">{day.error}</p>
							{/if}

							{#if day.blocks.length === 0}
								<div class="flex items-center gap-3 py-0.5">
									<span class="text-sm text-muted-foreground/50">{$t('availability.noHours')}</span>
									<button
										onclick={() => addBlock(day)}
										class="text-sm text-primary hover:underline"
									>
										{$t('availability.addHours')}
									</button>
								</div>
							{:else}
								{#each day.blocks as block}
									<div class="flex items-center gap-2">
										<Select.Root
											type="single"
											bind:value={block.start_time}
											onValueChange={() => updateBlock(block)}
											disabled={block.saving || !block.id}
										>
											<Select.Trigger class="w-fit">{timeLabel(block.start_time)}</Select.Trigger>
											<Select.Content>
												{#each timeOptions as opt}<Select.Item value={opt.value} label={opt.label}>{opt.label}</Select.Item>{/each}
											</Select.Content>
										</Select.Root>
										<span class="text-muted-foreground">–</span>
										<Select.Root
											type="single"
											bind:value={block.end_time}
											onValueChange={() => updateBlock(block)}
											disabled={block.saving || !block.id}
										>
											<Select.Trigger class="w-fit">{timeLabel(block.end_time)}</Select.Trigger>
											<Select.Content>
												{#each timeOptions as opt}<Select.Item value={opt.value} label={opt.label}>{opt.label}</Select.Item>{/each}
											</Select.Content>
										</Select.Root>
										{#if block.saving}
											<span class="text-xs text-muted-foreground">{$t('availability.saving')}</span>
										{:else if block.error}
											<span class="text-xs text-destructive">{block.error}</span>
										{/if}

										<span class="text-muted-foreground">|</span>
										<div class="text-sm font-normal text-muted-foreground">
											{$t('availability.eventTypes.appliesTo')}:
										</div>
										<Select.Root
											type="single"
											bind:value={block.event_type_id}
											onValueChange={() => updateBlock(block)}
											disabled={block.saving || !block.id}
										>
											<Select.Trigger class="w-fit">
												{#if block.event_type_id === "null"}
													{$t('availability.eventTypes.allEventTypes')}
												{:else}
													{eventName(block.event_type_id)?.name ?? block.event_type_id}
												{/if}
											</Select.Trigger>
											<Select.Content>
												<Select.Item value="null" label="All">{$t('availability.eventTypes.allEventTypes')}</Select.Item>
												{#each eventTypes as e}<Select.Item value={e.id} label={e.name}>{e.name}</Select.Item>{/each}
											</Select.Content>
										</Select.Root>

										<Tooltip.Root>
											<Tooltip.Trigger
												class={buttonVariants({ variant: 'ghost', size: 'icon' })}
												onclick={() => removeBlock(day, block)}
												disabled={block.saving}
											>
												<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
											</Tooltip.Trigger>
											<Tooltip.Content>{$t('availability.tooltip.remove')}</Tooltip.Content>
										</Tooltip.Root>
									</div>
								{/each}
								<button
									onclick={() => addBlock(day)}
									class="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
								>
									<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
									{$t('availability.addBlock')}
								</button>
							{/if}
						</div>
					</div>
				{/each}
			</Tooltip.Provider>
		</div>
	{/if}
</div>

<!-- Date Overrides -->
<div>
	<h2 class="mb-1 text-sm font-semibold uppercase tracking-wider text-muted-foreground">{$t('availability.overrides.title')}</h2>
	<p class="mb-3 text-sm text-muted-foreground">{$t('availability.overrides.subtitle')}</p>

	<div class="rounded-lg border bg-card">
		{#if overridesError}<p class="px-4 pt-4 text-sm text-destructive">{overridesError}</p>{/if}
		{#if overlappingOverrideDates.length > 0}
			<p class="px-4 pt-4 text-xs text-amber-700">{$t('availability.overrides.overlapWarning', { dates: overlappingOverrideDates.map((d) => fmtDate(d, $prefs)).join(', ') })}</p>
		{/if}

		{#if overridesLoading}
			<p class="px-4 py-4 text-sm text-muted-foreground">{$t('common.loading')}</p>
		{:else if overrides.length > 0}
			<table class="w-full text-sm">
				<thead>
					<tr class="border-b">
						<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('availability.overrides.table.date')}</th>
						<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('availability.overrides.table.type')}</th>
						<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('availability.overrides.table.duration')}</th>
						<th class="px-4 pb-3 pt-3"></th>
					</tr>
				</thead>
				<tbody class="divide-y">
					{#each overrideEntries as entry (entry.kind === 'span' ? entry.group_id : entry.ov.id)}
						{#if entry.kind === 'span'}
							<tr class="transition-colors hover:bg-muted/30">
								<td class="px-4 py-3 font-medium">{fmtDate(entry.start, $prefs)} – {fmtDate(entry.end, $prefs)}</td>
								<td class="px-4 py-3">
									{#if entry.reason === 'out_of_office'}
										<Badge class="bg-amber-50 text-amber-700 border-amber-200">{REASON_LABELS.out_of_office}</Badge>
									{:else}
										<Badge variant="secondary">{REASON_LABELS.day_off}</Badge>
									{/if}
								</td>
								<td class="px-4 py-3 text-muted-foreground">{$t('availability.overrides.duration.days', { count: entry.days })}</td>
								<td class="px-4 py-3">
									<Tooltip.Provider>
										<div class="flex items-center justify-end gap-1">
											<Tooltip.Root>
												<Tooltip.Trigger class={buttonVariants({ variant: 'ghost', size: 'icon' })} onclick={() => deleteGroup(entry.group_id)}>
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
												</Tooltip.Trigger>
												<Tooltip.Content>{$t('availability.tooltip.deleteSpan')}</Tooltip.Content>
											</Tooltip.Root>
										</div>
									</Tooltip.Provider>
								</td>
							</tr>
						{:else}
							{@const ov = entry.ov}
							<tr class="transition-colors hover:bg-muted/30">
								<td class="px-4 py-3 font-medium">{fmtDate(ov.date, $prefs)}</td>
								<td class="px-4 py-3">
									{#if ov.reason === 'custom_hours'}
										<Badge class="bg-blue-50 text-blue-700 border-blue-200">{REASON_LABELS.custom_hours}</Badge>
									{:else if ov.reason === 'out_of_office'}
										<Badge class="bg-amber-50 text-amber-700 border-amber-200">{REASON_LABELS.out_of_office}</Badge>
									{:else}
										<Badge variant="secondary">{REASON_LABELS.day_off}</Badge>
									{/if}
								</td>
								<td class="px-4 py-3 text-muted-foreground">
									{ov.is_available && ov.start_time && ov.end_time
										? `${fmtTime(ov.start_time)} – ${fmtTime(ov.end_time)}`
										: $t('availability.overrides.duration.oneDay')}
								</td>
								<td class="px-4 py-3">
									<Tooltip.Provider>
										<div class="flex items-center justify-end gap-1">
											<Tooltip.Root>
												<Tooltip.Trigger
													class={buttonVariants({ variant: 'ghost', size: 'icon' })}
													onclick={() => deleteOverride(ov.id)}
												>
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
												</Tooltip.Trigger>
												<Tooltip.Content>{$t('availability.tooltip.delete')}</Tooltip.Content>
											</Tooltip.Root>
										</div>
									</Tooltip.Provider>
								</td>
							</tr>
						{/if}
					{/each}
				</tbody>
			</table>
		{:else}
			<p class="px-4 py-4 text-sm text-muted-foreground">{$t('availability.overrides.empty')}</p>
		{/if}

		<!-- Add override form -->
		<div class="border-t px-4 py-4">
			{#if ovAddError}<p class="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{ovAddError}</p>{/if}
			<div class="flex flex-wrap items-end gap-3">
				<div class="space-y-1.5">
					<label for="ov-date" class="text-sm font-medium">{$t('availability.overrides.form.date')}</label>
					<DatePicker bind:value={ovForm.date} placeholder={$t('common.datePicker.pickDate')} />
				</div>
				{#if ovForm.reason !== 'custom_hours'}
					<div class="space-y-1.5">
						<label for="ov-end-date" class="text-sm font-medium">{$t('availability.overrides.form.toOptional')}</label>
						<DatePicker bind:value={ovForm.end_date} placeholder={$t('common.datePicker.sameDay')} />
					</div>
				{/if}
				<div class="space-y-1.5">
					<label for="ov-type" class="text-sm font-medium">{$t('availability.overrides.form.type')}</label>
					<Select.Root type="single" value={ovForm.reason} onValueChange={(v) => { if (v) ovForm.reason = v as OverrideReason; }}>
						<Select.Trigger id="ov-type" class="w-fit">{REASON_LABELS[ovForm.reason]}</Select.Trigger>
						<Select.Content>
							{#each REASON_OPTIONS as r}
								<Select.Item value={r.value} label={r.label}>{r.label}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
				</div>
				{#if ovForm.reason === 'custom_hours'}
					<div class="space-y-1.5">
						<label for="ov-start" class="text-sm font-medium">{$t('availability.overrides.form.from')}</label>
						<Select.Root type="single" bind:value={ovForm.start_time}>
							<Select.Trigger id="ov-start" class="w-fit">{timeLabel(ovForm.start_time)}</Select.Trigger>
							<Select.Content>
								{#each timeOptions as opt}<Select.Item value={opt.value} label={opt.label}>{opt.label}</Select.Item>{/each}
							</Select.Content>
						</Select.Root>
					</div>
					<div class="space-y-1.5">
						<label for="ov-end" class="text-sm font-medium">{$t('availability.overrides.form.to')}</label>
						<Select.Root type="single" bind:value={ovForm.end_time}>
							<Select.Trigger id="ov-end" class="w-fit">{timeLabel(ovForm.end_time)}</Select.Trigger>
							<Select.Content>
								{#each timeOptions as opt}<Select.Item value={opt.value} label={opt.label}>{opt.label}</Select.Item>{/each}
							</Select.Content>
						</Select.Root>
					</div>
				{/if}
				<Button onclick={addOverride} disabled={addingOv}>
					{addingOv ? $t('availability.overrides.adding') : $t('availability.overrides.add')}
				</Button>
				{#if ovForm.reason === 'custom_hours'}
					<p class="w-full text-xs text-muted-foreground">{$t('availability.overrides.splitHint')}</p>
				{/if}
			</div>
		</div>
	</div>
</div>
