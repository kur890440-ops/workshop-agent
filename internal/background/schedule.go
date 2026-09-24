package background

import (
	"time"
	_ "time/tzdata"
)

// Next uses calendar days in the named IANA zone, not server-local 24h arithmetic.
func Next(now time.Time, local, zone string, catchUp bool) (time.Time, error) {
	t, e := time.Parse("15:04", local)
	if e != nil || t.Format("15:04") != local {
		return time.Time{}, ErrInput
	}
	loc, e := time.LoadLocation(zone)
	if e != nil || zone == "Local" || zone == "" {
		return time.Time{}, ErrInput
	}
	n := now.In(loc)
	due := time.Date(n.Year(), n.Month(), n.Day(), t.Hour(), t.Minute(), 0, 0, loc)
	if !due.After(now) && !catchUp {
		due = time.Date(n.Year(), n.Month(), n.Day()+1, t.Hour(), t.Minute(), 0, 0, loc)
	}
	return due.UTC(), nil
}
