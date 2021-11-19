// package rotatelogs is a port of File-RotateLogs from Perl
// (https://metacpan.org/release/File-RotateLogs), and it allows
// you to automatically rotate output files when you write to them
// according to the filename pattern that you can specify.
package rotatelogs

import (
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chriszhangmq/file-rotatelogs/internal/fileutil"
	strftime "github.com/lestrrat-go/strftime"
	"github.com/pkg/errors"
)

const TimeFormat = "2006-01-02"
const FileSuffix = ".log"
const compressSuffix = ".gz"

var (
	FilePath string
	FileName string
	osStat   = os.Stat
)

func (c clockFn) Now() time.Time {
	return c()
}

// New creates a new RotateLogs object. A log filename pattern
// must be passed. Optional `Option` parameters may be passed
func New(filePath string, fileName string, options ...Option) (*RotateLogs, error) {
	p := filePath + fileName + "-" + TimeFormat + FileSuffix
	FilePath = filePath
	FileName = fileName
	globPattern := p
	for _, re := range patternConversionRegexps {
		globPattern = re.ReplaceAllString(globPattern, "*")
	}

	pattern, err := strftime.New(p)
	if err != nil {
		return nil, errors.Wrap(err, `invalid strftime pattern`)
	}

	var clock Clock = Local
	rotationTime := 24 * time.Hour
	var rotationSize int64
	var rotationCount uint
	var linkName string
	var maxAge time.Duration
	var handler Handler
	var forceNewFile bool

	for _, o := range options {
		switch o.Name() {
		case optkeyClock:
			clock = o.Value().(Clock)
		case optkeyLinkName:
			linkName = o.Value().(string)
		case optkeyMaxAge:
			maxAge = o.Value().(time.Duration)
			if maxAge < 0 {
				maxAge = 0
			}
		case optkeyRotationTime:
			rotationTime = o.Value().(time.Duration)
			if rotationTime < 0 {
				rotationTime = 0
			}
		case optkeyRotationSize:
			rotationSize = o.Value().(int64)
			if rotationSize < 0 {
				rotationSize = 0
			}
		case optkeyRotationCount:
			rotationCount = o.Value().(uint)
		case optkeyHandler:
			handler = o.Value().(Handler)
		case optkeyForceNewFile:
			forceNewFile = true
		}
	}

	if maxAge > 0 && rotationCount > 0 {
		return nil, errors.New("options MaxAge and RotationCount cannot be both set")
	}

	if maxAge == 0 && rotationCount == 0 {
		// if both are 0, give maxAge a sane default
		maxAge = 7 * 24 * time.Hour
	}

	return &RotateLogs{
		clock:         clock,
		eventHandler:  handler,
		globPattern:   globPattern,
		linkName:      linkName,
		maxAge:        maxAge,
		pattern:       pattern,
		rotationTime:  rotationTime,
		rotationSize:  rotationSize,
		rotationCount: rotationCount,
		forceNewFile:  forceNewFile,
	}, nil
}

// Write satisfies the io.Writer interface. It writes to the
// appropriate file handle that is currently being used.
// If we have reached rotation time, the target file gets
// automatically rotated, and also purged if necessary.
func (rl *RotateLogs) Write(p []byte) (n int, err error) {
	// Guard against concurrent writes
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	out, err := rl.getWriterNolock(false, false)
	if err != nil {
		return 0, errors.Wrap(err, `failed to acquite target io.Writer`)
	}

	return out.Write(p)
}

// must be locked during this operation
func (rl *RotateLogs) getWriterNolock(bailOnRotateFail, useGenerationalNames bool) (io.Writer, error) {
	generation := rl.generation
	previousFn := rl.curFn

	// This filename contains the name of the "NEW" filename
	// to log to, which may be newer than rl.currentFilename
	//baseFn := fileutil.GenerateFn(rl.pattern, rl.clock, rl.rotationTime)
	baseFn := fileutil.GenerateFileNme(FilePath, FileName, FileSuffix, rl.clock)
	filename := baseFn
	var forceNewFile bool

	fi, err := os.Stat(rl.curFn)
	sizeRotation := false
	//err != nil说明当前文件不存在
	if err != nil {
		//文件不存在
		forceNewFile = true
	} else if rl.rotationSize > 0 && rl.rotationSize <= fi.Size() {
		//是否需要按照大小分割文件：文件存在，且文件大小超过设定阈值。
		forceNewFile = true
		sizeRotation = true
	} else if !sizeRotation {
		//文件存在：判断当前文件是否为当天的文件
		currTime := rl.ParseTimeFromFileName("2006-01-02", rl.curFn)
		if !rl.isToday(currTime) {
			forceNewFile = true
		}
		//每次启动程序，新建文件
		//if rl.forceNewFile{
		//	forceNewFile = true
		//}
	}
	//不需要分割
	if !forceNewFile && !sizeRotation && !useGenerationalNames {
		// nothing to do
		return rl.outFh, nil
	}
	//需要创建新文件
	if forceNewFile {
		// A new file has been requested. Instead of just using the
		// regular strftime pattern, we create a new file name using
		// generational names such as "foo.1", "foo.2", "foo.3", etc

		if !sizeRotation {
			//按照天来分割文件，获取新的文件名
			var newFileName string
			//newFileName = fileutil.GenerateFn(rl.pattern, rl.clock, rl.rotationTime)
			newFileName = fileutil.GenerateFileNme(FilePath, FileName, FileSuffix, rl.clock)
			if _, err := os.Stat(newFileName); err != nil {
				filename = newFileName
			}
		} else {
			//按照文件大小分割文件：获取新的文件名
			var newFileName string
			var index int
			for {
				//newFileName = fileutil.GenerateFn(rl.pattern, rl.clock, rl.rotationTime)
				newFileName = fileutil.GenerateFileNme(FilePath, FileName, FileSuffix, rl.clock)
				newFileName = fmt.Sprintf("%s.%d%s", newFileName, index, ".log")
				index++
				//filename = newFileName
				fileInfo, err := os.Stat(newFileName)
				if err != nil {
					//文件不存在：创建新的文件
					filename = newFileName
					break
				}
				//文件存在：判断大小
				if rl.rotationSize > 0 && rl.rotationSize > fileInfo.Size() {
					filename = newFileName
					break
				}
			}
		}
	}

	fh, err := fileutil.CreateFile(filename)
	if err != nil {
		return nil, errors.Wrapf(err, `failed to create a new file %v`, filename)
	}

	if err := rl.rotateNolock(filename); err != nil {
		err = errors.Wrap(err, "failed to rotate")
		if bailOnRotateFail {
			// Failure to rotate is a problem, but it's really not a great
			// idea to stop your application just because you couldn't rename
			// your log.
			//
			// We only return this error when explicitly needed (as specified by bailOnRotateFail)
			//
			// However, we *NEED* to close `fh` here
			if fh != nil { // probably can't happen, but being paranoid
				fh.Close()
			}

			return nil, err
		}
		fmt.Fprintf(os.Stderr, "%s\n", err.Error())
	}

	rl.outFh.Close()
	rl.outFh = fh
	rl.curBaseFn = baseFn
	rl.curFn = filename
	rl.generation = generation

	if h := rl.eventHandler; h != nil {
		go h.Handle(&FileRotatedEvent{
			prev:    previousFn,
			current: filename,
		})
	}

	return fh, nil
}

// CurrentFileName returns the current file name that
// the RotateLogs object is writing to
func (rl *RotateLogs) CurrentFileName() string {
	rl.mutex.RLock()
	defer rl.mutex.RUnlock()

	return rl.curFn
}

var patternConversionRegexps = []*regexp.Regexp{
	regexp.MustCompile(`-[0-9]+`),
	regexp.MustCompile(`%[%+A-Za-z]`),
	regexp.MustCompile(`\*+`),
}

type cleanupGuard struct {
	enable bool
	fn     func()
	mutex  sync.Mutex
}

func (g *cleanupGuard) Enable() {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.enable = true
}

func (g *cleanupGuard) Run() {
	g.fn()
}

// Rotate forcefully rotates the log files. If the generated file name
// clash because file already exists, a numeric suffix of the form
// ".1", ".2", ".3" and so forth are appended to the end of the log file
//
// Thie method can be used in conjunction with a signal handler so to
// emulate servers that generate new log files when they receive a
// SIGHUP
func (rl *RotateLogs) Rotate() error {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()
	_, err := rl.getWriterNolock(true, true)

	return err
}

func (rl *RotateLogs) rotateNolock(filename string) error {
	lockfn := filename + `_lock`
	fh, err := os.OpenFile(lockfn, os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		// Can't lock, just return
		return err
	}

	var guard cleanupGuard
	guard.fn = func() {
		fh.Close()
		os.Remove(lockfn)
	}
	defer guard.Run()

	if rl.linkName != "" {
		tmpLinkName := filename + `_symlink`

		// Change how the link name is generated based on where the
		// target location is. if the location is directly underneath
		// the main filename's parent directory, then we create a
		// symlink with a relative path
		linkDest := filename
		linkDir := filepath.Dir(rl.linkName)

		baseDir := filepath.Dir(filename)
		if strings.Contains(rl.linkName, baseDir) {
			tmp, err := filepath.Rel(linkDir, filename)
			if err != nil {
				return errors.Wrapf(err, `failed to evaluate relative path from %#v to %#v`, baseDir, rl.linkName)
			}

			linkDest = tmp
		}

		if err := os.Symlink(linkDest, tmpLinkName); err != nil {
			return errors.Wrap(err, `failed to create new symlink`)
		}

		// the directory where rl.linkName should be created must exist
		_, err := os.Stat(linkDir)
		if err != nil { // Assume err != nil means the directory doesn't exist
			if err := os.MkdirAll(linkDir, 0755); err != nil {
				return errors.Wrapf(err, `failed to create directory %s`, linkDir)
			}
		}

		if err := os.Rename(tmpLinkName, rl.linkName); err != nil {
			return errors.Wrap(err, `failed to rename new symlink`)
		}
	}

	if rl.maxAge <= 0 && rl.rotationCount <= 0 {
		return errors.New("panic: maxAge and rotationCount are both set")
	}

	matches, err := filepath.Glob(rl.globPattern)
	if err != nil {
		return err
	}

	cutoff := rl.clock.Now().Add(-1 * rl.maxAge)

	// the linter tells me to pre allocate this...
	toUnlink := make([]string, 0, len(matches))
	compressFiles := make([]string, 0, len(matches))
	for _, path := range matches {
		// Ignore lock files
		if strings.HasSuffix(path, "_lock") || strings.HasSuffix(path, "_symlink") {
			continue
		}

		fi, err := os.Stat(path)
		if err != nil {
			continue
		}

		fl, err := os.Lstat(path)
		if err != nil {
			continue
		}
		//按天数判断是否保留
		if rl.maxAge > 0 && rl.IsNextDay(cutoff, fi.ModTime()) {
			if fi.Name() != filename {
				compressFiles = append(compressFiles, path)
			}
			continue
		}

		if rl.rotationCount > 0 && fl.Mode()&os.ModeSymlink == os.ModeSymlink {
			continue
		}
		toUnlink = append(toUnlink, path)
	}

	if rl.rotationCount > 0 {
		// Only delete if we have more than rotationCount
		if rl.rotationCount >= uint(len(toUnlink)) {
			return nil
		}

		toUnlink = toUnlink[:len(toUnlink)-int(rl.rotationCount)]
	}

	if len(toUnlink) <= 0 {
		return nil
	}

	guard.Enable()
	//执行删除文件
	go func() {
		// unlink files on a separate goroutine
		for _, path := range toUnlink {
			os.Remove(path)
		}
	}()

	//执行压缩命令
	go func() {
		compressFunc(compressFiles)
	}()

	return nil
}

// Close satisfies the io.Closer interface. You must
// call this method if you performed any writes to
// the object.
func (rl *RotateLogs) Close() error {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	if rl.outFh == nil {
		return nil
	}

	rl.outFh.Close()
	rl.outFh = nil

	return nil
}

func (rl *RotateLogs) ParseTimeFromFileName(fileNameTimeFormat string, fileName string) time.Time {
	//正则表达式：获取时间字符串
	fileNameTime := getTimeFromStr(fileName)
	if len(fileNameTime) <= 0 || fileNameTime == "" {
		return time.Time{}
	}
	//字符串转换为时间
	fileNameInTime := rl.changeFileNameByTime(fileNameTimeFormat, fileNameTime)
	return fileNameInTime
}

func (rl *RotateLogs) changeFileNameByTime(fileNameTimeFormat string, lastTime string) time.Time {
	var newFileTime time.Time
	var err error
	now := rl.clock.Now()
	//当前时间区域
	if now.Location() != time.UTC {
		newFileTime, err = time.ParseInLocation(fileNameTimeFormat, lastTime, time.Local)
	} else {
		newFileTime, err = time.Parse(fileNameTimeFormat, lastTime)
	}

	if err != nil {
		log.Fatal(err)
	}

	return newFileTime
}

func getTimeFromStr(str string) string {
	planRegx := regexp.MustCompile("([1-9])([0-9]|[ ]|[-]|[:]|[T])+")
	subs := planRegx.FindStringSubmatch(str)
	if len(subs) > 0 {
		return strings.TrimSpace(subs[0])
	}
	return ""
}

func (rl *RotateLogs) IsNextDay(oldTime time.Time, newTime time.Time) bool {
	oldYear, oldMonth, oldDay := oldTime.Date()
	newYear, newMonth, newDay := newTime.Date()
	if newYear > oldYear {
		return true
	}
	if newMonth > oldMonth {
		return true
	}
	if newDay > oldDay {
		return true
	}
	return false
}

func (rl *RotateLogs) isToday(currTime time.Time) bool {
	currYear, currMonth, currDay := currTime.Date()
	todayYear, todayMonth, todayDay := rl.clock.Now().Date()
	if currYear == todayYear && currMonth == todayMonth && currDay == todayDay {
		return true
	}
	return false
}

func compressFunc(compressFile []string) {
	for _, f := range compressFile {
		errCompress := compressLogFile(f, f+compressSuffix)
		if errCompress != nil {
			log.Println(errCompress)
		} else {
			if err := os.Remove(f); err != nil {
				log.Println(err)
			}
		}
	}
}

func compressLogFile(src, dst string) (err error) {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open log file: %v", err)
	}
	defer f.Close()

	fi, err := osStat(src)
	if err != nil {
		return fmt.Errorf("failed to stat log file: %v", err)
	}

	if err := chown(dst, fi); err != nil {
		return fmt.Errorf("failed to chown compressed log file: %v", err)
	}

	// If this file already exists, we presume it was created by
	// a previous attempt to compress the log file.
	gzf, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fi.Mode())
	if err != nil {
		return fmt.Errorf("failed to open compressed log file: %v", err)
	}
	defer gzf.Close()

	gz := gzip.NewWriter(gzf)

	defer func() {
		if err != nil {
			os.Remove(dst)
			err = fmt.Errorf("failed to compress log file: %v", err)
		}
	}()

	if _, err := io.Copy(gz, f); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := gzf.Close(); err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		return err
	}

	return nil
}

func chown(_ string, _ os.FileInfo) error {
	return nil
}
