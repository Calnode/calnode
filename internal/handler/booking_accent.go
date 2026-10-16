package handler

import (
	"math"
	"regexp"
	"strconv"
)

// accentFallback is the accent used when the stored value isn't a valid color.
// Writes are validated (setup.go), but reads must not trust the row: seeders,
// manual SQL, or a future regression could leave anything there, and the value
// flows into a style attribute.
const accentFallback = "#111827"

var validAccent = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// validAccentColor reports whether color is safe to interpolate into CSS.
func validAccentColor(color string) bool {
	return validAccent.MatchString(color)
}

// accentOrDefault returns the stored accent when valid, else the fallback.
func accentOrDefault(color string) string {
	if validAccentColor(color) {
		return color
	}
	return accentFallback
}

func accentForeground(color string) string {
	if len(color) != 7 {
		return "#ffffff"
	}
	value, err := strconv.ParseUint(color[1:], 16, 32)
	if err != nil {
		return "#ffffff"
	}
	linear := func(v uint64) float64 {
		c := float64(v) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	luminance := 0.2126*linear(value>>16) + 0.7152*linear((value>>8)&255) + 0.0722*linear(value&255)
	if luminance > 0.179 {
		return "#000000"
	}
	return "#ffffff"
}
