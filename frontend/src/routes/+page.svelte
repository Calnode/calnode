<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { base } from '$app/paths';
	import { api, type CalendarStatus, type AvailabilityRule, type EventType } from '$lib/api';

	let calendarConnected = $state(false);
	let hasAvailability = $state(false);
	let hasEventType = $state(false);
	let firstSlug = $state('');
	let origin = $state('');
	let loading = $state(true);
	let copied = $state(false);
	let copyFailed = $state(false);
	let copyTimer: ReturnType<typeof setTimeout> | null = null;

	onMount(async () => {
		origin = window.location.origin;
		try {
			const [cal, rules, events] = await Promise.all([
				api.get<CalendarStatus>('/v1/calendar/status').catch(() => ({ connected: false })),
				api.get<{ items: AvailabilityRule[] }>('/v1/availability-rules').catch(() => ({ items: [] })),
				api.get<{ items: EventType[] }>('/v1/event-types').catch(() => ({ items: [] }))
			]);
			calendarConnected = cal.connected;
			hasAvailability = (rules.items?.length ?? 0) > 0;
			hasEventType = (events.items?.length ?? 0) > 0;
			firstSlug = events.items?.[0]?.slug ?? '';
		} finally {
			loading = false;
		}
	});

	onDestroy(() => {
		if (copyTimer !== null) clearTimeout(copyTimer);
	});

	const allDone = $derived(calendarConnected && hasAvailability && hasEventType);
	const bookingUrl = $derived(firstSlug && origin ? `${origin}/book/${firstSlug}` : '');

	async function copyLink() {
		if (!bookingUrl) return;
		try {
			await navigator.clipboard.writeText(bookingUrl);
			copied = true;
			copyFailed = false;
		} catch {
			copyFailed = true;
			copied = false;
		}
		if (copyTimer !== null) clearTimeout(copyTimer);
		copyTimer = setTimeout(() => { copied = false; copyFailed = false; }, 2000);
	}
</script>

<svelte:head><title>Dashboard — Calnode</title></svelte:head>

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else if allDone}
	<div class="mb-8">
		<h1 class="text-2xl font-semibold tracking-tight">Dashboard</h1>
		<p class="mt-1 text-sm text-muted-foreground">Your booking page is live and ready to share.</p>
	</div>
	{#if bookingUrl}
		<div class="rounded-lg border bg-card px-5 py-4">
			<p class="text-sm font-medium mb-2">Your booking link</p>
			<div class="flex items-center gap-2">
				<a
					href={bookingUrl}
					target="_blank"
					rel="noopener noreferrer"
					class="truncate text-sm text-primary hover:underline font-mono"
				>
					{bookingUrl}
				</a>
				<button
					onclick={copyLink}
					class="shrink-0 rounded px-2 py-0.5 text-xs border bg-background hover:bg-muted transition-colors
						{copyFailed ? 'border-destructive text-destructive' : ''}"
				>
					{copied ? 'Copied!' : copyFailed ? 'Failed' : 'Copy'}
				</button>
			</div>
		</div>
	{/if}
{:else}
	<div class="mb-8">
		<h1 class="text-2xl font-semibold tracking-tight">Getting started</h1>
		<p class="mt-1 text-sm text-muted-foreground">Complete these steps and you'll be ready to take bookings.</p>
	</div>

	<!-- Checklist -->
	<div class="rounded-lg border bg-card divide-y">
		<!-- Calendar -->
		<a
			href="{base}/calendar"
			class="flex items-start gap-4 px-5 py-4 transition-colors hover:bg-muted/40 group"
		>
			<div class="mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full border-2
				{calendarConnected ? 'border-primary bg-primary' : 'border-muted-foreground/40 group-hover:border-primary/60'}">
				{#if calendarConnected}
					<svg width="10" height="10" viewBox="0 0 10 10" fill="none" xmlns="http://www.w3.org/2000/svg">
						<path d="M2 5l2.5 2.5L8 3" stroke="white" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
					</svg>
				{/if}
			</div>
			<div class="flex-1 min-w-0">
				<p class="text-sm font-medium {calendarConnected ? 'text-muted-foreground line-through' : ''}">
					Connect your calendar
				</p>
				<p class="mt-0.5 text-xs text-muted-foreground">
					Calnode checks your calendar to prevent double-bookings.
				</p>
			</div>
			<svg class="mt-0.5 shrink-0 text-muted-foreground/40 group-hover:text-muted-foreground transition-colors" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
				<polyline points="9 18 15 12 9 6"/>
			</svg>
		</a>

		<!-- Availability -->
		<a
			href="{base}/availability"
			class="flex items-start gap-4 px-5 py-4 transition-colors hover:bg-muted/40 group"
		>
			<div class="mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full border-2
				{hasAvailability ? 'border-primary bg-primary' : 'border-muted-foreground/40 group-hover:border-primary/60'}">
				{#if hasAvailability}
					<svg width="10" height="10" viewBox="0 0 10 10" fill="none" xmlns="http://www.w3.org/2000/svg">
						<path d="M2 5l2.5 2.5L8 3" stroke="white" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
					</svg>
				{/if}
			</div>
			<div class="flex-1 min-w-0">
				<p class="text-sm font-medium {hasAvailability ? 'text-muted-foreground line-through' : ''}">
					Set your availability
				</p>
				<p class="mt-0.5 text-xs text-muted-foreground">
					Define the hours when people can book time with you.
				</p>
			</div>
			<svg class="mt-0.5 shrink-0 text-muted-foreground/40 group-hover:text-muted-foreground transition-colors" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
				<polyline points="9 18 15 12 9 6"/>
			</svg>
		</a>

		<!-- Event type -->
		<a
			href="{base}/event-types"
			class="flex items-start gap-4 px-5 py-4 transition-colors hover:bg-muted/40 group"
		>
			<div class="mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full border-2
				{hasEventType ? 'border-primary bg-primary' : 'border-muted-foreground/40 group-hover:border-primary/60'}">
				{#if hasEventType}
					<svg width="10" height="10" viewBox="0 0 10 10" fill="none" xmlns="http://www.w3.org/2000/svg">
						<path d="M2 5l2.5 2.5L8 3" stroke="white" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
					</svg>
				{/if}
			</div>
			<div class="flex-1 min-w-0">
				<p class="text-sm font-medium {hasEventType ? 'text-muted-foreground line-through' : ''}">
					Create your first event type
				</p>
				<p class="mt-0.5 text-xs text-muted-foreground">
					An event type defines the duration, location, and availability for a booking.
				</p>
			</div>
			<svg class="mt-0.5 shrink-0 text-muted-foreground/40 group-hover:text-muted-foreground transition-colors" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
				<polyline points="9 18 15 12 9 6"/>
			</svg>
		</a>

		<!-- Booking link -->
		<div class="flex items-start gap-4 px-5 py-4 {hasEventType ? '' : 'opacity-40'}">
			<div class="mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full border-2
				{hasEventType ? 'border-primary bg-primary' : 'border-muted-foreground/40'}">
				{#if hasEventType}
					<svg width="10" height="10" viewBox="0 0 10 10" fill="none" xmlns="http://www.w3.org/2000/svg">
						<path d="M2 5l2.5 2.5L8 3" stroke="white" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
					</svg>
				{/if}
			</div>
			<div class="flex-1 min-w-0">
				<p class="text-sm font-medium">Share your booking link</p>
				<p class="mt-0.5 text-xs text-muted-foreground">
					{hasEventType ? 'Your page is live. Share it and start taking bookings.' : "Available once you've created an event type."}
				</p>
				{#if hasEventType && bookingUrl}
					<div class="mt-2 flex items-center gap-2">
						<a
							href={bookingUrl}
							target="_blank"
							rel="noopener noreferrer"
							class="truncate text-xs text-primary hover:underline font-mono"
						>
							{bookingUrl}
						</a>
						<button
							onclick={copyLink}
							class="shrink-0 rounded px-2 py-0.5 text-xs border bg-background hover:bg-muted transition-colors
								{copyFailed ? 'border-destructive text-destructive' : ''}"
						>
							{copied ? 'Copied!' : copyFailed ? 'Failed' : 'Copy'}
						</button>
					</div>
				{/if}
			</div>
		</div>
	</div>
{/if}
