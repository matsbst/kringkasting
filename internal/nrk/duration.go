package nrk

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/matsbst/kringkasting/internal/clock"
)

var isoDuration = regexp.MustCompile(
	`^P(?:(\d+(?:[.,]\d+)?)Y)?(?:(\d+(?:[.,]\d+)?)M)?(?:(\d+(?:[.,]\d+)?)W)?(?:(\d+(?:[.,]\d+)?)D)?` +
		`(?:T(?:(\d+(?:[.,]\d+)?)H)?(?:(\d+(?:[.,]\d+)?)M)?(?:(\d+(?:[.,]\d+)?)S)?)?$`)

// DurationSeconds converts an ISO 8601 duration ("PT3M19S") to seconds,
// as the iso8601-duration package did: calendar parts are applied to the
// current date, time parts are exact.
func DurationSeconds(value string) (float64, error) {
	m := isoDuration.FindStringSubmatch(value)
	if m == nil || value == "P" || value == "PT" {
		return 0, fmt.Errorf("invalid ISO 8601 duration %q", value)
	}
	num := func(s string) float64 {
		if s == "" {
			return 0
		}
		n, _ := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
		return n
	}
	years, months, weeks, days := num(m[1]), num(m[2]), num(m[3]), num(m[4])
	hours, minutes, seconds := num(m[5]), num(m[6]), num(m[7])

	start := clock.Now()
	end := start.AddDate(int(years), int(months), int(days+weeks*7))
	calendar := end.Sub(start).Seconds()
	return calendar + hours*3600 + minutes*60 + seconds, nil
}
