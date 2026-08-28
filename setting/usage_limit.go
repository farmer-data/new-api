package setting

import (
	"errors"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

// Per-cycle allowances, keyed by user group exactly like
// ModelRequestRateLimitGroup. 0 means uncapped for cost (Pro is bounded by its
// subscription pool instead) and none-allowed for images. An unknown group is
// uncapped: a group must opt in to a ceiling rather than inherit one.
//
// Weekly and monthly cost ceilings are independent and both enforced; the
// weekly one is the tighter, sooner-refilling wall. MonthlyImageLimitGroup
// keeps its name for config compatibility (renaming the option key would orphan
// the stored row) but is enforced against the WEEKLY cycle — see
// service.CheckUsageAllowance.
var (
	usageLimitMu           sync.RWMutex
	MonthlyCostLimitGroup  = map[string]int64{}
	WeeklyCostLimitGroup   = map[string]int64{}
	MonthlyImageLimitGroup = map[string]int{}
)

func WeeklyCostLimitGroup2JSONString() string {
	usageLimitMu.RLock()
	defer usageLimitMu.RUnlock()
	b, err := common.Marshal(WeeklyCostLimitGroup)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func UpdateWeeklyCostLimitGroupByJSONString(jsonStr string) error {
	usageLimitMu.Lock()
	defer usageLimitMu.Unlock()
	WeeklyCostLimitGroup = make(map[string]int64)
	return common.Unmarshal([]byte(jsonStr), &WeeklyCostLimitGroup)
}

func GetWeeklyCostLimit(group string) int64 {
	usageLimitMu.RLock()
	defer usageLimitMu.RUnlock()
	return WeeklyCostLimitGroup[group]
}

func CheckWeeklyCostLimitGroup(jsonStr string) error {
	check := make(map[string]int64)
	if err := common.Unmarshal([]byte(jsonStr), &check); err != nil {
		return err
	}
	for group, limit := range check {
		if limit < 0 {
			return errors.New("weekly cost limit must be >= 0 for group " + group)
		}
	}
	return nil
}

func MonthlyCostLimitGroup2JSONString() string {
	usageLimitMu.RLock()
	defer usageLimitMu.RUnlock()
	b, err := common.Marshal(MonthlyCostLimitGroup)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func UpdateMonthlyCostLimitGroupByJSONString(jsonStr string) error {
	usageLimitMu.Lock()
	defer usageLimitMu.Unlock()
	MonthlyCostLimitGroup = make(map[string]int64)
	return common.Unmarshal([]byte(jsonStr), &MonthlyCostLimitGroup)
}

func GetMonthlyCostLimit(group string) int64 {
	usageLimitMu.RLock()
	defer usageLimitMu.RUnlock()
	return MonthlyCostLimitGroup[group]
}

func CheckMonthlyCostLimitGroup(jsonStr string) error {
	check := make(map[string]int64)
	if err := common.Unmarshal([]byte(jsonStr), &check); err != nil {
		return err
	}
	for group, limit := range check {
		if limit < 0 {
			return errors.New("monthly cost limit must be >= 0 for group " + group)
		}
	}
	return nil
}

func MonthlyImageLimitGroup2JSONString() string {
	usageLimitMu.RLock()
	defer usageLimitMu.RUnlock()
	b, err := common.Marshal(MonthlyImageLimitGroup)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func UpdateMonthlyImageLimitGroupByJSONString(jsonStr string) error {
	usageLimitMu.Lock()
	defer usageLimitMu.Unlock()
	MonthlyImageLimitGroup = make(map[string]int)
	return common.Unmarshal([]byte(jsonStr), &MonthlyImageLimitGroup)
}

// GetMonthlyImageLimit reports the group's configured image limit and whether
// the group is present in the map at all. A group configured at 0 (found=true)
// deliberately gets no images; a group absent from the map (found=false) has
// no entitlement configured yet and must not be treated the same way — see
// service.CheckUsageAllowance, which only enforces the limit when found.
func GetMonthlyImageLimit(group string) (int, bool) {
	usageLimitMu.RLock()
	defer usageLimitMu.RUnlock()
	limit, found := MonthlyImageLimitGroup[group]
	return limit, found
}

func CheckMonthlyImageLimitGroup(jsonStr string) error {
	check := make(map[string]int)
	if err := common.Unmarshal([]byte(jsonStr), &check); err != nil {
		return err
	}
	for group, limit := range check {
		if limit < 0 {
			return errors.New("monthly image limit must be >= 0 for group " + group)
		}
	}
	return nil
}
