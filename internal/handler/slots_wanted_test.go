package handler

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// agentSlotCallers are the two paths that hand computed availability to a machine rather
// than to a person: the MCP tool and the booking assistant's tool loop.
var agentSlotCallers = []string{"mcp.go", "booking_assistant.go"}

// computeSlotsCall matches a computeSlots invocation and captures the slotsWanted literal
// it ends with.
var computeSlotsCall = regexp.MustCompile(`computeSlots\([^)]*?(slotsWanted\{[^}]*\})\)`)

// TestAgentSlotCallersAskForNothingOptional pins both halves of the agent boundary in one
// place, because they are one literal apart and nothing else would notice a change.
//
//   - Taken slots must never reach an agent: they are times it would eventually offer,
//     with nothing in the payload marking them unbookable. That invariant already has a
//     behavioural test (TestMCPGetAvailableSlots_neverReturnsTakenSlots).
//   - The notice gap must not be COMPUTED for these callers. This half cannot be asserted
//     behaviourally: getSlotsOut serialises only Slots, so a stray NoticeGap:true would
//     leak nothing and simply waste a map plus a routing pass on every agent call. A test
//     written against the tool's output passes either way and guards nothing - the first
//     version of this test did exactly that.
//
// So it is checked at the call site, the same way TestEmbedJSDoesNotDependOnBookingLogic
// and the booking-surfaces contract test check theirs: read the source and assert what it
// says. Crude, but it fails when it should, which the behavioural version did not.
func TestAgentSlotCallersAskForNothingOptional(t *testing.T) {
	for _, name := range agentSlotCallers {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		matches := computeSlotsCall.FindAllStringSubmatch(string(src), -1)
		if len(matches) == 0 {
			t.Errorf("%s: no computeSlots(…, slotsWanted{…}) call found. If this file no "+
				"longer reaches availability, drop it from agentSlotCallers; if the call "+
				"was reshaped, update the pattern rather than deleting the guard", name)
			continue
		}
		for _, m := range matches {
			if got := strings.TrimSpace(m[1]); got != "slotsWanted{}" {
				t.Errorf("%s passes %s to computeSlots; agent-facing callers must pass "+
					"slotsWanted{}.\nTaken slots are times an agent would offer as bookable, "+
					"and the notice gap is a presentation aid an agent has no use for - it "+
					"only costs a map and a routing pass on every call.", name, got)
			}
		}
	}
}
