<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Question } from '$lib/api';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Textarea } from '$lib/components/ui/textarea';
	import * as Select from '$lib/components/ui/select';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { toast } from 'svelte-sonner';

	let { slug }: { slug: string } = $props();

	const QUESTION_TYPES = [
		{ value: 'text', label: 'Text' },
		{ value: 'checkbox', label: 'Checkbox (yes/no)' },
		{ value: 'select', label: 'Dropdown' },
	];

	let questions: Question[] = $state([]);
	let qLoading = $state(true);

	let qForm = $state({ label: '', type: 'text' as 'text'|'select'|'checkbox', options: '', required: false });
	let qAdding = $state(false);

	let editingQId = $state<string | null>(null);
	let deleteQOpen = $state(false);
	let pendingQ = $state<Question | null>(null);
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

	// This panel is only mounted once the parent's "Questions" tab is selected
	// ({#if activeTab === 'questions'}), so it must fetch its own data on mount
	// rather than relying on the parent to trigger a load — previously nothing
	// called loadQuestions() until a mutation (add/save/delete) did so as a
	// side effect, which is why existing questions never showed up until you
	// added a new one.
	onMount(loadQuestions);

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

	function deleteQuestion(q: Question) {
		pendingQ = q;
		deleteQOpen = true;
	}

	async function doDeleteQuestion() {
		if (!pendingQ) return;
		try {
			await api.del(`/v1/event-types/${slug}/questions/${pendingQ.id}`);
			await loadQuestions();
		} catch (e: any) {
			toast.error(e.message || 'Could not delete question');
		}
	}
</script>

<ConfirmDialog
	bind:open={deleteQOpen}
	title="Remove question?"
	description={pendingQ ? `"${pendingQ.label}" will be removed from this event type's booking form.` : ''}
	confirmText="Remove"
	destructive
	onConfirm={doDeleteQuestion}
/>

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
											<Select.Root type="single" value={editQForm.type} onValueChange={(v) => { if (v) editQForm.type = v as 'text'|'select'|'checkbox'; }}>
												<Select.Trigger id="eq-type-{q.id}" class="w-full">
													{QUESTION_TYPES.find((t) => t.value === editQForm.type)?.label ?? 'Select…'}
												</Select.Trigger>
												<Select.Content>
													{#each QUESTION_TYPES as t}
														<Select.Item value={t.value} label={t.label}>{t.label}</Select.Item>
													{/each}
												</Select.Content>
											</Select.Root>
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
					<Select.Root type="single" value={qForm.type} onValueChange={(v) => { if (v) qForm.type = v as 'text'|'select'|'checkbox'; }}>
						<Select.Trigger id="q-type" class="w-full">
							{QUESTION_TYPES.find((t) => t.value === qForm.type)?.label ?? 'Select…'}
						</Select.Trigger>
						<Select.Content>
							{#each QUESTION_TYPES as t}
								<Select.Item value={t.value} label={t.label}>{t.label}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
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
