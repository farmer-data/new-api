package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/require"
)

func TestUsageLimitMessageNamesTheRefillDate(t *testing.T) {
	msg := UsageLimitMessage(i18n.LangEn, UsageLimitMonthly, pauseResumeAt)

	require.Contains(t, msg, "September 1, 2026")
	require.NotContains(t, msg, "usage.", "untranslated key leaked: "+msg)
}

func TestUsageLimitMessageDistinguishesImagesFromUsage(t *testing.T) {
	monthly := UsageLimitMessage(i18n.LangEn, UsageLimitMonthly, pauseResumeAt)
	images := UsageLimitMessage(i18n.LangEn, UsageLimitImages, pauseResumeAt)

	require.NotEqual(t, monthly, images)
	require.Contains(t, images, "image")
}

func TestUsageLimitMessageInEveryLocale(t *testing.T) {
	for _, lang := range []string{i18n.LangEn, i18n.LangZhCN, i18n.LangZhTW} {
		for _, kind := range []string{UsageLimitMonthly, UsageLimitImages} {
			msg := UsageLimitMessage(lang, kind, pauseResumeAt)
			require.NotEmpty(t, msg)
			require.NotContains(t, msg, "{{", "unfilled template in "+lang)
			require.NotContains(t, msg, "usage.", "untranslated key in "+lang)
		}
	}
}

func TestUsageLimitMessageWithoutADateStillReads(t *testing.T) {
	dated := UsageLimitMessage(i18n.LangEn, UsageLimitMonthly, pauseResumeAt)

	for _, lang := range []string{i18n.LangEn, i18n.LangZhCN, i18n.LangZhTW} {
		msg := UsageLimitMessage(lang, UsageLimitMonthly, 0)

		require.NotEmpty(t, msg)
		require.NotContains(t, msg, "{{", "unfilled template in "+lang)
		require.NotContains(t, msg, "1970", "no date is better than the epoch")
		require.NotContains(t, msg, "next cycle", "the rule is the exact date or nothing, never a stand-in")
		require.NotContains(t, msg, "下个周期", "the rule is the exact date or nothing, never a stand-in")
		require.NotContains(t, msg, "下個週期", "the rule is the exact date or nothing, never a stand-in")
	}

	require.NotEqual(t, dated, UsageLimitMessage(i18n.LangEn, UsageLimitMonthly, 0),
		"the dateless message must not read the same as the dated one")
}

func withLimits(t *testing.T, cost string, images string) {
	t.Helper()
	require.NoError(t, setting.UpdateMonthlyCostLimitGroupByJSONString(cost))
	require.NoError(t, setting.UpdateMonthlyImageLimitGroupByJSONString(images))
	t.Cleanup(func() {
		_ = setting.UpdateMonthlyCostLimitGroupByJSONString(`{}`)
		_ = setting.UpdateMonthlyImageLimitGroupByJSONString(`{}`)
	})
}

func TestCheckUsageAllowsAFreshCycle(t *testing.T) {
	truncate(t)
	withLimits(t, `{"free":1300000}`, `{"free":0}`)

	require.NoError(t, CheckUsageAllowance(41, "free", i18n.LangEn, nil, nil, time.Now()))
}

func TestCheckUsageRefusesOnceTheCostLimitIsSpent(t *testing.T) {
	truncate(t)
	withLimits(t, `{"free":1000}`, `{"free":0}`)
	start, _ := UsageCycle(CycleMonth, 0, nil, time.Now())
	require.NoError(t, model.AddUsage(42, CycleMonth, start, 1000, 1))

	err := CheckUsageAllowance(42, "free", i18n.LangEn, nil, nil, time.Now())

	require.Error(t, err)
	require.Contains(t, err.Error(), "refills on")
}

func TestCheckUsageRefusalIsLocalised(t *testing.T) {
	truncate(t)
	withLimits(t, `{"free":1000}`, `{"free":0}`)
	start, _ := UsageCycle(CycleMonth, 0, nil, time.Now())
	require.NoError(t, model.AddUsage(46, CycleMonth, start, 1000, 1))

	en := CheckUsageAllowance(46, "free", i18n.LangEn, nil, nil, time.Now())
	require.Error(t, en)
	require.Contains(t, en.Error(), "refills on")

	zh := CheckUsageAllowance(46, "free", i18n.LangZhCN, nil, nil, time.Now())
	require.Error(t, zh)
	require.Contains(t, zh.Error(), "已用完")
	require.NotEqual(t, en.Error(), zh.Error(), "the same exhausted condition must not read the same in a different locale")
}

func TestCheckUsageWithAnUncappedGroupNeverRefuses(t *testing.T) {
	truncate(t)
	withLimits(t, `{"pro":0}`, `{"pro":100}`)
	start, _ := UsageCycle(CycleMonth, 0, nil, time.Now())
	require.NoError(t, model.AddUsage(43, CycleMonth, start, 999999999, 1))

	require.NoError(t, CheckUsageAllowance(43, "pro", i18n.LangEn, nil, nil, time.Now()))
}

func TestCheckUsageRefusesImagesForATierWithoutTheEntitlement(t *testing.T) {
	truncate(t)
	withLimits(t, `{"plus":13200000}`, `{"plus":0}`)

	err := CheckUsageAllowance(44, "plus", i18n.LangEn, nil, []string{"hash-a"}, time.Now())

	require.Error(t, err)
}

// FINDING 1: a group entirely absent from the image-limit map (the option
// defaults to {}) must be uncapped, exactly like an absent cost-limit group —
// not refused. Refusing here is the "every vision request 403s until someone
// fills in the config" bug.
func TestCheckUsageWithNoImageLimitConfiguredAtAllNeverRefusesImages(t *testing.T) {
	truncate(t)
	withLimits(t, `{}`, `{}`)

	err := CheckUsageAllowance(50, "pro", i18n.LangEn, nil, []string{"hash-unconfigured"}, time.Now())

	require.NoError(t, err, "an unconfigured group must not refuse images the way an explicit 0 does")
}

// An explicit 0 is a deliberate "this tier gets no images" setting and must
// still refuse — this is the control proving the fix didn't just always allow.
func TestCheckUsageWithExplicitZeroImageLimitStillRefuses(t *testing.T) {
	truncate(t)
	withLimits(t, `{}`, `{"free":0}`)

	err := CheckUsageAllowance(51, "free", i18n.LangEn, nil, []string{"hash-a"}, time.Now())

	require.Error(t, err)
}

// FINDING 6: a group with an explicit 0 image entitlement has never had any
// images to spend, so the "it refills on {Date}" wall is a lie. It must read
// differently from the real exhausted-a-real-allowance message and name no date.
func TestCheckUsageNoEntitlementMessageDiffersFromExhaustedMessage(t *testing.T) {
	truncate(t)
	withLimits(t, `{}`, `{"free":0}`)
	noEntitlement := CheckUsageAllowance(52, "free", i18n.LangEn, nil, []string{"hash-a"}, time.Now())
	require.Error(t, noEntitlement)

	withLimits(t, `{}`, `{"pro":1}`)
	start, _ := UsageCycle(CycleMonth, 0, nil, time.Now())
	require.NoError(t, CheckUsageAllowance(53, "pro", i18n.LangEn, nil, []string{"hash-x"}, time.Now()))
	_ = start
	exhausted := CheckUsageAllowance(53, "pro", i18n.LangEn, nil, []string{"hash-y"}, time.Now())
	require.Error(t, exhausted)

	require.NotEqual(t, noEntitlement.Error(), exhausted.Error())
	require.NotContains(t, noEntitlement.Error(), "refills on", "no entitlement was ever granted, so nothing refills")
	require.Contains(t, exhausted.Error(), "refills on")
}

func TestCheckUsageChargesAnImageOnlyOncePerCycle(t *testing.T) {
	truncate(t)
	withLimits(t, `{"pro":0}`, `{"pro":1}`)
	now := time.Now()

	require.NoError(t, CheckUsageAllowance(45, "pro", i18n.LangEn, nil, []string{"hash-a"}, now))
	require.NoError(t, CheckUsageAllowance(45, "pro", i18n.LangEn, nil, []string{"hash-a"}, now),
		"the same image re-sent on the next turn is already paid for")
	require.Error(t, CheckUsageAllowance(45, "pro", i18n.LangEn, nil, []string{"hash-b"}, now))
}

// --- weekly ----------------------------------------------------------------

func withWeeklyLimit(t *testing.T, weekly string) {
	t.Helper()
	require.NoError(t, setting.UpdateWeeklyCostLimitGroupByJSONString(weekly))
	t.Cleanup(func() {
		_ = setting.UpdateWeeklyCostLimitGroupByJSONString(`{}`)
	})
}

func TestUsageLimitMessageDistinguishesWeeklyFromMonthly(t *testing.T) {
	weekly := UsageLimitMessage(i18n.LangEn, UsageLimitWeekly, pauseResumeAt)
	monthly := UsageLimitMessage(i18n.LangEn, UsageLimitMonthly, pauseResumeAt)

	require.NotEqual(t, weekly, monthly, "the two walls refill at different times and must not read alike")
	require.Contains(t, weekly, "week")
	require.Contains(t, monthly, "month")
}

// 本周期 and 本周 differ by one character; a reader glancing at the wall must be
// able to tell which allowance ran out.
func TestUsageLimitMessageWeeklyAndMonthlyDifferInEveryLocale(t *testing.T) {
	for _, lang := range []string{i18n.LangEn, i18n.LangZhCN, i18n.LangZhTW} {
		weekly := UsageLimitMessage(lang, UsageLimitWeekly, pauseResumeAt)
		monthly := UsageLimitMessage(lang, UsageLimitMonthly, pauseResumeAt)

		require.NotEmpty(t, weekly)
		require.NotContains(t, weekly, "{{", "unfilled template in "+lang)
		require.NotContains(t, weekly, "usage.", "untranslated key in "+lang)
		require.NotEqual(t, weekly, monthly, "weekly and monthly must not read alike in "+lang)
	}
}

func TestCheckUsageRefusesOnceTheWeeklyLimitIsSpent(t *testing.T) {
	truncate(t)
	withLimits(t, `{"free":0}`, `{"free":0}`)
	withWeeklyLimit(t, `{"free":1000}`)
	start, _ := UsageCycle(CycleWeek, 51, nil, time.Now())
	require.NoError(t, model.AddUsage(51, CycleWeek, start, 1000, 1))

	err := CheckUsageAllowance(51, "free", i18n.LangEn, nil, nil, time.Now())

	require.Error(t, err)
	require.Contains(t, err.Error(), "week")
}

// When both ceilings are spent the user is not free until the later of the two
// refills, so that is the date the wall must name. Naming the sooner one sends
// them away to return on a day they are still blocked — the exact failure the
// "real date or nothing" rule exists to prevent.
func TestCheckUsageNamesTheLaterWallWhenBothAreUp(t *testing.T) {
	truncate(t)
	withLimits(t, `{"free":1000}`, `{"free":0}`)
	withWeeklyLimit(t, `{"free":1000}`)
	now := time.Now()
	weekStart, weekResetAt := UsageCycle(CycleWeek, 52, nil, now)
	monthStart, monthResetAt := UsageCycle(CycleMonth, 52, nil, now)
	require.NoError(t, model.AddUsage(52, CycleWeek, weekStart, 5000, 1))
	require.NoError(t, model.AddUsage(52, CycleMonth, monthStart, 5000, 1))

	err := CheckUsageAllowance(52, "free", i18n.LangEn, nil, nil, now)

	require.Error(t, err)
	// Which of the two is later depends on where in the month "now" falls, so
	// the expectation is derived rather than hard-coded.
	later, sooner := "month", "week"
	if weekResetAt > monthResetAt {
		later, sooner = "week", "month"
	}
	require.Contains(t, err.Error(), later)
	require.NotContains(t, err.Error(), sooner)
}

// The one-wall cases must still name their own date: the "later of the two"
// rule applies only among ceilings that are actually spent.
func TestCheckUsageNamesTheWeeklyWallWhenOnlyItIsUp(t *testing.T) {
	truncate(t)
	withLimits(t, `{"free":999999999}`, `{"free":0}`)
	withWeeklyLimit(t, `{"free":1000}`)
	now := time.Now()
	weekStart, _ := UsageCycle(CycleWeek, 55, nil, now)
	require.NoError(t, model.AddUsage(55, CycleWeek, weekStart, 5000, 1))

	err := CheckUsageAllowance(55, "free", i18n.LangEn, nil, nil, now)

	require.Error(t, err)
	require.Contains(t, err.Error(), "week")
	require.NotContains(t, err.Error(), "month")
}

// The weekly counter is a separate row; exhausting it must not be inferred
// from monthly spend.
func TestCheckUsageWeeklyIgnoresTheMonthlyCounter(t *testing.T) {
	truncate(t)
	withLimits(t, `{"free":0}`, `{"free":0}`)
	withWeeklyLimit(t, `{"free":1000}`)
	monthStart, _ := UsageCycle(CycleMonth, 53, nil, time.Now())
	require.NoError(t, model.AddUsage(53, CycleMonth, monthStart, 999999, 1))

	require.NoError(t, CheckUsageAllowance(53, "free", i18n.LangEn, nil, nil, time.Now()))
}

// Images are charged to the weekly row, so the weekly row is where the count
// has to land — the monthly row must stay untouched by an image reservation.
func TestCheckUsageChargesImagesToTheWeeklyRow(t *testing.T) {
	truncate(t)
	withLimits(t, `{"pro":0}`, `{"pro":70}`)
	now := time.Now()

	require.NoError(t, CheckUsageAllowance(54, "pro", i18n.LangEn, nil, []string{"img-a", "img-b"}, now))

	weekStart, _ := UsageCycle(CycleWeek, 54, nil, now)
	monthStart, _ := UsageCycle(CycleMonth, 54, nil, now)
	_, _, weekImages, err := model.GetUsage(54, CycleWeek, weekStart)
	require.NoError(t, err)
	require.Equal(t, 2, weekImages)

	_, _, monthImages, err := model.GetUsage(54, CycleMonth, monthStart)
	require.NoError(t, err)
	require.Equal(t, 0, monthImages, "images must not also land on the monthly row")
}
