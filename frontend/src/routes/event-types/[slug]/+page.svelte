<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { base } from '$app/paths';
	import { api, type EventType, type Question } from '$lib/api';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Switch } from '$lib/components/ui/switch';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { toast } from 'svelte-sonner';

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
		link: 'Meeting URL', zoom: 'Zoom link', google_meet: 'Meet link',
		teams: 'Teams link', phone: 'Phone number', in_person: 'Address', custom_video: 'Meeting URL',
	};

	// ── Event type ───────────────────────────────────────────────────────────────
	let et: EventType | null = $state(null);
	let etLoading = $state(true);
	let etError = $state('');
	let etSaving = $state(false);

	let form = $state({
		name: '', description: '', duration_minutes: 30,
		is_active: true, is_public: true,
		location_type: 'link', location_value: '',
		buffer_before_minutes: 0, buffer_after_minutes: 0,
		min_notice_minutes: 0, max_future_days: 60,
	});

	// Notification / messaging state
	const REMINDER_OPTIONS = [
		{ value: 1, label: '1 hour before' },
		{ value: 2, label: '2 hours before' },
		{ value: 4, label: '4 hours before' },
		{ value: 8, label: '8 hours before' },
		{ value: 12, label: '12 hours before' },
		{ value: 24, label: '24 hours before' },
		{ value: 48, label: '2 days before' },
		{ value: 72, label: '3 days before' },
		{ value: 168, label: '1 week before' },
	];
	let reminders = $state<number[]>([]);
	let msg_confirmation = $state('');
	let msg_cancellation = $state('');
	let msg_reschedule = $state('');
	let msg_reminder = $state('');
	// Track which message accordions are open
	let msgOpen = $state({ confirmation: false, cancellation: false, reschedule: false, reminder: false });
	// Track preview toggle per accordion
	let previewOpen = $state({ confirmation: false, cancellation: false, reschedule: false, reminder: false });
	// Test email send state per type
	type MsgKey = 'confirmation' | 'cancellation' | 'reschedule' | 'reminder';
	let testSending = $state<Partial<Record<MsgKey, boolean>>>({});
	let testSent    = $state<Partial<Record<MsgKey, boolean>>>({});
	let testError   = $state<Partial<Record<MsgKey, string>>>({});

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
			reminders = et.reminders ?? [];
			msg_confirmation = et.msg_confirmation ?? '';
			msg_cancellation = et.msg_cancellation ?? '';
			msg_reschedule = et.msg_reschedule ?? '';
			msg_reminder = et.msg_reminder ?? '';
		} catch (e: any) {
			etError = e.message;
		} finally {
			etLoading = false;
		}
	}

	async function saveET() {
		if (!form.name.trim()) { toast.error('Name is required.'); return; }
		if (form.duration_minutes < 5) { toast.error('Duration must be at least 5 minutes.'); return; }
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
				reminders,
				msg_confirmation: msg_confirmation.trim() || null,
				msg_cancellation: msg_cancellation.trim() || null,
				msg_reschedule: msg_reschedule.trim() || null,
				msg_reminder: msg_reminder.trim() || null,
			});
			toast.success('Changes saved');
			await loadET();
		} catch (e: any) {
			toast.error(e.message || 'Could not save changes');
		} finally {
			etSaving = false;
		}
	}

	async function sendTestEmail(type: MsgKey) {
		testSending = { ...testSending, [type]: true };
		testSent    = { ...testSent,    [type]: false };
		testError   = { ...testError,   [type]: '' };
		try {
			await api.post(`/v1/event-types/${slug}/test-email`, { type });
			testSent = { ...testSent, [type]: true };
			setTimeout(() => { testSent = { ...testSent, [type]: false }; }, 4000);
		} catch (e: any) {
			testError = { ...testError, [type]: e.message };
		} finally {
			testSending = { ...testSending, [type]: false };
		}
	}

	function buildPreview(type: MsgKey, note: string): string {
		const name     = et?.name ?? 'My Event';
		const loc      = et?.location_value ? `\nLocation: ${et.location_value}` : '';
		const dur      = et?.duration_minutes ?? 30;
		const noteBlk  = note.trim() ? `\n---\n${note.trim()}\n` : '';
		const start    = 'Tomorrow, 2:00 PM UTC';
		const prev     = 'Today, 2:00 PM UTC';
		// Compute end time correctly by adding duration to 14:00.
		const endTotalMin = 14 * 60 + dur;
		const endH24      = Math.floor(endTotalMin / 60) % 24;
		const endMin      = endTotalMin % 60;
		const endPeriod   = endH24 < 12 ? 'AM' : 'PM';
		const endH12      = endH24 % 12 || 12;
		const end         = `Tomorrow, ${endH12}:${String(endMin).padStart(2, '0')} ${endPeriod} UTC`;

		switch (type) {
			case 'confirmation':
				return `Hi Alex Johnson,\n\nYour booking has been confirmed.\n\nEvent:    ${name}\nWith:     ${et?.name ?? 'Host'}\nStart:    ${start}\nEnd:      ${end}${loc}\n\nBooking reference: preview-test\n\nTo cancel, visit:\n[booking page]${noteBlk}\n— Calnode`;
			case 'cancellation':
				return `Hi Alex Johnson,\n\nYour booking has been cancelled.\n\nEvent:    ${name}\nWith:     ${et?.name ?? 'Host'}\nStart:    ${start}\nEnd:      ${end}\n\nTo rebook, visit:\n[booking page]${noteBlk}\n— Calnode`;
			case 'reschedule':
				return `Hi Alex Johnson,\n\nYour booking has been rescheduled.\n\nEvent:    ${name}\nWith:     ${et?.name ?? 'Host'}\nWas:      ${prev}\nNow:      ${start}\nEnd:      ${end}${loc}\n\nBooking reference: preview-test${noteBlk}\n— Calnode`;
			case 'reminder':
				return `Hi Alex Johnson,\n\nThis is a reminder that your booking is coming up.\n\nEvent:    ${name}\nWith:     ${et?.name ?? 'Host'}\nStart:    ${start}\nEnd:      ${end}${loc}\n\nBooking reference: preview-test${noteBlk}\n— Calnode`;
		}
	}

	// ── Intake questions ──────────────────────────────────────────────────────────
	let questions: Question[] = $state([]);
	let qLoading = $state(true);

	let qForm = $state({ label: '', type: 'text' as 'text'|'select'|'checkbox', options: '', required: false });
	let qAdding = $state(false);

	let editingQId: string | null = $state(null);
	let editQForm = $state({ label: '', type: 'text' as 'text'|'select'|'checkbox', options: '', required: false });
	let qSaving = $state(false);

	async function loadQuestions() {
		try {
			const res = await api.get<{ items: Question[] }>(`/v1/event-types/${slug}/questions/admin`);
			questions = (res.items ?? []).sort((a, b) => a.position - b.position);
		} catch (e: any) {
			toast.error(e.message || 'Could not load questions');
		} finally {
			qLoading = false;
		}
	}

	function optionsArray(raw: string): string[] {
		return raw.split('\n').map(s => s.trim()).filter(Boolean);
	}

	async function addQuestion() {
		if (!qForm.label.trim()) { toast.error('Label is required.'); return; }
		if (qForm.type === 'select' && optionsArray(qForm.options).length === 0) {
			toast.error('At least one option is required for dropdown questions.'); return;
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
			toast.error(e.message || 'Could not add question');
		} finally {
			qAdding = false;
		}
	}

	function startEditQ(q: Question) {
		editingQId = q.id;
		editQForm = { label: q.label, type: q.type, options: (q.options ?? []).join('\n'), required: q.required };
	}

	function cancelEditQ() { editingQId = null; }

	async function saveQuestion(q: Question) {
		if (!editQForm.label.trim()) { toast.error('Label is required.'); return; }
		if (editQForm.type === 'select' && optionsArray(editQForm.options).length === 0) {
			toast.error('At least one option is required.'); return;
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
			toast.error(e.message || 'Could not save question');
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
			toast.error(e.message || 'Could not delete question');
		}
	}

	onMount(() => {
		loadET();
		loadQuestions();
	});

	const selectCls = 'flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring';
</script>

<svelte:head><title>{et?.name ?? slug} — Event Type — Calnode</title></svelte:head>

<div class="mb-8">
	<a href="{base}/event-types" class="mb-2 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
		<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"/></svg>
		Event Types
	</a>
	<h1 class="text-2xl font-semibold tracking-tight">{et?.name ?? slug}</h1>
</div>

{#if etLoading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else if etError}
	<p class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{etError}</p>
{:else}

<!-- General Settings -->
<div class="mb-8">
	<h2 class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground">General</h2>
	<div class="rounded-lg border bg-card p-6">
		<div class="grid grid-cols-2 gap-4">
			<div class="space-y-1.5">
				<Label for="et-name">Name</Label>
				<Input id="et-name" bind:value={form.name} />
			</div>
			<div class="space-y-1.5">
				<Label for="et-dur">Duration (minutes)</Label>
				<Input id="et-dur" type="number" min="5" step="5" bind:value={form.duration_minutes} />
			</div>
			<div class="col-span-2 space-y-1.5">
				<Label for="et-desc">Description</Label>
				<Textarea id="et-desc" bind:value={form.description} placeholder="Optional — supports **bold** and *italic* markdown" rows={3} class="resize-y" />
			</div>
			<div class="space-y-1.5">
				<p class="text-sm font-medium">Status</p>
				<div class="flex items-center gap-2">
					<Checkbox id="is-active" bind:checked={form.is_active} />
					<Label for="is-active" class="cursor-pointer font-normal">Active (accepting bookings)</Label>
				</div>
			</div>
			<div class="space-y-1.5">
				<p class="text-sm font-medium">Visibility</p>
				<div class="flex items-center gap-2">
					<Checkbox id="is-public" bind:checked={form.is_public} />
					<Label for="is-public" class="cursor-pointer font-normal">Public (visible in booking page)</Label>
				</div>
			</div>
		</div>

		<!-- Location -->
		<div class="mt-6 border-t pt-5">
			<p class="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Location</p>
			<div class="grid grid-cols-2 gap-4">
				<div class="space-y-1.5">
					<Label for="et-loc">Type</Label>
					<select id="et-loc" bind:value={form.location_type} class={selectCls}>
						{#each LOCATION_TYPES as lt}
							<option value={lt.value}>{lt.label}</option>
						{/each}
					</select>
				</div>
				<div class="space-y-1.5">
					<Label for="et-loc-val">{LOCATION_NEEDS_VALUE[form.location_type] ?? 'Details'}</Label>
					<Input id="et-loc-val" bind:value={form.location_value} placeholder="Optional" />
				</div>
			</div>
		</div>

		<!-- Scheduling -->
		<div class="mt-6 border-t pt-5">
			<p class="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Scheduling</p>
			<div class="grid grid-cols-2 gap-4">
				<div class="space-y-1.5">
					<Label for="et-buf-before">Buffer before (min)</Label>
					<Input id="et-buf-before" type="number" min="0" step="5" bind:value={form.buffer_before_minutes} />
					<p class="text-xs text-muted-foreground">Blocked time before each meeting</p>
				</div>
				<div class="space-y-1.5">
					<Label for="et-buf-after">Buffer after (min)</Label>
					<Input id="et-buf-after" type="number" min="0" step="5" bind:value={form.buffer_after_minutes} />
					<p class="text-xs text-muted-foreground">Blocked time after each meeting</p>
				</div>
				<div class="space-y-1.5">
					<Label for="et-notice">Minimum notice (min)</Label>
					<Input id="et-notice" type="number" min="0" step="30" bind:value={form.min_notice_minutes} />
					<p class="text-xs text-muted-foreground">e.g. 60 = bookings must be 1h+ in future</p>
				</div>
				<div class="space-y-1.5">
					<Label for="et-future">Booking window (days)</Label>
					<Input id="et-future" type="number" min="0" bind:value={form.max_future_days} />
					<p class="text-xs text-muted-foreground">How far ahead people can book. 0 = unlimited</p>
				</div>
			</div>
		</div>

		<Button onclick={saveET} disabled={etSaving} class="mt-6">
			{etSaving ? 'Saving…' : 'Save changes'}
		</Button>
	</div>
</div>

<!-- Notifications -->
<div class="mb-8">
	<h2 class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground">Notifications</h2>
	<div class="rounded-lg border bg-card p-6 space-y-6">

		<!-- Reminders -->
		<div>
			<p class="mb-1 text-sm font-medium">Reminders</p>
			<p class="mb-3 text-xs text-muted-foreground">Send attendees a reminder email before the meeting. Defaults to 24 hours before if none are set.</p>
			<div class="space-y-2">
				{#each reminders as hb, i}
					<div class="flex items-center gap-2">
						<select
							value={hb}
							onchange={(e) => {
								const v = Number((e.target as HTMLSelectElement).value);
								reminders = reminders.map((r, idx) => idx === i ? v : r);
							}}
							class="flex h-9 rounded-md border border-input bg-background px-3 py-1 text-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
						>
							{#each REMINDER_OPTIONS as opt}
								<option value={opt.value}>{opt.label}</option>
							{/each}
						</select>
						<Button
							type="button"
							variant="ghost"
							size="icon"
							onclick={() => { reminders = reminders.filter((_, idx) => idx !== i); }}
						>
							<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
						</Button>
					</div>
				{/each}
				{#if reminders.length < 5}
					<Button
						type="button"
						variant="outline"
						size="sm"
						onclick={() => {
							const used = new Set(reminders);
							const next = REMINDER_OPTIONS.find(o => !used.has(o.value));
							if (next) reminders = [...reminders, next.value];
						}}
					>
						+ Add reminder
					</Button>
				{/if}
			</div>
		</div>

		<!-- Custom messages -->
		<div class="border-t pt-5">
			<p class="mb-1 text-sm font-medium">Custom messages</p>
			<p class="mb-3 text-xs text-muted-foreground">Add an optional note appended to each email type.</p>
			<div class="space-y-2">
				{#each [
					{ key: 'confirmation' as const, label: 'Booking confirmation' },
					{ key: 'cancellation' as const, label: 'Cancellation notice' },
					{ key: 'reschedule' as const, label: 'Reschedule notice' },
					{ key: 'reminder' as const, label: 'Reminder email' },
				] as item}
					<div class="rounded-md border">
						<button
							type="button"
							class="flex w-full items-center justify-between px-4 py-3 text-sm font-medium hover:bg-muted/30 transition-colors"
							onclick={() => { msgOpen[item.key] = !msgOpen[item.key]; }}
						>
							<span>{item.label}</span>
							<svg
								xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24"
								fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
								class="transition-transform {msgOpen[item.key] ? 'rotate-180' : ''}"
							><polyline points="6 9 12 15 18 9"/></svg>
						</button>
						{#if msgOpen[item.key]}
							{@const note = item.key === 'confirmation' ? msg_confirmation
								: item.key === 'cancellation' ? msg_cancellation
								: item.key === 'reschedule'   ? msg_reschedule
								: msg_reminder}
							<div class="border-t px-4 pb-4 pt-3 space-y-3">
								{#if item.key === 'confirmation'}
									<Textarea bind:value={msg_confirmation} rows={3} placeholder="Add a custom note for attendees…" />
								{:else if item.key === 'cancellation'}
									<Textarea bind:value={msg_cancellation} rows={3} placeholder="Add a custom note for attendees…" />
								{:else if item.key === 'reschedule'}
									<Textarea bind:value={msg_reschedule} rows={3} placeholder="Add a custom note for attendees…" />
								{:else if item.key === 'reminder'}
									<Textarea bind:value={msg_reminder} rows={3} placeholder="Add a custom note for attendees…" />
								{/if}

								<!-- Preview toggle -->
								<button
									type="button"
									class="text-xs text-muted-foreground hover:text-foreground underline-offset-2 hover:underline"
									onclick={() => { previewOpen[item.key] = !previewOpen[item.key]; }}
								>{previewOpen[item.key] ? 'Hide preview' : 'Show email preview'}</button>

								{#if previewOpen[item.key]}
									<pre class="rounded-md border bg-muted/30 px-4 py-3 text-xs leading-relaxed whitespace-pre-wrap font-mono text-muted-foreground overflow-auto max-h-64">{buildPreview(item.key, note)}</pre>
								{/if}

								<!-- Send test button -->
								<div class="flex items-center gap-3">
									<Button
										type="button"
										variant="outline"
										size="sm"
										disabled={testSending[item.key]}
										onclick={() => sendTestEmail(item.key)}
									>
										{testSending[item.key] ? 'Sending…' : 'Send test email'}
									</Button>
									{#if testSent[item.key]}
										<span class="text-xs text-green-600">Test email sent to your inbox.</span>
									{/if}
									{#if testError[item.key]}
										<span class="text-xs text-destructive">
											{#if testError[item.key] === 'Email is not configured on this server — add SMTP settings to enable sending'}
												SMTP is not configured — <a href="{base}/settings/email" class="underline">configure it in Settings</a>.
											{:else}
												{testError[item.key]}
											{/if}
										</span>
									{/if}
								</div>
							</div>
						{/if}
					</div>
				{/each}
			</div>
		</div>

		<Button onclick={saveET} disabled={etSaving}>
			{etSaving ? 'Saving…' : 'Save changes'}
		</Button>
	</div>
</div>

<!-- Intake Questions -->
<div>
	<h2 class="mb-1 text-sm font-semibold uppercase tracking-wider text-muted-foreground">Intake Questions</h2>
	<p class="mb-3 text-sm text-muted-foreground">Collect information from attendees when they book.</p>

	<div class="rounded-lg border bg-card">
		{#if qLoading}
			<p class="px-4 py-4 text-sm text-muted-foreground">Loading…</p>
		{:else if questions.length > 0}
			<table class="w-full text-sm">
				<thead>
					<tr class="border-b">
						<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">#</th>
						<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Question</th>
						<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Type</th>
						<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Required</th>
						<th class="px-4 pb-3 pt-3"></th>
					</tr>
				</thead>
				<tbody class="divide-y">
					{#each questions as q}
						{#if editingQId === q.id}
							<tr class="bg-muted/20">
								<td class="px-4 py-3 text-muted-foreground">{q.position + 1}</td>
								<td colspan="3" class="px-4 py-3">
									<div class="grid grid-cols-2 gap-3">
										<div class="space-y-1.5">
											<Label for="eq-label-{q.id}" class="text-xs text-muted-foreground">Label</Label>
											<Input id="eq-label-{q.id}" bind:value={editQForm.label} />
										</div>
										<div class="space-y-1.5">
											<Label for="eq-type-{q.id}" class="text-xs text-muted-foreground">Type</Label>
											<select id="eq-type-{q.id}" bind:value={editQForm.type} class={selectCls}>
												<option value="text">Text</option>
												<option value="checkbox">Checkbox (yes/no)</option>
												<option value="select">Dropdown</option>
											</select>
										</div>
										{#if editQForm.type === 'select'}
											<div class="col-span-2 space-y-1.5">
												<Label for="eq-options-{q.id}" class="text-xs text-muted-foreground">Options (one per line)</Label>
												<Textarea id="eq-options-{q.id}" bind:value={editQForm.options} rows={3} />
											</div>
										{/if}
										<div class="col-span-2 flex items-center gap-2">
											<Checkbox id="eq-required-{q.id}" bind:checked={editQForm.required} />
											<Label for="eq-required-{q.id}" class="cursor-pointer font-normal">Required</Label>
										</div>
									</div>
								</td>
								<td class="px-4 py-3 align-top">
									<div class="flex items-center justify-end gap-2 pt-5">
										<Button size="sm" onclick={() => saveQuestion(q)} disabled={qSaving}>
											{qSaving ? 'Saving…' : 'Save'}
										</Button>
										<Button size="sm" variant="outline" onclick={cancelEditQ}>Cancel</Button>
									</div>
								</td>
							</tr>
						{:else}
							<tr class="transition-colors hover:bg-muted/30">
								<td class="px-4 py-3 text-muted-foreground">{q.position + 1}</td>
								<td class="px-4 py-3">
									<div class="font-medium">{q.label}</div>
									{#if q.type === 'select' && q.options?.length}
										<div class="mt-0.5 text-xs text-muted-foreground">{q.options.join(', ')}</div>
									{/if}
								</td>
								<td class="px-4 py-3">
									<span class="inline-flex items-center rounded-md bg-secondary px-2 py-0.5 text-xs font-medium text-secondary-foreground">
										{q.type}
									</span>
								</td>
								<td class="px-4 py-3 text-muted-foreground">{q.required ? '✓' : '—'}</td>
								<td class="px-4 py-3">
									<Tooltip.Provider>
										<div class="flex items-center justify-end gap-1">
											<Tooltip.Root>
												<Tooltip.Trigger
													class={buttonVariants({ variant: 'ghost', size: 'icon' })}
													onclick={() => startEditQ(q)}
												>
													<!-- Pencil/edit icon -->
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
												</Tooltip.Trigger>
												<Tooltip.Content>Edit</Tooltip.Content>
											</Tooltip.Root>
											<Tooltip.Root>
												<Tooltip.Trigger
													class={buttonVariants({ variant: 'ghost', size: 'icon' })}
													onclick={() => deleteQuestion(q)}
												>
													<!-- Trash icon -->
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
												</Tooltip.Trigger>
												<Tooltip.Content>Remove</Tooltip.Content>
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
			<p class="px-4 py-4 text-sm text-muted-foreground">No questions yet.</p>
		{/if}

		<!-- Add question form -->
		<div class="border-t px-4 py-4">
			<div class="grid grid-cols-2 gap-3">
				<div class="space-y-1.5">
					<Label for="q-label">Label</Label>
					<Input id="q-label" bind:value={qForm.label} placeholder="e.g. What's the meeting about?" />
				</div>
				<div class="space-y-1.5">
					<Label for="q-type">Type</Label>
					<select id="q-type" bind:value={qForm.type} class={selectCls}>
						<option value="text">Text</option>
						<option value="checkbox">Checkbox (yes/no)</option>
						<option value="select">Dropdown</option>
					</select>
				</div>
				{#if qForm.type === 'select'}
					<div class="col-span-2 space-y-1.5">
						<Label for="q-options">Options (one per line)</Label>
						<Textarea id="q-options" bind:value={qForm.options} rows={3} placeholder={"Option A\nOption B\nOption C"} />
					</div>
				{/if}
				<div class="col-span-2 flex items-center gap-2">
					<Checkbox id="q-required" bind:checked={qForm.required} />
					<Label for="q-required" class="cursor-pointer font-normal">Required</Label>
				</div>
			</div>
			<Button onclick={addQuestion} disabled={qAdding} class="mt-3">
				{qAdding ? 'Adding…' : 'Add question'}
			</Button>
		</div>
	</div>
</div>

{/if}
