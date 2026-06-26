<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { toast } from 'svelte-sonner';

	type Recording = {
		id: string;
		booking_id: string;
		room: string;
		status: string;
		duration_s: number;
		has_file: boolean;
		created_at: string;
	};

	let loading = $state(true);
	let recordings = $state<Recording[]>([]);

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
	function fmtDate(iso: string) {
		try { return new Date(iso).toLocaleString(); } catch { return iso; }
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

	async function viewNotes(r: Recording) {
		if (openNotes === r.id) { openNotes = null; return; }
		openNotes = r.id; notesContent = ''; notesLoading = true;
		try {
			const res = await api.get<{ exists: boolean; content?: string }>(`/v1/bookings/${r.booking_id}/notes`);
			notesContent = res.exists ? (res.content ?? '') : '';
		} catch (e: any) {
			toast.error(e.message || 'Could not load notes');
		} finally {
			notesLoading = false;
		}
	}

	let deleting = $state(false);

	async function deleteRecording(r: Recording) {
		if (!confirm('Delete this recording? This permanently removes the video file, its transcript, and the booking notes. This cannot be undone.')) return;
		try {
			await api.del(`/v1/recordings/${r.id}`);
			recordings = recordings.filter((x) => x.id !== r.id);
			if (openNotes === r.id) openNotes = null;
			toast.success('Recording deleted');
		} catch (e: any) {
			toast.error(e.message || 'Could not delete recording');
		}
	}

	async function deleteAll() {
		const n = recordings.filter((r) => r.status !== 'active').length;
		if (n === 0) { toast.info('Nothing to delete (any in-progress recordings are kept).'); return; }
		if (!confirm(`Permanently delete all ${n} recording${n === 1 ? '' : 's'} — files, transcripts and notes? In-progress recordings are kept. This cannot be undone.`)) return;
		deleting = true;
		try {
			const res = await api.del<{ deleted: number; failed: number }>('/v1/recordings');
			const reloaded = await api.get<{ recordings: Recording[] }>('/v1/recordings');
			recordings = reloaded.recordings ?? [];
			openNotes = null;
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
		<p class="mt-1 text-sm text-muted-foreground">Meeting recordings captured from Calnode video calls. Files live in your storage bucket; links below are short-lived.</p>
	</div>
	{#if $currentUser?.is_admin && recordings.length > 0}
		<Button variant="destructive" size="sm" class="shrink-0" disabled={deleting} onclick={deleteAll}>
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
	<div class="divide-y rounded-lg border bg-card">
		{#each recordings as r (r.id)}
			<div class="p-4">
				<div class="flex items-center justify-between gap-4">
					<div class="min-w-0">
						<p class="truncate font-medium">{r.booking_id ? `Booking ${r.booking_id.slice(0, 8)}` : r.room}</p>
						<p class="mt-0.5 text-xs text-muted-foreground">{fmtDate(r.created_at)} · {fmtDuration(r.duration_s)}</p>
					</div>
					<div class="flex shrink-0 items-center gap-3">
						<span class="rounded-full px-2 py-0.5 text-xs font-medium {statusStyle[r.status] ?? 'bg-muted text-muted-foreground'}">{r.status}</span>
						{#if r.booking_id}
							<Button variant="ghost" size="sm" onclick={() => viewNotes(r)}>{openNotes === r.id ? 'Hide notes' : 'Notes'}</Button>
						{/if}
						<Button variant="outline" size="sm" disabled={!r.has_file} onclick={() => download(r)}>
							{r.has_file ? 'Download' : 'Not ready'}
						</Button>
						<Button variant="destructive" size="sm" disabled={r.status === 'active'} onclick={() => deleteRecording(r)}>Delete</Button>
					</div>
				</div>
				{#if openNotes === r.id}
					<div class="mt-3 rounded-md border bg-muted/40 p-3">
						{#if notesLoading}
							<p class="text-xs text-muted-foreground">Loading notes…</p>
						{:else if notesContent}
							<div class="whitespace-pre-wrap text-sm leading-relaxed">{notesContent}</div>
						{:else}
							<p class="text-xs text-muted-foreground">No notes yet — they appear a few minutes after a recorded meeting (needs the notetaker enabled in Settings → Video).</p>
						{/if}
					</div>
				{/if}
			</div>
		{/each}
	</div>
{/if}
