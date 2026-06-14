<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { base } from '$app/paths';
	import { api, type EventType, type Question } from '$lib/api';

	const LOCATION_TYPES = [
		{ value: 'link',         label: 'Video link (custom)' },
		{ value: 'zoom',         label: 'Zoom' },
		{ value: 'google_meet',  label: 'Google Meet' },
		{ value: 'teams',        label: 'Microsoft Teams' },
		{ value: 'phone',        label: 'Phone call' },
		{ value: 'in_person',    label: 'In person' },
		{ value: 'custom_video', label: 'Other video' },
	];

	const LOCATION_NEEDS_VALUE: Record<string, string> = {
		link: 'Meeting URL',
		zoom: 'Zoom link',
		google_meet: 'Meet link',
		teams: 'Teams link',
		phone: 'Phone number',
		in_person: 'Address',
		custom_video: 'Meeting URL',
	};

	// ── Event type ───────────────────────────────────────────────────────────────
	let et: EventType | null = null;
	let etLoading = true;
	let etError = '';
	let etSaving = false;
	let etSaveError = '';
	let etSaved = false;

	let form = {
		name: '', description: '', duration_minutes: 30,
		is_active: true, is_public: true,
		location_type: 'link', location_value: '',
		buffer_before_minutes: 0, buffer_after_minutes: 0,
		min_notice_minutes: 0, max_future_days: 60,
	};

	const slug = $page.params.slug;

	async function loadET() {
		etError = '';
		try {
			et = await api.get<EventType>(`/v1/event-types/${slug}`);
			form = {
				name: et.name,
				description: et.description ?? '',
				duration_minutes: et.duration_minutes,
				is_active: et.is_active,
				is_public: et.is_public,
				location_type: et.location_type,
				location_value: et.location_value ?? '',
				buffer_before_minutes: et.buffer_before_minutes,
				buffer_after_minutes: et.buffer_after_minutes,
				min_notice_minutes: et.min_notice_minutes,
				max_future_days: et.max_future_days,
			};
		} catch (e: any) {
			etError = e.message;
		} finally {
			etLoading = false;
		}
	}

	async function saveET() {
		etSaveError = '';
		etSaved = false;
		if (!form.name.trim()) { etSaveError = 'Name is required.'; return; }
		if (form.duration_minutes < 5) { etSaveError = 'Duration must be at least 5 minutes.'; return; }
		etSaving = true;
		try {
			await api.patch(`/v1/event-types/${slug}`, {
				name: form.name.trim(),
				description: form.description.trim() || null,
				duration_minutes: Number(form.duration_minutes),
				is_active: form.is_active,
				is_public: form.is_public,
				location_type: form.location_type,
				location_value: form.location_value.trim() || null,
				buffer_before_minutes: Number(form.buffer_before_minutes),
				buffer_after_minutes: Number(form.buffer_after_minutes),
				min_notice_minutes: Number(form.min_notice_minutes),
				max_future_days: Number(form.max_future_days),
			});
			etSaved = true;
			await loadET();
		} catch (e: any) {
			etSaveError = e.message;
		} finally {
			etSaving = false;
		}
	}

	// ── Intake questions ─────────────────────────────────────────────────────────
	let questions: Question[] = [];
	let qLoading = true;
	let qError = '';

	let qForm = { label: '', type: 'text' as 'text'|'select'|'checkbox', options: '', required: false };
	let qAdding = false;
	let qAddError = '';

	let editingQId: string | null = null;
	let editQForm = { label: '', type: 'text' as 'text'|'select'|'checkbox', options: '', required: false };
	let qSaving = false;
	let qSaveError = '';

	async function loadQuestions() {
		qError = '';
		try {
			const res = await api.get<{ items: Question[] }>(`/v1/event-types/${slug}/questions`);
			questions = (res.items ?? []).sort((a, b) => a.position - b.position);
		} catch (e: any) {
			qError = e.message;
		} finally {
			qLoading = false;
		}
	}

	function optionsArray(raw: string): string[] {
		return raw.split('\n').map(s => s.trim()).filter(Boolean);
	}

	async function addQuestion() {
		qAddError = '';
		if (!qForm.label.trim()) { qAddError = 'Label is required.'; return; }
		if (qForm.type === 'select' && optionsArray(qForm.options).length === 0) {
			qAddError = 'At least one option is required for dropdown questions.'; return;
		}
		qAdding = true;
		try {
			await api.post(`/v1/event-types/${slug}/questions`, {
				label: qForm.label.trim(),
				type: qForm.type,
				options: qForm.type === 'select' ? optionsArray(qForm.options) : undefined,
				required: qForm.required,
			});
			qForm = { label: '', type: 'text', options: '', required: false };
			await loadQuestions();
		} catch (e: any) {
			qAddError = e.message;
		} finally {
			qAdding = false;
		}
	}

	function startEditQ(q: Question) {
		editingQId = q.id;
		editQForm = {
			label: q.label,
			type: q.type,
			options: (q.options ?? []).join('\n'),
			required: q.required,
		};
		qSaveError = '';
	}

	function cancelEditQ() { editingQId = null; qSaveError = ''; }

	async function saveQuestion(q: Question) {
		qSaveError = '';
		if (!editQForm.label.trim()) { qSaveError = 'Label is required.'; return; }
		if (editQForm.type === 'select' && optionsArray(editQForm.options).length === 0) {
			qSaveError = 'At least one option is required.'; return;
		}
		qSaving = true;
		try {
			await api.patch(`/v1/event-types/${slug}/questions/${q.id}`, {
				label: editQForm.label.trim(),
				type: editQForm.type,
				options: editQForm.type === 'select' ? optionsArray(editQForm.options) : [],
				required: editQForm.required,
			});
			editingQId = null;
			await loadQuestions();
		} catch (e: any) {
			qSaveError = e.message;
		} finally {
			qSaving = false;
		}
	}

	async function deleteQuestion(q: Question) {
		if (!confirm(`Remove question "${q.label}"?`)) return;
		try {
			await api.del(`/v1/event-types/${slug}/questions/${q.id}`);
			await loadQuestions();
		} catch (e: any) {
			qError = e.message;
		}
	}

	onMount(() => {
		loadET();
		loadQuestions();
	});
</script>

<svelte:head><title>{et?.name ?? slug} — Event Type — Calnode</title></svelte:head>

<div class="page-header">
	<div>
		<a href="{base}/event-types" class="back-link">← Event Types</a>
		<h1>{et?.name ?? slug}</h1>
	</div>
</div>

{#if etLoading}
	<div style="color:var(--text-muted);padding:24px 0;">Loading…</div>
{:else if etError}
	<div class="error-msg">{etError}</div>
{:else}

<!-- ── General settings ──────────────────────────────────────────────────────── -->
<div class="section-label">General</div>
<div class="card" style="margin-bottom:24px;">
	{#if etSaveError}<div class="error-msg">{etSaveError}</div>{/if}
	{#if etSaved}<div class="success-msg">Saved.</div>{/if}

	<div class="settings-grid">
		<div class="field">
			<label for="et-name">Name</label>
			<input id="et-name" bind:value={form.name} />
		</div>
		<div class="field">
			<label for="et-dur">Duration (minutes)</label>
			<input id="et-dur" type="number" min="5" step="5" bind:value={form.duration_minutes} />
		</div>
		<div class="field" style="grid-column:1/-1;">
			<label for="et-desc">Description</label>
			<input id="et-desc" bind:value={form.description} placeholder="Optional" />
		</div>
		<div class="field">
			<label>Status</label>
			<label class="toggle">
				<input type="checkbox" bind:checked={form.is_active} />
				<span>Active (accepting bookings)</span>
			</label>
		</div>
		<div class="field">
			<label>Visibility</label>
			<label class="toggle">
				<input type="checkbox" bind:checked={form.is_public} />
				<span>Public (visible in booking page)</span>
			</label>
		</div>
	</div>

	<!-- Location -->
	<div class="subsection-label">Location</div>
	<div class="settings-grid">
		<div class="field">
			<label for="et-loc">Type</label>
			<select id="et-loc" bind:value={form.location_type}>
				{#each LOCATION_TYPES as lt}
					<option value={lt.value}>{lt.label}</option>
				{/each}
			</select>
		</div>
		<div class="field">
			<label for="et-loc-val">{LOCATION_NEEDS_VALUE[form.location_type] ?? 'Details'}</label>
			<input id="et-loc-val" bind:value={form.location_value} placeholder="Optional" />
		</div>
	</div>

	<!-- Scheduling -->
	<div class="subsection-label">Scheduling</div>
	<div class="settings-grid">
		<div class="field">
			<label for="et-buf-before">Buffer before (min)</label>
			<input id="et-buf-before" type="number" min="0" step="5" bind:value={form.buffer_before_minutes} />
			<div class="field-hint">Blocked time before each meeting</div>
		</div>
		<div class="field">
			<label for="et-buf-after">Buffer after (min)</label>
			<input id="et-buf-after" type="number" min="0" step="5" bind:value={form.buffer_after_minutes} />
			<div class="field-hint">Blocked time after each meeting</div>
		</div>
		<div class="field">
			<label for="et-notice">Minimum notice (min)</label>
			<input id="et-notice" type="number" min="0" step="30" bind:value={form.min_notice_minutes} />
			<div class="field-hint">e.g. 60 = bookings must be 1h+ in future. 0 = no restriction</div>
		</div>
		<div class="field">
			<label for="et-future">Booking window (days)</label>
			<input id="et-future" type="number" min="0" bind:value={form.max_future_days} />
			<div class="field-hint">How far ahead people can book. 0 = unlimited</div>
		</div>
	</div>

	<button class="btn-primary" on:click={saveET} disabled={etSaving} style="margin-top:8px;">
		{etSaving ? 'Saving…' : 'Save changes'}
	</button>
</div>

<!-- ── Intake questions ──────────────────────────────────────────────────────── -->
<div class="section-label">Intake Questions</div>
<p class="section-hint">Collect information from attendees when they book.</p>
<div class="card">
	{#if qError}<div class="error-msg">{qError}</div>{/if}

	{#if qLoading}
		<div style="color:var(--text-muted);">Loading…</div>
	{:else if questions.length === 0}
		<div style="color:var(--text-muted);padding:8px 0 16px;">No questions yet.</div>
	{:else}
		<table style="margin-bottom:20px;">
			<thead>
				<tr><th>#</th><th>Question</th><th>Type</th><th>Required</th><th></th></tr>
			</thead>
			<tbody>
				{#each questions as q}
					{#if editingQId === q.id}
						<tr class="editing-row">
							<td style="color:var(--text-muted);">{q.position + 1}</td>
							<td colspan="3">
								<div class="q-edit-grid">
									<div class="field" style="margin:0;">
										<label>Label</label>
										<input bind:value={editQForm.label} />
									</div>
									<div class="field" style="margin:0;">
										<label>Type</label>
										<select bind:value={editQForm.type}>
											<option value="text">Text</option>
											<option value="checkbox">Checkbox (yes/no)</option>
											<option value="select">Dropdown</option>
										</select>
									</div>
									{#if editQForm.type === 'select'}
										<div class="field" style="margin:0;grid-column:1/-1;">
											<label>Options (one per line)</label>
											<textarea bind:value={editQForm.options} rows="3" style="resize:vertical;"></textarea>
										</div>
									{/if}
									<div class="field" style="margin:0;grid-column:1/-1;">
										<label class="toggle">
											<input type="checkbox" bind:checked={editQForm.required} />
											<span>Required</span>
										</label>
									</div>
								</div>
								{#if qSaveError}<div class="error-msg" style="margin-top:6px;">{qSaveError}</div>{/if}
							</td>
							<td style="text-align:right;white-space:nowrap;vertical-align:top;padding-top:8px;">
								<button class="btn-primary btn-sm" on:click={() => saveQuestion(q)} disabled={qSaving}>
									{qSaving ? 'Saving…' : 'Save'}
								</button>
								<button class="btn-secondary btn-sm" on:click={cancelEditQ} style="margin-left:4px;">Cancel</button>
							</td>
						</tr>
					{:else}
						<tr>
							<td style="color:var(--text-muted);">{q.position + 1}</td>
							<td style="font-weight:500;">{q.label}
								{#if q.type === 'select' && q.options?.length}
									<div style="font-size:11px;color:var(--text-muted);margin-top:2px;">{q.options.join(', ')}</div>
								{/if}
							</td>
							<td><span class="badge badge-gray">{q.type}</span></td>
							<td>{q.required ? '✓' : '—'}</td>
							<td style="text-align:right;white-space:nowrap;">
								<button class="btn-secondary btn-sm" on:click={() => startEditQ(q)}>Edit</button>
								<button class="btn-danger btn-sm" on:click={() => deleteQuestion(q)} style="margin-left:4px;">Remove</button>
							</td>
						</tr>
					{/if}
				{/each}
			</tbody>
		</table>
	{/if}

	<div class="add-rule-form">
		{#if qAddError}<div class="error-msg">{qAddError}</div>{/if}
		<div class="q-edit-grid">
			<div class="field" style="margin:0;">
				<label for="q-label">Label</label>
				<input id="q-label" bind:value={qForm.label} placeholder="e.g. What's the meeting about?" />
			</div>
			<div class="field" style="margin:0;">
				<label for="q-type">Type</label>
				<select id="q-type" bind:value={qForm.type}>
					<option value="text">Text</option>
					<option value="checkbox">Checkbox (yes/no)</option>
					<option value="select">Dropdown</option>
				</select>
			</div>
			{#if qForm.type === 'select'}
				<div class="field" style="margin:0;grid-column:1/-1;">
					<label for="q-options">Options (one per line)</label>
					<textarea id="q-options" bind:value={qForm.options} rows="3" style="resize:vertical;" placeholder="Option A&#10;Option B&#10;Option C"></textarea>
				</div>
			{/if}
			<div class="field" style="margin:0;grid-column:1/-1;">
				<label class="toggle">
					<input type="checkbox" bind:checked={qForm.required} />
					<span>Required</span>
				</label>
			</div>
		</div>
		<button class="btn-primary" on:click={addQuestion} disabled={qAdding} style="margin-top:12px;">
			{qAdding ? 'Adding…' : 'Add question'}
		</button>
	</div>
</div>

{/if}

<style>
	.back-link {
		font-size: 13px;
		color: var(--text-muted);
		text-decoration: none;
		display: block;
		margin-bottom: 4px;
	}
	.back-link:hover { color: var(--accent); }

	.section-label {
		font-size: 13px;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-muted);
		margin-bottom: 8px;
	}

	.subsection-label {
		font-size: 12px;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-muted);
		margin: 20px 0 10px;
		padding-top: 16px;
		border-top: 1px solid var(--border);
	}

	.section-hint {
		font-size: 13px;
		color: var(--text-muted);
		margin: -4px 0 10px;
	}

	.settings-grid {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 12px;
	}

	.field-hint {
		font-size: 11px;
		color: var(--text-muted);
		margin-top: 3px;
	}

	.toggle {
		display: flex;
		align-items: center;
		gap: 8px;
		cursor: pointer;
		font-weight: normal;
		margin-top: 4px;
	}
	.toggle input[type="checkbox"] { width: auto; margin: 0; }

	.add-rule-form {
		border-top: 1px solid var(--border);
		padding-top: 16px;
	}

	.q-edit-grid {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 10px;
	}

	.editing-row { background: var(--surface); }

	.success-msg {
		color: #16a34a;
		background: #f0fdf4;
		border: 1px solid #bbf7d0;
		border-radius: var(--radius);
		padding: 8px 12px;
		font-size: 13px;
		margin-bottom: 12px;
	}
</style>
