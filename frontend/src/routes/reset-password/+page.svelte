<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { t, tr } from '$lib/i18n';

	let token = $state('');
	let password = $state('');
	let confirm = $state('');
	let submitting = $state(false);
	let error = $state('');

	onMount(() => {
		token = $page.url.searchParams.get('token') ?? '';
		if (!token) {
			error = tr('auth.reset.missingToken');
		}
	});

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (password.length < 8) {
			error = tr('auth.reset.tooShort');
			return;
		}
		if (password !== confirm) {
			error = tr('auth.reset.mismatch');
			return;
		}
		error = '';
		submitting = true;
		try {
			const res = await fetch('/v1/auth/password/reset', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ token, new_password: password })
			});
			if (res.ok) {
				// The reset signs the user straight in.
				window.location.href = '/admin';
			} else {
				const data = await res.json().catch(() => ({}));
				error = data.error || tr('auth.reset.failed');
			}
		} catch {
			error = tr('auth.reset.retry');
		} finally {
			submitting = false;
		}
	}
</script>

<svelte:head><title>{$t('auth.reset.headTitle')}</title></svelte:head>

<div class="flex min-h-screen items-center justify-center bg-muted/30 p-6">
	<div class="w-full max-w-sm">
		<div class="mb-8 text-center">
			<h1 class="text-xl font-semibold tracking-tight">{$t('auth.reset.title')}</h1>
			<p class="mt-2 text-sm text-muted-foreground">{$t('auth.reset.subtitle')}</p>
		</div>

		{#if error && !token}
			<div class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>
			<p class="mt-4 text-center text-sm">
				<a href="/admin/forgot-password" class="text-muted-foreground hover:underline"
					>{$t('auth.reset.requestNew')}</a
				>
			</p>
		{:else}
			<form onsubmit={submit} class="space-y-4">
				<div class="space-y-1.5">
					<Label for="password">{$t('auth.reset.password')}</Label>
					<Input
						id="password"
						type="password"
						autocomplete="new-password"
						bind:value={password}
						required
					/>
				</div>
				<div class="space-y-1.5">
					<Label for="confirm">{$t('auth.reset.confirm')}</Label>
					<Input
						id="confirm"
						type="password"
						autocomplete="new-password"
						bind:value={confirm}
						required
					/>
				</div>
				{#if error}
					<div class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>
				{/if}
				<Button type="submit" class="h-11 w-full" disabled={submitting || !token}>
					{submitting ? $t('auth.reset.submitting') : $t('auth.reset.submit')}
				</Button>
			</form>
		{/if}
	</div>
</div>
