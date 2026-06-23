<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Switch } from '$lib/components/ui/switch';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';

	type Tracking = {
		head_html: string;
		csp_allow: string;
		datalayer_enabled: boolean;
		datalayer_fields: string[];
		available_fields: string[];
	};

	const fieldLabels: Record<string, string> = {
		booking_id: 'Booking reference', event_type_slug: 'Event type slug', event_type_name: 'Event type name',
		start_at: 'Start time', end_at: 'End time', status: 'Status', location: 'Location',
		host_name: 'Host name', attendee_name: 'Attendee name', attendee_email: 'Attendee email',
		attendee_timezone: 'Attendee timezone', answers: 'Intake answers',
		value: 'Revenue / amount', currency: 'Currency', is_paid: 'Paid flag', transaction_id: 'Transaction ID'
	};
	const piiFields = new Set(['attendee_name', 'attendee_email', 'attendee_timezone', 'answers']);

	let loading = $state(true);
	let saving = $state(false);
	let headHtml = $state('');
	let cspAllow = $state('');
	let dlEnabled = $state(false);
	let dlFields = $state<string[]>([]);
	let availableFields = $state<string[]>([]);

	onMount(async () => {
		try {
			const t = await api.get<Tracking>('/v1/settings/tracking');
			headHtml = t.head_html ?? '';
			cspAllow = t.csp_allow ?? '';
			dlEnabled = t.datalayer_enabled;
			dlFields = t.datalayer_fields ?? [];
			availableFields = t.available_fields ?? [];
		} catch (e: any) {
			toast.error(e.message || 'Could not load tracking settings');
		} finally {
			loading = false;
		}
	});

	function toggleField(key: string) {
		dlFields = dlFields.includes(key) ? dlFields.filter((f) => f !== key) : [...dlFields, key];
	}

	async function save() {
		saving = true;
		try {
			const t = await api.patch<Tracking>('/v1/settings/tracking', {
				head_html: headHtml,
				csp_allow: cspAllow,
				datalayer_enabled: dlEnabled,
				datalayer_fields: dlFields
			});
			dlFields = t.datalayer_fields ?? [];
			toast.success('Tracking settings saved');
		} catch (e: any) {
			toast.error(e.message || 'Could not save tracking settings');
		} finally {
			saving = false;
		}
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !saving)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">Admin access required.</p>
{:else if loading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else}
	<div class="max-w-2xl space-y-6">
		<!-- Code injection -->
		<div class="rounded-lg border bg-card p-6">
			<h2 class="text-sm font-semibold">Code injection (head)</h2>
			<p class="mt-0.5 text-xs text-muted-foreground">
				Raw HTML/JS injected into the &lt;head&gt; of your public booking and manage pages — for
				Google Tag Manager, GA4, Meta Pixel, etc. It runs on visitors' browsers; only admins can set it.
			</p>
			<div class="mt-4 space-y-1.5">
				<Label for="head-html">&lt;head&gt; HTML</Label>
				<Textarea id="head-html" bind:value={headHtml} rows={8} class="font-mono text-xs"
					placeholder="<!-- Paste your GTM / GA4 / Meta Pixel snippet here -->" />
			</div>
			<div class="mt-4 space-y-1.5">
				<Label for="csp-allow">Allowed origins <span class="font-normal text-muted-foreground">(optional)</span></Label>
				<Input id="csp-allow" bind:value={cspAllow}
					placeholder="https://www.googletagmanager.com https://*.google-analytics.com" />
				<p class="text-xs text-muted-foreground">
					Leave blank to allow any <code class="rounded bg-muted px-1">https:</code> origin while injection is
					active — simplest, and GTM-managed tags just work. Fill in to lock the page's CSP to only these
					origins (space-separated); tags from other domains will then be blocked.
				</p>
			</div>
		</div>

		<!-- dataLayer events -->
		<div class="rounded-lg border bg-card p-6">
			<div class="flex items-start justify-between gap-4">
				<div>
					<h2 class="text-sm font-semibold">dataLayer events</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">
						Push <code class="rounded bg-muted px-1">calnode_booking_confirmed</code> /
						<code class="rounded bg-muted px-1">_cancelled</code> /
						<code class="rounded bg-muted px-1">_rescheduled</code> into
						<code class="rounded bg-muted px-1">window.dataLayer</code> so GTM can trigger on them.
					</p>
				</div>
				<Switch bind:checked={dlEnabled} />
			</div>
			{#if dlEnabled}
				<div class="mt-4 space-y-2">
					<p class="text-xs font-medium text-muted-foreground">
						Fields to include — untick anything you don't want exposed to the browser / GTM.
					</p>
					<div class="grid grid-cols-2 gap-x-4 gap-y-1">
						{#each availableFields as key}
							<label class="flex cursor-pointer items-center gap-2 font-mono text-sm">
								<Checkbox checked={dlFields.includes(key)} onCheckedChange={() => toggleField(key)} />
								<span>{fieldLabels[key] ?? key}{#if piiFields.has(key)}<span class="ml-1 text-[10px] font-medium uppercase text-amber-600">PII</span>{/if}</span>
							</label>
						{/each}
					</div>
				</div>
			{/if}
		</div>

		<Button onclick={save} disabled={saving}>{saving ? 'Saving…' : 'Save'}</Button>
	</div>
{/if}
