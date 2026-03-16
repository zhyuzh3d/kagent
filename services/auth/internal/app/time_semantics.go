package app

import (
	"fmt"
	"time"
)

type semanticTimeFields struct {
	ISO          string
	LocalYMDHMS  string
	LocalWeekday string
	LocalLunar   string
}

func buildSemanticTimeFields(tsMS int64) semanticTimeFields {
	if tsMS <= 0 {
		tsMS = nowMS()
	}
	localTime := time.UnixMilli(tsMS).In(time.Local)
	return semanticTimeFields{
		ISO:          localTime.Format(time.RFC3339),
		LocalYMDHMS:  localTime.Format("2006年01月02日 15:04:05"),
		LocalWeekday: chineseWeekday(localTime.Weekday()),
		LocalLunar:   lunarLabel(localTime),
	}
}

func chineseWeekday(day time.Weekday) string {
	switch day {
	case time.Sunday:
		return "星期日"
	case time.Monday:
		return "星期一"
	case time.Tuesday:
		return "星期二"
	case time.Wednesday:
		return "星期三"
	case time.Thursday:
		return "星期四"
	case time.Friday:
		return "星期五"
	case time.Saturday:
		return "星期六"
	default:
		return "星期?"
	}
}

var lunarInfo = [...]int{
	0x04bd8, 0x04ae0, 0x0a570, 0x054d5, 0x0d260,
	0x0d950, 0x16554, 0x056a0, 0x09ad0, 0x055d2,
	0x04ae0, 0x0a5b6, 0x0a4d0, 0x0d250, 0x1d255,
	0x0b540, 0x0d6a0, 0x0ada2, 0x095b0, 0x14977,
	0x04970, 0x0a4b0, 0x0b4b5, 0x06a50, 0x06d40,
	0x1ab54, 0x02b60, 0x09570, 0x052f2, 0x04970,
	0x06566, 0x0d4a0, 0x0ea50, 0x06e95, 0x05ad0,
	0x02b60, 0x186e3, 0x092e0, 0x1c8d7, 0x0c950,
	0x0d4a0, 0x1d8a6, 0x0b550, 0x056a0, 0x1a5b4,
	0x025d0, 0x092d0, 0x0d2b2, 0x0a950, 0x0b557,
	0x06ca0, 0x0b550, 0x15355, 0x04da0, 0x0a5d0,
	0x14573, 0x052d0, 0x0a9a8, 0x0e950, 0x06aa0,
	0x0aea6, 0x0ab50, 0x04b60, 0x0aae4, 0x0a570,
	0x05260, 0x0f263, 0x0d950, 0x05b57, 0x056a0,
	0x096d0, 0x04dd5, 0x04ad0, 0x0a4d0, 0x0d4d4,
	0x0d250, 0x0d558, 0x0b540, 0x0b5a0, 0x195a6,
	0x095b0, 0x049b0, 0x0a974, 0x0a4b0, 0x0b27a,
	0x06a50, 0x06d40, 0x0af46, 0x0ab60, 0x09570,
	0x04af5, 0x04970, 0x064b0, 0x074a3, 0x0ea50,
	0x06b58, 0x05ac0, 0x0ab60, 0x096d5, 0x092e0,
	0x0c960, 0x0d954, 0x0d4a0, 0x0da50, 0x07552,
	0x056a0, 0x0abb7, 0x025d0, 0x092d0, 0x0cab5,
	0x0a950, 0x0b4a0, 0x0baa4, 0x0ad50, 0x055d9,
	0x04ba0, 0x0a5b0, 0x15176, 0x052b0, 0x0a930,
	0x07954, 0x06aa0, 0x0ad50, 0x05b52, 0x04b60,
	0x0a6e6, 0x0a4e0, 0x0d260, 0x0ea65, 0x0d530,
	0x05aa0, 0x076a3, 0x096d0, 0x04bd7, 0x04ad0,
	0x0a4d0, 0x1d0b6, 0x0d250, 0x0d520, 0x0dd45,
	0x0b5a0, 0x056d0, 0x055b2, 0x049b0, 0x0a577,
	0x0a4b0, 0x0aa50, 0x1b255, 0x06d20, 0x0ada0,
}

func lunarLabel(t time.Time) string {
	year, month, day, leap, ok := solarToLunar(t)
	if !ok {
		return "农历待确认"
	}
	_ = year
	prefix := "农历"
	if leap {
		prefix += "闰"
	}
	return prefix + chineseMonthName(month) + chineseDayName(day)
}

func solarToLunar(t time.Time) (int, int, int, bool, bool) {
	base := time.Date(1900, 1, 31, 0, 0, 0, 0, time.Local)
	current := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	if current.Before(base) {
		return 0, 0, 0, false, false
	}
	offset := int(current.Sub(base).Hours() / 24)
	year := 1900
	for year < 1900+len(lunarInfo) {
		yDays := lunarYearDays(year)
		if offset < yDays {
			break
		}
		offset -= yDays
		year++
	}
	if year >= 1900+len(lunarInfo) {
		return 0, 0, 0, false, false
	}
	leapMonth := lunarLeapMonth(year)
	month := 1
	leap := false
	for month <= 12 {
		monthDays := lunarMonthDays(year, month)
		if leap && leapMonth == month {
			monthDays = lunarLeapDays(year)
		}
		if offset < monthDays {
			day := offset + 1
			return year, month, day, leap, true
		}
		offset -= monthDays
		if leapMonth == month && !leap {
			leap = true
			continue
		}
		if leap {
			leap = false
		}
		month++
	}
	return 0, 0, 0, false, false
}

func lunarYearDays(year int) int {
	info := lunarInfo[year-1900]
	sum := 348
	for mask := 0x8000; mask > 0x8; mask >>= 1 {
		if info&mask != 0 {
			sum++
		}
	}
	return sum + lunarLeapDays(year)
}

func lunarLeapMonth(year int) int {
	return lunarInfo[year-1900] & 0xf
}

func lunarLeapDays(year int) int {
	if lunarLeapMonth(year) != 0 {
		if lunarInfo[year-1900]&0x10000 != 0 {
			return 30
		}
		return 29
	}
	return 0
}

func lunarMonthDays(year int, month int) int {
	if month < 1 || month > 12 {
		return 0
	}
	if lunarInfo[year-1900]&(0x10000>>month) != 0 {
		return 30
	}
	return 29
}

func chineseMonthName(month int) string {
	months := []string{"", "正月", "二月", "三月", "四月", "五月", "六月", "七月", "八月", "九月", "十月", "冬月", "腊月"}
	if month < 1 || month >= len(months) {
		return fmt.Sprintf("%d月", month)
	}
	return months[month]
}

func chineseDayName(day int) string {
	days := []string{"", "初一", "初二", "初三", "初四", "初五", "初六", "初七", "初八", "初九", "初十", "十一", "十二", "十三", "十四", "十五", "十六", "十七", "十八", "十九", "二十", "廿一", "廿二", "廿三", "廿四", "廿五", "廿六", "廿七", "廿八", "廿九", "三十"}
	if day < 1 || day >= len(days) {
		return fmt.Sprintf("%d", day)
	}
	return days[day]
}
