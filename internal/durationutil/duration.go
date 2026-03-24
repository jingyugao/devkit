package durationutil

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var unitDurations = map[string]time.Duration{
	"ns": time.Nanosecond,
	"us": time.Microsecond,
	"µs": time.Microsecond,
	"ms": time.Millisecond,
	"s":  time.Second,
	"m":  time.Minute,
	"h":  time.Hour,
	"d":  24 * time.Hour,
	"w":  7 * 24 * time.Hour,
}

func Parse(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "0" {
		return 0, nil
	}

	var total time.Duration
	for value != "" {
		numEnd := 0
		dotSeen := false
		for numEnd < len(value) {
			ch := rune(value[numEnd])
			if ch == '.' {
				if dotSeen {
					break
				}
				dotSeen = true
				numEnd++
				continue
			}
			if !unicode.IsDigit(ch) {
				break
			}
			numEnd++
		}
		if numEnd == 0 {
			return 0, fmt.Errorf("invalid duration segment %q", value)
		}
		number := value[:numEnd]
		value = value[numEnd:]

		unitEnd := 0
		for unitEnd < len(value) {
			ch := rune(value[unitEnd])
			if unicode.IsLetter(ch) || ch == 'µ' {
				unitEnd++
				continue
			}
			break
		}
		if unitEnd == 0 {
			return 0, fmt.Errorf("missing duration unit after %q", number)
		}
		unit := value[:unitEnd]
		value = value[unitEnd:]

		base, ok := unitDurations[unit]
		if !ok {
			return 0, fmt.Errorf("unsupported duration unit %q", unit)
		}

		f, err := strconv.ParseFloat(number, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration number %q: %w", number, err)
		}
		total += time.Duration(float64(base) * f)
	}
	return total, nil
}

func MustParse(value string) time.Duration {
	d, err := Parse(value)
	if err != nil {
		panic(err)
	}
	return d
}
