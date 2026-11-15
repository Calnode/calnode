<script lang="ts">
  import { onMount } from "svelte";
  import { api, type Question } from "$lib/api";
  import { Button, buttonVariants } from "$lib/components/ui/button";
  import { ConfirmDialog } from "$lib/components/ui/confirm-dialog";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import { Switch } from "$lib/components/ui/switch";
  import { Textarea } from "$lib/components/ui/textarea";
  import * as Select from "$lib/components/ui/select";
  import * as Tooltip from "$lib/components/ui/tooltip";
  import * as Dialog from "$lib/components/ui/dialog";
  import { toast } from "svelte-sonner";
  import { t, tr } from "$lib/i18n";

  let {
    slug,
    allowPhoneCall = false,
    onQuestionsChange,
  }: {
    slug: string;
    allowPhoneCall?: boolean;
    onQuestionsChange?: (items: Question[]) => void;
  } = $props();

  const QUESTION_TYPES = $derived([
    { value: "text", label: $t("eventTypeEditor.questions.types.text") },
    {
      value: "checkbox",
      label: $t("eventTypeEditor.questions.types.checkbox"),
    },
    { value: "select", label: $t("eventTypeEditor.questions.types.select") },
  ]);

  let questions: Question[] = $state([]);
  let qLoading = $state(true);

  let qForm = $state({
    label: "",
    type: "text" as "text" | "select" | "checkbox",
    options: "",
    required: false,
  });
  let qAdding = $state(false);
  let addQOpen = $state(false);

  let editingQId = $state<string | null>(null);
  let deleteQOpen = $state(false);
  let pendingQ = $state<Question | null>(null);
  let editQForm = $state({
    label: "",
    type: "text" as "text" | "select" | "checkbox",
    options: "",
    required: false,
  });
  let qSaving = $state(false);

  async function loadQuestions() {
    try {
      const res = await api.get<{ items: Question[] }>(
        `/v1/event-types/${slug}/questions/admin`,
      );
      questions = (res.items ?? []).sort((a, b) => a.position - b.position);
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.questions.error.load"));
    } finally {
      qLoading = false;
    }
  }

  // This panel is only mounted once the parent's "Questions" tab is selected
  // ({#if activeTab === 'questions'}), so it must fetch its own data on mount
  // rather than relying on the parent to trigger a load — previously nothing
  // called loadQuestions() until a mutation (add/save/delete) did so as a
  // side effect, which is why existing questions never showed up until you
  // added a new one.
  onMount(loadQuestions);

  $effect(() => {
    onQuestionsChange?.(questions);
  });

  function optionsArray(raw: string): string[] {
    return raw
      .split("\n")
      .map((s) => s.trim())
      .filter(Boolean);
  }

  async function addQuestion() {
    if (!qForm.label.trim()) {
      toast.error(tr("eventTypeEditor.questions.validation.label"));
      return;
    }
    if (qForm.type === "select" && optionsArray(qForm.options).length === 0) {
      toast.error(tr("eventTypeEditor.questions.validation.options"));
      return;
    }
    qAdding = true;
    try {
      await api.post(`/v1/event-types/${slug}/questions`, {
        label: qForm.label.trim(),
        type: qForm.type,
        options:
          qForm.type === "select" ? optionsArray(qForm.options) : undefined,
        required: qForm.required,
      });
      qForm = { label: "", type: "text", options: "", required: false };
      await loadQuestions();
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.questions.error.add"));
    } finally {
      qAdding = false;
    }
  }

  function startEditQ(q: Question) {
    editingQId = q.id;
    editQForm = {
      label: q.label,
      type: q.type,
      options: (q.options ?? []).join("\n"),
      required: q.required,
    };
  }

  function cancelEditQ() {
    editingQId = null;
  }

  async function saveQuestion(q: Question) {
    if (!editQForm.label.trim()) {
      toast.error(tr("eventTypeEditor.questions.validation.label"));
      return;
    }
    if (
      editQForm.type === "select" &&
      optionsArray(editQForm.options).length === 0
    ) {
      toast.error(tr("eventTypeEditor.questions.validation.optionsShort"));
      return;
    }
    qSaving = true;
    try {
      await api.patch(`/v1/event-types/${slug}/questions/${q.id}`, {
        label: editQForm.label.trim(),
        type: editQForm.type,
        options:
          editQForm.type === "select" ? optionsArray(editQForm.options) : [],
        required: editQForm.required,
      });
      editingQId = null;
      await loadQuestions();
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.questions.error.save"));
    } finally {
      qSaving = false;
    }
  }

  function deleteQuestion(q: Question) {
    pendingQ = q;
    deleteQOpen = true;
  }

  async function doDeleteQuestion() {
    if (!pendingQ) return;
    try {
      await api.del(`/v1/event-types/${slug}/questions/${pendingQ.id}`);
      await loadQuestions();
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.questions.error.delete"));
    }
  }

  function typeLabel(value: string): string {
    return QUESTION_TYPES.find((t) => t.value === value)?.label ?? value;
  }

  let dragIdx = $state<number | null>(null);
  let reordering = $state(false);

  async function reorderQuestions(fromIdx: number, toIdx: number) {
    if (
      fromIdx === toIdx ||
      fromIdx < 0 ||
      toIdx < 0 ||
      toIdx >= questions.length ||
      reordering
    )
      return;
    const next = [...questions];
    const [moved] = next.splice(fromIdx, 1);
    next.splice(toIdx, 0, moved);
    questions = next.map((item, idx) => ({ ...item, position: idx }));
    reordering = true;
    try {
      await Promise.all(
        questions.map((item, idx) =>
          api.patch(`/v1/event-types/${slug}/questions/${item.id}`, {
            position: idx,
          }),
        ),
      );
    } catch (e: any) {
      toast.error(e.message || "Failed to reorder questions");
      await loadQuestions();
    } finally {
      reordering = false;
    }
  }
</script>

<ConfirmDialog
  bind:open={deleteQOpen}
  title={$t("eventTypeEditor.questions.confirm.title")}
  description={pendingQ
    ? $t("eventTypeEditor.questions.confirm.description", {
        label: pendingQ.label,
      })
    : ""}
  confirmText={$t("common.remove")}
  destructive
  onConfirm={doDeleteQuestion}
/>

<!-- Booking Form -->
<div class="space-y-4">
  <h2
    class="mb-1 text-sm font-semibold uppercase tracking-wider text-muted-foreground"
  >
    Booking form
  </h2>
  <p class="mb-3 text-sm text-muted-foreground">
    {$t("eventTypeEditor.questions.subtitle")}
  </p>

  <!-- Standard built-in fields -->
  <div class="mb-4 rounded-lg border bg-card divide-y">
    <div class="flex items-center justify-between px-4 py-2.5">
      <div>
        <div class="text-sm font-medium">Your name</div>
        <div class="text-xs text-muted-foreground">Text</div>
      </div>
      <span
        class="rounded-full bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground"
        >Required</span
      >
    </div>
    <div class="flex items-center justify-between px-4 py-2.5">
      <div>
        <div class="text-sm font-medium">Email address</div>
        <div class="text-xs text-muted-foreground">Email</div>
      </div>
      <span
        class="rounded-full bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground"
        >Required</span
      >
    </div>
    {#if allowPhoneCall}
      <div class="flex items-center justify-between px-4 py-2.5">
        <div>
          <div class="text-sm font-medium">Phone number</div>
          <div class="text-xs text-muted-foreground">Phone</div>
        </div>
        <span
          class="rounded-full border px-2 py-0.5 text-[11px] font-medium text-muted-foreground"
          >Optional</span
        >
      </div>
    {/if}
  </div>

  <h3
    class="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground"
  >
    Custom questions
  </h3>

  <div class="rounded-lg border bg-card overflow-hidden">
    {#if qLoading}
      <p class="px-4 py-4 text-sm text-muted-foreground">
        {$t("common.loading")}
      </p>
    {:else if questions.length > 0}
      <!-- Mobile card list (< md) -->
      <div class="divide-y md:hidden">
        {#each questions as q, idx (q.id)}
          {#if editingQId === q.id}
            <div class="bg-muted/20 p-4 space-y-3">
              <div class="space-y-1.5">
                <Label
                  for="m-eq-label-{q.id}"
                  class="text-xs text-muted-foreground"
                  >{$t("eventTypeEditor.questions.label")}</Label
                >
                <Input id="m-eq-label-{q.id}" bind:value={editQForm.label} />
              </div>
              <div class="space-y-1.5">
                <Label
                  for="m-eq-type-{q.id}"
                  class="text-xs text-muted-foreground"
                  >{$t("eventTypeEditor.questions.table.type")}</Label
                >
                <Select.Root
                  type="single"
                  value={editQForm.type}
                  onValueChange={(v) => {
                    if (v) editQForm.type = v as "text" | "select" | "checkbox";
                  }}
                >
                  <Select.Trigger id="m-eq-type-{q.id}" class="w-full">
                    {QUESTION_TYPES.find((t) => t.value === editQForm.type)
                      ?.label ?? $t("common.select")}
                  </Select.Trigger>
                  <Select.Content>
                    {#each QUESTION_TYPES as qt}
                      <Select.Item value={qt.value} label={qt.label}
                        >{qt.label}</Select.Item
                      >
                    {/each}
                  </Select.Content>
                </Select.Root>
              </div>
              {#if editQForm.type === "select"}
                <div class="space-y-1.5">
                  <Label
                    for="m-eq-options-{q.id}"
                    class="text-xs text-muted-foreground"
                    >{$t("eventTypeEditor.questions.options")}</Label
                  >
                  <Textarea
                    id="m-eq-options-{q.id}"
                    bind:value={editQForm.options}
                    rows={3}
                  />
                </div>
              {/if}
              <div
                class="flex items-center justify-between rounded-md border px-3 py-2"
              >
                <Label
                  for="m-eq-required-{q.id}"
                  class="cursor-pointer font-normal"
                  >{$t("eventTypeEditor.questions.table.required")}</Label
                >
                <Switch
                  id="m-eq-required-{q.id}"
                  bind:checked={editQForm.required}
                />
              </div>
              <div class="flex gap-2">
                <Button
                  size="sm"
                  onclick={() => saveQuestion(q)}
                  disabled={qSaving}
                  >{qSaving ? $t("common.saving") : $t("common.save")}</Button
                >
                <Button size="sm" variant="outline" onclick={cancelEditQ}
                  >{$t("common.cancel")}</Button
                >
              </div>
            </div>
          {:else}
            <div class="p-4 space-y-2">
              <div class="flex items-start justify-between gap-2">
                <div class="min-w-0">
                  <div class="font-medium break-words">
                    {idx + 1}. {q.label}
                  </div>
                  <div class="mt-1 flex flex-wrap items-center gap-1.5">
                    <span
                      class="inline-flex items-center rounded-md bg-secondary px-2 py-0.5 text-xs font-medium text-secondary-foreground"
                      >{typeLabel(q.type)}</span
                    >
                    <span class="text-xs text-muted-foreground"
                      >{q.required ? "Required" : "Optional"}</span
                    >
                  </div>
                </div>
              </div>
              <div class="flex flex-wrap items-center gap-1 pt-1">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  class="h-8 px-2 text-xs"
                  disabled={idx === 0 || reordering}
                  onclick={() => reorderQuestions(idx, idx - 1)}>↑</Button
                >
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  class="h-8 px-2 text-xs"
                  disabled={idx === questions.length - 1 || reordering}
                  onclick={() => reorderQuestions(idx, idx + 1)}>↓</Button
                >
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  class="h-8 text-xs"
                  onclick={() => startEditQ(q)}>Edit</Button
                >
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  class="h-8 text-xs text-destructive hover:text-destructive"
                  onclick={() => deleteQuestion(q)}>Remove</Button
                >
              </div>
            </div>
          {/if}
        {/each}
      </div>

      <!-- Desktop table (>= md) -->
      <table class="hidden md:table w-full text-sm">
        <thead>
          <tr class="border-b">
            <th
              class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground"
              >{$t("eventTypeEditor.questions.table.number")}</th
            >
            <th
              class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground"
              >{$t("eventTypeEditor.questions.table.question")}</th
            >
            <th
              class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground"
              >{$t("eventTypeEditor.questions.table.type")}</th
            >
            <th
              class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground"
              >{$t("eventTypeEditor.questions.table.required")}</th
            >
            <th class="px-4 pb-3 pt-3"></th>
          </tr>
        </thead>
        <tbody class="divide-y">
          {#each questions as q, idx (q.id)}
            {#if editingQId === q.id}
              <tr class="bg-muted/20">
                <td class="px-4 py-3 text-muted-foreground">{q.position + 1}</td
                >
                <td colspan="3" class="px-4 py-3">
                  <div class="grid grid-cols-2 gap-3">
                    <div class="space-y-1.5">
                      <Label
                        for="eq-label-{q.id}"
                        class="text-xs text-muted-foreground"
                        >{$t("eventTypeEditor.questions.label")}</Label
                      >
                      <Input
                        id="eq-label-{q.id}"
                        bind:value={editQForm.label}
                      />
                    </div>
                    <div class="space-y-1.5">
                      <Label
                        for="eq-type-{q.id}"
                        class="text-xs text-muted-foreground"
                        >{$t("eventTypeEditor.questions.table.type")}</Label
                      >
                      <Select.Root
                        type="single"
                        value={editQForm.type}
                        onValueChange={(v) => {
                          if (v)
                            editQForm.type = v as
                              | "text"
                              | "select"
                              | "checkbox";
                        }}
                      >
                        <Select.Trigger id="eq-type-{q.id}" class="w-full">
                          {QUESTION_TYPES.find(
                            (t) => t.value === editQForm.type,
                          )?.label ?? $t("common.select")}
                        </Select.Trigger>
                        <Select.Content>
                          {#each QUESTION_TYPES as qt}
                            <Select.Item value={qt.value} label={qt.label}
                              >{qt.label}</Select.Item
                            >
                          {/each}
                        </Select.Content>
                      </Select.Root>
                    </div>
                    {#if editQForm.type === "select"}
                      <div class="col-span-2 space-y-1.5">
                        <Label
                          for="eq-options-{q.id}"
                          class="text-xs text-muted-foreground"
                          >{$t("eventTypeEditor.questions.options")}</Label
                        >
                        <Textarea
                          id="eq-options-{q.id}"
                          bind:value={editQForm.options}
                          rows={3}
                        />
                      </div>
                    {/if}
                    <div
                      class="col-span-2 flex items-center justify-between rounded-md border px-3 py-2"
                    >
                      <Label
                        for="eq-required-{q.id}"
                        class="cursor-pointer font-normal"
                        >{$t("eventTypeEditor.questions.table.required")}</Label
                      >
                      <Switch
                        id="eq-required-{q.id}"
                        bind:checked={editQForm.required}
                      />
                    </div>
                  </div>
                </td>
                <td class="px-4 py-3 align-top">
                  <div class="flex items-center justify-end gap-2 pt-5">
                    <Button
                      size="sm"
                      onclick={() => saveQuestion(q)}
                      disabled={qSaving}
                    >
                      {qSaving ? $t("common.saving") : $t("common.save")}
                    </Button>
                    <Button size="sm" variant="outline" onclick={cancelEditQ}
                      >{$t("common.cancel")}</Button
                    >
                  </div>
                </td>
              </tr>
            {:else}
              <tr
                draggable="true"
                ondragstart={() => (dragIdx = idx)}
                ondragover={(e) => e.preventDefault()}
                ondrop={(e) => {
                  e.preventDefault();
                  if (dragIdx !== null) reorderQuestions(dragIdx, idx);
                  dragIdx = null;
                }}
                ondragend={() => (dragIdx = null)}
                class="transition-colors hover:bg-muted/30 {dragIdx === idx
                  ? 'opacity-50'
                  : ''}"
              >
                <td class="px-4 py-3 text-muted-foreground">
                  <div class="flex items-center gap-1.5">
                    <span
                      class="cursor-grab text-muted-foreground/60 hover:text-foreground"
                      title="Drag to reorder"
                    >
                      <svg
                        xmlns="http://www.w3.org/2000/svg"
                        width="14"
                        height="14"
                        viewBox="0 0 24 24"
                        fill="none"
                        stroke="currentColor"
                        stroke-width="2"
                        stroke-linecap="round"
                        stroke-linejoin="round"
                        ><circle cx="9" cy="5" r="1" /><circle
                          cx="9"
                          cy="12"
                          r="1"
                        /><circle cx="9" cy="19" r="1" /><circle
                          cx="15"
                          cy="5"
                          r="1"
                        /><circle cx="15" cy="12" r="1" /><circle
                          cx="15"
                          cy="19"
                          r="1"
                        /></svg
                      >
                    </span>
                    <span>{idx + 1}</span>
                  </div>
                </td>
                <td class="px-4 py-3">
                  <div class="font-medium">{q.label}</div>
                  {#if q.type === "select" && q.options?.length}
                    <div class="mt-0.5 text-xs text-muted-foreground">
                      {q.options.join(", ")}
                    </div>
                  {/if}
                </td>
                <td class="px-4 py-3">
                  <span
                    class="inline-flex items-center rounded-md bg-secondary px-2 py-0.5 text-xs font-medium text-secondary-foreground"
                  >
                    {typeLabel(q.type)}
                  </span>
                </td>
                <td class="px-4 py-3 text-muted-foreground"
                  >{q.required ? "Required" : "Optional"}</td
                >
                <td class="px-4 py-3">
                  <Tooltip.Provider>
                    <div class="flex items-center justify-end gap-1">
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        disabled={idx === 0 || reordering}
                        aria-label="Move up"
                        onclick={() => reorderQuestions(idx, idx - 1)}
                      >
                        <svg
                          xmlns="http://www.w3.org/2000/svg"
                          width="14"
                          height="14"
                          viewBox="0 0 24 24"
                          fill="none"
                          stroke="currentColor"
                          stroke-width="2"
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          ><polyline points="18 15 12 9 6 15" /></svg
                        >
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        disabled={idx === questions.length - 1 || reordering}
                        aria-label="Move down"
                        onclick={() => reorderQuestions(idx, idx + 1)}
                      >
                        <svg
                          xmlns="http://www.w3.org/2000/svg"
                          width="14"
                          height="14"
                          viewBox="0 0 24 24"
                          fill="none"
                          stroke="currentColor"
                          stroke-width="2"
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          ><polyline points="6 9 12 15 18 9" /></svg
                        >
                      </Button>
                      <Tooltip.Root>
                        <Tooltip.Trigger
                          class={buttonVariants({
                            variant: "ghost",
                            size: "icon",
                          })}
                          onclick={() => startEditQ(q)}
                        >
                          <!-- Pencil/edit icon -->
                          <svg
                            xmlns="http://www.w3.org/2000/svg"
                            width="16"
                            height="16"
                            viewBox="0 0 24 24"
                            fill="none"
                            stroke="currentColor"
                            stroke-width="2"
                            stroke-linecap="round"
                            stroke-linejoin="round"
                            ><path
                              d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"
                            /><path
                              d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"
                            /></svg
                          >
                        </Tooltip.Trigger>
                        <Tooltip.Content
                          >{$t(
                            "eventTypeEditor.questions.tooltip.edit",
                          )}</Tooltip.Content
                        >
                      </Tooltip.Root>
                      <Tooltip.Root>
                        <Tooltip.Trigger
                          class={buttonVariants({
                            variant: "ghost",
                            size: "icon",
                          })}
                          onclick={() => deleteQuestion(q)}
                        >
                          <!-- Trash icon -->
                          <svg
                            xmlns="http://www.w3.org/2000/svg"
                            width="16"
                            height="16"
                            viewBox="0 0 24 24"
                            fill="none"
                            stroke="currentColor"
                            stroke-width="2"
                            stroke-linecap="round"
                            stroke-linejoin="round"
                            ><polyline points="3 6 5 6 21 6" /><path
                              d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"
                            /><path d="M10 11v6" /><path d="M14 11v6" /><path
                              d="M9 6V4h6v2"
                            /></svg
                          >
                        </Tooltip.Trigger>
                        <Tooltip.Content
                          >{$t(
                            "eventTypeEditor.questions.tooltip.remove",
                          )}</Tooltip.Content
                        >
                      </Tooltip.Root>
                    </div>
                  </Tooltip.Provider>
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    {:else}
      <p class="px-4 py-4 text-sm text-muted-foreground">
        {$t("eventTypeEditor.questions.empty")}
      </p>
    {/if}

    <!-- Add question button + modal -->
    <div class="border-t px-4 py-3">
      <Button
        type="button"
        variant="outline"
        size="sm"
        onclick={() => (addQOpen = true)}
      >
        + {$t("eventTypeEditor.questions.add")}
      </Button>
    </div>
  </div>
</div>

<Dialog.Root bind:open={addQOpen}>
  <Dialog.Content class="sm:max-w-md">
    <Dialog.Header>
      <Dialog.Title>{$t("eventTypeEditor.questions.add")}</Dialog.Title>
      <Dialog.Description
        >Add a custom question to the booking form.</Dialog.Description
      >
    </Dialog.Header>
    <div class="space-y-4 py-2">
      <div class="space-y-1.5">
        <Label for="q-type">{$t("eventTypeEditor.questions.table.type")}</Label>
        <Select.Root
          type="single"
          value={qForm.type}
          onValueChange={(v) => {
            if (v) qForm.type = v as "text" | "select" | "checkbox";
          }}
        >
          <Select.Trigger id="q-type" class="w-full">
            {QUESTION_TYPES.find((t) => t.value === qForm.type)?.label ??
              $t("common.select")}
          </Select.Trigger>
          <Select.Content>
            {#each QUESTION_TYPES as qt}
              <Select.Item value={qt.value} label={qt.label}
                >{qt.label}</Select.Item
              >
            {/each}
          </Select.Content>
        </Select.Root>
      </div>
      <div class="space-y-1.5">
        <Label for="q-label">{$t("eventTypeEditor.questions.label")}</Label>
        <Input
          id="q-label"
          bind:value={qForm.label}
          placeholder={$t("eventTypeEditor.questions.labelPlaceholder")}
        />
      </div>
      {#if qForm.type === "select"}
        <div class="space-y-1.5">
          <Label for="q-options"
            >{$t("eventTypeEditor.questions.options")}</Label
          >
          <Textarea
            id="q-options"
            bind:value={qForm.options}
            rows={3}
            placeholder={$t("eventTypeEditor.questions.optionsPlaceholder")}
          />
        </div>
      {/if}
      <div
        class="flex items-center justify-between rounded-md border px-3 py-2.5"
      >
        <Label for="q-required" class="cursor-pointer font-normal"
          >{$t("eventTypeEditor.questions.table.required")}</Label
        >
        <Switch id="q-required" bind:checked={qForm.required} />
      </div>
    </div>
    <Dialog.Footer>
      <Button
        type="button"
        variant="outline"
        onclick={() => (addQOpen = false)}
      >
        {$t("common.cancel")}
      </Button>
      <Button
        type="button"
        disabled={qAdding}
        onclick={async () => {
          await addQuestion();
          if (!qForm.label) addQOpen = false;
        }}
      >
        {qAdding
          ? $t("eventTypeEditor.questions.adding")
          : $t("eventTypeEditor.questions.add")}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
