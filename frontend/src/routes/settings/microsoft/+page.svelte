<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { toast } from 'svelte-sonner';

	type MicrosoftSettings = {
		client_id: string;
		client_secret_set: boolean;
		tenant_id: string;
		allowed_signup_domains: string;
		configured: boolean;
		base_url: string;
	};

	let loading = $state(true);
	let saving = $state(false);
	let clientId = $state('');
	let clientSecret = $state('');
	let clientSecretSet = $state(false);
	let tenantId = $state('');
	let allowedDomains = $state('');
	let baseUrl = $state('');
	let configured = $state(false);

	const authRedirectUri = $derived(
		`${(baseUrl || (typeof window !== 'undefined' ? window.location.origin : '')).replace(/\/$/, '')}/v1/auth/microsoft/callback`
	);
	const calRedirectUri = $derived(
		`${(baseUrl || (typeof window !== 'undefined' ? window.location.origin : '')).replace(/\/$/, '')}/v1/calendar/microsoft/callback`
	);

	onMount(async () => {
		try {
			const res = await api.get<MicrosoftSettings>('/v1/settings/microsoft');
			clientId = res.client_id ?? '';
			clientSecretSet = !!res.client_secret_set;
			tenantId = res.tenant_id ?? '';
			allowedDomains = res.allowed_signup_domains ?? '';
			baseUrl = res.base_url ?? '';
			configured = !!res.configured;
		} catch (e: any) {
			toast.error(e.message || 'Failed to load Microsoft OAuth settings');
		} finally {
			loading = false;
		}
	});

	async function save(e: SubmitEvent) {
		e.preventDefault();
		saving = true;
		try {
			const res = await api.put<MicrosoftSettings>('/v1/settings/microsoft', {
				client_id: clientId.trim(),
				client_secret: clientSecret,
				tenant_id: tenantId.trim(),
				allowed_signup_domains: allowedDomains.trim()
			});
			clientId = res.client_id ?? '';
			clientSecret = '';
			clientSecretSet = !!res.client_secret_set;
			tenantId = res.tenant_id ?? '';
			allowedDomains = res.allowed_signup_domains ?? '';
			configured = !!res.configured;
			toast.success('Microsoft OAuth settings saved');
		} catch (err: any) {
			toast.error(err.message || 'Failed to save Microsoft OAuth settings');
		} finally {
			saving = false;
		}
	}

	async function copyUri(uri: string) {
		try {
			await navigator.clipboard.writeText(uri);
			toast.success('Redirect URI copied to clipboard');
		} catch {
			toast.error('Unable to copy automatically');
		}
	}
</script>

<svelte:head><title>Microsoft OAuth — Settings</title></svelte:head>

<div class="max-w-xl space-y-6">
	<div>
		<h2 class="text-lg font-semibold">Microsoft OAuth</h2>
		<p class="mt-1 text-sm text-muted-foreground">
			Configure Microsoft Entra ID (Azure AD) for single sign-on and Microsoft 365 calendar sync.
		</p>
	</div>

	{#if loading}
		<p class="text-sm text-muted-foreground">Loading...</p>
	{:else}
		<div class="rounded-lg border bg-card p-6">
			<div class="mb-5 flex items-center justify-between">
				<span class="text-sm font-medium">Connection Status</span>
				<span class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium {configured ? 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300' : 'bg-muted text-muted-foreground'}">
					<span class="h-1.5 w-1.5 rounded-full {configured ? 'bg-emerald-500' : 'bg-muted-foreground'}"></span>
					{configured ? 'Configured' : 'Not configured'}
				</span>
			</div>

			<form onsubmit={save} class="space-y-4">
				<div class="space-y-1.5">
					<Label for="ms-client-id">Client ID</Label>
					<Input id="ms-client-id" bind:value={clientId} placeholder="00000000-0000-0000-0000-000000000000" />
				</div>

				<div class="space-y-1.5">
					<Label for="ms-client-secret">Client Secret</Label>
					<Input
						id="ms-client-secret"
						type="password"
						bind:value={clientSecret}
						placeholder={clientSecretSet ? '•••••••• (leave blank to keep existing)' : 'Enter client secret'}
					/>
				</div>

				<div class="space-y-1.5">
					<Label for="ms-tenant-id">Tenant ID (optional — defaults to common)</Label>
					<Input id="ms-tenant-id" bind:value={tenantId} placeholder="common" />
					<p class="text-xs text-muted-foreground">
						Leave blank or enter <code class="rounded bg-muted px-1 py-0.5">common</code> to allow multi-tenant Microsoft accounts. Enter your Directory (Tenant) ID to restrict SSO to a single Entra ID tenant.
					</p>
				</div>

				<div class="space-y-1.5">
					<Label for="ms-domains">Allowed SSO Domains (optional)</Label>
					<Input id="ms-domains" bind:value={allowedDomains} placeholder="example.com, subsidiary.com" />
					<p class="text-xs text-muted-foreground">
						Comma-separated list of email domains allowed to auto-provision accounts on Microsoft SSO login.
					</p>
				</div>

				<div class="space-y-2 rounded-md border bg-muted/40 p-3">
					<p class="text-xs font-medium">OAuth Redirect URIs</p>
					<div class="flex items-center justify-between gap-2">
						<code class="truncate text-xs text-muted-foreground">{authRedirectUri}</code>
						<Button type="button" variant="outline" size="sm" class="h-7 shrink-0 px-2 text-xs" onclick={() => copyUri(authRedirectUri)}>Copy</Button>
					</div>
					<div class="flex items-center justify-between gap-2">
						<code class="truncate text-xs text-muted-foreground">{calRedirectUri}</code>
						<Button type="button" variant="outline" size="sm" class="h-7 shrink-0 px-2 text-xs" onclick={() => copyUri(calRedirectUri)}>Copy</Button>
					</div>
				</div>

				<Button type="submit" disabled={saving}>
					{saving ? 'Saving...' : 'Save Microsoft OAuth'}
				</Button>
			</form>
		</div>
	{/if}
</div>
