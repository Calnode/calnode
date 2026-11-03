<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type LLMSettings } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { Input } from '$lib/components/ui/input';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { toast } from 'svelte-sonner';
	import { t, tr, localeTag } from '$lib/i18n';

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

	type NotetakerSettings = { enabled: boolean; stt_api_key_set: boolean };

	let loading = $state(true);
	let recordings = $state<Recording[]>([]);
	let query = $state('');

	// Loaded once, best-effort, to explain a precise reason when notes are missing instead of a
	// generic "check back later" — the notetaker needs three separate things on (recording +
	// Deepgram key + an LLM), and it's not obvious which one is missing just by staring at a
	// blank notes panel. Failures here are non-fatal: the notes panel just falls back to the
	// generic message if either fetch fails (e.g. a non-admin viewer, though this page is
	// admin-only anyway).
	let notetaker = $state<NotetakerSettings | null>(null);
	let llm = $state<LLMSettings | null>(null);

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
			toast.error(e.message || tr('recordings.error.load'));
		} finally {
			loading = false;
		}
		try {
			notetaker = await api.get<NotetakerSettings>('/v1/settings/notetaker');
			llm = await api.get<LLMSettings>('/v1/settings/llm');
		} catch {
			// Non-fatal — the "no notes yet" message just falls back to being generic.
		}
	});

	// The precise reason notes haven't shown up, in priority order — mirrors the exact gate
	// maybeStartNotetaker checks server-side (recording enabled, then Deepgram key, then LLM).
	// null once all three are satisfied: at that point it's genuinely just "still processing."
	const notesBlockedReason = $derived.by(() => {
		if (notetaker && !notetaker.enabled) {
			return { text: $t('recordings.notes.blocked.notetakerOff'), href: '/admin/settings/video', label: $t('recordings.link.settingsVideo') };
		}
		if (notetaker && !notetaker.stt_api_key_set) {
			return { text: $t('recordings.notes.blocked.noDeepgram'), href: '/admin/settings/video', label: $t('recordings.link.settingsVideo') };
		}
		if (llm && !llm.active) {
			return { text: $t('recordings.notes.blocked.noLlm'), href: '/admin/settings/ai', label: $t('recordings.link.settingsAi') };
		}
		return null;
	});

	function fmtDuration(s: number) {
		if (!s) return tr('common.emDash');
		const m = Math.floor(s / 60), sec = s % 60;
		return `${m}:${String(sec).padStart(2, '0')}`;
	}

	async function copyText(text: string, label: string) {
		if (!text) return;
		try {
			await navigator.clipboard.writeText(text);
			toast.success(tr('recordings.toast.copied', { label }));
		} catch {
			toast.error(tr('common.copyClipboardError'));
		}
	}
	function fmtDay(iso: string) {
		try { return new Date(iso).toLocaleDateString(localeTag(), { day: 'numeric', month: 'short', year: 'numeric' }); } catch { return iso; }
	}
	function fmtTime(iso: string) {
		try { return new Date(iso).toLocaleTimeString(localeTag(), { hour: 'numeric', minute: '2-digit' }); } catch { return ''; }
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
	let notesStatus = $state('');
	let notesLoading = $state(false);

	async function loadNotes(r: Recording) {
		notesContent = ''; notesStatus = ''; notesLoading = true;
		try {
			const res = await api.get<{ exists: boolean; content?: string; status?: string }>(`/v1/bookings/${r.booking_id}/notes`);
			notesContent = res.exists ? (res.content ?? '') : '';
			notesStatus = res.status ?? '';
		} catch (e: any) {
			toast.error(e.message || tr('recordings.error.loadNotes'));
		} finally {
			notesLoading = false;
		}
	}

	async function viewNotes(r: Recording) {
		if (openNotes === r.id) { openNotes = null; return; }
		openNotes = r.id; openConsent = null; openTranscript = null;
		await loadNotes(r);
	}

	// Re-run just the LLM summary over the existing Deepgram transcript (no re-recording). The
	// endpoint runs it inline and returns the notes, so they appear immediately — no refetch.
	async function regenNotes(r: Recording) {
		notesLoading = true;
		try {
			const res = await api.post<{ exists: boolean; content?: string; status?: string }>(
				`/v1/bookings/${r.booking_id}/notes/regenerate`,
				{}
			);
			notesContent = res.content ?? '';
			notesStatus = res.status ?? '';
			if (notesContent) toast.success(tr('recordings.toast.notesRegenerated'));
			else toast.info(tr('recordings.toast.notesEmpty'));
		} catch (e: any) {
			toast.error(e.message || tr('recordings.error.regen'));
		} finally {
			notesLoading = false;
		}
	}

	// Raw Deepgram transcript (separate from the LLM notes).
	let openTranscript = $state<string | null>(null);
	let transcriptContent = $state('');
	let transcriptLoading = $state(false);

	async function viewTranscript(r: Recording) {
		if (openTranscript === r.id) { openTranscript = null; return; }
		openTranscript = r.id; openNotes = null; openConsent = null; transcriptContent = ''; transcriptLoading = true;
		try {
			const res = await api.get<{ exists: boolean; text?: string }>(`/v1/bookings/${r.booking_id}/transcript`);
			transcriptContent = res.exists ? (res.text ?? '') : '';
		} catch (e: any) {
			toast.error(e.message || tr('recordings.error.loadTranscript'));
		} finally {
			transcriptLoading = false;
		}
	}

	type Consent = { identity: string; name: string; decision: string; decided_at: string };
	let openConsent = $state<string | null>(null);
	let consentRows = $state<Consent[]>([]);
	let consentLoading = $state(false);

	async function viewConsent(r: Recording) {
		if (openConsent === r.id) { openConsent = null; return; }
		openConsent = r.id; openNotes = null; openTranscript = null; consentRows = []; consentLoading = true;
		try {
			const res = await api.get<{ consents: Consent[] }>(`/v1/recordings/${r.id}/consent`);
			consentRows = res.consents ?? [];
		} catch (e: any) {
			toast.error(e.message || tr('recordings.error.loadConsent'));
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
			if (openTranscript === r.id) openTranscript = null;
			toast.success(tr('recordings.toast.deleted'));
		} catch (e: any) {
			toast.error(e.message || tr('recordings.error.deleteOne'));
		}
	}

	function askDeleteAll() {
		if (deletableCount === 0) {
			toast.info(tr('recordings.toast.nothingToDelete'));
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
			openNotes = null; openConsent = null; openTranscript = null;
			if (res.failed) toast.error(tr('recordings.toast.deletePartial', { deleted: res.deleted, failed: res.failed }));
			else toast.success(tr('recordings.toast.deleteAll', { count: res.deleted }));
		} catch (e: any) {
			toast.error(e.message || tr('recordings.error.deleteAll'));
		} finally {
			deleting = false;
		}
	}
</script>

<svelte:head><title>{$t('recordings.headTitle')}</title></svelte:head>

<div class="mb-8 flex items-start justify-between gap-4">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">{$t('recordings.title')}</h1>
		<p class="mt-1 text-sm text-muted-foreground">{$t('recordings.subtitle')}</p>
	</div>
	{#if $currentUser?.is_admin && recordings.length > 0}
		<Button variant="outline" size="sm" class="shrink-0" disabled={deleting} onclick={askDeleteAll}>
			{deleting ? $t('recordings.deleting') : $t('recordings.deleteAll')}
		</Button>
	{/if}
</div>

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">{$t('common.adminRequired')}</p>
{:else if loading}
	<p class="py-8 text-sm text-muted-foreground">{$t('common.loading')}</p>
{:else if recordings.length === 0}
	<div class="rounded-lg border bg-card p-8 text-center">
		<p class="text-sm font-medium">{$t('recordings.empty.title')}</p>
		<p class="mt-1 text-sm text-muted-foreground">{$t('recordings.empty.description')}</p>
	</div>
{:else}
	<div class="mb-4">
		<Input type="search" placeholder={$t('recordings.searchPlaceholder')} bind:value={query} class="max-w-sm" />
	</div>
	{#if filtered.length === 0}
		<div class="rounded-lg border bg-card p-8 text-center">
			<p class="text-sm text-muted-foreground">{$t('recordings.noMatch', { query })}</p>
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
							<Button variant="ghost" size="sm" onclick={() => viewNotes(r)}>{openNotes === r.id ? $t('recordings.hideNotes') : $t('recordings.notes.label')}</Button>
							<Button variant="ghost" size="sm" onclick={() => viewTranscript(r)}>{openTranscript === r.id ? $t('recordings.hideTranscript') : $t('recordings.transcript.label')}</Button>
						{/if}
						<Button variant="ghost" size="sm" onclick={() => viewConsent(r)}>{openConsent === r.id ? $t('recordings.hideConsent') : $t('recordings.consent.label')}</Button>
						<Button variant="outline" size="sm" disabled={!r.has_file} onclick={() => download(r)}>
							{r.has_file ? $t('recordings.download') : $t('recordings.notReady')}
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
								<Tooltip.Content>{$t('common.delete')}</Tooltip.Content>
							</Tooltip.Root>
						</Tooltip.Provider>
					</div>
				</div>
				{#if openNotes === r.id}
					<div class="mt-3 rounded-md border bg-muted/40 p-3">
						<div class="mb-2 flex items-center justify-between gap-2">
							<p class="text-xs font-medium text-muted-foreground">{$t('recordings.notes.aiSummary')}</p>
							<div class="flex items-center gap-1">
								<Button variant="ghost" size="sm" class="h-7 gap-1.5 px-2 text-xs" disabled={!notesContent} onclick={() => copyText(notesContent, $t('recordings.notes.label'))}>
									<svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
									{$t('common.copy')}
								</Button>
								<Button variant="ghost" size="sm" class="h-7 gap-1.5 px-2 text-xs" disabled={notesLoading} onclick={() => regenNotes(r)}>
									<svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v4"/><path d="M12 3a9 9 0 1 0 9 9"/><polyline points="16 3 21 3 21 8"/></svg>
									{$t('recordings.notes.regenerate')}
								</Button>
								<Button variant="ghost" size="sm" class="h-7 gap-1.5 px-2 text-xs" disabled={notesLoading} onclick={() => loadNotes(r)}>
									<svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class={notesLoading ? 'animate-spin' : ''}><path d="M21 12a9 9 0 1 1-2.64-6.36"/><polyline points="21 3 21 9 15 9"/></svg>
									{notesLoading ? $t('recordings.notes.checking') : $t('recordings.notes.refresh')}
								</Button>
							</div>
						</div>
						{#if notesLoading}
							<p class="text-xs text-muted-foreground">{$t('recordings.notes.loading')}</p>
						{:else if notesContent}
							<div class="whitespace-pre-wrap text-sm leading-relaxed">{notesContent}</div>
						{:else if notesStatus === 'empty'}
							<p class="text-xs text-muted-foreground">{$t('recordings.notes.emptyAi')}</p>
						{:else if notesBlockedReason}
							<p class="text-xs text-muted-foreground">
								{notesBlockedReason.text}
								<a href={notesBlockedReason.href} class="font-medium text-primary underline underline-offset-2">{notesBlockedReason.label}</a>
							</p>
						{:else}
							<p class="text-xs text-muted-foreground">{$t('recordings.notes.pending')}</p>
						{/if}
					</div>
				{/if}
				{#if openTranscript === r.id}
					<div class="mt-3 rounded-md border bg-muted/40 p-3">
						<div class="mb-2 flex items-center justify-between gap-2">
							<p class="text-xs font-medium text-muted-foreground">{$t('recordings.transcript.deepgram')}</p>
							<Button variant="ghost" size="sm" class="h-7 gap-1.5 px-2 text-xs" disabled={!transcriptContent} onclick={() => copyText(transcriptContent, $t('recordings.transcript.label'))}>
								<svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
								{$t('common.copy')}
							</Button>
						</div>
						{#if transcriptLoading}
							<p class="text-xs text-muted-foreground">{$t('recordings.transcript.loading')}</p>
						{:else if transcriptContent}
							<div class="max-h-80 overflow-y-auto whitespace-pre-wrap text-sm leading-relaxed">{transcriptContent}</div>
						{:else}
							<p class="text-xs text-muted-foreground">{$t('recordings.transcript.empty')}</p>
						{/if}
					</div>
				{/if}
				{#if openConsent === r.id}
					<div class="mt-3 rounded-md border bg-muted/40 p-3">
						{#if consentLoading}
							<p class="text-xs text-muted-foreground">{$t('recordings.consent.loading')}</p>
						{:else if consentRows.length > 0}
							<p class="mb-2 text-xs font-medium text-muted-foreground">{$t('recordings.consent.title')}</p>
							<ul class="divide-y divide-border/60">
								{#each consentRows as c (c.identity)}
									<li class="flex items-center justify-between gap-3 py-1.5 text-sm">
										<span class="truncate">{c.name || $t('recordings.consent.guest')}</span>
										<span class="flex shrink-0 items-center gap-2">
											<span class="rounded-full px-2 py-0.5 text-xs font-medium {c.decision === 'leave' ? 'bg-destructive/10 text-destructive' : 'bg-green-50 text-green-700'}">{c.decision === 'leave' ? $t('recordings.consent.left') : $t('recordings.consent.continued')}</span>
											<span class="text-xs text-muted-foreground">{fmtDay(c.decided_at)} · {fmtTime(c.decided_at)}</span>
										</span>
									</li>
								{/each}
							</ul>
						{:else}
							<p class="text-xs text-muted-foreground">{$t('recordings.consent.empty')}</p>
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
	title={$t('recordings.confirm.deleteOne.title')}
	description={$t('recordings.confirm.deleteOne.description')}
	confirmText={$t('common.delete')}
	destructive
	onConfirm={doDeleteOne}
/>

<ConfirmDialog
	bind:open={deleteAllOpen}
	title={$t('recordings.confirm.deleteAll.title')}
	description={$t('recordings.confirm.deleteAll.description', { count: deletableCount })}
	confirmText={$t('recordings.confirm.deleteAll.confirm')}
	destructive
	onConfirm={doDeleteAll}
/>
