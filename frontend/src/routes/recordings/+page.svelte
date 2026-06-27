<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { Input } from '$lib/components/ui/input';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { toast } from 'svelte-sonner';

	type Recording = {
		id: string;
		booking_id: string;
		room: string;
		status: string;
		duration_s: number;
		has_file: boolean;
		created_at: string;
		booker_name: string;
	};

	let loading = $state(true);
	let recordings = $state<Recording[]>([]);
	let query = $state('');

	const filtered = $derived(
		query.trim()
			? recordings.filter((r) => {
					const q = query.toLowerCase();
					return (
						(r.booker_name || '').toLowerCase().includes(q) ||
						r.room.toLowerCase().includes(q) ||
						r.booking_id.toLowerCase().includes(q) ||
						fmtDay(r.created_at).toLowerCase().includes(q)
					);
				})
			: recordings
	);

	onMount(async () => {
		try {
			const res = await api.get<{ recordings: Recording[] }>('/v1/recordings');
			recordings = res.recordings ?? [];
		} catch (e: any) {
			toast.error(e.message || 'Could not load recordings');
		} finally {
			loading = false;
		}
	});

	function fmtDuration(s: number) {
		if (!s) return '—';
		const m = Math.floor(s / 60), sec = s % 60;
		return `${m}:${String(sec).padStart(2, '0')}`;
	}
	function fmtDay(iso: string) {
		try { return new Date(iso).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' }); } catch { return iso; }
	}
	function fmtTime(iso: string) {
		try { return new Date(iso).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' }); } catch { return ''; }
	}
	const statusStyle: Record<string, string> = {
		complete: 'bg-green-50 text-green-700',
		active: 'bg-blue-50 text-blue-700',
		failed: 'bg-destructive/10 text-destructive'
	};

	function download(r: Recording) {
		// The endpoint redirects to a short-lived presigned URL; open it directly.
		window.location.href = `/v1/recordings/${r.id}/download`;
	}

	let openNotes = $state<string | null>(null);
	let notesContent = $state('');
	let notesLoading = $state(false);

	async function loadNotes(r: Recording) {
		notesContent = ''; notesLoading = true;
		try {
			const res = await api.get<{ exists: boolean; content?: string }>(`/v1/bookings/${r.booking_id}/notes`);
			notesContent = res.exists ? (res.content ?? '') : '';
		} catch (e: any) {
			toast.error(e.message || 'Could not load notes');
		} finally {
			notesLoading = false;
		}
	}

	async function viewNotes(r: Recording) {
		if (openNotes === r.id) { openNotes = null; return; }
		openNotes = r.id; openConsent = null;
		await loadNotes(r);
	}

	type Consent = { identity: string; name: string; decision: string; decided_at: string };
	let openConsent = $state<string | null>(null);
	let consentRows = $state<Consent[]>([]);
	let consentLoading = $state(false);

	async function viewConsent(r: Recording) {
		if (openConsent === r.id) { openConsent = null; return; }
		openConsent = r.id; openNotes = null; consentRows = []; consentLoading = true;
		try {
			const res = await api.get<{ consents: Consent[] }>(`/v1/recordings/${r.id}/consent`);
			consentRows = res.consents ?? [];
		} catch (e: any) {
			toast.error(e.message || 'Could not load consent log');
		} finally {
			consentLoading = false;
		}
	}

	let deleting = $state(false);
	let deleteOneOpen = $state(false);
	let pendingDelete = $state<Recording | null>(null);
	let deleteAllOpen = $state(false);
	const deletableCount = $derived(recordings.filter((r) => r.status !== 'active').length);

	function askDelete(r: Recording) {
		pendingDelete = r;
		deleteOneOpen = true;
	}

	async function doDeleteOne() {
		const r = pendingDelete;
		if (!r) return;
		try {
			await api.del(`/v1/recordings/${r.id}`);
			recordings = recordings.filter((x) => x.id !== r.id);
			if (openNotes === r.id) openNotes = null;
			if (openConsent === r.id) openConsent = null;
			toast.success('Recording deleted');
		} catch (e: any) {
			toast.error(e.message || 'Could not delete recording');
		}
	}

	function askDeleteAll() {
		if (deletableCount === 0) {
			toast.info('Nothing to delete (any in-progress recordings are kept).');
			return;
		}
		deleteAllOpen = true;
	}

	async function doDeleteAll() {
		deleting = true;
		try {
			const res = await api.del<{ deleted: number; failed: number }>('/v1/recordings');
			const reloaded = await api.get<{ recordings: Recording[] }>('/v1/recordings');
			recordings = reloaded.recordings ?? [];
			openNotes = null; openConsent = null;
			if (res.failed) toast.error(`Deleted ${res.deleted}; ${res.failed} could not be deleted.`);
			else toast.success(`Deleted ${res.deleted} recording${res.deleted === 1 ? '' : 's'}.`);
		} catch (e: any) {
			toast.error(e.message || 'Could not delete recordings');
		} finally {
			deleting = false;
		}
	}
</script>

<svelte:head><title>Recordings — Calnode</title></svelte:head>

<div class="mb-8 flex items-start justify-between gap-4">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">Recordings</h1>
		<p class="mt-1 text-sm text-muted-foreground">Meeting recordings captured from Calnode video calls. Files live in your storage bucket; links below are short-lived. Download filenames use the meeting's date in UTC.</p>
	</div>
	{#if $currentUser?.is_admin && recordings.length > 0}
		<Button variant="outline" size="sm" class="shrink-0" disabled={deleting} onclick={askDeleteAll}>
			{deleting ? 'Deleting…' : 'Delete all'}
		</Button>
	{/if}
</div>

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">Admin access required.</p>
{:else if loading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else if recordings.length === 0}
	<div class="rounded-lg border bg-card p-8 text-center">
		<p class="text-sm font-medium">No recordings yet</p>
		<p class="mt-1 text-sm text-muted-foreground">When a host records a video meeting, it'll appear here.</p>
	</div>
{:else}
	<div class="mb-4">
		<Input type="search" placeholder="Search by booker, room, or date…" bind:value={query} class="max-w-sm" />
	</div>
	{#if filtered.length === 0}
		<div class="rounded-lg border bg-card p-8 text-center">
			<p class="text-sm text-muted-foreground">No recordings match “{query}”.</p>
		</div>
	{:else}
	<div class="divide-y rounded-lg border bg-card">
		{#each filtered as r (r.id)}
			<div class="p-4">
				<div class="flex items-center justify-between gap-4">
					<div class="min-w-0">
						<p class="truncate font-medium">{r.booker_name || r.room} · {fmtDay(r.created_at)}</p>
						<p class="mt-0.5 text-xs text-muted-foreground">{fmtTime(r.created_at)} · {fmtDuration(r.duration_s)}</p>
					</div>
					<div class="flex shrink-0 items-center gap-3">
						<span class="rounded-full px-2 py-0.5 text-xs font-medium {statusStyle[r.status] ?? 'bg-muted text-muted-foreground'}">{r.status}</span>
						{#if r.booking_id}
							<Button variant="ghost" size="sm" onclick={() => viewNotes(r)}>{openNotes === r.id ? 'Hide notes' : 'Notes'}</Button>
						{/if}
						<Button variant="ghost" size="sm" onclick={() => viewConsent(r)}>{openConsent === r.id ? 'Hide consent' : 'Consent'}</Button>
						<Button variant="outline" size="sm" disabled={!r.has_file} onclick={() => download(r)}>
							{r.has_file ? 'Download' : 'Not ready'}
						</Button>
						<Tooltip.Provider>
							<Tooltip.Root>
								<Tooltip.Trigger
									class={buttonVariants({ variant: 'ghost', size: 'icon' })}
									disabled={r.status === 'active'}
									onclick={() => askDelete(r)}
								>
									<!-- Trash icon -->
									<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
								</Tooltip.Trigger>
								<Tooltip.Content>Delete</Tooltip.Content>
							</Tooltip.Root>
						</Tooltip.Provider>
					</div>
				</div>
				{#if openNotes === r.id}
					<div class="mt-3 rounded-md border bg-muted/40 p-3">
						<div class="mb-2 flex items-center justify-between gap-2">
							<p class="text-xs font-medium text-muted-foreground">Notes</p>
							<Button variant="ghost" size="sm" class="h-7 gap-1.5 px-2 text-xs" disabled={notesLoading} onclick={() => loadNotes(r)}>
								<svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class={notesLoading ? 'animate-spin' : ''}><path d="M21 12a9 9 0 1 1-2.64-6.36"/><polyline points="21 3 21 9 15 9"/></svg>
								{notesLoading ? 'Checking…' : 'Refresh'}
							</Button>
						</div>
						{#if notesLoading}
							<p class="text-xs text-muted-foreground">Loading notes…</p>
						{:else if notesContent}
							<div class="whitespace-pre-wrap text-sm leading-relaxed">{notesContent}</div>
						{:else}
							<p class="text-xs text-muted-foreground">No notes yet — they appear a few minutes after a recorded meeting (needs the notetaker enabled in Settings → Video). Use Refresh to check again.</p>
						{/if}
					</div>
				{/if}
				{#if openConsent === r.id}
					<div class="mt-3 rounded-md border bg-muted/40 p-3">
						{#if consentLoading}
							<p class="text-xs text-muted-foreground">Loading consent log…</p>
						{:else if consentRows.length > 0}
							<p class="mb-2 text-xs font-medium text-muted-foreground">Recording notice — who acknowledged</p>
							<ul class="divide-y divide-border/60">
								{#each consentRows as c (c.identity)}
									<li class="flex items-center justify-between gap-3 py-1.5 text-sm">
										<span class="truncate">{c.name || 'Guest'}</span>
										<span class="flex shrink-0 items-center gap-2">
											<span class="rounded-full px-2 py-0.5 text-xs font-medium {c.decision === 'leave' ? 'bg-destructive/10 text-destructive' : 'bg-green-50 text-green-700'}">{c.decision === 'leave' ? 'Left' : 'Continued'}</span>
											<span class="text-xs text-muted-foreground">{fmtDate(c.decided_at)}</span>
										</span>
									</li>
								{/each}
							</ul>
						{:else}
							<p class="text-xs text-muted-foreground">No consent responses recorded for this meeting. Acknowledgements are captured only while recording is on.</p>
						{/if}
					</div>
				{/if}
			</div>
		{/each}
	</div>
	{/if}
{/if}

<ConfirmDialog
	bind:open={deleteOneOpen}
	title="Delete recording?"
	description="This permanently removes the video file, its transcript, and the booking's notes. This cannot be undone."
	confirmText="Delete"
	destructive
	onConfirm={doDeleteOne}
/>

<ConfirmDialog
	bind:open={deleteAllOpen}
	title="Delete all recordings?"
	description={`This permanently deletes ${deletableCount} recording${deletableCount === 1 ? '' : 's'} — files, transcripts and notes. In-progress recordings are kept. This cannot be undone.`}
	confirmText="Delete all"
	destructive
	onConfirm={doDeleteAll}
/>
