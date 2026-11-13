<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type AvailabilityRule, type AvailabilityOverride } from '$lib/api';
	import { prefs, fmtDate, weekDays } from '$lib/prefs';
	import { t, tr, localeTag } from '$lib/i18n';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Badge } from '$lib/components/ui/badge';
	import { Switch } from '$lib/components/ui/switch';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import * as Select from '$lib/components/ui/select';
	import { DatePicker } from '$lib/components/ui/date-picker';
	import { toast } from 'svelte-sonner';

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
	type DayBlock = { id: string; start_time: string; end_time: string; saving: boolean; error: string }
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
				if (day) day.blocks.push({ id: r.id, start_time: r.start_time, end_time: r.end_time, saving: false, error: '' });
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
		day.blocks.push({ id: '', start_time: '09:00', end_time: '17:00', saving: true, error: '' });
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

	onMount(() => {
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

	let copyOpenDay = $state<number | null>(null);
	let copyTargets = $state<Set<number>>(new Set());
	let copyingTimes = $state(false);

	async function toggleDay(day: DayState, enabled: boolean) {
		if (enabled) {
			if (day.blocks.length === 0) await addBlock(day);
			return;
		}
		day.error = '';
		const toRemove = [...day.blocks];
		day.blocks = [];
		for (const b of toRemove) {
			if (!b.id) continue;
			try {
				await api.del(`/v1/availability-rules/${b.id}`);
			} catch (e: any) {
				day.error = e.message;
			}
		}
	}

	function openCopyMenu(dayOfWeek: number) {
		if (copyOpenDay === dayOfWeek) {
			copyOpenDay = null;
			return;
		}
		copyOpenDay = dayOfWeek;
		copyTargets = new Set();
	}

	function toggleCopyTarget(dayOfWeek: number) {
		const next = new Set(copyTargets);
		if (next.has(dayOfWeek)) next.delete(dayOfWeek);
		else next.add(dayOfWeek);
		copyTargets = next;
	}

	function toggleCopySelectAll(sourceDayOfWeek: number) {
		const others = orderedDays.map((d) => d.day_of_week).filter((d) => d !== sourceDayOfWeek);
		if (others.every((d) => copyTargets.has(d))) {
			copyTargets = new Set();
		} else {
			copyTargets = new Set(others);
		}
	}

	async function applyCopyTimes(sourceDay: DayState) {
		if (copyTargets.size === 0 || copyingTimes) return;
		copyingTimes = true;
		try {
			const templateBlocks = sourceDay.blocks.map((b) => ({
				start_time: b.start_time,
				end_time: b.end_time
			}));
			for (const targetDow of copyTargets) {
				const target = days[targetDow];
				if (!target) continue;
				for (const old of target.blocks) {
					if (old.id) await api.del(`/v1/availability-rules/${old.id}`);
				}
				const created: DayBlock[] = [];
				for (const tb of templateBlocks) {
					const r = await api.post<AvailabilityRule>('/v1/availability-rules', {
						day_of_week: targetDow,
						start_time: tb.start_time,
						end_time: tb.end_time
					});
					created.push({ id: r.id, start_time: r.start_time, end_time: r.end_time, saving: false, error: '' });
				}
				target.blocks = created;
				target.error = '';
			}
			copyOpenDay = null;
			toast.success('Copied hours to selected days');
		} catch (e: any) {
			toast.error(e.message || 'Failed to copy hours');
		} finally {
			copyingTimes = false;
		}
	}

	// ── Cal.com-style Date Override Modal ──────────────────────────────────────
	let ovModalOpen = $state(false);
	let ovCalMonth = $state(new Date(new Date().getFullYear(), new Date().getMonth(), 1));
	let ovSelectedDates = $state<Set<string>>(new Set());
	let ovLastSelectedDate = $state<string | null>(null);
	let ovAllDayUnavailable = $state(false);
	let ovTimeRanges = $state<{ start_time: string; end_time: string }[]>([
		{ start_time: '09:00', end_time: '17:00' }
	]);
	let ovSavingModal = $state(false);
	let ovModalError = $state('');

	const ovMonthLabel = $derived(
		ovCalMonth.toLocaleDateString(localeTag(), { month: 'long', year: 'numeric' })
	);

	const ovCalendarCells = $derived.by(() => {
		const y = ovCalMonth.getFullYear();
		const m = ovCalMonth.getMonth();
		const firstDow = new Date(y, m, 1).getDay();
		const daysInMonth = new Date(y, m + 1, 0).getDate();
		const cells: { day: number | null; iso: string }[] = [];
		for (let i = 0; i < firstDow; i++) cells.push({ day: null, iso: '' });
		for (let d = 1; d <= daysInMonth; d++) {
			const iso = `${y}-${String(m + 1).padStart(2, '0')}-${String(d).padStart(2, '0')}`;
			cells.push({ day: d, iso });
		}
		return cells;
	});

	function openOverrideModal() {
		ovSelectedDates = new Set();
		ovLastSelectedDate = null;
		ovAllDayUnavailable = false;
		ovTimeRanges = [{ start_time: '09:00', end_time: '17:00' }];
		ovModalError = '';
		ovModalOpen = true;
	}

	function parseIsoDateUTC(iso: string): Date {
		const [y, m, d] = iso.split('-').map(Number);
		return new Date(Date.UTC(y, m - 1, d));
	}

	function toggleOvDate(iso: string, e?: MouseEvent) {
		if (!iso) return;
		const next = new Set(ovSelectedDates);
		if (e?.shiftKey && ovLastSelectedDate) {
			const a = ovLastSelectedDate < iso ? ovLastSelectedDate : iso;
			const b = ovLastSelectedDate < iso ? iso : ovLastSelectedDate;
			const cur = parseIsoDateUTC(a);
			const end = parseIsoDateUTC(b);
			while (cur <= end) {
				next.add(cur.toISOString().slice(0, 10));
				cur.setUTCDate(cur.getUTCDate() + 1);
			}
		} else {
			if (next.has(iso)) next.delete(iso);
			else next.add(iso);
		}
		ovLastSelectedDate = iso;
		ovSelectedDates = next;
	}

	function shiftOvMonth(delta: number) {
		ovCalMonth = new Date(ovCalMonth.getFullYear(), ovCalMonth.getMonth() + delta, 1);
	}

	function addOvTimeRange() {
		ovTimeRanges = [...ovTimeRanges, { start_time: '13:00', end_time: '17:00' }];
	}

	function removeOvTimeRange(idx: number) {
		if (ovTimeRanges.length <= 1) return;
		ovTimeRanges = ovTimeRanges.filter((_, i) => i !== idx);
	}

	async function saveOverrideModal() {
		ovModalError = '';
		if (ovSelectedDates.size === 0) {
			ovModalError = 'Select at least one date.';
			return;
		}
		if (!ovAllDayUnavailable) {
			for (const r of ovTimeRanges) {
				if (r.start_time >= r.end_time) {
					ovModalError = tr('availability.validation.endAfterStart');
					return;
				}
			}
		}
		ovSavingModal = true;
		try {
			const dates = [...ovSelectedDates].sort();
			for (const date of dates) {
				if (ovAllDayUnavailable) {
					await api.post('/v1/availability-overrides', {
						date,
						reason: 'day_off'
					});
				} else {
					for (const r of ovTimeRanges) {
						await api.post('/v1/availability-overrides', {
							date,
							reason: 'custom_hours',
							start_time: r.start_time,
							end_time: r.end_time
						});
					}
				}
			}
			ovModalOpen = false;
			await loadOverrides();
			toast.success('Date override saved');
		} catch (e: any) {
			ovModalError = e.message || 'Failed to save override';
		} finally {
			ovSavingModal = false;
		}
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
					<div class="relative flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-start sm:gap-4 {i > 0 ? 'border-t' : ''}">
						<!-- Day toggle + name -->
						<div class="flex w-32 shrink-0 items-center gap-2.5 pt-1.5">
							<Switch
								checked={day.blocks.length > 0}
								onCheckedChange={(v) => toggleDay(day, v)}
								aria-label={`Toggle ${DAY_NAMES[day.day_of_week]}`}
							/>
							<span class="text-sm {day.blocks.length === 0 ? 'font-normal text-muted-foreground' : 'font-medium'}">
								{DAY_NAMES[day.day_of_week]}
							</span>
						</div>

						<!-- Blocks -->
						<div class="flex-1 space-y-2">
							{#if day.error}
								<p class="text-xs text-destructive">{day.error}</p>
							{/if}

							{#if day.blocks.length === 0}
								<div class="flex items-center py-1.5">
									<span class="text-sm text-muted-foreground">Unavailable</span>
								</div>
							{:else}
								{#each day.blocks as block, bi}
									<div class="flex flex-wrap items-center gap-2">
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
										<Button
											variant="ghost"
											size="icon"
											class="h-8 w-8 text-muted-foreground hover:text-foreground"
											onclick={() => removeBlock(day, block)}
											disabled={block.saving}
											title={$t('availability.tooltip.remove')}
										>
											<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
										</Button>
										{#if bi === 0}
											<div class="ml-auto flex items-center gap-1">
												<Button
													variant="ghost"
													size="icon"
													class="h-8 w-8 text-muted-foreground hover:text-foreground"
													onclick={() => addBlock(day)}
													title={$t('availability.addBlock')}
												>
													<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
												</Button>
												<Button
													variant="ghost"
													size="icon"
													class="h-8 w-8 text-muted-foreground hover:text-foreground"
													onclick={() => openCopyMenu(day.day_of_week)}
													title="Copy times to..."
												>
													<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
												</Button>
											</div>
										{/if}
									</div>
								{/each}
							{/if}
						</div>

						{#if copyOpenDay === day.day_of_week}
							<div class="absolute right-4 top-12 z-30 w-52 rounded-lg border bg-popover p-2 text-popover-foreground shadow-lg">
								<p class="px-2 py-1 text-xs font-medium text-muted-foreground">Copy times to</p>
								<button
									type="button"
									class="flex w-full items-center justify-between rounded px-2 py-1.5 text-left text-xs hover:bg-accent"
									onclick={() => toggleCopySelectAll(day.day_of_week)}
								>
									<span>Select all</span>
									{#if orderedDays.filter((d) => d.day_of_week !== day.day_of_week).every((d) => copyTargets.has(d.day_of_week))}
										<span>✓</span>
									{/if}
								</button>
								<div class="my-1 border-t"></div>
								<div class="space-y-0.5">
									{#each orderedDays as target}
										<button
											type="button"
											disabled={target.day_of_week === day.day_of_week}
											class="flex w-full items-center justify-between rounded px-2 py-1.5 text-left text-xs transition-colors {target.day_of_week === day.day_of_week ? 'opacity-40 cursor-default' : 'hover:bg-accent'}"
											onclick={() => toggleCopyTarget(target.day_of_week)}
										>
											<span>{DAY_NAMES[target.day_of_week]}</span>
											{#if target.day_of_week === day.day_of_week || copyTargets.has(target.day_of_week)}
												<span>✓</span>
											{/if}
										</button>
									{/each}
								</div>
								<div class="mt-2 flex items-center justify-end gap-1.5 border-t pt-2">
									<Button variant="ghost" size="sm" class="h-7 px-2 text-xs" onclick={() => (copyOpenDay = null)}>Cancel</Button>
									<Button size="sm" class="h-7 px-2.5 text-xs" disabled={copyTargets.size === 0 || copyingTimes} onclick={() => applyCopyTimes(day)}>
										{copyingTimes ? 'Applying...' : 'Apply'}
									</Button>
								</div>
							</div>
						{/if}
					</div>
				{/each}
			</Tooltip.Provider>
		</div>
	{/if}
</div>

<!-- Date Overrides -->
<div>
	<div class="mb-3 flex flex-wrap items-center justify-between gap-3">
		<div>
			<h2 class="text-sm font-semibold uppercase tracking-wider text-muted-foreground">{$t('availability.overrides.title')}</h2>
			<p class="mt-0.5 text-sm text-muted-foreground">{$t('availability.overrides.subtitle')}</p>
		</div>
		<Button variant="outline" size="sm" onclick={openOverrideModal}>
			+ Add an override
		</Button>
	</div>

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
	</div>
</div>

<Dialog.Root bind:open={ovModalOpen}>
	<Dialog.Content class="w-[calc(100vw-1.5rem)] max-w-3xl sm:max-w-3xl max-h-[90dvh] overflow-y-auto p-0">
		<div class="grid grid-cols-1 md:grid-cols-[310px_1fr]">
			<!-- Left: Date selector -->
			<div class="border-b p-4 sm:p-6 md:border-b-0 md:border-r">
				<div class="mb-1 flex items-center justify-between gap-2 pr-8 md:pr-0">
					<h3 class="text-sm font-semibold">Select the dates to override</h3>
					{#if ovSelectedDates.size > 0}
						<span class="shrink-0 rounded-full bg-primary/10 px-2 py-0.5 text-[11px] font-medium text-primary">{ovSelectedDates.size} selected</span>
					{/if}
				</div>
				<p class="mb-3 text-[11px] text-muted-foreground">Tip: Shift+click to select a date range.</p>
				<div class="mb-3 flex items-center justify-between">
					<button type="button" class="rounded p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground" onclick={() => shiftOvMonth(-1)} aria-label="Previous month">
						<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"/></svg>
					</button>
					<span class="text-xs font-semibold">{ovMonthLabel}</span>
					<button type="button" class="rounded p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground" onclick={() => shiftOvMonth(1)} aria-label="Next month">
						<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="9 18 15 12 9 6"/></svg>
					</button>
				</div>
				<div class="grid grid-cols-7 gap-1 text-center text-[11px] text-muted-foreground">
					{#each ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'] as wd}
						<div class="py-1 font-medium">{wd}</div>
					{/each}
					{#each ovCalendarCells as cell}
						{#if cell.day === null}
							<div></div>
						{:else}
							{@const selected = ovSelectedDates.has(cell.iso)}
							<button
								type="button"
								onclick={(e) => toggleOvDate(cell.iso, e)}
								class="h-9 sm:h-10 rounded-md text-xs font-medium transition-colors select-none {selected
									? 'bg-primary text-primary-foreground'
									: 'hover:bg-muted text-foreground'}"
							>
								{cell.day}
							</button>
						{/if}
					{/each}
				</div>
			</div>

			<!-- Right: Free hours or Mark unavailable -->
			<div class="flex flex-col justify-between p-4 sm:p-6">
				<div class="space-y-4">
					<h3 class="text-sm font-semibold">Which hours are you free?</h3>
					<label class="flex cursor-pointer items-center gap-2.5 text-sm">
						<Switch bind:checked={ovAllDayUnavailable} />
						<span>Mark unavailable (All day)</span>
					</label>

					{#if !ovAllDayUnavailable}
						<div class="space-y-2">
							{#each ovTimeRanges as range, idx}
								<div class="flex flex-wrap items-center gap-2">
									<Select.Root type="single" bind:value={range.start_time}>
										<Select.Trigger class="w-fit">{timeLabel(range.start_time)}</Select.Trigger>
										<Select.Content>
											{#each timeOptions as opt}<Select.Item value={opt.value} label={opt.label}>{opt.label}</Select.Item>{/each}
										</Select.Content>
									</Select.Root>
									<span class="text-muted-foreground">–</span>
									<Select.Root type="single" bind:value={range.end_time}>
										<Select.Trigger class="w-fit">{timeLabel(range.end_time)}</Select.Trigger>
										<Select.Content>
											{#each timeOptions as opt}<Select.Item value={opt.value} label={opt.label}>{opt.label}</Select.Item>{/each}
										</Select.Content>
									</Select.Root>
									{#if ovTimeRanges.length > 1}
										<Button variant="ghost" size="icon" class="h-8 w-8" onclick={() => removeOvTimeRange(idx)} title="Remove interval">
											<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
										</Button>
									{/if}
									{#if idx === ovTimeRanges.length - 1}
										<Button variant="ghost" size="icon" class="h-8 w-8" onclick={addOvTimeRange} title="Add interval">
											<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
										</Button>
									{/if}
								</div>
							{/each}
						</div>
					{/if}

					{#if ovModalError}
						<p class="rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">{ovModalError}</p>
					{/if}
				</div>

				<div class="mt-6 flex items-center justify-end gap-2 border-t pt-4">
					<Button variant="ghost" onclick={() => (ovModalOpen = false)} disabled={ovSavingModal}>Close</Button>
					<Button onclick={saveOverrideModal} disabled={ovSavingModal}>
						{ovSavingModal ? 'Saving...' : 'Save override'}
					</Button>
				</div>
			</div>
		</div>
	</Dialog.Content>
</Dialog.Root>
