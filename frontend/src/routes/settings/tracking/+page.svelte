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
	import { createAsyncFlag } from '$lib/async-action.svelte';
	import { t, tr } from '$lib/i18n';

	type Tracking = {
		head_html: string;
		csp_allow: string;
		datalayer_enabled: boolean;
		datalayer_fields: string[];
		available_fields: string[];
		gtm_container_id: string;
		ga4_measurement_id: string;
	};

	const FIELD_KEYS = [
		'booking_id', 'event_type_slug', 'event_type_name',
		'start_at', 'end_at', 'status', 'location',
		'host_name', 'attendee_name', 'attendee_email',
		'attendee_timezone', 'answers',
		'value', 'currency', 'is_paid', 'transaction_id'
	] as const;
	const piiFields = new Set(['attendee_name', 'attendee_email', 'attendee_timezone', 'answers']);

	const loadingFlag = createAsyncFlag(true);
	const savingFlag = createAsyncFlag();
	let headHtml = $state('');
	let cspAllow = $state('');
	let dlEnabled = $state(false);
	let dlFields = $state<string[]>([]);
	let availableFields = $state<string[]>([]);
	let gtmId = $state('');
	let ga4Id = $state('');

	onMount(() => loadingFlag.run(async () => {
		const tracking = await api.get<Tracking>('/v1/settings/tracking');
		headHtml = tracking.head_html ?? '';
		cspAllow = tracking.csp_allow ?? '';
		dlEnabled = tracking.datalayer_enabled;
		dlFields = tracking.datalayer_fields ?? [];
		availableFields = tracking.available_fields ?? [];
		gtmId = tracking.gtm_container_id ?? '';
		ga4Id = tracking.ga4_measurement_id ?? '';
	}, tr('settings.tracking.error.load')));

	function toggleField(key: string) {
		dlFields = dlFields.includes(key) ? dlFields.filter((f) => f !== key) : [...dlFields, key];
	}

	async function save() {
		await savingFlag.run(async () => {
			const tracking = await api.patch<Tracking>('/v1/settings/tracking', {
				head_html: headHtml,
				csp_allow: cspAllow,
				datalayer_enabled: dlEnabled,
				datalayer_fields: dlFields,
				gtm_container_id: gtmId.trim(),
				ga4_measurement_id: ga4Id.trim()
			});
			dlFields = tracking.datalayer_fields ?? [];
			gtmId = tracking.gtm_container_id ?? '';
			ga4Id = tracking.ga4_measurement_id ?? '';
			toast.success(tr('settings.tracking.toast.saved'));
		}, tr('settings.tracking.error.save'));
	}

	function fieldLabel(key: string, translate: (k: string) => string): string {
		const path = `settings.tracking.fields.${key}`;
		const hit = translate(path);
		return hit === path ? key : hit;
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !savingFlag.active)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">{$t('common.adminRequired')}</p>
{:else if loadingFlag.active}
	<p class="py-8 text-sm text-muted-foreground">{$t('common.loading')}</p>
{:else}
	<div class="max-w-2xl space-y-6">
		<!-- Native GA4 / GTM -->
		<div class="rounded-lg border bg-card p-6">
			<h2 class="text-sm font-semibold">{$t('settings.tracking.ga.title')}</h2>
			<p class="mt-0.5 text-xs text-muted-foreground">{$t('settings.tracking.ga.subtitle')}</p>
			<div class="mt-4 grid gap-4 sm:grid-cols-2">
				<div class="space-y-1.5">
					<Label for="gtm-id">{$t('settings.tracking.gtm.label')}</Label>
					<Input id="gtm-id" bind:value={gtmId} placeholder={$t('settings.tracking.gtm.placeholder')} class="font-mono" />
					<p class="text-xs text-muted-foreground">{$t('settings.tracking.gtm.hint')}</p>
				</div>
				<div class="space-y-1.5">
					<Label for="ga4-id">{$t('settings.tracking.ga4.label')}</Label>
					<Input id="ga4-id" bind:value={ga4Id} placeholder={$t('settings.tracking.ga4.placeholder')} class="font-mono" />
					<p class="text-xs text-muted-foreground">{$t('settings.tracking.ga4.hint')}</p>
				</div>
			</div>
		</div>

		<!-- Code injection -->
		<div class="rounded-lg border bg-card p-6">
			<h2 class="text-sm font-semibold">{$t('settings.tracking.inject.title')}</h2>
			<p class="mt-0.5 text-xs text-muted-foreground">{$t('settings.tracking.inject.subtitle')}</p>
			<div class="mt-4 space-y-1.5">
				<Label for="head-html">{$t('settings.tracking.inject.headHtml')}</Label>
				<Textarea id="head-html" bind:value={headHtml} rows={8} class="font-mono text-xs"
					placeholder={$t('settings.tracking.inject.headPlaceholder')} />
			</div>
			<div class="mt-4 space-y-1.5">
				<Label for="csp-allow">{$t('settings.tracking.csp.label')} <span class="font-normal text-muted-foreground">{$t('settings.tracking.csp.optional')}</span></Label>
				<Input id="csp-allow" bind:value={cspAllow}
					placeholder={$t('settings.tracking.csp.placeholder')} />
				<p class="text-xs text-muted-foreground">{$t('settings.tracking.csp.hint')}</p>
			</div>
		</div>

		<!-- dataLayer events -->
		<div class="rounded-lg border bg-card p-6">
			<div class="flex items-start justify-between gap-4">
				<div>
					<h2 class="text-sm font-semibold">{$t('settings.tracking.datalayer.title')}</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">{$t('settings.tracking.datalayer.subtitle')}</p>
				</div>
				<Switch bind:checked={dlEnabled} />
			</div>
			{#if dlEnabled}
				<div class="mt-4 space-y-2">
					<p class="text-xs font-medium text-muted-foreground">{$t('settings.tracking.datalayer.fieldsHint')}</p>
					<div class="grid grid-cols-2 gap-x-4 gap-y-1">
						{#each availableFields as key}
							<label class="flex cursor-pointer items-center gap-2 font-mono text-sm">
								<Checkbox checked={dlFields.includes(key)} onCheckedChange={() => toggleField(key)} />
								<span>{fieldLabel(key, $t)}{#if piiFields.has(key)}<span class="ml-1 text-[10px] font-medium uppercase text-amber-600">{$t('settings.tracking.piiBadge')}</span>{/if}</span>
							</label>
						{/each}
					</div>
				</div>
			{/if}
		</div>

		<Button onclick={save} disabled={savingFlag.active}>{savingFlag.active ? $t('common.saving') : $t('common.save')}</Button>
	</div>
{/if}
