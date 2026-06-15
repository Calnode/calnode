// Loaded before every browser test (see vitest.config.ts setupFiles).
// 1. Import the real app stylesheet so Tailwind compiles the actual utilities
//    (incl. the @custom-variant rules) into the test page — computed styles are
//    only meaningful if the real CSS is present.
// 2. Kill transitions/animations so getComputedStyle reads final values, not
//    mid-interpolation ones (the trap that misled the original manual debugging:
//    a switch read mid-transition looks unstyled).
import './app.css';

const style = document.createElement('style');
style.textContent = '*,*::before,*::after{transition:none!important;animation:none!important}';
document.head.appendChild(style);
