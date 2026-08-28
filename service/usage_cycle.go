package service

import (
	"time"

	"github.com/QuantumNous/new-api/model"
)

// Cycle kinds. The counter table carries the kind, so the two coexist per user
// without a migration (see model.UserUsageCounter's unique index).
const (
	CycleMonth = "month"
	CycleWeek  = "week"
)

const weekSeconds int64 = 7 * 24 * 60 * 60

// weekGridEpoch is a Monday, used only to give slotted accounts a stable grid
// to sit on. It is not a refill date anyone sees: slot pushes each account off
// it by a whole number of days.
func weekGridEpoch(loc *time.Location) time.Time {
	return time.Date(2024, 1, 1, 0, 0, 0, 0, loc) // a Monday
}

// UsageCycle reports which cycle `now` falls in and when that cycle refills.
// A user with a metered subscription counts against the subscription's own
// cycle, so the pool and the counters always share one refill date. Everyone
// else uses the calendar month, or — for CycleWeek — a per-account slot.
//
// slot spreads unsubscribed accounts across the seven weekdays. Pass the user
// id. It is ignored for CycleMonth, and for any account with a billing anchor
// to follow. Slotting is not a load-shedding nicety: on a shared calendar week
// an account created on a Saturday would get a two-day first cycle and hit its
// cap almost immediately, which reads as a broken limit rather than a short
// week.
func UsageCycle(kind string, slot int, sub *model.UserSubscription, now time.Time) (cycleStart int64, resetAt int64) {
	if kind == CycleWeek {
		return weeklyCycle(slot, sub, now)
	}
	if sub != nil {
		if sub.AmountTotal > 0 {
			start := sub.LastResetTime
			if start <= 0 {
				start = sub.StartTime
			}
			return start, sub.NextResetTime
		}
		// A paying tier with no metered pool (Plus today) has no reset date to
		// read — next_reset_time is 0 — but it does have a billing date. Anchor
		// to that, so the renewal date and the usage refill date on the portal
		// card are the same day rather than two dates that invite the question.
		if sub.StartTime > 0 {
			return monthlyAnniversary(sub.StartTime, now)
		}
	}
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	return first.Unix(), first.AddDate(0, 1, 0).Unix()
}

// monthlyAnniversary reports the most recent monthly anniversary of anchor on
// or before now, and the next one. It walks months rather than using the
// subscription's end_time because an annual plan runs a year end to end, and
// the usage cycle must stay monthly inside it.
func monthlyAnniversary(anchorUnix int64, now time.Time) (int64, int64) {
	anchor := time.Unix(anchorUnix, 0).In(now.Location())
	if now.Before(anchor) {
		return anchor.Unix(), addMonthsClamped(anchor, 1).Unix()
	}
	months := (now.Year()-anchor.Year())*12 + int(now.Month()) - int(anchor.Month())
	if addMonthsClamped(anchor, months).After(now) {
		months--
	}
	return addMonthsClamped(anchor, months).Unix(), addMonthsClamped(anchor, months+1).Unix()
}

// weeklyCycle walks fixed seven-day steps from an anchor. Unlike the monthly
// branch there is no clamping to worry about, because a week has no short
// months — which is also why it does not reuse monthlyAnniversary.
//
// A metered subscription's LastResetTime/NextResetTime are deliberately NOT
// consulted here: those describe the pool's monthly refill and carry no weekly
// meaning. Only StartTime, the billing anchor, is used.
func weeklyCycle(slot int, sub *model.UserSubscription, now time.Time) (int64, int64) {
	var anchor time.Time
	if sub != nil && sub.StartTime > 0 {
		anchor = time.Unix(sub.StartTime, 0).In(now.Location())
	} else {
		offset := slot % 7
		if offset < 0 {
			offset += 7
		}
		anchor = weekGridEpoch(now.Location()).AddDate(0, 0, offset)
	}

	if !now.After(anchor) {
		return anchor.Unix(), anchor.Unix() + weekSeconds
	}
	elapsed := now.Unix() - anchor.Unix()
	start := anchor.Unix() + (elapsed/weekSeconds)*weekSeconds
	return start, start + weekSeconds
}

// addMonthsClamped adds whole months, clamping a day-of-month that the target
// month is too short to hold. Go's AddDate rolls the overflow forward instead —
// 31 January plus one month is 3 March — which would drift a subscriber's cycle
// every short month.
func addMonthsClamped(t time.Time, months int) time.Time {
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()).AddDate(0, months, 0)
	day := t.Day()
	if last := daysInMonth(first.Year(), first.Month()); day > last {
		day = last
	}
	return time.Date(first.Year(), first.Month(), day, t.Hour(), t.Minute(), t.Second(), 0, t.Location())
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// CycleSubscription picks the row whose billing date anchors a user's usage
// cycle. Every caller — the gate, the accrual and the portal endpoint — must
// pick the same one, or they would read and write different counter rows for
// the same user. Selection is "the first active row", matching the order
// GetAllActiveUserSubscriptions returns.
func CycleSubscription(subs []model.UserSubscription) *model.UserSubscription {
	if len(subs) == 0 {
		return nil
	}
	return &subs[0]
}

// CycleSubscriptionFor loads the anchoring row for a user. Returns nil when
// they have none, which is the Free case and falls back to the calendar month.
func CycleSubscriptionFor(userId int) *model.UserSubscription {
	summaries, err := model.GetAllActiveUserSubscriptions(userId)
	if err != nil {
		return nil
	}
	subs := make([]model.UserSubscription, 0, len(summaries))
	for _, summary := range summaries {
		if summary.Subscription != nil {
			subs = append(subs, *summary.Subscription)
		}
	}
	return CycleSubscription(subs)
}
