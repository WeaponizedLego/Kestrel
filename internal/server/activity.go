package server

import (
	"net/http"
	"sync/atomic"
	"time"
)

// Activity tracks the wall-clock time of the most recent user-
// originated API request. The background rescanner reads LastActive
// to decide whether the user is idle enough to justify a low-intensity
// sweep; it never writes here.
//
// Two intentional simplifications:
//   - Every /api/* request counts, including cheap ones like
//     /api/scan/status. The frontend doesn't poll in a tight loop
//     (see frontend/src/transport/) so this stays a useful signal.
//   - In-process callers (the scheduler itself, OnFinish hooks) don't
//     go through HTTP, so they naturally don't pollute the counter.
type Activity struct {
	unixNano atomic.Int64
}

// NewActivity returns an Activity seeded so LastActive is non-zero at
// boot. Without the seed, the scheduler would treat a just-started
// process as "idle for decades" and kick off a rescan before the user
// has had a chance to open their browser.
func NewActivity() *Activity {
	a := &Activity{}
	a.Touch()
	return a
}

// Touch stamps the current time as the latest activity. Safe for
// concurrent callers.
func (a *Activity) Touch() {
	a.unixNano.Store(time.Now().UnixNano())
}

// LastActive returns the wall-clock time of the most recent Touch.
// Returns the zero time on an un-touched Activity.
func (a *Activity) LastActive() time.Time {
	v := a.unixNano.Load()
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(0, v)
}

// activityMiddleware calls a.Touch on every request before handing
// off to next. Cheap enough to sit in front of every /api/* route.
func activityMiddleware(a *Activity, next http.Handler) http.Handler {
	if a == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.Touch()
		next.ServeHTTP(w, r)
	})
}
