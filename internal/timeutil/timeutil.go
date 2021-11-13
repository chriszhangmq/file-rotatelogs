package timeutil

import (
	"github.com/chriszhangmq/file-rotatelogs/internal/common"
	"time"
)

/**
 * Created by Chris on 2021/11/14.
 */

func IsToday(currTime time.Time, todayTime time.Time) bool {
	currYear, currMonth, currDay := currTime.Date()
	todayYear, todayMonth, todayDay := todayTime.Date()
	if currYear == todayYear && currMonth == todayMonth && currDay == todayDay {
		return true
	}
	return false
}

func IsMaxDay(cutOffTime time.Time, fileTime time.Time) bool {
	cutOffDateString := cutOffTime.Format(common.TimeFormat)
	cutOffDate, _ := time.Parse(common.TimeFormat, cutOffDateString)
	fileDateString := fileTime.Format(common.TimeFormat)
	fileDate, _ := time.Parse(common.TimeFormat, fileDateString)
	return fileDate.Before(cutOffDate)
}

func CompareTimeWithDay(cutOffTime time.Time, fileTime time.Time) bool {
	cutOffDateString := cutOffTime.Format(common.TimeFormat)
	cutOffDate, _ := time.Parse(common.TimeFormat, cutOffDateString)
	return fileTime.Before(cutOffDate)
}

func FileIsNotToday(currTime time.Time, fileTime time.Time) bool {
	fileYear, fileMonth, fileDay := fileTime.Date()
	todayYear, todayMonth, todayDay := currTime.Date()
	if fileYear == todayYear && fileMonth == todayMonth && fileDay == todayDay {
		return false
	}
	return true
}
