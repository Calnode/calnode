<script lang="ts">
	import { Toaster as Sonner, type ToasterProps } from "svelte-sonner";

	let { ...restProps }: ToasterProps = $props();
</script>

<Sonner theme="light" class="toaster group" richColors {...restProps} />
