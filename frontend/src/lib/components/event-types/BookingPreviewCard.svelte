<script lang="ts">
	let {
		name = 'New Meeting',
		description = '',
		duration = 30,
		locationType = 'link',
		locationValue = '',
		hostName = 'Host',
		hostAvatar = ''
	}: {
		name?: string;
		description?: string;
		duration?: number;
		locationType?: string;
		locationValue?: string;
		hostName?: string;
		hostAvatar?: string;
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
					<button
						type="button"
						onclick={() => (selectedDay = d)}
						class="h-6 rounded text-[11px] font-medium transition-colors {selectedDay === d
							? 'bg-primary text-primary-foreground'
							: d % 7 === 0 || d % 7 === 6
								? 'text-muted-foreground/40'
								: 'hover:bg-muted text-foreground'}"
					>
						{d}
					</button>
				{/each}
			</div>
		</div>
	</div>
</div>
