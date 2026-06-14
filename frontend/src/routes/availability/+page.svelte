<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type AvailabilityRule, type AvailabilityOverride } from '$lib/api';
	import { prefs } from '$lib/prefs';

	const DAY_NAMES = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

	// 30-minute time slots for the selects
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

	// ── Weekly rules ────────────────────────────────────────────────────────────
	let rules: AvailabilityRule[] = [];
	let rulesLoading = true;
	let rulesError = '';

	let ruleForm = { day_of_week: 1, start_time: '09:00', end_time: '17:00' };
	let addingRule = false;
	let ruleAddError = '';

	// Inline edit state for rules
	let editingRuleId: string | null = null;
	let editRuleForm = { day_of_week: 0, start_time: '', end_time: '' };
	let savingRule = false;
	let ruleSaveError = '';

	async function loadRules() {
		rulesError = '';
		try {
			const res = await api.get<{ items: AvailabilityRule[] }>('/v1/availability-rules');
			rules = (res.items ?? []).sort((a, b) => a.day_of_week - b.day_of_week);
		} catch (e: any) {
			rulesError = e.message;
		} finally {
			rulesLoading = false;
		}
	}

	function startEditRule(r: AvailabilityRule) {
		editingRuleId = r.id;
		editRuleForm = { day_of_week: r.day_of_week, start_time: r.start_time, end_time: r.end_time };
		ruleSaveError = '';
	}

	function cancelEditRule() {
		editingRuleId = null;
		ruleSaveError = '';
	}

	async function saveRule(id: string) {
		ruleSaveError = '';
		if (editRuleForm.start_time >= editRuleForm.end_time) {
			ruleSaveError = 'End time must be after start time.';
			return;
		}
		savingRule = true;
		try {
			await api.patch(`/v1/availability-rules/${id}`, {
				day_of_week: Number(editRuleForm.day_of_week),
				start_time: editRuleForm.start_time,
				end_time: editRuleForm.end_time
			});
			editingRuleId = null;
			await loadRules();
		} catch (e: any) {
			ruleSaveError = e.message;
		} finally {
			savingRule = false;
		}
	}

	async function addRule() {
		ruleAddError = '';
		if (!ruleForm.start_time || !ruleForm.end_time) {
			ruleAddError = 'Start and end time are required.';
			return;
		}
		if (ruleForm.start_time >= ruleForm.end_time) {
			ruleAddError = 'End time must be after start time.';
			return;
		}
		addingRule = true;
		try {
			await api.post('/v1/availability-rules', {
				day_of_week: Number(ruleForm.day_of_week),
				start_time: ruleForm.start_time,
				end_time: ruleForm.end_time
			});
			await loadRules();
		} catch (e: any) {
			ruleAddError = e.message;
		} finally {
			addingRule = false;
		}
	}

	async function deleteRule(id: string) {
		if (!confirm('Remove this availability rule?')) return;
		try {
			await api.del(`/v1/availability-rules/${id}`);
			await loadRules();
		} catch (e: any) {
			rulesError = e.message;
		}
	}

	// ── Date overrides ──────────────────────────────────────────────────────────
	let overrides: AvailabilityOverride[] = [];
	let overridesLoading = true;
	let overridesError = '';

	let ovForm = { date: '', is_available: false, start_time: '09:00', end_time: '17:00' };
	let addingOv = false;
	let ovAddError = '';

	// Inline edit state for overrides
	let editingOvId: string | null = null;
	let editOvForm = { is_available: false, start_time: '09:00', end_time: '17:00' };
	let savingOv = false;
	let ovSaveError = '';

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
			is_available: ov.is_available,
			start_time: ov.start_time ?? '09:00',
			end_time: ov.end_time ?? '17:00'
		};
		ovSaveError = '';
	}

	function cancelEditOv() {
		editingOvId = null;
		ovSaveError = '';
	}

	async function saveOv(id: string) {
		ovSaveError = '';
		if (editOvForm.is_available && editOvForm.start_time >= editOvForm.end_time) {
			ovSaveError = 'End time must be after start time.';
			return;
		}
		savingOv = true;
		try {
			await api.patch(`/v1/availability-overrides/${id}`, {
				is_available: editOvForm.is_available,
				...(editOvForm.is_available
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
		if (ovForm.is_available && (!ovForm.start_time || !ovForm.end_time)) {
			ovAddError = 'Start and end time are required for custom hours.';
			return;
		}
		if (ovForm.is_available && ovForm.start_time >= ovForm.end_time) {
			ovAddError = 'End time must be after start time.';
			return;
		}
		addingOv = true;
		try {
			await api.post('/v1/availability-overrides', {
				date: ovForm.date,
				is_available: ovForm.is_available,
				...(ovForm.is_available
					? { start_time: ovForm.start_time, end_time: ovForm.end_time }
					: {})
			});
			ovForm = { date: '', is_available: false, start_time: '09:00', end_time: '17:00' };
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
</script>

<svelte:head><title>Availability — Calnode</title></svelte:head>

<!-- ── Weekly Hours ─────────────────────────────────────────────────────────── -->
<div class="page-header">
	<h1>Availability</h1>
</div>

<div class="section-label">Weekly Hours</div>
<div class="card" style="margin-bottom:24px;">
	{#if rulesError}<div class="error-msg">{rulesError}</div>{/if}

	{#if rulesLoading}
		<div style="color:var(--text-muted);">Loading…</div>
	{:else if rules.length === 0}
		<div style="color:var(--text-muted);padding:8px 0 16px;">No weekly rules yet. Add one below.</div>
	{:else}
		<table style="margin-bottom:20px;">
			<thead>
				<tr>
					<th>Day</th>
					<th>Hours</th>
					<th></th>
				</tr>
			</thead>
			<tbody>
				{#each rules as r}
					{#if editingRuleId === r.id}
						<tr class="editing-row">
							<td>
								<select bind:value={editRuleForm.day_of_week} class="inline-input">
									{#each DAY_NAMES as name, i}
										<option value={i}>{name}</option>
									{/each}
								</select>
							</td>
							<td>
								<div class="inline-time-fields">
									<select class="inline-input" bind:value={editRuleForm.start_time}>
										{#each TIME_SLOTS as t}<option value={t}>{fmtTime(t)}</option>{/each}
									</select>
									<span style="color:var(--text-muted);">–</span>
									<select class="inline-input" bind:value={editRuleForm.end_time}>
										{#each TIME_SLOTS as t}<option value={t}>{fmtTime(t)}</option>{/each}
									</select>
								</div>
								{#if ruleSaveError}<div class="error-msg" style="margin-top:4px;">{ruleSaveError}</div>{/if}
							</td>
							<td style="text-align:right;white-space:nowrap;">
								<button class="btn-primary btn-sm" on:click={() => saveRule(r.id)} disabled={savingRule}>
									{savingRule ? 'Saving…' : 'Save'}
								</button>
								<button class="btn-secondary btn-sm" on:click={cancelEditRule} style="margin-left:4px;">Cancel</button>
							</td>
						</tr>
					{:else}
						<tr>
							<td style="font-weight:500;">{DAY_NAMES[r.day_of_week]}</td>
							<td class="mono">{r.start_time} – {r.end_time}</td>
							<td style="text-align:right;white-space:nowrap;">
								<button class="btn-secondary btn-sm" on:click={() => startEditRule(r)}>Edit</button>
								<button class="btn-danger btn-sm" on:click={() => deleteRule(r.id)} style="margin-left:4px;">Remove</button>
							</td>
						</tr>
					{/if}
				{/each}
			</tbody>
		</table>
	{/if}

	<div class="add-rule-form">
		{#if ruleAddError}<div class="error-msg">{ruleAddError}</div>{/if}
		<div class="inline-fields">
			<div class="field">
				<label for="rule-day">Day</label>
				<select id="rule-day" bind:value={ruleForm.day_of_week}>
					{#each DAY_NAMES as name, i}
						<option value={i}>{name}</option>
					{/each}
				</select>
			</div>
			<div class="field">
				<label for="rule-start">From</label>
				<select id="rule-start" bind:value={ruleForm.start_time}>
					{#each TIME_SLOTS as t}<option value={t}>{fmtTime(t)}</option>{/each}
				</select>
			</div>
			<div class="field">
				<label for="rule-end">To</label>
				<select id="rule-end" bind:value={ruleForm.end_time}>
					{#each TIME_SLOTS as t}<option value={t}>{fmtTime(t)}</option>{/each}
				</select>
			</div>
			<div class="field-btn">
				<button class="btn-primary" on:click={addRule} disabled={addingRule}>
					{addingRule ? 'Adding…' : 'Add rule'}
				</button>
			</div>
		</div>
	</div>
</div>

<!-- ── Date Overrides ───────────────────────────────────────────────────────── -->
<div class="section-label">Date Overrides</div>
<p class="section-hint">Block out a specific date, or set custom hours for it.</p>

<div class="card">
	{#if overridesError}<div class="error-msg">{overridesError}</div>{/if}

	{#if overridesLoading}
		<div style="color:var(--text-muted);">Loading…</div>
	{:else if overrides.length === 0}
		<div style="color:var(--text-muted);padding:8px 0 16px;">No overrides yet.</div>
	{:else}
		<table style="margin-bottom:20px;">
			<thead>
				<tr>
					<th>Date</th>
					<th>Type</th>
					<th>Hours</th>
					<th></th>
				</tr>
			</thead>
			<tbody>
				{#each overrides as ov}
					{#if editingOvId === ov.id}
						<tr class="editing-row">
							<td style="font-weight:500;">{ov.date}</td>
							<td>
								<select bind:value={editOvForm.is_available} class="inline-input">
									<option value={false}>Day off</option>
									<option value={true}>Custom hours</option>
								</select>
							</td>
							<td>
								{#if editOvForm.is_available}
									<div class="inline-time-fields">
										<select class="inline-input" bind:value={editOvForm.start_time}>
											{#each TIME_SLOTS as t}<option value={t}>{fmtTime(t)}</option>{/each}
										</select>
										<span style="color:var(--text-muted);">–</span>
										<select class="inline-input" bind:value={editOvForm.end_time}>
											{#each TIME_SLOTS as t}<option value={t}>{fmtTime(t)}</option>{/each}
										</select>
									</div>
								{:else}
									<span style="color:var(--text-muted);">—</span>
								{/if}
								{#if ovSaveError}<div class="error-msg" style="margin-top:4px;">{ovSaveError}</div>{/if}
							</td>
							<td style="text-align:right;white-space:nowrap;">
								<button class="btn-primary btn-sm" on:click={() => saveOv(ov.id)} disabled={savingOv}>
									{savingOv ? 'Saving…' : 'Save'}
								</button>
								<button class="btn-secondary btn-sm" on:click={cancelEditOv} style="margin-left:4px;">Cancel</button>
							</td>
						</tr>
					{:else}
						<tr>
							<td style="font-weight:500;">{ov.date}</td>
							<td>
								{#if ov.is_available}
									<span class="badge badge-green">Custom hours</span>
								{:else}
									<span class="badge badge-gray">Day off</span>
								{/if}
							</td>
							<td class="mono">
								{ov.is_available && ov.start_time && ov.end_time
									? `${ov.start_time} – ${ov.end_time}`
									: '—'}
							</td>
							<td style="text-align:right;white-space:nowrap;">
								<button class="btn-secondary btn-sm" on:click={() => startEditOv(ov)}>Edit</button>
								<button class="btn-danger btn-sm" on:click={() => deleteOverride(ov.id)} style="margin-left:4px;">Remove</button>
							</td>
						</tr>
					{/if}
				{/each}
			</tbody>
		</table>
	{/if}

	<div class="add-rule-form">
		{#if ovAddError}<div class="error-msg">{ovAddError}</div>{/if}
		<div class="inline-fields">
			<div class="field">
				<label for="ov-date">Date</label>
				<input id="ov-date" type="date" bind:value={ovForm.date} />
			</div>
			<div class="field">
				<label for="ov-type">Type</label>
				<select id="ov-type" bind:value={ovForm.is_available}>
					<option value={false}>Day off</option>
					<option value={true}>Custom hours</option>
				</select>
			</div>
			{#if ovForm.is_available}
				<div class="field">
					<label for="ov-start">From</label>
					<select id="ov-start" bind:value={ovForm.start_time}>
						{#each TIME_SLOTS as t}<option value={t}>{fmtTime(t)}</option>{/each}
					</select>
				</div>
				<div class="field">
					<label for="ov-end">To</label>
					<select id="ov-end" bind:value={ovForm.end_time}>
						{#each TIME_SLOTS as t}<option value={t}>{fmtTime(t)}</option>{/each}
					</select>
				</div>
			{/if}
			<div class="field-btn">
				<button class="btn-primary" on:click={addOverride} disabled={addingOv}>
					{addingOv ? 'Adding…' : 'Add override'}
				</button>
			</div>
		</div>
	</div>
</div>

<style>
	.section-label {
		font-size: 13px;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-muted);
		margin-bottom: 8px;
	}

	.section-hint {
		font-size: 13px;
		color: var(--text-muted);
		margin: -4px 0 10px;
	}

	.add-rule-form {
		border-top: 1px solid var(--border);
		padding-top: 16px;
	}

	.inline-fields {
		display: flex;
		gap: 12px;
		align-items: flex-end;
		flex-wrap: wrap;
	}

	.inline-fields .field {
		margin-bottom: 0;
		min-width: 120px;
		flex: 1;
	}

	.field-btn {
		flex: 0 0 auto;
		min-width: unset;
	}

	.field-btn button {
		white-space: nowrap;
	}

	.editing-row {
		background: var(--surface);
	}

	.inline-input {
		padding: 4px 8px;
		font-size: 13px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--bg);
		color: inherit;
		width: 100%;
	}

	.inline-time-fields {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.inline-time-fields .inline-input {
		width: auto;
		flex: 1;
	}
</style>
