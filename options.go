package rotatelogs

import (
	"time"

	"github.com/chriszhangmq/file-rotatelogs/internal/option"
)

const (
	optkeyClock         = "clock"
	optkeyHandler       = "handler"
	optkeyMaxAge        = "max-age"
	optkeyRotationTime  = "rotation-time"
	optkeyRotationSize  = "rotation-size"
	optkeyRotationCount = "rotation-count"
	optkeyFilePath      = "file-path"
	optkeyFileName      = "file-name"
	optkeyCompressFile  = "compress-file"
	optkeyCronTime      = "cron-time"
)

// WithClock creates a new Option that sets a clock
// that the RotateLogs object will use to determine
// the current time.
//
// By default rotatelogs.Local, which returns the
// current time in the local time zone, is used. If you
// would rather use UTC, use rotatelogs.UTC as the argument
// to this option, and pass it to the constructor.
func WithClock(c Clock) Option {
	return option.New(optkeyClock, c)
}

// WithLocation creates a new Option that sets up a
// "Clock" interface that the RotateLogs object will use
// to determine the current time.
//
// This optin works by always returning the in the given
// location.
func WithLocation(loc *time.Location) Option {
	return option.New(optkeyClock, clockFn(func() time.Time {
		return time.Now().In(loc)
	}))
}

// WithLinkName creates a new Option that sets the
// symbolic link name that gets linked to the current
// file name being used.
//func WithLinkName(s string) Option {
//	return option.New(optkeyLinkName, s)
//}

// WithMaxAge creates a new Option that sets the
// max age of a log file before it gets purged from
// the file system.
func WithMaxAge(day int) Option {
	return option.New(optkeyMaxAge, day)
}

// WithRotationTime creates a new Option that sets the
// time between rotation.
func WithRotationTime(day int) Option {
	return option.New(optkeyRotationTime, day)
}

// WithRotationSize creates a new Option that sets the
// log file size between rotation.
func WithRotationSize(sizeMB int) Option {
	return option.New(optkeyRotationSize, sizeMB)
}

// WithRotationCount creates a new Option that sets the
// number of files should be kept before it gets
// purged from the file system.
func WithRotationCount(num uint) Option {
	return option.New(optkeyRotationCount, num)
}

// WithHandler creates a new Option that specifies the
// Handler object that gets invoked when an event occurs.
// Currently `FileRotated` event is supported
func WithHandler(h Handler) Option {
	return option.New(optkeyHandler, h)
}

func WithFilePath(filePath string) Option {
	return option.New(optkeyFilePath, filePath)
}

func WithFileName(fileName string) Option {
	return option.New(optkeyFileName, fileName)
}

func WithCompressFile(needCompress bool) Option {
	return option.New(optkeyCompressFile, needCompress)
}

func WithCronTime(cronTime string) Option {
	return option.New(optkeyCronTime, cronTime)
}
