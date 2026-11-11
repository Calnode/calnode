#!/usr/bin/env node
// Source-level guards for the admin catalogue.
//
// The catalogue tests (src/lib/i18n/i18n.test.ts) prove things about the data: keys line
// up, placeholders match, no baked plurals. They cannot see the 1,270-odd call sites that
// use that data, and they cannot see how it is rendered. That gap is what this checks:
//
//   1. Every literal key passed to $t/tr/trp/trp2 exists in en.json. A typo currently
//      renders the dotted key on screen and every test stays green.
//   2. No catalogue string is rendered with {@html}. Catalogue text is plain text by
//      contract (see catalog.ts); community locale PRs are a real input, so HTML in a
//      catalogue is an injection surface, not a styling convenience.
//
// Runs from the frontend directory: `pnpm check:i18n`, and in CI.
//
// Dynamic keys (template literals, ternaries) are skipped rather than guessed at. They
// are listed at the end so a reviewer can see what is not covered.

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative, extname } from 'node:path';
import { fileURLToPath } from 'node:url';

const frontendDir = fileURLToPath(new URL('..', import.meta.url));
const srcDir = join(frontendDir, 'src');
const cataloguePath = join(srcDir, 'lib/i18n/en.json');

// The catalogue's own module and tests define the helpers; they are not call sites.
const SKIP_DIRS = [join(srcDir, 'lib/i18n')];

function flatten(value, prefix = '', leaves = new Map(), groups = new Set()) {
	if (value && typeof value === 'object' && !Array.isArray(value)) {
		// An object is a real key too: trp/trp2 append `.one`/`.other` themselves, so a
		// plural call passes the group path, not a leaf.
		if (prefix) groups.add(prefix);
		for (const [k, v] of Object.entries(value)) flatten(v, prefix ? `${prefix}.${k}` : k, leaves, groups);
	} else {
		leaves.set(prefix, value);
	}
	return { leaves, groups };
}

function* walk(dir) {
	for (const entry of readdirSync(dir)) {
		const full = join(dir, entry);
		if (SKIP_DIRS.some((skip) => full === skip || full.startsWith(skip + '/'))) continue;
		const st = statSync(full);
		if (st.isDirectory()) yield* walk(full);
		else if (['.svelte', '.ts'].includes(extname(full))) yield full;
	}
}

const { leaves: keys, groups } = flatten(JSON.parse(readFileSync(cataloguePath, 'utf8')));

// `$t('key')`, `tr('key')`, `trp('key', n)`, `trp2('key', a, b)`. The key must be the
// first argument and a plain quoted string; anything else is dynamic and is counted, not
// checked.
const CALL = /(?:\$t\(|\b(?:tr|trp|trp2)\()\s*(['"])((?:(?!\1).)+)\1/g;
const ANY_CALL = /(?:\$t\(|\b(?:tr|trp|trp2)\()/g;
const RENDERED_AS_HTML = /\{@html[^}]*?(?:\$t\(|\b(?:tr|trp|trp2)\()/;

const missing = [];
const asHtml = [];
let checked = 0;
let dynamic = 0;

for (const file of walk(srcDir)) {
	const rel = relative(frontendDir, file);
	const source = readFileSync(file, 'utf8');
	source.split('\n').forEach((line, i) => {
		if (RENDERED_AS_HTML.test(line)) asHtml.push(`${rel}:${i + 1}: ${line.trim()}`);
		const literalKeys = new Set();
		for (const m of line.matchAll(CALL)) {
			checked++;
			literalKeys.add(m[2]);
			if (!keys.has(m[2]) && !groups.has(m[2])) missing.push(`${rel}:${i + 1}: ${m[2]}`);
		}
		// Calls on this line with no literal key are dynamic.
		const total = [...line.matchAll(ANY_CALL)].length;
		dynamic += Math.max(0, total - literalKeys.size);
	});
}

let failed = false;

if (missing.length > 0) {
	failed = true;
	console.error(`\n${missing.length} call site(s) use a key that is not in en.json:\n`);
	for (const m of missing) console.error(`  ${m}`);
	console.error('\nA missing key renders as the dotted key on screen. Check the spelling, or add it to en.json first.\n');
}

if (asHtml.length > 0) {
	failed = true;
	console.error(`\n${asHtml.length} call site(s) render catalogue text with {@html}:\n`);
	for (const h of asHtml) console.error(`  ${h}`);
	console.error(
		'\nCatalogue values are plain text and community locale PRs are a real input, so this is an injection surface.\n' +
			'Render the markup in the component and use a key per fragment, or use LinkedText for operator links.\n'
	);
}

console.log(
	`i18n guard: ${checked} literal call site(s) checked against ${keys.size} keys ` +
		`(+${groups.size} plural groups), ` +
		`${dynamic} dynamic call site(s) skipped (template/ternary keys).`
);

if (failed) process.exit(1);
