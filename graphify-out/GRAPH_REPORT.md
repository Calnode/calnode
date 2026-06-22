# Graph Report - .  (2026-06-22)

## Corpus Check
- cluster-only mode — file stats not available

## Summary
- 2005 nodes · 4860 edges · 121 communities (109 shown, 12 thin omitted)
- Extraction: 82% EXTRACTED · 18% INFERRED · 0% AMBIGUOUS · INFERRED: 899 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `9af3cc1c`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- [[_COMMUNITY_Availability Override UI|Availability Override UI]]
- [[_COMMUNITY_Email Template Rendering|Email Template Rendering]]
- [[_COMMUNITY_Booking Service Logic|Booking Service Logic]]
- [[_COMMUNITY_SMTP Configuration Handler|SMTP Configuration Handler]]
- [[_COMMUNITY_UI Component Library|UI Component Library]]
- [[_COMMUNITY_Availability Slot Generation|Availability Slot Generation]]
- [[_COMMUNITY_System Providers and Services|System Providers and Services]]
- [[_COMMUNITY_Booking Metadata Validation|Booking Metadata Validation]]
- [[_COMMUNITY_Database Schema Migrations|Database Schema Migrations]]
- [[_COMMUNITY_Calendar Provider Interface|Calendar Provider Interface]]
- [[_COMMUNITY_Booking Page Branding|Booking Page Branding]]
- [[_COMMUNITY_Booking Management Tests|Booking Management Tests]]
- [[_COMMUNITY_Time Interval Utilities|Time Interval Utilities]]
- [[_COMMUNITY_Frontend Dependencies|Frontend Dependencies]]
- [[_COMMUNITY_Availability Logic|Availability Logic]]
- [[_COMMUNITY_Core Infrastructure Utilities|Core Infrastructure Utilities]]
- [[_COMMUNITY_API Client Types|API Client Types]]
- [[_COMMUNITY_Booking Integration Tests|Booking Integration Tests]]
- [[_COMMUNITY_Intake Question Tests|Intake Question Tests]]
- [[_COMMUNITY_User and Webhook Tests|User and Webhook Tests]]
- [[_COMMUNITY_Google Settings Management|Google Settings Management]]
- [[_COMMUNITY_OAuth Encryption Utilities|OAuth Encryption Utilities]]
- [[_COMMUNITY_Availability Override Tests|Availability Override Tests]]
- [[_COMMUNITY_OAuth Client Configuration|OAuth Client Configuration]]
- [[_COMMUNITY_Booking Payload Enrichment|Booking Payload Enrichment]]
- [[_COMMUNITY_Booking Widget Embed|Booking Widget Embed]]
- [[_COMMUNITY_Authentication and Claiming|Authentication and Claiming]]
- [[_COMMUNITY_Email Auth Service|Email Auth Service]]
- [[_COMMUNITY_Google Calendar Tests|Google Calendar Tests]]
- [[_COMMUNITY_User Role Management|User Role Management]]
- [[_COMMUNITY_App Configuration Methods|App Configuration Methods]]
- [[_COMMUNITY_Google OAuth Tests|Google OAuth Tests]]
- [[_COMMUNITY_Email Settings Tests|Email Settings Tests]]
- [[_COMMUNITY_Calendar Event Tests|Calendar Event Tests]]
- [[_COMMUNITY_User Profile Tests|User Profile Tests]]
- [[_COMMUNITY_Availability Slot Handler|Availability Slot Handler]]
- [[_COMMUNITY_Reschedule Booking Tests|Reschedule Booking Tests]]
- [[_COMMUNITY_Microsoft Calendar Tests|Microsoft Calendar Tests]]
- [[_COMMUNITY_Google Calendar Integration|Google Calendar Integration]]
- [[_COMMUNITY_Microsoft Graph Integration|Microsoft Graph Integration]]
- [[_COMMUNITY_Authentication Middleware|Authentication Middleware]]
- [[_COMMUNITY_Event Type Management|Event Type Management]]
- [[_COMMUNITY_Booking Question Handler|Booking Question Handler]]
- [[_COMMUNITY_Invite System Tests|Invite System Tests]]
- [[_COMMUNITY_Booking Token Tests|Booking Token Tests]]
- [[_COMMUNITY_UI Theme Configuration|UI Theme Configuration]]
- [[_COMMUNITY_Google Auth Handler|Google Auth Handler]]
- [[_COMMUNITY_Team Management Service|Team Management Service]]
- [[_COMMUNITY_Availability Rule Tests|Availability Rule Tests]]
- [[_COMMUNITY_Branding and Logos|Branding and Logos]]
- [[_COMMUNITY_TypeScript Configuration|TypeScript Configuration]]
- [[_COMMUNITY_FreeBusy Integration Tests|FreeBusy Integration Tests]]
- [[_COMMUNITY_Microsoft Auth Handler|Microsoft Auth Handler]]
- [[_COMMUNITY_Event Host Tests|Event Host Tests]]
- [[_COMMUNITY_Webhook Management|Webhook Management]]
- [[_COMMUNITY_Email Testing Utilities|Email Testing Utilities]]
- [[_COMMUNITY_Availability Override Handler|Availability Override Handler]]
- [[_COMMUNITY_Team Integration Tests|Team Integration Tests]]
- [[_COMMUNITY_Project Architecture Overview|Project Architecture Overview]]
- [[_COMMUNITY_Public Booking Tests|Public Booking Tests]]
- [[_COMMUNITY_Event Type Hosts|Event Type Hosts]]
- [[_COMMUNITY_Config Loading Tests|Config Loading Tests]]
- [[_COMMUNITY_Booking Limit Tests|Booking Limit Tests]]
- [[_COMMUNITY_Live Mailer Service|Live Mailer Service]]
- [[_COMMUNITY_Calendar Connection Handler|Calendar Connection Handler]]
- [[_COMMUNITY_Availability Rule Handler|Availability Rule Handler]]
- [[_COMMUNITY_Event Visibility Tests|Event Visibility Tests]]
- [[_COMMUNITY_API Key Management|API Key Management]]
- [[_COMMUNITY_Avatar Upload Handler|Avatar Upload Handler]]
- [[_COMMUNITY_OAuth State Management|OAuth State Management]]
- [[_COMMUNITY_Email Settings Handler|Email Settings Handler]]
- [[_COMMUNITY_Round Robin Scheduling|Round Robin Scheduling]]
- [[_COMMUNITY_Mailer Interface|Mailer Interface]]
- [[_COMMUNITY_Booking Data Models|Booking Data Models]]
- [[_COMMUNITY_Booking Visibility Tests|Booking Visibility Tests]]
- [[_COMMUNITY_User Lifecycle Management|User Lifecycle Management]]
- [[_COMMUNITY_Static Asset Handlers|Static Asset Handlers]]
- [[_COMMUNITY_User Administration|User Administration]]
- [[_COMMUNITY_Google Settings Handler|Google Settings Handler]]
- [[_COMMUNITY_Booking Reassignment|Booking Reassignment]]
- [[_COMMUNITY_Ownership and Roles|Ownership and Roles]]
- [[_COMMUNITY_Idempotency Tests|Idempotency Tests]]
- [[_COMMUNITY_Abuse Prevention Tests|Abuse Prevention Tests]]
- [[_COMMUNITY_Session Management|Session Management]]
- [[_COMMUNITY_Test Email Handler|Test Email Handler]]
- [[_COMMUNITY_Public Event Tests|Public Event Tests]]
- [[_COMMUNITY_UID Generation Tests|UID Generation Tests]]
- [[_COMMUNITY_Frontend Utility Functions|Frontend Utility Functions]]
- [[_COMMUNITY_Build Info Tests|Build Info Tests]]
- [[_COMMUNITY_FreeBusy Data Models|FreeBusy Data Models]]
- [[_COMMUNITY_Concurrency Tests|Concurrency Tests]]
- [[_COMMUNITY_Team Data Models|Team Data Models]]
- [[_COMMUNITY_Docker Entrypoint|Docker Entrypoint]]
- [[_COMMUNITY_Email Templates|Email Templates]]
- [[_COMMUNITY_Svelte Configuration|Svelte Configuration]]
- [[_COMMUNITY_Availability Rule Models|Availability Rule Models]]
- [[_COMMUNITY_Test Style Setup|Test Style Setup]]
- [[_COMMUNITY_Community 100|Community 100]]
- [[_COMMUNITY_Community 120|Community 120]]

## God Nodes (most connected - your core abstractions)
1. `authReq()` - 162 edges
2. `setupWorkspaceWithDB()` - 134 edges
3. `setupWorkspace()` - 104 edges
4. `seedEventTypeHTTP()` - 86 edges
5. `userFromContext()` - 74 edges
6. `contains()` - 61 edges
7. `Generate()` - 40 edges
8. `$lib/utils.js` - 35 edges
9. `T` - 31 edges
10. `seedHost()` - 29 edges

## Surprising Connections (you probably didn't know these)
- `main()` --calls--> `Migrate()`  [INFERRED]
  cmd/calnode/main.go → internal/db/db.go
- `main()` --calls--> `Info`  [INFERRED]
  cmd/calnode/main.go → internal/buildinfo/buildinfo.go
- `runRecoverKey()` --calls--> `getEnv()`  [INFERRED]
  cmd/calnode/recover_key.go → internal/config/config.go
- `runRecoverKey()` --calls--> `RecoverPrimary()`  [INFERRED]
  cmd/calnode/recover_key.go → internal/keyvault/keyvault.go
- `runRotateKey()` --calls--> `getEnv()`  [INFERRED]
  cmd/calnode/rotate_key.go → internal/config/config.go

## Import Cycles
- None detected.

## Communities (121 total, 12 thin omitted)

### Community 0 - "Availability Override UI"
Cohesion: 0.05
Nodes (56): addingOv, addOverride(), deleteOvId, deleteOvOpen, doDeleteOverride(), editOvForm, loadOverrides(), orderedDays (+48 more)

### Community 1 - "Email Template Rendering"
Cohesion: 0.06
Nodes (72): Attachment, Builder, contains(), TestManageTemplate_jsConstantsNotDoubleQuoted(), T, Context, Mailer, Template (+64 more)

### Community 2 - "Booking Service Logic"
Cohesion: 0.06
Nodes (50): scanner, isUniqueViolation(), leastLoadedHost(), New(), pickRotationHost(), scanBooking(), CreateParams, nextWeekday() (+42 more)

### Community 3 - "SMTP Configuration Handler"
Cohesion: 0.05
Nodes (62): LoadEmailSettingsFromDB(), SMTPConfig, DB, Context, Message, HandlerFunc, ResponseRecorder, T (+54 more)

### Community 4 - "UI Component Library"
Cohesion: 0.05
Nodes (16): $lib/components/ui/calendar, $lib/components/ui/popover, $lib/components/ui/separator/index.js, $lib/utils.js, @lucide/svelte/icons/check, @lucide/svelte/icons/chevron-down, @lucide/svelte/icons/chevron-up, @lucide/svelte/icons/chevrons-up-down (+8 more)

### Community 5 - "Availability Slot Generation"
Cohesion: 0.13
Nodes (61): AvailabilityOverride, Request, main(), printSlots(), AvailabilityRule, Interval, Location, Time (+53 more)

### Community 6 - "System Providers and Services"
Cohesion: 0.07
Nodes (48): Get(), Info, Calendar Service, Google Calendar Provider, Keyvault (Envelope Encryption), Litestream Replication, Microsoft 365 Provider, main() (+40 more)

### Community 7 - "Booking Metadata Validation"
Cohesion: 0.08
Nodes (33): assignedHost, attendeeJSON, TestHostsLabel(), TestProviderMintsPlatform(), TestValidMeetingLink(), TestValidPhone(), TestValidVideoURL(), isForeignKeyViolation() (+25 more)

### Community 8 - "Database Schema Migrations"
Cohesion: 0.08
Nodes (45): TestCanAutoGenerate(), AppliedVersion(), Migrate(), Open(), parseDSN(), SchemaReady(), TargetVersion(), TestDoubleBookingIndex_exists() (+37 more)

### Community 9 - "Calendar Provider Interface"
Cohesion: 0.08
Nodes (32): NewService(), CreateEventParams, Provider, Service, newHandlerWithGCal(), TestCalendarCallback_missingCode_returns400(), TestCalendarCallback_missingState_returns400(), TestCalendarCallback_noGCal_returns501() (+24 more)

### Community 10 - "Booking Page Branding"
Cohesion: 0.08
Nodes (34): durationLabel(), firstRune(), hostsLabel(), locationLabel(), renderMarkdown(), bookPageData, bookQuestion, opacityCSS() (+26 more)

### Community 11 - "Booking Management Tests"
Cohesion: 0.19
Nodes (40): TestCancelByToken_alreadyCancelled(), TestCancelByToken_invalidToken(), TestCancelByToken_success(), TestIssueManageToken_multipleTokensAllValid(), TestIssueManageToken_returnsHexToken(), TestReschedule_alreadyCancelled(), TestReschedule_doubleBooked(), TestReschedule_sameTime() (+32 more)

### Community 12 - "Time Interval Utilities"
Cohesion: 0.14
Nodes (36): Duration, Time, Interval, T, Time, Duration, Interval, T (+28 more)

### Community 13 - "Frontend Dependencies"
Cohesion: 0.05
Nodes (37): dependencies, clsx, cropperjs, tailwind-merge, devDependencies, bits-ui, @internationalized/date, @lucide/svelte (+29 more)

### Community 14 - "Availability Logic"
Cohesion: 0.15
Nodes (35): Interval, Location, Time, Weekday, Location, Month, T, Time (+27 more)

### Community 15 - "Core Infrastructure Utilities"
Cohesion: 0.10
Nodes (24): T, Context, Message, T, Client, Context, DB, Duration (+16 more)

### Community 16 - "API Client Types"
Cohesion: 0.06
Nodes (27): api, APIKey, AvailabilityOverride, AvailabilityRule, Booking, CalendarStatus, EmailSettings, EventType (+19 more)

### Community 17 - "Booking Integration Tests"
Cohesion: 0.17
Nodes (30): setupWorkspace(), TestCancelBooking(), TestCreateAndListAvailabilityRule(), TestCreateBooking_doubleBooked(), TestCreateBooking_success(), TestCreateEventType_duplicateSlug(), TestCreateEventType_success(), TestDeleteEventType() (+22 more)

### Community 18 - "Intake Question Tests"
Cohesion: 0.18
Nodes (30): seedEventTypeHTTP(), createQuestion(), TestCreateBooking_invalidSelectOption(), TestCreateBooking_unknownQuestionID(), TestCreateBooking_validSelectOption(), TestCreateBooking_withRequiredAnswer(), TestCreateQuestion_autoPosition(), TestCreateQuestion_invalidType() (+22 more)

### Community 19 - "User and Webhook Tests"
Cohesion: 0.17
Nodes (28): authReq(), TestDeleteUser_cannotDeleteSelf(), TestDeleteUser_notFound(), TestDeleteUser_removesUser(), TestDeleteUser_requiresAdmin(), TestListUsers_requiresAdmin(), TestListUsers_responseShape(), TestListUsers_returnsAllUsers() (+20 more)

### Community 20 - "Google Settings Management"
Cohesion: 0.17
Nodes (28): LoadGoogleSettingsFromDB(), containsStr(), getGoogleSettings(), newGoogleHandler(), patchGoogleSettings(), strReader(), TestCalendarStatus_configuredField_false_whenGCalNil(), TestCalendarStatus_configuredField_true_whenGCalSet() (+20 more)

### Community 21 - "OAuth Encryption Utilities"
Cohesion: 0.13
Nodes (10): New(), savingTokenSource, Config, Context, DB, Encoding, Client, Logger (+2 more)

### Community 22 - "Availability Override Tests"
Cohesion: 0.18
Nodes (27): createOverride(), nextMonday(), TestCreateAvailabilityOverride_blockedDay(), TestCreateAvailabilityOverride_customHours(), TestCreateAvailabilityOverride_duplicateDateReturns409(), TestCreateAvailabilityOverride_invalidDate(), TestCreateAvailabilityOverride_invalidHHMM(), TestCreateAvailabilityOverride_missingTimesWhenAvailable() (+19 more)

### Community 23 - "OAuth Client Configuration"
Cohesion: 0.13
Nodes (11): Config, Context, DB, Encoding, Logger, Client, Token, TokenSource (+3 more)

### Community 24 - "Booking Payload Enrichment"
Cohesion: 0.15
Nodes (16): T, Context, DB, Time, BookingPayload, Delivery, enrichedBooking, TestBuildData_defaultSetMatchesOriginalShape() (+8 more)

### Community 25 - "Booking Widget Embed"
Cohesion: 0.20
Nodes (14): addMonths(), api(), CalnodeBooking, dayKey(), el(), endOfMonth(), esc(), mondayIndex() (+6 more)

### Community 26 - "Authentication and Claiming"
Cohesion: 0.18
Nodes (24): newEmptyHandler(), TestAuthStatus_claimed(), TestAuthStatus_unclaimed(), TestClaim_alreadyClaimed(), TestClaim_firstUser(), TestClaim_missingFields(), TestClaim_shortPassword(), loginEmailReq() (+16 more)

### Community 27 - "Email Auth Service"
Cohesion: 0.16
Nodes (12): validatePassword(), hashInviteToken(), Handler, Request, ResponseWriter, Handler, Request, ResponseWriter (+4 more)

### Community 28 - "Google Calendar Tests"
Cohesion: 0.23
Nodes (24): newTestClient(), newTestDB(), seedUser(), TestAuthURL_containsExpectedParams(), TestConnected_falseWhenNoConnection(), TestDecrypt_emptyStringRejected(), TestDecrypt_tamperedCiphertextRejected(), TestDecryptState_emptyRejected() (+16 more)

### Community 29 - "User Role Management"
Cohesion: 0.18
Nodes (21): TestArchiveUser_archivesAndBlocksLogin(), TestArchiveUser_blockedByUpcomingBookings(), TestArchiveUser_cannotArchiveOwner(), TestArchiveUser_hiddenFromDefaultListShownWithFlag(), TestRestoreUser_adminOnlyRestoresOwnArchives(), TestRestoreUser_reenablesLogin(), sha256HexForTest(), TestAdminSetPassword_adminCannotResetAdmin() (+13 more)

### Community 30 - "App Configuration Methods"
Cohesion: 0.12
Nodes (9): New(), Config, DB, Logger, Mailer, ResponseWriter, RWMutex, Service (+1 more)

### Community 31 - "Google OAuth Tests"
Cohesion: 0.22
Nodes (20): authTestSetup(), seedSession(), TestCallbackGoogle_redirectsToDeniedOnGoogleError(), TestCallbackGoogle_rejectsMissingStateCookie(), TestCallbackGoogle_rejectsStateMismatch(), TestLoginGoogle_returns503WhenNotConfigured(), TestLoginGoogle_setsStateCookieAndRedirects(), TestLogout_clearsCookieAndRedirects() (+12 more)

### Community 32 - "Email Settings Tests"
Cohesion: 0.22
Nodes (20): TestGetEmailSettings_requiresAuth(), TestGetEmailSettings_unconfigured(), TestPatchEmailSettings_clearHostDisablesEmail(), TestPatchEmailSettings_invalidPort(), TestPatchEmailSettings_keepExistingPassword(), TestPatchEmailSettings_nonAdminForbidden(), TestPatchEmailSettings_requiresAuth(), TestPatchEmailSettings_savesSettings() (+12 more)

### Community 33 - "Calendar Event Tests"
Cohesion: 0.25
Nodes (20): calEventReq, mockCreateEventServer(), saveDestinationConnection(), TestCancelEvent_emptyEventID_noOp(), TestCancelEvent_goneIsNotAnError(), TestCancelEvent_notConnected_returnsNil(), TestCancelEvent_serverError_returnsError(), TestCancelEvent_success() (+12 more)

### Community 34 - "User Profile Tests"
Cohesion: 0.22
Nodes (20): patchMe(), TestGetMe_returnsDefaultPrefs(), TestPatchMe_emptyBody_isNoop(), TestPatchMe_invalidDateFormat(), TestPatchMe_invalidTimeFormat(), TestPatchMe_invalidTimezone(), TestPatchMe_invalidWeekStart(), TestPatchMe_partialUpdate_othersUnchanged() (+12 more)

### Community 35 - "Availability Slot Handler"
Cohesion: 0.18
Nodes (15): slotJSON, parseDateRange(), TestParseDateRange_farFutureToParam_clampedToEffectiveCap(), TestParseDateRange_farFutureToParam_zeroMax_clampedToOneYear(), TestParseDateRange_malformedDate_returnsNotOk(), TestParseDateRange_toBeforeFrom_returnsNotOk(), TestParseDateRange_validExplicitRange(), TestParseDateRange_zeroMaxFuture_defaultsToOneYear() (+7 more)

### Community 36 - "Reschedule Booking Tests"
Cohesion: 0.25
Nodes (19): futureAt(), patchReschedule(), TestRescheduleBooking_alreadyCancelled(), TestRescheduleBooking_badStartAt(), TestRescheduleBooking_doubleBooked(), TestRescheduleBooking_missingStartAt(), TestRescheduleBooking_notFound(), TestRescheduleBooking_pastDate() (+11 more)

### Community 37 - "Microsoft Calendar Tests"
Cohesion: 0.31
Nodes (18): Client, DB, T, connect(), mkIDToken(), newTestClient(), newTestDB(), seedUser() (+10 more)

### Community 38 - "Google Calendar Integration"
Cohesion: 0.18
Nodes (12): calConferenceData, calConferenceSolutionKey, calCreateConferenceRequest, calEntryPoint, calEventAttendee, calEventDateTime, calEventReq, calEventResp (+4 more)

### Community 39 - "Microsoft Graph Integration"
Cohesion: 0.15
Nodes (13): graphDateTime, graphItemBody, Context, CreateEventParams, Client, Time, graphErrBody(), graphAttendee (+5 more)

### Community 40 - "Authentication Middleware"
Cohesion: 0.15
Nodes (11): extractAPIKey(), hashAPIKey(), AuthUser, contextKey, Context, Handler, HandlerFunc, Request (+3 more)

### Community 41 - "Event Type Management"
Cohesion: 0.32
Nodes (9): nullableString(), scanEventType(), scanEventTypeRow(), eventTypeJSON, rowScanner, Context, Handler, Request (+1 more)

### Community 42 - "Booking Question Handler"
Cohesion: 0.25
Nodes (8): Answer, answerJSON, scanQuestion(), questionJSON, questionScanner, Handler, Request, ResponseWriter

### Community 43 - "Invite System Tests"
Cohesion: 0.29
Nodes (15): createInviteReq(), TestClaimInvite_cannotReuseToken(), TestClaimInvite_expiredToken(), TestClaimInvite_success(), TestCreateInvite_adminCreates(), TestCreateInvite_existingUserConflict(), TestCreateInvite_replacesExisting(), TestCreateInvite_requiresAdmin() (+7 more)

### Community 44 - "Booking Token Tests"
Cohesion: 0.38
Nodes (15): createBookingViaHTTP(), issueTestToken(), TestCancelByToken_alreadyCancelled(), TestCancelByToken_invalidToken(), TestCancelByToken_noReason(), TestCancelByToken_success(), TestManagePage_cancelledBooking(), TestManagePage_invalidToken() (+7 more)

### Community 45 - "UI Theme Configuration"
Cohesion: 0.15
Nodes (12): aliases, components, hooks, ui, utils, $schema, style, tailwind (+4 more)

### Community 46 - "Google Auth Handler"
Cohesion: 0.26
Nodes (8): fetchGoogleUserInfo(), googleUserInfo, Config, Context, Handler, Request, ResponseWriter, Token

### Community 47 - "Team Management Service"
Cohesion: 0.47
Nodes (5): userFromContext(), slugify(), Handler, Request, ResponseWriter

### Community 48 - "Availability Rule Tests"
Cohesion: 0.32
Nodes (12): createRule(), TestUpdateAvailabilityRule_conflictWith409(), TestUpdateAvailabilityRule_endNotAfterStart(), TestUpdateAvailabilityRule_invalidDayOfWeek(), TestUpdateAvailabilityRule_invalidHHMM(), TestUpdateAvailabilityRule_notFound(), TestUpdateAvailabilityRule_updateDayOfWeek(), TestUpdateAvailabilityRule_updateTimes() (+4 more)

### Community 49 - "Branding and Logos"
Cohesion: 0.40
Nodes (5): BookingData, Context, Handler, Request, ResponseWriter

### Community 50 - "TypeScript Configuration"
Cohesion: 0.17
Nodes (11): compilerOptions, allowJs, checkJs, esModuleInterop, forceConsistentCasingInFileNames, moduleResolution, resolveJsonModule, skipLibCheck (+3 more)

### Community 51 - "FreeBusy Integration Tests"
Cohesion: 0.38
Nodes (11): mockFreeBusyServer(), saveAndConnectClient(), TestFreeBusy_emptyBusyList(), TestFreeBusy_nonOK_returnsError(), TestFreeBusy_notConnected_returnsNil(), TestFreeBusy_onlyCheckConflictsConnections(), TestFreeBusy_returnsIntervals(), TestFreeBusy_sendsAuthorizationHeader() (+3 more)

### Community 52 - "Microsoft Auth Handler"
Cohesion: 0.23
Nodes (8): fetchMicrosoftEmail(), microsoftUserInfo, Config, Context, Handler, Request, ResponseWriter, Token

### Community 53 - "Event Host Tests"
Cohesion: 0.38
Nodes (11): getHosts(), putHosts(), TestEventTypeHosts_ownerSeededAsRequired(), TestEventTypeHosts_rejectsAllOptional(), TestEventTypeHosts_rejectsArchived(), TestEventTypeHosts_rejectsEmpty(), TestEventTypeHosts_replaceWithRotation(), hostsResp (+3 more)

### Community 54 - "Webhook Management"
Cohesion: 0.36
Nodes (6): validateWebhookURL(), Context, Handler, Request, ResponseWriter, URL

### Community 55 - "Email Testing Utilities"
Cohesion: 0.26
Nodes (10): stubMailer, TestSendTestEmail_emailNotConfigured(), TestSendTestEmail_invalidType(), TestSendTestEmail_notFound(), TestSendTestEmail_requiresAuth(), TestSendTestEmail_success(), TestSendTestEmail_wrongSlug(), Context (+2 more)

### Community 56 - "Availability Override Handler"
Cohesion: 0.35
Nodes (6): availOverrideJSON, validHHMM(), validOverrideReason(), Handler, Request, ResponseWriter

### Community 57 - "Team Integration Tests"
Cohesion: 0.38
Nodes (10): createTeamHTTP(), TestAddTeamMember_rejectsArchived(), TestCreateTeam_derivesSlugAndRejectsDuplicate(), TestCreateTeam_requiresAdmin(), TestDeleteTeam_cascadesMembership(), TestListUsers_includesTeams(), TestTeamMembers_addUpdateRemove(), Handler (+2 more)

### Community 58 - "Project Architecture Overview"
Cohesion: 0.20
Nodes (10): Calnode CLI, SvelteKit Admin UI, Frontend Global CSS, Frontend Embed, Frontend Visual Testing, Database Migrations, Public Booking Templates, Litestream Replication (+2 more)

### Community 59 - "Public Booking Tests"
Cohesion: 0.36
Nodes (9): TestBookPage_durationLabels(), TestBookPage_inactiveEventType_returns404(), TestBookPage_knownSlug_returns200WithHTML(), TestBookPage_locationLabels(), TestBookPage_maxFutureDays0(), TestBookPage_privateEventType_returns404(), TestBookPage_rendersIntakeQuestions(), TestBookPage_unknownSlug_returns404() (+1 more)

### Community 60 - "Event Type Hosts"
Cohesion: 0.42
Nodes (5): EventHost, Context, Handler, Request, ResponseWriter

### Community 61 - "Config Loading Tests"
Cohesion: 0.43
Nodes (7): TestLoad_defaults(), TestLoad_encryptionKeyAbsent(), TestLoad_encryptionKeyFromEnv(), TestLoad_envOverrides(), TestLoad_publicBaseURLDefaultsToBaseURL(), TestLoad_publicBaseURLOverride(), T

### Community 62 - "Booking Limit Tests"
Cohesion: 0.54
Nodes (7): postBooking(), seedEventTypeWithCap(), TestCreateBooking_maxActiveBookingsLimit(), TestCreateBooking_unlimitedActiveBookings(), Handler, ResponseRecorder, T

### Community 63 - "Live Mailer Service"
Cohesion: 0.32
Nodes (4): Context, Mailer, Message, RWMutex

### Community 64 - "Calendar Connection Handler"
Cohesion: 0.57
Nodes (3): Handler, Request, ResponseWriter

### Community 65 - "Availability Rule Handler"
Cohesion: 0.57
Nodes (3): Handler, Request, ResponseWriter

### Community 66 - "Event Visibility Tests"
Cohesion: 0.47
Nodes (5): etVisItem, listOwned(), TestEventTypes_assignedHostSeesReadOnly(), Handler, T

### Community 67 - "API Key Management"
Cohesion: 0.60
Nodes (3): Handler, Request, ResponseWriter

### Community 68 - "Avatar Upload Handler"
Cohesion: 0.60
Nodes (3): Handler, Request, ResponseWriter

### Community 69 - "OAuth State Management"
Cohesion: 0.53
Nodes (3): Handler, Request, ResponseWriter

### Community 70 - "Email Settings Handler"
Cohesion: 0.67
Nodes (3): Handler, Request, ResponseWriter

### Community 71 - "Round Robin Scheduling"
Cohesion: 0.60
Nodes (5): makeRoundRobin(), TestRoundRobin_evenDistribution(), TestRoundRobin_skipsBusyHost(), Handler, T

### Community 72 - "Mailer Interface"
Cohesion: 0.53
Nodes (4): Context, Attachment, Message, Noop

### Community 73 - "Booking Data Models"
Cohesion: 0.80
Nodes (4): Answer, Attendee, CreateParams, Time

### Community 74 - "Booking Visibility Tests"
Cohesion: 0.60
Nodes (4): TestListBookings_adminSeesAllWithScope(), TestListBookings_hostOnly(), TestListBookings_nonAdminScopeIgnored(), T

### Community 75 - "User Lifecycle Management"
Cohesion: 0.60
Nodes (3): Handler, Request, ResponseWriter

### Community 76 - "Static Asset Handlers"
Cohesion: 0.60
Nodes (3): Handler, Request, ResponseWriter

### Community 77 - "User Administration"
Cohesion: 0.60
Nodes (3): Handler, Request, ResponseWriter

### Community 78 - "Google Settings Handler"
Cohesion: 0.70
Nodes (3): Handler, Request, ResponseWriter

### Community 79 - "Booking Reassignment"
Cohesion: 0.60
Nodes (3): Handler, Request, ResponseWriter

### Community 80 - "Ownership and Roles"
Cohesion: 0.60
Nodes (3): Handler, Request, ResponseWriter

### Community 81 - "Idempotency Tests"
Cohesion: 0.60
Nodes (4): TestCreateBooking_differentKeysSameSlot(), TestCreateBooking_idempotencyKeyReusedDifferentBody(), TestCreateBooking_idempotentReplay(), T

### Community 82 - "Abuse Prevention Tests"
Cohesion: 0.67
Nodes (3): TestCreateBooking_honeypotRejected(), TestCreateBooking_perEmailThrottle(), T

### Community 83 - "Session Management"
Cohesion: 0.50
Nodes (3): Context, Handler, ResponseWriter

### Community 84 - "Test Email Handler"
Cohesion: 0.50
Nodes (3): Handler, Request, ResponseWriter

### Community 85 - "Public Event Tests"
Cohesion: 0.67
Nodes (3): TestPublicEventType_404ForUnknown(), TestPublicEventType_returnsPublicInfo(), T

### Community 86 - "UID Generation Tests"
Cohesion: 0.67
Nodes (3): T, TestNew_format(), TestNew_unique()

## Knowledge Gaps
- **273 isolated node(s):** `Request`, `entrypoint.sh script`, `$schema`, `style`, `config` (+268 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **12 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `contains()` connect `Email Template Rendering` to `Booking Service Logic`, `Booking Metadata Validation`, `Calendar Provider Interface`, `Booking Page Branding`, `Google Settings Management`, `Authentication and Claiming`, `Google Calendar Tests`, `User Role Management`, `Google OAuth Tests`, `Calendar Event Tests`, `Reschedule Booking Tests`, `Microsoft Calendar Tests`, `Event Type Management`, `Booking Token Tests`, `Team Management Service`, `Webhook Management`, `Availability Override Handler`, `Public Booking Tests`, `Booking Limit Tests`, `Availability Rule Handler`, `Public Event Tests`?**
  _High betweenness centrality (0.162) - this node is a cross-community bridge._
- **Why does `userFromContext()` connect `Team Management Service` to `Booking Service Logic`, `Booking Metadata Validation`, `Booking Page Branding`, `Email Auth Service`, `Authentication Middleware`, `Event Type Management`, `Booking Question Handler`, `Branding and Logos`, `Webhook Management`, `Availability Override Handler`, `Event Type Hosts`, `Calendar Connection Handler`, `Availability Rule Handler`, `API Key Management`, `Avatar Upload Handler`, `Email Settings Handler`, `User Lifecycle Management`, `User Administration`, `Google Settings Handler`, `Booking Reassignment`, `Ownership and Roles`, `Test Email Handler`?**
  _High betweenness centrality (0.109) - this node is a cross-community bridge._
- **Why does `Migrate()` connect `Database Schema Migrations` to `Booking Service Logic`, `Microsoft Calendar Tests`, `System Providers and Services`, `Calendar Provider Interface`, `Booking Management Tests`, `Authentication and Claiming`, `Google Calendar Tests`, `Google OAuth Tests`?**
  _High betweenness centrality (0.096) - this node is a cross-community bridge._
- **Are the 143 inferred relationships involving `authReq()` (e.g. with `TestArchiveUser_archivesAndBlocksLogin()` and `TestArchiveUser_blockedByUpcomingBookings()`) actually correct?**
  _`authReq()` has 143 INFERRED edges - model-reasoned connections that need verification._
- **Are the 117 inferred relationships involving `setupWorkspaceWithDB()` (e.g. with `TestArchiveUser_archivesAndBlocksLogin()` and `TestArchiveUser_blockedByUpcomingBookings()`) actually correct?**
  _`setupWorkspaceWithDB()` has 117 INFERRED edges - model-reasoned connections that need verification._
- **Are the 78 inferred relationships involving `setupWorkspace()` (e.g. with `newTestHandler()` and `TestUpdateAvailabilityRule_conflictWith409()`) actually correct?**
  _`setupWorkspace()` has 78 INFERRED edges - model-reasoned connections that need verification._
- **Are the 70 inferred relationships involving `seedEventTypeHTTP()` (e.g. with `TestUpdateAvailabilityRule_conflictWith409()` and `TestBookPage_inactiveEventType_returns404()`) actually correct?**
  _`seedEventTypeHTTP()` has 70 INFERRED edges - model-reasoned connections that need verification._