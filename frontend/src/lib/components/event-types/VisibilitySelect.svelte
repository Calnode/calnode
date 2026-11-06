<script lang="ts">
	import type { EventType } from '$lib/api';
	import { Label } from '$lib/components/ui/label';
	import * as Select from '$lib/components/ui/select';
	import { t } from '$lib/i18n';

	let { value = $bindable('public') }: { value?: EventType['visibility'] } = $props();
</script>

<div class="space-y-1.5">
	<Label for="et-visibility">{$t('eventTypes.visibility.label')}</Label>
	<Select.Root type="single" bind:value>
		<Select.Trigger id="et-visibility" class="w-full" aria-describedby="et-visibility-hint">
			{$t(`eventTypes.visibility.${value}`)}
		</Select.Trigger>
		<Select.Content>
			<Select.Item value="public">{$t('eventTypes.visibility.public')}</Select.Item>
			<Select.Item value="unlisted">{$t('eventTypes.visibility.unlisted')}</Select.Item>
			<Select.Item value="private">{$t('eventTypes.visibility.private')}</Select.Item>
		</Select.Content>
	</Select.Root>
	<p id="et-visibility-hint" class="text-xs text-muted-foreground">{$t(`eventTypes.visibility.hint.${value}`)}</p>
</div>
