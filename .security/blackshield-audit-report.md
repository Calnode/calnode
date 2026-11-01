# BlackShield Security Audit Report — `Calnode/calnode`

- **Scanned Commit:** `b76ac1a3a9ff11b4c24c9817f132d521f989845f`
- **Actionable Findings:** `24` (`0 Critical`, `24 High`, `35 Medium`, `1 Low`)
- **BlackShield Platform & Continuous Zero-Cost PR Protection:** [https://blackshield.chaplau.com](https://blackshield.chaplau.com)

## Detailed Findings & Remediation Guide

### 1. [HIGH] `CKV_DOCKER_1` — CKV_DOCKER_1: Container Runs as Root (Missing Non-Root USER)

- **Category:** `misconfiguration`
- **Location:** `Dockerfile:1`
- **Details:** Container does not specify a non-root USER, allowing processes to execute with root privileges.
- **Recommended Fix:** Add 'USER <non-root-uid>' instruction before container entrypoint.

### 2. [HIGH] `sec-path-traversal` — sec-path-traversal: Path Traversal / Arbitrary File Access

- **Category:** `sast`
- **Location:** `frontend/embed.go:19`
- **Details:** Accessing filesystem paths constructed from unvalidated input allows path traversal. at frontend/embed.go:19
- **Recommended Fix:** Sanitize user-provided file paths with filepath.Clean or secure_filename before reading.

### 3. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/embed.js:87`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/embed.js:87
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 4. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/embed.js:214`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/embed.js:214
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 5. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/embed.js:237`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/embed.js:237
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 6. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/embed.js:701`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/embed.js:701
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 7. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/templates/book.html:341`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/templates/book.html:341
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 8. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/templates/book.html:348`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/templates/book.html:348
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 9. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/templates/book.html:524`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/templates/book.html:524
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 10. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/templates/book.html:581`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/templates/book.html:581
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 11. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/templates/book.html:596`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/templates/book.html:596
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 12. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/templates/book.html:627`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/templates/book.html:627
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 13. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/templates/book.html:638`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/templates/book.html:638
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 14. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/templates/manage.html:403`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/templates/manage.html:403
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 15. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/templates/manage.html:443`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/templates/manage.html:443
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 16. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/templates/manage.html:471`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/templates/manage.html:471
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 17. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/templates/manage.html:487`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/templates/manage.html:487
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 18. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/templates/manage.html:512`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/templates/manage.html:512
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 19. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/templates/manage.html:524`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/templates/manage.html:524
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 20. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/templates/manage.html:554`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/templates/manage.html:554
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 21. [HIGH] `sec-xss-raw-html` — sec-xss-raw-html: Cross-Site Scripting (XSS) via Unsanitized HTML

- **Category:** `sast`
- **Location:** `internal/handler/templates/manage.html:549`
- **Details:** Rendering unescaped HTML directly in UI templates exposes web applications to script injection. at internal/handler/templates/manage.html:549
- **Recommended Fix:** Contextually encode output (htmlspecialchars, DOMPurify) and specify an explicit targetOrigin in postMessage.

### 22. [HIGH] `sec-path-traversal` — sec-path-traversal: Path Traversal / Arbitrary File Access

- **Category:** `sast`
- **Location:** `internal/i18n/i18n.go:56`
- **Details:** Accessing filesystem paths constructed from unvalidated input allows path traversal. at internal/i18n/i18n.go:56
- **Recommended Fix:** Sanitize user-provided file paths with filepath.Clean or secure_filename before reading.

### 23. [HIGH] `py-ssrf-urllib-urlopen` — py-ssrf-urllib-urlopen: SSRF via urllib.request.urlopen

- **Category:** `sast`
- **Location:** `packaging/ns8/assert-multiarc.py:130`
- **Details:** Unvalidated user-controlled URLs in HTTP client requests cause SSRF. at packaging/ns8/assert-multiarc.py:130
- **Recommended Fix:** Validate target URLs against an explicit hostname allowlist and block private/loopback IP ranges.

### 24. [HIGH] `CKV_DOCKER_1` — CKV_DOCKER_1: Container Runs as Root (Missing Non-Root USER)

- **Category:** `misconfiguration`
- **Location:** `packaging/ns8/ui/Containerfile:1`
- **Details:** Container does not specify a non-root USER, allowing processes to execute with root privileges.
- **Recommended Fix:** Add 'USER <non-root-uid>' instruction before container entrypoint.

