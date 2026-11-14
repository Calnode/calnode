<script lang="ts">
	import type { Question } from '$lib/api';

	let {
		name = 'New Meeting',
		description = '',
		duration = 30,
		locationType = 'link',
		locationValue = '',
		hostName = 'Host',
		hostAvatar = '',
		mode = 'calendar',
		allowPhoneCall = false,
		questions = []
	}: {
		name?: string;
		description?: string;
		duration?: number;
		locationType?: string;
		locationValue?: string;
		hostName?: string;
		hostAvatar?: string;
		mode?: 'calendar' | 'form';
		allowPhoneCall?: boolean;
		questions?: Question[];
	} = $props();

	let selectedDay = $state(18);
	const days = Array.from({ length: 30 }, (_, i) => i + 1);
	const weekdays = ['Su', 'Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa'];

	const locationLabel = $derived.by(() => {
		switch (locationType) {
			case 'google_meet': return 'Google Meet';
			case 'teams': return 'Microsoft Teams';
			case 'zoom': return 'Zoom';
			case 'livekit': return 'Calnode Video';
			case 'phone': return locationValue || 'Phone call';
			case 'in_person': return locationValue || 'In person';
			default: return locationValue || 'Web conferencing';
		}
	});
</script>

<div class="sticky top-20 rounded-xl border bg-card p-4 shadow-sm">
	<div class="mb-3 flex items-center justify-between border-b pb-2.5">
		<span class="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Live Preview</span>
		<span class="rounded-full bg-muted px-2 py-0.5 text-[10px] font-medium text-muted-foreground">Interactive</span>
	</div>

	<div class="space-y-4">
		<div>
			<div class="mb-2 flex items-center gap-2">
				{#if hostAvatar}
					<img src={hostAvatar} alt={hostName} class="h-7 w-7 rounded-full object-cover" />
				{:else}
					<div class="flex h-7 w-7 items-center justify-center rounded-full bg-primary text-xs font-semibold text-primary-foreground">
						{(hostName || 'H').slice(0, 1).toUpperCase()}
					</div>
				{/if}
				<span class="truncate text-xs font-medium text-muted-foreground">{hostName}</span>
			</div>
			<h3 class="truncate text-base font-semibold tracking-tight">{name || 'Untitled Event'}</h3>
			{#if description}
				<p class="mt-1 line-clamp-2 text-xs text-muted-foreground">{description}</p>
			{/if}
			<div class="mt-2.5 flex flex-wrap gap-1.5 text-[11px] text-muted-foreground">
				<span class="inline-flex items-center gap-1 rounded-md bg-muted px-2 py-0.5 font-medium text-foreground">
					{duration || 30}m
				</span>
				<span class="inline-flex items-center gap-1 rounded-md bg-muted px-2 py-0.5">
					{locationLabel}
				</span>
			</div>
		</div>

		{#if mode === 'form'}
			<div class="rounded-lg border bg-background/60 p-3 space-y-2.5">
				<div class="flex items-center justify-between border-b pb-2 text-[11px]">
					<span class="font-medium text-foreground">Fri, Nov {selectedDay} · 10:00 AM</span>
					<span class="text-muted-foreground">UTC</span>
				</div>
				<div class="space-y-2 text-[11px]">
					<div>
						<div class="mb-1 font-medium text-foreground">Your name <span class="text-destructive">*</span></div>
						<div class="rounded border bg-background px-2 py-1.5 text-muted-foreground">Jane Doe</div>
					</div>
					<div>
						<div class="mb-1 font-medium text-foreground">Email address <span class="text-destructive">*</span></div>
						<div class="rounded border bg-background px-2 py-1.5 text-muted-foreground">jane@example.com</div>
					</div>
					{#if allowPhoneCall}
						<div>
							<div class="mb-1 font-medium text-foreground">Phone number <span class="text-muted-foreground font-normal">(optional)</span></div>
							<div class="rounded border bg-background px-2 py-1.5 text-muted-foreground">+1 (555) 000-0000</div>
						</div>
					{/if}
					{#each questions as q (q.id)}
						<div>
							{#if q.type === 'checkbox'}
								<div class="flex items-center gap-1.5 py-0.5">
									<div class="h-3.5 w-3.5 rounded border bg-background shrink-0"></div>
									<span class="font-medium text-foreground">{q.label}{#if q.required} <span class="text-destructive">*</span>{/if}</span>
								</div>
							{:else if q.type === 'select'}
								<div class="mb-1 font-medium text-foreground">{q.label}{#if q.required} <span class="text-destructive">*</span>{/if}</div>
								<div class="flex items-center justify-between rounded border bg-background px-2 py-1.5 text-muted-foreground">
									<span>{q.options?.[0] ?? 'Choose an option'}</span>
									<span>▾</span>
								</div>
							{:else}
								<div class="mb-1 font-medium text-foreground">{q.label}{#if q.required} <span class="text-destructive">*</span>{/if}</div>
								<div class="rounded border bg-background px-2 py-2 text-muted-foreground">Answer...</div>
							{/if}
						</div>
					{/each}
					<div class="pt-1">
						<div class="w-full rounded-md bg-primary py-1.5 text-center font-medium text-primary-foreground">
							Confirm booking
						</div>
					</div>
				</div>
			</div>
		{:else}
			<div class="rounded-lg border bg-background/60 p-3">
				<div class="mb-2 flex items-center justify-between text-xs font-medium">
					<span>November 2026</span>
					<span class="text-[11px] text-muted-foreground">UTC</span>
				</div>
				<div class="grid grid-cols-7 gap-1 text-center text-[10px] text-muted-foreground">
					{#each weekdays as wd}
						<div class="py-0.5 font-medium">{wd}</div>
					{/each}
					{#each days as d}
						{@const isWeekend = d % 7 === 0 || d % 7 === 6}
						<button
							type="button"
							disabled={isWeekend}
							onclick={() => (selectedDay = d)}
							class="h-6 rounded text-[11px] font-medium transition-colors {selectedDay === d
								? 'bg-primary text-primary-foreground'
								: isWeekend
									? 'text-muted-foreground/35 cursor-default'
									: 'bg-muted/60 text-foreground hover:bg-muted'}"
						>
							{d}
						</button>
					{/each}
				</div>
			</div>
		{/if}
	</div>
</div>
