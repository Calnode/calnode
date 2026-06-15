<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type AvailabilityRule, type AvailabilityOverride } from '$lib/api';
	import { prefs, fmtDate } from '$lib/prefs';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { DatePicker } from '$lib/components/ui/date-picker';

	const DAY_NAMES = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

	const TIME_SLOTS: string[] = [];
	for (let h = 0; h < 24; h++) {
		for (const m of [0, 30]) {
			TIME_SLOTS.push(`${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`);
		}
	}

	function fmtTime(t: string) {
		const [hh, mm] = t.split(':').map(Number);
		if ($prefs.time_format === '24h') {
			return mm === 0 ? `${String(hh).padStart(2,'0')}:00` : `${String(hh).padStart(2,'0')}:${String(mm).padStart(2,'0')}`;
		}
		const ampm = hh < 12 ? 'am' : 'pm';
		const h12 = hh % 12 || 12;
		return mm === 0 ? `${h12}${ampm}` : `${h12}:${String(mm).padStart(2,'0')}${ampm}`;
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
		const newBlock: DayBlock = { id: '', start_time: '09:00', end_time: '17:00', saving: true, error: '' };
		day.blocks.push(newBlock);
		try {
			const r = await api.post<AvailabilityRule>('/v1/availability-rules', {
				day_of_week: day.day_of_week,
				start_time: '09:00',
				end_time: '17:00'
			});
			newBlock.id = r.id;
		} catch (e: any) {
			const idx = day.blocks.indexOf(newBlock);
			if (idx !== -1) day.blocks.splice(idx, 1);
			day.error = e.message;
		} finally {
			newBlock.saving = false;
		}
	}

	async function updateBlock(block: DayBlock) {
		if (!block.id) return;
		if (block.start_time >= block.end_time) {
			block.error = 'End must be after start';
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
	const REASON_LABELS: Record<OverrideReason, string> = {
		day_off: 'Day off',
		out_of_office: 'Out of office',
		custom_hours: 'Custom hours'
	};

	let ovForm = $state({ date: '', reason: 'day_off' as OverrideReason, start_time: '09:00', end_time: '17:00' });
	let addingOv = $state(false);
	let ovAddError = $state('');

	let editingOvId: string | null = $state(null);
	let editOvForm = $state({ reason: 'day_off' as OverrideReason, start_time: '09:00', end_time: '17:00' });
	let savingOv = $state(false);
	let ovSaveError = $state('');

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

	function startEditOv(ov: AvailabilityOverride) {
		editingOvId = ov.id;
		editOvForm = {
			reason: (ov.reason ?? 'day_off') as OverrideReason,
			start_time: ov.start_time ?? '09:00',
			end_time: ov.end_time ?? '17:00'
		};
		ovSaveError = '';
	}

	function cancelEditOv() { editingOvId = null; ovSaveError = ''; }

	async function saveOv(id: string) {
		ovSaveError = '';
		if (editOvForm.reason === 'custom_hours' && editOvForm.start_time >= editOvForm.end_time) {
			ovSaveError = 'End time must be after start time.'; return;
		}
		savingOv = true;
		try {
			await api.patch(`/v1/availability-overrides/${id}`, {
				reason: editOvForm.reason,
				...(editOvForm.reason === 'custom_hours'
					? { start_time: editOvForm.start_time, end_time: editOvForm.end_time }
					: {})
			});
			editingOvId = null;
			await loadOverrides();
		} catch (e: any) {
			ovSaveError = e.message;
		} finally {
			savingOv = false;
		}
	}

	async function addOverride() {
		ovAddError = '';
		if (!ovForm.date) { ovAddError = 'Date is required.'; return; }
		if (ovForm.reason === 'custom_hours' && (!ovForm.start_time || !ovForm.end_time)) {
			ovAddError = 'Start and end time are required for custom hours.'; return;
		}
		if (ovForm.reason === 'custom_hours' && ovForm.start_time >= ovForm.end_time) {
			ovAddError = 'End time must be after start time.'; return;
		}
		addingOv = true;
		try {
			await api.post('/v1/availability-overrides', {
				date: ovForm.date,
				reason: ovForm.reason,
				...(ovForm.reason === 'custom_hours'
					? { start_time: ovForm.start_time, end_time: ovForm.end_time }
					: {})
			});
			ovForm = { date: '', reason: 'day_off', start_time: '09:00', end_time: '17:00' };
			await loadOverrides();
		} catch (e: any) {
			ovAddError = e.message;
		} finally {
			addingOv = false;
		}
	}

	async function deleteOverride(id: string) {
		if (!confirm('Remove this date override?')) return;
		try {
			await api.del(`/v1/availability-overrides/${id}`);
			await loadOverrides();
		} catch (e: any) {
			overridesError = e.message;
		}
	}

	onMount(() => {
		loadRules();
		loadOverrides();
	});

	const selectCls = 'flex h-8 rounded-md border border-input bg-background px-2 py-1 text-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring';
	const selectFullCls = 'flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring';
</script>

<svelte:head><title>Availability — Calnode</title></svelte:head>

<div class="mb-8">
	<h1 class="text-2xl font-semibold tracking-tight">Availability</h1>
	<p class="mt-1 text-sm text-muted-foreground">Set your weekly hours and block off specific dates.</p>
</div>

<!-- Weekly Hours -->
<div class="mb-8">
	<h2 class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground">Weekly Hours</h2>

	{#if rulesError}<p class="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{rulesError}</p>{/if}

	{#if rulesLoading}
		<p class="py-4 text-sm text-muted-foreground">Loading…</p>
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
									<span class="text-sm text-muted-foreground/50">No hours set</span>
									<button
										onclick={() => addBlock(day)}
										class="text-sm text-primary hover:underline"
									>
										+ Add hours
									</button>
								</div>
							{:else}
								{#each day.blocks as block}
									<div class="flex items-center gap-2">
										<select
											bind:value={block.start_time}
											onchange={() => updateBlock(block)}
											disabled={block.saving || !block.id}
											class={selectCls}
										>
											{#each TIME_SLOTS as t}<option value={t}>{fmtTime(t)}</option>{/each}
										</select>
										<span class="text-muted-foreground">–</span>
										<select
											bind:value={block.end_time}
											onchange={() => updateBlock(block)}
											disabled={block.saving || !block.id}
											class={selectCls}
										>
											{#each TIME_SLOTS as t}<option value={t}>{fmtTime(t)}</option>{/each}
										</select>
										{#if block.saving}
											<span class="text-xs text-muted-foreground">saving…</span>
										{:else if block.error}
											<span class="text-xs text-destructive">{block.error}</span>
										{/if}
										<Tooltip.Root>
											<Tooltip.Trigger
												class={buttonVariants({ variant: 'ghost', size: 'icon' })}
												onclick={() => removeBlock(day, block)}
												disabled={block.saving}
											>
												<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
											</Tooltip.Trigger>
											<Tooltip.Content>Remove</Tooltip.Content>
										</Tooltip.Root>
									</div>
								{/each}
								<button
									onclick={() => addBlock(day)}
									class="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
								>
									<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
									Add block
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
	<h2 class="mb-1 text-sm font-semibold uppercase tracking-wider text-muted-foreground">Date Overrides</h2>
	<p class="mb-3 text-sm text-muted-foreground">Block out a specific date, or set custom hours for it.</p>

	<div class="rounded-lg border bg-card">
		{#if overridesError}<p class="px-4 pt-4 text-sm text-destructive">{overridesError}</p>{/if}

		{#if overridesLoading}
			<p class="px-4 py-4 text-sm text-muted-foreground">Loading…</p>
		{:else if overrides.length > 0}
			<table class="w-full text-sm">
				<thead>
					<tr class="border-b">
						<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Date</th>
						<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Type</th>
						<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Hours</th>
						<th class="px-4 pb-3 pt-3"></th>
					</tr>
				</thead>
				<tbody class="divide-y">
					{#each overrides as ov}
						{#if editingOvId === ov.id}
							<tr class="bg-muted/20">
								<td class="px-4 py-3 font-medium">{fmtDate(ov.date, $prefs)}</td>
								<td class="px-4 py-3">
									<select bind:value={editOvForm.reason} class={selectFullCls} style="width: auto">
										<option value="day_off">Day off</option>
										<option value="out_of_office">Out of office</option>
										<option value="custom_hours">Custom hours</option>
									</select>
								</td>
								<td class="px-4 py-3">
									{#if editOvForm.reason === 'custom_hours'}
										<div class="flex items-center gap-2">
											<select class={selectCls} bind:value={editOvForm.start_time}>
												{#each TIME_SLOTS as t}<option value={t}>{fmtTime(t)}</option>{/each}
											</select>
											<span class="text-muted-foreground">–</span>
											<select class={selectCls} bind:value={editOvForm.end_time}>
												{#each TIME_SLOTS as t}<option value={t}>{fmtTime(t)}</option>{/each}
											</select>
										</div>
									{:else}
										<span class="text-muted-foreground">—</span>
									{/if}
									{#if ovSaveError}<p class="mt-1 text-xs text-destructive">{ovSaveError}</p>{/if}
								</td>
								<td class="px-4 py-3">
									<div class="flex items-center justify-end gap-2">
										<Button size="sm" onclick={() => saveOv(ov.id)} disabled={savingOv}>
											{savingOv ? 'Saving…' : 'Save'}
										</Button>
										<Button size="sm" variant="outline" onclick={cancelEditOv}>Cancel</Button>
									</div>
								</td>
							</tr>
						{:else}
							<tr class="transition-colors hover:bg-muted/30">
								<td class="px-4 py-3 font-medium">{fmtDate(ov.date, $prefs)}</td>
								<td class="px-4 py-3">
									{#if ov.reason === 'custom_hours'}
										<Badge class="bg-blue-50 text-blue-700 border-blue-200">Custom hours</Badge>
									{:else if ov.reason === 'out_of_office'}
										<Badge class="bg-amber-50 text-amber-700 border-amber-200">Out of office</Badge>
									{:else}
										<Badge variant="secondary">Day off</Badge>
									{/if}
								</td>
								<td class="px-4 py-3 font-mono text-xs text-muted-foreground">
									{ov.is_available && ov.start_time && ov.end_time
										? `${fmtTime(ov.start_time)} – ${fmtTime(ov.end_time)}`
										: '—'}
								</td>
								<td class="px-4 py-3">
									<Tooltip.Provider>
										<div class="flex items-center justify-end gap-1">
											<Tooltip.Root>
												<Tooltip.Trigger
													class={buttonVariants({ variant: 'ghost', size: 'icon' })}
													onclick={() => startEditOv(ov)}
												>
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
												</Tooltip.Trigger>
												<Tooltip.Content>Edit</Tooltip.Content>
											</Tooltip.Root>
											<Tooltip.Root>
												<Tooltip.Trigger
													class={buttonVariants({ variant: 'ghost', size: 'icon' })}
													onclick={() => deleteOverride(ov.id)}
												>
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
												</Tooltip.Trigger>
												<Tooltip.Content>Delete</Tooltip.Content>
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
			<p class="px-4 py-4 text-sm text-muted-foreground">No overrides yet.</p>
		{/if}

		<!-- Add override form -->
		<div class="border-t px-4 py-4">
			{#if ovAddError}<p class="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{ovAddError}</p>{/if}
			<div class="flex flex-wrap items-end gap-3">
				<div class="space-y-1.5">
					<label for="ov-date" class="text-sm font-medium">Date</label>
					<DatePicker bind:value={ovForm.date} placeholder="Pick a date" />
				</div>
				<div class="space-y-1.5">
					<label for="ov-type" class="text-sm font-medium">Type</label>
					<select id="ov-type" bind:value={ovForm.reason} class={selectFullCls} style="width: auto">
						<option value="day_off">Day off</option>
						<option value="out_of_office">Out of office</option>
						<option value="custom_hours">Custom hours</option>
					</select>
				</div>
				{#if ovForm.reason === 'custom_hours'}
					<div class="space-y-1.5">
						<label for="ov-start" class="text-sm font-medium">From</label>
						<select id="ov-start" bind:value={ovForm.start_time} class={selectCls}>
							{#each TIME_SLOTS as t}<option value={t}>{fmtTime(t)}</option>{/each}
						</select>
					</div>
					<div class="space-y-1.5">
						<label for="ov-end" class="text-sm font-medium">To</label>
						<select id="ov-end" bind:value={ovForm.end_time} class={selectCls}>
							{#each TIME_SLOTS as t}<option value={t}>{fmtTime(t)}</option>{/each}
						</select>
					</div>
				{/if}
				<Button onclick={addOverride} disabled={addingOv}>
					{addingOv ? 'Adding…' : 'Add override'}
				</Button>
			</div>
		</div>
	</div>
</div>
