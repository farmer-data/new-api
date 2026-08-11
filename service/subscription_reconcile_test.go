package service

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/airwallex"
)

func truncateSubscriptionTables(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM user_subscriptions")
		model.DB.Exec("DELETE FROM airwallex_billing_customers")
	})
}

func withStubbedAirwallex(t *testing.T, fn func(customerId string) ([]airwallex.BillingSubscription, error)) {
	t.Helper()
	orig := listBillingSubscriptions
	listBillingSubscriptions = fn
	t.Cleanup(func() { listBillingSubscriptions = orig })
}

func seedRenewingSub(t *testing.T, userId int, customerId string) *model.UserSubscription {
	t.Helper()
	now := common.GetTimestamp()
	sub := &model.UserSubscription{UserId: userId, PlanId: 1, Status: "active",
		StartTime: now - 100, EndTime: now + 86400, UpgradeGroup: "plus", Source: "order"}
	if err := model.DB.Create(sub).Error; err != nil {
		t.Fatal(err)
	}
	if customerId != "" {
		if err := model.SaveAirwallexBillingCustomerId(userId, customerId); err != nil {
			t.Fatal(err)
		}
	}
	return sub
}

func cancelledAt(t *testing.T, id int) int64 {
	t.Helper()
	var got model.UserSubscription
	if err := model.DB.First(&got, id).Error; err != nil {
		t.Fatal(err)
	}
	return got.CancelledAt
}

func TestReconcileMarksCustomerWithNoActiveSubscription(t *testing.T) {
	truncateSubscriptionTables(t)
	sub := seedRenewingSub(t, 7, "bcus_gone")

	withStubbedAirwallex(t, func(string) ([]airwallex.BillingSubscription, error) {
		return []airwallex.BillingSubscription{{Id: "sub_1", Status: "CANCELLED"}}, nil
	})

	n, err := ReconcileAirwallexCancellations(100)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("marked %d users, want 1", n)
	}
	if cancelledAt(t, sub.Id) == 0 {
		t.Fatal("row should have been marked cancelled")
	}
}

func TestReconcileLeavesLiveSubscriptionAlone(t *testing.T) {
	truncateSubscriptionTables(t)
	sub := seedRenewingSub(t, 7, "bcus_live")

	withStubbedAirwallex(t, func(string) ([]airwallex.BillingSubscription, error) {
		return []airwallex.BillingSubscription{{Id: "sub_1", Status: "ACTIVE"}}, nil
	})

	if _, err := ReconcileAirwallexCancellations(100); err != nil {
		t.Fatal(err)
	}
	if got := cancelledAt(t, sub.Id); got != 0 {
		t.Fatalf("cancelled_at = %d, want 0 — a live subscription must not be marked", got)
	}
}

func TestReconcileDoesNotMarkOnApiError(t *testing.T) {
	truncateSubscriptionTables(t)
	sub := seedRenewingSub(t, 7, "bcus_err")

	withStubbedAirwallex(t, func(string) ([]airwallex.BillingSubscription, error) {
		return nil, errors.New("airwallex unavailable")
	})

	n, err := ReconcileAirwallexCancellations(100)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("marked %d users during an outage, want 0", n)
	}
	// The whole pass reasons from evidence. An API error is the absence of
	// evidence, not evidence of absence — marking here would cancel every live
	// subscription in the system during an Airwallex outage.
	if got := cancelledAt(t, sub.Id); got != 0 {
		t.Fatalf("cancelled_at = %d, want 0 — an API error must never mark a row", got)
	}
}

func TestReconcileSkipsUsersWithNoAirwallexCustomer(t *testing.T) {
	truncateSubscriptionTables(t)
	sub := seedRenewingSub(t, 7, "") // paid another way, or predates the mapping

	called := false
	withStubbedAirwallex(t, func(string) ([]airwallex.BillingSubscription, error) {
		called = true
		return nil, nil
	})

	if _, err := ReconcileAirwallexCancellations(100); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("should not query Airwallex for a user with no billing customer")
	}
	if got := cancelledAt(t, sub.Id); got != 0 {
		t.Fatalf("cancelled_at = %d, want 0", got)
	}
}

func TestReconcileQueriesEachCustomerOnce(t *testing.T) {
	truncateSubscriptionTables(t)
	seedRenewingSub(t, 7, "bcus_dup")
	seedRenewingSub(t, 7, "bcus_dup") // same user, second row

	calls := 0
	withStubbedAirwallex(t, func(string) ([]airwallex.BillingSubscription, error) {
		calls++
		return nil, nil
	})

	if _, err := ReconcileAirwallexCancellations(100); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("made %d Airwallex calls for one user, want 1", calls)
	}
}

// The statuses below are the ones that made the first version of this pass
// wrong in production: it asked Airwallex for ACTIVE subscriptions and read an
// empty answer as "cancelled", which silently swallowed every other state.
func TestReconcileDoesNotMarkNonTerminalStatuses(t *testing.T) {
	for _, status := range []string{"UNPAID", "PENDING", "IN_TRIAL", "SOMETHING_NEW"} {
		t.Run(status, func(t *testing.T) {
			truncateSubscriptionTables(t)
			sub := seedRenewingSub(t, 7, "bcus_"+status)

			withStubbedAirwallex(t, func(string) ([]airwallex.BillingSubscription, error) {
				return []airwallex.BillingSubscription{{Id: "sub_1", Status: status}}, nil
			})

			n, err := ReconcileAirwallexCancellations(100)
			if err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatalf("marked %d users on status %s, want 0", n, status)
			}
			if got := cancelledAt(t, sub.Id); got != 0 {
				t.Fatalf("cancelled_at = %d on status %s, want 0 — only a definitively "+
					"finished subscription is a cancellation; UNPAID is dunning and can recover", got, status)
			}
		})
	}
}

func TestReconcileMarksOnlyWhenEverySubscriptionIsFinished(t *testing.T) {
	truncateSubscriptionTables(t)
	sub := seedRenewingSub(t, 7, "bcus_mixed")

	// An old cancelled subscription alongside a live one: the live one wins.
	withStubbedAirwallex(t, func(string) ([]airwallex.BillingSubscription, error) {
		return []airwallex.BillingSubscription{
			{Id: "sub_old", Status: "CANCELLED"},
			{Id: "sub_new", Status: "ACTIVE"},
		}, nil
	})
	if _, err := ReconcileAirwallexCancellations(100); err != nil {
		t.Fatal(err)
	}
	if got := cancelledAt(t, sub.Id); got != 0 {
		t.Fatalf("cancelled_at = %d, want 0 — one live subscription means still renewing", got)
	}

	// Now every one of them is over.
	withStubbedAirwallex(t, func(string) ([]airwallex.BillingSubscription, error) {
		return []airwallex.BillingSubscription{
			{Id: "sub_old", Status: "CANCELLED"},
			{Id: "sub_new", Status: "EXPIRED"},
		}, nil
	})
	if _, err := ReconcileAirwallexCancellations(100); err != nil {
		t.Fatal(err)
	}
	if cancelledAt(t, sub.Id) == 0 {
		t.Fatal("every subscription finished — should have been marked")
	}
}

func TestSubscriptionIsFinished(t *testing.T) {
	for status, want := range map[string]bool{
		"CANCELLED": true, "cancelled": true, " CANCELLED ": true, "EXPIRED": true,
		"ACTIVE": false, "UNPAID": false, "PENDING": false, "IN_TRIAL": false, "": false,
	} {
		if got := subscriptionIsFinished(status); got != want {
			t.Errorf("subscriptionIsFinished(%q) = %v, want %v", status, got, want)
		}
	}
}

func TestReconcileMarksWhenAirwallexHoldsNoSubscriptionAtAll(t *testing.T) {
	truncateSubscriptionTables(t)
	sub := seedRenewingSub(t, 7, "bcus_empty")

	// Pinned deliberately rather than incidentally: the candidate already has a
	// local source="order" row, so an empty list is genuine absence at the
	// processor, not ambiguity. Requiring an explicit CANCELLED row instead
	// would reinstate the original bug for any customer whose cancelled
	// subscription drops off the list entirely.
	withStubbedAirwallex(t, func(string) ([]airwallex.BillingSubscription, error) {
		return nil, nil
	})

	if _, err := ReconcileAirwallexCancellations(100); err != nil {
		t.Fatal(err)
	}
	if cancelledAt(t, sub.Id) == 0 {
		t.Fatal("no subscription at the processor — should have been marked")
	}
}
