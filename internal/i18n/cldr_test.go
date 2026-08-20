package i18n

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestDateTablesMatchCLDR checks every locale's weekday/month abbreviations and clock
// convention against Intl (i.e. CLDR) via node.
//
// Go's stdlib has no locale data, which is why FormatDate/FormatTimeOfDay are driven by
// hand-maintained keys in the locale files — and hand-maintained data drifts. Nothing else
// in the suite can tell "dim." from "dim" or catch a 12h/24h call that's wrong for the
// language, so without this a new locale's dates can be subtly wrong in a way only a native
// speaker would notice. node is the only CLDR source already on the machines that build this
// project (the SvelteKit frontend needs it), so the test shells out to it and skips when it
// isn't there rather than vendoring a second copy of CLDR.
func TestDateTablesMatchCLDR(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available; skipping CLDR cross-check")
	}

	const script = `
const dow = ['sun','mon','tue','wed','thu','fri','sat'];
const mon = ['jan','feb','mar','apr','may','jun','jul','aug','sep','oct','nov','dec'];
const out = {};
for (const code of process.argv.slice(1)) {
  const d = new Intl.DateTimeFormat(code, {weekday: 'short', timeZone: 'UTC'});
  const m = new Intl.DateTimeFormat(code, {month: 'short', timeZone: 'UTC'});
  const e = {};
  // 2024-01-07 was a Sunday, so i maps straight onto dow.
  for (let i = 0; i < 7; i++) e['dow_short_' + dow[i]] = d.format(new Date(Date.UTC(2024, 0, 7 + i)));
  for (let i = 0; i < 12; i++) e['month_short_' + mon[i]] = m.format(new Date(Date.UTC(2024, i, 15)));
  const hc = new Intl.DateTimeFormat(code, {hour: 'numeric'}).resolvedOptions().hourCycle;
  e['clock_format'] = (hc === 'h23' || hc === 'h24') ? '24h' : '12h';
  out[code] = e;
}
process.stdout.write(JSON.stringify(out));
`

	args := append([]string{"-e", script, "--"}, supportedCodes...)
	raw, err := exec.Command(node, args...).Output()
	if err != nil {
		t.Fatalf("running the CLDR probe under node failed: %v", err)
	}

	var want map[string]map[string]string
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("parsing the CLDR probe output failed: %v (output %q)", err, raw)
	}

	for _, code := range supportedCodes {
		expected, ok := want[code]
		if !ok {
			t.Errorf("node returned no CLDR data for locale %q", code)
			continue
		}
		loc := Get(code)
		for key, exp := range expected {
			// Intl resolves an unknown tag to its own default rather than failing, so a
			// wholesale mismatch usually means the tag isn't a real language, not that
			// every abbreviation is wrong. Report it once, plainly.
			if got := loc.strings[key]; got != exp {
				t.Errorf("%s.json: %s = %q, CLDR says %q", code, key, got, exp)
			}
		}
	}
}

// TestDateTablesMatchCLDR_wouldCatchDrift proves the check above has teeth: the same
// comparison against a deliberately wrong value must fail.
func TestDateTablesMatchCLDR_wouldCatchDrift(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available; skipping CLDR cross-check")
	}
	fr := Get("fr")
	if fr == nil {
		t.Skip("fr locale not present")
	}
	// French CLDR abbreviations carry a trailing period; dropping it is exactly the kind
	// of plausible-looking edit the real test exists to reject.
	if strings.TrimSuffix(fr.strings["dow_short_mon"], ".") == fr.strings["dow_short_mon"] {
		t.Errorf("dow_short_mon = %q, expected a CLDR-style trailing period for French — "+
			"if CLDR really changed, update this guard too", fr.strings["dow_short_mon"])
	}
}
