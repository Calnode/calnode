<script lang="ts">
	import { Calendar as CalendarPrimitive } from 'bits-ui';
	import { cn } from '$lib/utils.js';

	let {
		value = $bindable(),
		onValueChange,
		weekStartsOn = 1,
		class: className,
		...restProps
		// bits-ui v2 only exposes the union RootProps; this component is always type="single",
		// so derive the single-date variant (drop `type`, which we set below).
	}: Omit<Extract<CalendarPrimitive.RootProps, { type: 'single' }>, 'type'> & { class?: string } = $props();
</script>

<CalendarPrimitive.Root
	type="single"
	bind:value
	{onValueChange}
	{weekStartsOn}
	class={cn('p-3', className)}
	{...restProps}
>
	{#snippet children({ months, weekdays })}
		<CalendarPrimitive.Header class="relative flex w-full items-center justify-between pt-1 pb-4">
			<CalendarPrimitive.PrevButton
				class="inline-flex size-7 items-center justify-center rounded-md border border-input bg-background text-muted-foreground hover:bg-accent hover:text-accent-foreground disabled:opacity-40 disabled:pointer-events-none"
			>
				<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"/></svg>
			</CalendarPrimitive.PrevButton>
			<CalendarPrimitive.Heading class="text-sm font-semibold" />
			<CalendarPrimitive.NextButton
				class="inline-flex size-7 items-center justify-center rounded-md border border-input bg-background text-muted-foreground hover:bg-accent hover:text-accent-foreground disabled:opacity-40 disabled:pointer-events-none"
			>
				<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="9 18 15 12 9 6"/></svg>
			</CalendarPrimitive.NextButton>
		</CalendarPrimitive.Header>

		{#each months as month}
			<CalendarPrimitive.Grid class="w-full border-collapse select-none">
				<CalendarPrimitive.GridHead>
					<CalendarPrimitive.GridRow class="flex">
						{#each weekdays as weekday}
							<CalendarPrimitive.HeadCell
								class="w-8 rounded-md pb-2 text-center text-[0.75rem] font-medium text-muted-foreground"
							>
								{weekday.slice(0, 2)}
							</CalendarPrimitive.HeadCell>
						{/each}
					</CalendarPrimitive.GridRow>
				</CalendarPrimitive.GridHead>
				<CalendarPrimitive.GridBody>
					{#each month.weeks as weekDates}
						<CalendarPrimitive.GridRow class="mt-1 flex w-full">
							{#each weekDates as date}
								<CalendarPrimitive.Cell
									{date}
									month={month.value}
									class="relative size-8 p-0 text-center text-sm focus-within:relative focus-within:z-20"
								>
									<CalendarPrimitive.Day
										class={cn(
											'inline-flex size-8 items-center justify-center rounded-md text-sm transition-colors',
											'hover:bg-accent hover:text-accent-foreground',
											'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring',
											'data-selected:bg-primary data-selected:text-primary-foreground data-selected:hover:bg-primary data-selected:hover:text-primary-foreground',
											'data-today:font-semibold data-today:underline data-today:underline-offset-2',
											'data-outside-month:text-muted-foreground data-outside-month:opacity-40',
											'data-disabled:text-muted-foreground data-disabled:opacity-40 data-disabled:pointer-events-none',
										)}
									/>
								</CalendarPrimitive.Cell>
							{/each}
						</CalendarPrimitive.GridRow>
					{/each}
				</CalendarPrimitive.GridBody>
			</CalendarPrimitive.Grid>
		{/each}
	{/snippet}
</CalendarPrimitive.Root>
