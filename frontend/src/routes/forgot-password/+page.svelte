<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { t, tr } from '$lib/i18n';

	let email = $state('');
	let submitting = $state(false);
	let message = $state('');
	let error = $state('');

	const isValidEmail = (e: string) => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(e);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		const addr = email.trim().toLowerCase();
		// Require a valid address up front — otherwise the request silently no-ops
		// (the endpoint returns the same generic message), which looks broken.
		if (!isValidEmail(addr)) {
			error = tr('auth.forgot.invalidEmail');
			return;
		}
		error = '';
		submitting = true;
		try {
			const res = await fetch('/v1/auth/password/forgot', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ email: addr })
			});
			const data = await res.json().catch(() => ({}));
			message =
				data.message ||
				tr('auth.forgot.defaultMessage');
		} catch {
			message = tr('auth.forgot.defaultMessage');
		} finally {
			submitting = false;
		}
	}
</script>

<svelte:head><title>{$t('auth.forgot.headTitle')}</title></svelte:head>

<div class="flex min-h-screen items-center justify-center bg-muted/30 p-6">
	<div class="w-full max-w-sm">
		<div class="mb-8 text-center">
			<h1 class="text-xl font-semibold tracking-tight">{$t('auth.forgot.title')}</h1>
			<p class="mt-2 text-sm text-muted-foreground">
				{$t('auth.forgot.subtitle')}
			</p>
		</div>

		{#if message}
			<div class="rounded-md bg-green-50 px-3 py-2.5 text-sm text-green-700">{message}</div>
			<p class="mt-4 text-center text-sm">
				<a href="/admin/login" class="text-muted-foreground hover:underline">{$t('auth.forgot.back')}</a>
			</p>
		{:else}
			<form onsubmit={submit} class="space-y-4">
				<div class="space-y-1.5">
					<Label for="email">{$t('auth.forgot.email')}</Label>
					<Input
						id="email"
						type="email"
						autocomplete="email"
						placeholder={$t('auth.forgot.placeholder')}
						bind:value={email}
						oninput={() => (error = '')}
						aria-invalid={error ? 'true' : undefined}
						required
					/>
					{#if error}
						<p class="text-xs text-destructive">{error}</p>
					{/if}
				</div>
				<Button type="submit" class="h-11 w-full" disabled={submitting}>
					{submitting ? $t('auth.forgot.submitting') : $t('auth.forgot.submit')}
				</Button>
			</form>
			<p class="mt-4 text-center text-sm">
				<a href="/admin/login" class="text-muted-foreground hover:underline">{$t('auth.forgot.back')}</a>
			</p>
		{/if}
	</div>
</div>
