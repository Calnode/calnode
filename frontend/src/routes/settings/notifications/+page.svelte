<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type User } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Label } from '$lib/components/ui/label';
	import { Switch } from '$lib/components/ui/switch';

	let loading = $state(true);
	let saving = $state(false);
	let saved = $state(false);
	let error = $state('');

	let notify_confirmation = $state(true);
	let notify_cancellation = $state(true);
	let notify_reschedule = $state(true);
	let notify_reminder = $state(true);
	let notify_host_booking = $state(true);
	let notify_host_cancel = $state(true);
	let notify_host_reschedule = $state(true);

	onMount(async () => {
		try {
			const user = await api.get<User>('/v1/users/me');
			notify_confirmation = user.notify_confirmation ?? true;
			notify_cancellation = user.notify_cancellation ?? true;
			notify_reschedule = user.notify_reschedule ?? true;
			notify_reminder = user.notify_reminder ?? true;
			notify_host_booking = user.notify_host_booking ?? true;
			notify_host_cancel = user.notify_host_cancel ?? true;
			notify_host_reschedule = user.notify_host_reschedule ?? true;
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	});

	async function save() {
		saving = true;
		saved = false;
		error = '';
		try {
			const updated = await api.patch<User>('/v1/users/me', {
				notify_confirmation, notify_cancellation, notify_reschedule, notify_reminder,
				notify_host_booking, notify_host_cancel, notify_host_reschedule,
			});
			currentUser.set(updated);
			saved = true;
			setTimeout(() => (saved = false), 3000);
		} catch (e: any) {
			error = e.message;
		} finally {
			saving = false;
		}
	}
</script>

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}
{#if saved}<p class="mb-4 rounded-md bg-green-50 px-3 py-2 text-sm text-green-700">Notification preferences saved.</p>{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else}
	<div class="max-w-lg space-y-4">
		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-1 text-sm font-semibold">Notifications</h2>
			<p class="mb-5 text-xs text-muted-foreground">Control which emails you and your attendees receive.</p>

			<div class="space-y-5">
				<div>
					<p class="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Emails sent to your attendees</p>
					<div class="space-y-3">
						<div class="flex items-center justify-between gap-4">
							<Label for="nc" class="cursor-pointer font-normal">Booking confirmation</Label>
							<Switch id="nc" bind:checked={notify_confirmation} />
						</div>
						<div class="flex items-center justify-between gap-4">
							<Label for="nca" class="cursor-pointer font-normal">Cancellation notice</Label>
							<Switch id="nca" bind:checked={notify_cancellation} />
						</div>
						<div class="flex items-center justify-between gap-4">
							<Label for="nr" class="cursor-pointer font-normal">Reschedule notice</Label>
							<Switch id="nr" bind:checked={notify_reschedule} />
						</div>
						<div class="flex items-center justify-between gap-4">
							<Label for="nrm" class="cursor-pointer font-normal">Reminder emails</Label>
							<Switch id="nrm" bind:checked={notify_reminder} />
						</div>
					</div>
				</div>

				<div class="border-t pt-5">
					<p class="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Emails sent to you</p>
					<div class="space-y-3">
						<div class="flex items-center justify-between gap-4">
							<Label for="nhb" class="cursor-pointer font-normal">New booking received</Label>
							<Switch id="nhb" bind:checked={notify_host_booking} />
						</div>
						<div class="flex items-center justify-between gap-4">
							<Label for="nhc" class="cursor-pointer font-normal">Booking cancelled</Label>
							<Switch id="nhc" bind:checked={notify_host_cancel} />
						</div>
						<div class="flex items-center justify-between gap-4">
							<Label for="nhr" class="cursor-pointer font-normal">Booking rescheduled</Label>
							<Switch id="nhr" bind:checked={notify_host_reschedule} />
						</div>
					</div>
				</div>
			</div>
		</div>

		<Button onclick={save} disabled={saving}>
			{saving ? 'Saving…' : 'Save'}
		</Button>
	</div>
{/if}
