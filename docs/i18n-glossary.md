# Translation glossary

One concept, one word, per language. The point is that a booking does not become a
"rendez-vous" on one screen and a "réservation" on the next, and that a new translator does
not have to invent terminology that is already decided.

**How to use it**

- Before translating a term, look it up here. If it is missing, add it with its key.
- The values below were read from the shipped catalogues, not invented. If a catalogue
  disagrees with this table, the table is wrong and should be fixed in the same PR.
- **Adding a language means adding a column.** Do not introduce a second word for a concept
  that already has one.
- Two catalogues exist: the **booker** surfaces (`internal/i18n/locales`) and the **admin**
  SPA (`frontend/src/lib/i18n`). They are separate systems with separate registers, and they
  currently disagree in places. Those rows are marked **divergent** and are decisions, not
  accidents.

## Core terms

| Concept | English | French (booker) | French (admin) | German (booker) | German (admin) | Notes |
|---|---|---|---|---|---|---|
| booking (the object) | booking | réservation | réservation | Buchung | Buchung | Never "rendez-vous" for the object. |
| to book (verb) | book | réserver | réserver | buchen | buchen | |
| event type | event type | - | type de rendez-vous | - | Terminart | **divergent**: the booker has no public name for it. |
| host | host | - | hôte | Gastgeber | Gastgeber | The booker addresses the booker, not the host. |
| attendee / booker | attendee | - | participant | - | Teilnehmer | Use "participant" in admin; the booker is "vous". |
| team | team | équipe | équipe | Team | Team | |
| team member | team member | membre de l'équipe | membre | Teammitglied | Mitglied | |
| availability | availability | disponibilités | disponibilités | Verfügbarkeit | Verfügbarkeit | Plural in French. |
| slot / time slot | slot | créneau | créneau | Termin / Zeit | Zeitfenster | |
| timezone | timezone | fuseau horaire | fuseau horaire | Zeitzone | Zeitzone | |
| reschedule | reschedule | reprogrammer | reprogrammer | verschieben | verschieben | Noun: "reprogrammation". |
| cancel | cancel | annuler | annuler | stornieren | stornieren | Noun: "annulation". |
| round robin | round robin | - | tourniquet | - | Round Robin | French admin uses "tourniquet"; do not calque "tourner entre eux". |
| reminder | reminder | rappel | rappel | Erinnerung | Erinnerung | |
| directory | directory | - | annuaire | - | Verzeichnis | Only the person and team pages. |

## Register

| Surface | Register | Reason |
|---|---|---|
| Booker | Formal, second person plural (`vous`) | A customer booking a call from a business page. |
| Admin | Formal (`vous`), terse | An operator working in the tool all day. |

The existing booker locales are deliberately split by market: `fr`/`de`/`it`/`pt` formal,
`es`/`nl`/`sv` informal. Match the language, not the English.

German follows the same split: booker `de` formal (*Sie*), admin `de` formal and terse. A booking is
*verschoben* (moved), not *umgebucht*: the latter reads as travel rebooking.

`de-x-neutral` is the one exception, and it is a third option rather than a change of side:
German with no address at all (infinitive and nominal style, "Tag auswählen"), for
operators whose own site does not say *Sie*. It is opt-in and never offered to visitors, so
`de` remains the formal default. When a string changes in `de.json`, change it in
`de-x-neutral.json` too, without the pronoun.

## Things that are not translated

Brand names, URLs, protocol tokens (`STARTTLS`, `RSVP`, `PII`), third-party console labels
("APIs & Services", "Authorised redirect URIs"), and code identifiers. Translating those is
almost always a mistake; leave them byte-identical.

## Placeholders

Never translate a placeholder. `%s`, `%d`, `%q` (booker) and `{name}` (admin) must survive
verbatim, and a missing one is a runtime bug rather than a stylistic choice. The tests check
this, but a test cannot see that you moved a word into the wrong place.

## Cross-references

- `CONTRIBUTING.md` - how to add or extend a language
- `docs/ARCHITECTURE.md` §23 - how the catalogue system works and its limitations
