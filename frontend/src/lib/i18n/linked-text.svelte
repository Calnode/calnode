<script lang="ts">
	import { splitLinked } from './catalog';

	export type LinkedHref = {
		href: string;
		text: string;
		target?: string;
		rel?: string;
		class?: string;
	};

	let {
		text,
		links
	}: {
		text: string;
		links: Record<string, LinkedHref>;
	} = $props();

	const segments = $derived(splitLinked(text, Object.keys(links)));
</script>

{#each segments as seg, i (i)}
	{#if seg.type === 'text'}{seg.value}{:else}{@const link = links[seg.name]}{#if link}<a href={link.href} target={link.target} rel={link.rel} class={link.class}>{link.text}</a>{:else}{'{' + seg.name + '}'}{/if}{/if}
{/each}
