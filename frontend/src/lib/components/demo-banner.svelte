<script lang="ts">
	import { toast } from 'svelte-sonner';
	import { api } from '$lib/api';
	import { authStatus } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';

	let resetOpen = $state(false);
	let resetting = $state(false);

	async function doReset() {
		resetting = true;
		try {
			await api.post('/v1/demo/reset');
			toast.success('Demo reset — reloading…');
			window.location.href = '/admin/';
		} catch (e: any) {
			toast.error(e.message ?? 'Reset failed');
		} finally {
			resetting = false;
		}
	}
</script>

<ConfirmDialog
	bind:open={resetOpen}
	title="Reset the demo now?"
	description="Every event type, booking, and setting is wiped and replaced with fresh sample data. This also happens automatically on a timer."
	confirmText="Reset demo"
	onConfirm={doReset}
/>

<div class="flex items-center justify-between gap-3 border-b border-amber-200 bg-amber-50 px-4 py-2 text-sm text-amber-900">
	<p>
		<span class="font-semibold">Public demo</span> — data here is visible to everyone and resets
		automatically. Don't enter anything private.
		<a
			href="https://github.com/Calnode/calnode"
			target="_blank"
			rel="noopener noreferrer"
			class="ml-1 font-medium underline"
		>
			View source
		</a>
	</p>
	<Button
		variant="outline"
		size="sm"
		class="shrink-0 border-amber-300 bg-white hover:bg-amber-100"
		onclick={() => (resetOpen = true)}
		disabled={resetting}
	>
		{resetting ? 'Resetting…' : 'Reset demo now'}
	</Button>
</div>
