package handler

import (
	"bytes"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/i18n"
)

// The manage template injects JS constants from server data. Using
// {{printf "%q" .X}} inside an html/template <script> double-quotes the value
// (the value gets JS-escaped again), producing SLUG=`"20-min-call"` with literal
// quotes — which broke the reschedule slots fetch (404) and the "Invalid Date".
// Guard that the constants are single-layer quoted.
func TestManageTemplate_jsConstantsNotDoubleQuoted(t *testing.T) {
	var buf bytes.Buffer
	data := managePageData{
		Token:           "tok123",
		EventTypeSlug:   "20-min-call",
		EventTypeName:   "20-minute call",
		CurrentStartISO: "2026-06-18T10:00:00Z",
		OrganizerTZ:     "Pacific/Auckland",
		Status:          "confirmed",
		MaxFutureDays:   60,
		DurationMinutes: 20,
		T:               i18n.Default().T,
	}
	if err := manageTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("render manage template: %v", err)
	}
	out := buf.String()

	if strings.Contains(out, `\"20-min-call\"`) || strings.Contains(out, `\"2026-06-18`) {
		t.Error("JS constant is double-quoted (literal quotes in the value)")
	}
	if !strings.Contains(out, `"20-min-call"`) {
		t.Error("SLUG not emitted as a clean quoted JS string")
	}
	if !strings.Contains(out, `"2026-06-18T10:00:00Z"`) {
		t.Error("CURRENT_ISO not emitted as a clean quoted JS string")
	}
}

// Exercise html/template's JavaScript string context in both public templates.
// Node parses the entire script, then evaluates only the URL constant so the
// test needs no DOM or browser and catches double quoting and query corruption.
func TestInvitationTemplatesSlotsURLJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to validate rendered JavaScript")
	}
	for _, url := range []string{
		"/v1/schedule/token/slots",
		`/v1/schedule/token/slots?lang=fr-CA&tz=Europe%2FParis&label="a b"&end=</script>&value=${secret}`,
	} {
		for _, surface := range []string{"book", "manage"} {
			t.Run(surface+"/"+url, func(t *testing.T) {
				var buf bytes.Buffer
				var err error
				if surface == "book" {
					err = bookTmpl.Execute(&buf, bookPageData{IsInvitation: true, SlotsURL: url, T: i18n.Default().T})
				} else {
					err = manageTmpl.Execute(&buf, managePageData{IsInvitation: true, SlotsURL: url, T: i18n.Default().T})
				}
				if err != nil {
					t.Fatal(err)
				}
				scripts := regexp.MustCompile(`(?s)<script>(.*?)</script>`).FindAllStringSubmatch(buf.String(), -1)
				var script string
				for _, match := range scripts {
					if strings.Contains(match[1], "const SLOTS_URL") {
						script = match[1]
					}
				}
				if script == "" {
					t.Fatal("slots script missing")
				}
				cmd := exec.Command(node, "--check")
				cmd.Stdin = strings.NewReader(script)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("invalid rendered JavaScript: %s %v", output, err)
				}
				constant := regexp.MustCompile(`const SLOTS_URL = ([^\n]+);`).FindStringSubmatch(script)
				if len(constant) != 2 {
					t.Fatal("slots constant missing")
				}
				output, err := exec.Command(node, "-e", "process.stdout.write("+constant[1]+")").CombinedOutput()
				if err != nil || string(output) != url {
					t.Fatalf("rendered URL=%q, want %q; error=%v", output, url, err)
				}
			})
		}
	}
}
