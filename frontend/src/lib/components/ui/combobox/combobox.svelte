<script lang="ts">
	import * as Popover from '$lib/components/ui/popover';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { cn } from '$lib/utils.js';
	import CheckIcon from '@lucide/svelte/icons/check';
	import ChevronsUpDownIcon from '@lucide/svelte/icons/chevrons-up-down';

	type Item = { value: string; label: string };

	let {
		items,
		value = $bindable(''),
		placeholder = 'Select…',
		searchPlaceholder = 'Search…',
		class: className,
	}: {
		items: Item[];
		value?: string;
		placeholder?: string;
		searchPlaceholder?: string;
		class?: string;
	} = $props();

	let open = $state(false);
	let filter = $state('');

	const selectedLabel = $derived(items.find((i) => i.value === value)?.label ?? '');
	const filtered = $derived(
		filter.trim()
			? items.filter((i) => i.label.toLowerCase().includes(filter.trim().toLowerCase()))
			: items
	);

	function select(v: string) {
		value = v;
		open = false;
		filter = '';
	}

	function onInputKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter') {
			e.preventDefault();
			if (filtered.length > 0) select(filtered[0].value);
		}
	}
</script>

<Popover.Root bind:open onOpenChange={(o) => { if (!o) filter = ''; }}>
	<Popover.Trigger
		class={cn(
			'border-input focus-visible:border-ring focus-visible:ring-ring/50 inline-flex h-8 w-full items-center justify-between gap-2 rounded-lg border bg-transparent px-2.5 py-1 text-sm transition-colors outline-none focus-visible:ring-3 disabled:cursor-not-allowed disabled:opacity-50',
			className
		)}
	>
		<span class={cn('truncate', !selectedLabel && 'text-muted-foreground')}>
			{selectedLabel || placeholder}
		</span>
		<ChevronsUpDownIcon class="text-muted-foreground size-4 shrink-0 opacity-50" />
	</Popover.Trigger>
	<Popover.Content class="w-[var(--bits-floating-anchor-width)] p-0" align="start">
		<div class="p-2">
			<Input bind:value={filter} placeholder={searchPlaceholder} onkeydown={onInputKeydown} autofocus />
		</div>
		<div class="max-h-72 overflow-y-auto p-1">
			{#if filtered.length === 0}
				<p class="px-2 py-3 text-center text-sm text-muted-foreground">No results.</p>
			{:else}
				{#each filtered as item (item.value)}
					<button
						type="button"
						onclick={() => select(item.value)}
						class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm hover:bg-accent hover:text-accent-foreground"
					>
						<CheckIcon class={cn('size-4 shrink-0', value === item.value ? 'opacity-100' : 'opacity-0')} />
						<span class="truncate">{item.label}</span>
					</button>
				{/each}
			{/if}
		</div>
	</Popover.Content>
</Popover.Root>
