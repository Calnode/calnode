# Frontend testing

## Visual smoke tests (`pnpm test:visual`)

`src/visual-smoke.test.ts` mounts shadcn-svelte components in a **real browser**
(Vitest 4 browser mode + Playwright/Chromium) and asserts their **computed
styles** — e.g. a checked `Switch` is filled with `--primary`, a Calendar's
selected day is filled, today is underlined.

### The bug class it guards

shadcn-svelte/bits-ui components expose state through `data-*` attributes and
style it with Tailwind variants. There's a silent failure mode:

- Tailwind v4 has **built-in** `data-*` variants: `data-foo:bg-primary` →
  `.…[data-foo]{…}`.
- bits-ui exposes some states as **presence** attributes (`data-selected`,
  `data-today`, `data-outside-month`, `data-disabled`) — the built-in matches
  these, no config needed.
- But on/off and open/closed come through a single **`data-state="…"`**
  attribute. `[data-checked]`/`[data-open]` do **not** match `data-state="checked"`,
  so the utility renders with a non-matching selector and the component is
  **visually unstyled for that state** — while its logic, events, and
  `data-state` attribute are all correct.

These states are remapped in `src/app.css`:

```css
@custom-variant data-checked   (&:where([data-state="checked"]));
@custom-variant data-unchecked (&:where([data-state="unchecked"]));
@custom-variant data-open      (&:where([data-state="open"]));
@custom-variant data-closed    (&:where([data-state="closed"]));
```

**Why a real-browser test (and not a unit test):** the bug is purely CSS. A
jsdom/Vitest unit test asserting "the switch is checked" **passes with the bug
present** — `data-state` is correct; only the compiled CSS doesn't match. Only a
real browser computing real Tailwind output catches it. (There is no reliable
*static* check either: Tailwind always emits a rule, it just may not match — so a
"is every variant declared?" linter gives false confidence and false positives.)

### When to run

After changing anything that affects component styling:

- `src/lib/components/ui/**` (adding/upgrading shadcn-svelte components)
- `src/app.css` (variants, theme tokens)
- bumping `bits-ui`, `shadcn-svelte`, `tailwindcss`, or `vite`

If you add a new stateful shadcn component, add a case to `visual-smoke.test.ts`.

### Adding a case

Mount the component checked/active, then compare computed style to the resolved
token via the `resolveColor('var(--primary)')` probe helper (exact, color-space
agnostic). Query the element that carries the styling classes — for the Calendar
that's the Day button (`[data-bits-day]`), not the parent `<td>` cell.

## Toolchain

- `vitest.config.ts` is standalone (bare `svelte()` + `tailwindcss()`, not
  `sveltekit()`), so components mount in isolation with the real Tailwind
  pipeline. It is **not** the app's `vite.config.ts`.
- `test-setup.ts` imports `app.css` (so real utilities compile) and disables
  transitions/animations (so `getComputedStyle` reads final values).
- Requires the Chromium binary: `pnpm exec playwright install chromium`
  (already downloaded locally; CI must run this).
