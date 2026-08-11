package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/airwallex"
)

// listBillingSubscriptions is a seam so tests can run the reconcile without
// live Airwallex calls. Deliberately unfiltered: see subscriptionIsFinished for
// why asking only for ACTIVE is not enough to conclude anything.
var listBillingSubscriptions = func(customerId string) ([]airwallex.BillingSubscription, error) {
	return airwallex.ListBillingSubscriptions(customerId, "")
}

// Airwallex subscription statuses: PENDING | IN_TRIAL | ACTIVE | UNPAID |
// CANCELLED. Only these two mean the subscription is over for good.
var finishedSubscriptionStatuses = map[string]bool{
	"CANCELLED": true,
	"EXPIRED":   true,
}

// subscriptionIsFinished reports whether a subscription is definitively over.
//
// The inference runs this way round on purpose. Asking Airwallex for ACTIVE
// subscriptions and treating an empty answer as "cancelled" also swallows
// UNPAID — a subscription in dunning whose payment failed and is being retried,
// which is emphatically not a cancellation and can recover. It would tell a
// customer with a temporarily declined card that their plan is ending, at the
// exact moment that is most alarming and least true. PENDING and IN_TRIAL are
// wrong for the same reason.
//
// An unrecognised status also returns false, so a status Airwallex adds later
// blocks the mark rather than causing a wrong one. Silence is the safe default
// here: failing to record a cancellation is a display lag the next pass fixes,
// while recording one that did not happen is visible to the customer.
func subscriptionIsFinished(status string) bool {
	return finishedSubscriptionStatuses[strings.ToUpper(strings.TrimSpace(status))]
}

// ReconcileAirwallexCancellations repairs local rows whose cancellation webhook
// never arrived.
//
// Webhooks get dropped. Without this pass, one lost delivery leaves a row
// permanently claiming it will renew, with nothing in the system able to notice
// — which is the failure the portal was showing: the cancel button coming back
// forever. The endpoint and the webhook are the fast paths; this is the one that
// makes "the database is the truth" actually true.
//
// A user is marked only when EVERY subscription Airwallex holds for them is
// definitively over (see subscriptionIsFinished). Anything else — dunning,
// pending, trialling, or a status this build does not recognise — leaves the
// row alone for the next pass.
//
// Returns the number of users marked.
func ReconcileAirwallexCancellations(limit int) (int, error) {
	subs, err := model.ListRenewingUserSubscriptions(limit)
	if err != nil {
		return 0, err
	}
	if len(subs) == 0 {
		return 0, nil
	}

	ctx := context.Background()
	marked := 0
	// One row per user is enough: MarkUserSubscriptionsCancelled covers every
	// row the user holds, and a user with two rows would otherwise cost two
	// identical Airwallex round trips.
	seen := make(map[int]bool, len(subs))
	for _, sub := range subs {
		if seen[sub.UserId] {
			continue
		}
		seen[sub.UserId] = true

		customerId := model.GetAirwallexBillingCustomerId(sub.UserId)
		if customerId == "" {
			// Paid through some other rail, or predates the customer mapping.
			// Absence of an Airwallex customer is not evidence of cancellation.
			continue
		}
		remote, err := listBillingSubscriptions(customerId)
		if err != nil {
			// The whole point of this pass is repairing state from evidence. An
			// API error is the absence of evidence, not evidence of absence —
			// marking here would cancel live subscriptions during an Airwallex
			// outage. Leave it; the next pass will try again.
			logger.LogWarn(ctx, fmt.Sprintf("订阅对账：Airwallex 查询失败 user=%d customer=%s: %s", sub.UserId, customerId, err.Error()))
			continue
		}
		// Every subscription must be definitively over. One that is merely not
		// ACTIVE — dunning, pending, trialling — means "wait", not "cancelled".
		stillLive := false
		for _, s := range remote {
			if !subscriptionIsFinished(s.Status) {
				stillLive = true
				break
			}
		}
		if stillLive {
			continue
		}
		n, err := model.MarkUserSubscriptionsCancelled(sub.UserId, common.GetTimestamp())
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("订阅对账：本地标记失败 user=%d: %s", sub.UserId, err.Error()))
			continue
		}
		if n > 0 {
			marked++
			logger.LogInfo(ctx, fmt.Sprintf("订阅对账：user=%d 在 Airwallex 的订阅均已终止，补记停止续费 rows=%d", sub.UserId, n))
		}
	}
	return marked, nil
}
