<script lang="ts">
	import { toast } from 'svelte-sonner';
	import { api } from '$lib/api';
	import { authStatus } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { t, tr } from '$lib/i18n';

	let resetOpen = $state(false);
	let resetting = $state(false);

	async function doReset() {
		resetting = true;
		try {
			await api.post('/v1/demo/reset');
			toast.success(tr('demo.toast.resetSuccess'));
			window.location.href = '/admin/';
		} catch (e: any) {
			toast.error(e.message ?? tr('demo.toast.resetFailed'));
		} finally {
			resetting = false;
		}
	}
</script>

<ConfirmDialog
	bind:open={resetOpen}
	title={$t('demo.confirm.title')}
	description={$t('demo.confirm.description')}
	confirmText={$t('demo.confirm.confirm')}
	onConfirm={doReset}
/>

<div class="flex items-center justify-between gap-3 border-b border-amber-200 bg-amber-50 px-4 py-2 text-sm text-amber-900">
	<p>
		<span class="font-semibold">{$t('demo.banner.title')}</span> — {$t('demo.banner.body')}
		<a
			href="https://github.com/Calnode/calnode"
			target="_blank"
			rel="noopener noreferrer"
			class="ml-1 font-medium underline"
		>
			{$t('demo.banner.viewSource')}
		</a>
	</p>
	<Button
		variant="outline"
		size="sm"
		class="shrink-0 border-amber-300 bg-white hover:bg-amber-100"
		onclick={() => (resetOpen = true)}
		disabled={resetting}
	>
		{resetting ? $t('demo.resetting') : $t('demo.resetButton')}
	</Button>
</div>
