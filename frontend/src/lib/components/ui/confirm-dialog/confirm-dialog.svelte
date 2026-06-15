<script lang="ts">
	import { AlertDialog as AlertDialogPrimitive } from 'bits-ui';
	import { buttonVariants } from '$lib/components/ui/button';
	import { cn } from '$lib/utils.js';

	let {
		open = $bindable(false),
		title = 'Are you sure?',
		description = '',
		confirmText = 'Confirm',
		cancelText = 'Cancel',
		destructive = false,
		onConfirm
	}: {
		open?: boolean;
		title?: string;
		description?: string;
		confirmText?: string;
		cancelText?: string;
		destructive?: boolean;
		onConfirm?: () => void;
	} = $props();
</script>

<AlertDialogPrimitive.Root bind:open>
	<AlertDialogPrimitive.Portal>
		<AlertDialogPrimitive.Overlay
			class={cn(
				'fixed inset-0 z-50 bg-black/50',
				'data-[state=open]:animate-in data-[state=closed]:animate-out',
				'data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0'
			)}
		/>
		<AlertDialogPrimitive.Content
			class={cn(
				'fixed left-1/2 top-1/2 z-50 grid w-full max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2',
				'gap-4 rounded-lg border bg-background p-6 shadow-lg sm:max-w-md',
				'data-[state=open]:animate-in data-[state=closed]:animate-out',
				'data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0',
				'data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95'
			)}
		>
			<div class="flex flex-col gap-2">
				<AlertDialogPrimitive.Title class="text-base font-semibold">
					{title}
				</AlertDialogPrimitive.Title>
				{#if description}
					<AlertDialogPrimitive.Description class="text-sm text-muted-foreground">
						{description}
					</AlertDialogPrimitive.Description>
				{/if}
			</div>
			<div class="flex justify-end gap-2">
				<AlertDialogPrimitive.Cancel class={buttonVariants({ variant: 'outline' })}>
					{cancelText}
				</AlertDialogPrimitive.Cancel>
				<AlertDialogPrimitive.Action
					class={buttonVariants({ variant: destructive ? 'destructive' : 'default' })}
					onclick={() => onConfirm?.()}
				>
					{confirmText}
				</AlertDialogPrimitive.Action>
			</div>
		</AlertDialogPrimitive.Content>
	</AlertDialogPrimitive.Portal>
</AlertDialogPrimitive.Root>
