// package rotatelogs is a port of File-RotateLogs from Perl
// (https://metacpan.org/release/File-RotateLogs), and it allows
// you to automatically rotate output files when you write to them
// according to the filename pattern that you can specify.
// Chris fixes the bug. The current code concurrency is 20000
// Can be used as an alternative to the logrotate tool
package rotatelogs

import (
	"fmt"
	"github.com/chriszhangmq/file-rotatelogs/internal/common"
	"github.com/chriszhangmq/file-rotatelogs/internal/timeutil"
	"github.com/robfig/cron"
	"io"
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

func (c clockFn) Now() time.Time {
	return c()
}

// New creates a new RotateLogs object. A log filename pattern
// must be passed. Optional `Option` parameters may be passed
func New(options ...Option) (*RotateLogs, error) {
	var clock Clock = Local
	var rotationTime int
	var rotationSize int64
	var rotationCount uint
	var maxAge int
	var handler Handler
	var filePath string
	var fileName string
	var compressFile bool
	var cronTime string
	var linkName string

	for _, o := range options {
		switch o.Name() {
		case optkeyClock:
			clock = o.Value().(Clock)
		case optkeyMaxAge:
			maxAge = o.Value().(int)
			if maxAge < 0 {
				maxAge = 0
			}
		case optkeyRotationTime:
			rotationTime = o.Value().(int)
			if rotationTime < 0 {
				rotationTime = 0
			}
		case optkeyRotationSize:
			rotationSize = int64(o.Value().(int))
			if rotationSize < 0 {
				rotationSize = 0
			}
		case optkeyRotationCount:
			rotationCount = o.Value().(uint)
		case optkeyHandler:
			handler = o.Value().(Handler)
		case optkeyFilePath:
			filePath = o.Value().(string)
		case optkeyFileName:
			fileName = o.Value().(string)
		case optkeyCompressFile:
			compressFile = o.Value().(bool)
		case optkeyCronTime:
			cronTime = o.Value().(string)
		case optkeyLinkName:
			if o.Value().(bool) {
				linkName = filePath + fileName
			}
		}
	}

	if maxAge > 0 && rotationCount > 0 {
		return nil, errors.New("options MaxAge and RotationCount cannot be both set")
	}

	if len(strings.Trim(filePath, common.Space)) <= 0 || len(strings.Trim(fileName, common.Space)) <= 0 {
		return nil, errors.New("The log file path or file name is missing")
	}

	p := filePath + fileName + "-" + common.TimeFormat
	globLogPattern := p
	for _, re := range patternConversionRegexps {
		globLogPattern = re.ReplaceAllString(globLogPattern, "*")
	}

	pattern, err := strftime.New(p)
	if err != nil {
		return nil, errors.New("invalid strftime pattern")
	}

	if (rotationTime > 0 || maxAge > 0) && cronTime == common.IsNull {
		return nil, errors.New("cronTime is required")
	}

	if (rotationTime <= 0 || maxAge <= 0) && cronTime != common.IsNull {
		return nil, errors.New("rotationTime or maxAge is required")
	}

	if compressFile && cronTime == common.IsNull {
		return nil, errors.New("To use compressFile, you need to fill in cronTime")
	}

	return &RotateLogs{
		clock:          clock,
		eventHandler:   handler,
		globLogPattern: globLogPattern,
		linkName:       linkName,
		maxAge:         time.Duration(maxAge*24) * time.Hour,
		pattern:        pattern,
		rotationTime:   time.Duration(rotationTime*24) * time.Hour,
		rotationSize:   rotationSize * 1024 * 1024,
		rotationCount:  rotationCount,
		fileName:       fileName,
		filePath:       filePath,
		compressFile:   compressFile,
		cronTime:       cronTime,
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
	filename := common.IsNull
	forceNewFile := false
	sizeRotation := false
	fi, err := os.Stat(rl.curFn)
	//err != nil说明当前文件不存在
	if err != nil {
		//文件不存在
		forceNewFile = true
	} else if rl.rotationSize > 0 && rl.rotationSize <= fi.Size() {
		//是否需要按照大小分割文件：文件存在，且文件大小超过设定阈值。
		forceNewFile = true
		sizeRotation = true
	} else if !sizeRotation && rl.rotationTime > 0 {
		//文件存在：判断当前文件是否需要按天的分割
		currFileTime, err := fileutil.ParseTimeFromFileName(common.TimeFormat, rl.curFn, rl.clock.Now())
		if err != nil {
			forceNewFile = true
		} else if timeutil.CompareTimeWithDay(rl.clock.Now().Add(-1*rl.rotationTime), currFileTime) {
			forceNewFile = true
		}
	}
	//不需要分割
	if !forceNewFile && !sizeRotation && !useGenerationalNames {
		return rl.outFh, nil
	}
	//需要创建新文件
	if forceNewFile {
		//按照天、文件大小分割文件：获取新的文件名
		filename = fileutil.GetNewFileName(rl.filePath, rl.fileName, rl.rotationSize, rl.clock)
	}

	fh, err := fileutil.CreateFile(filename)
	if err != nil {
		return nil, errors.Wrapf(err, `failed to create a new file %v`, filename)
	}

	if err := rl.rotateNolock(filename); err != nil {
		err = errors.Wrap(err, "failed to rotate")
		if bailOnRotateFail {
			if fh != nil { // probably can't happen, but being paranoid
				fh.Close()
			}

			return nil, err
		}
		fmt.Fprintf(os.Stderr, "%s\n", err.Error())
	}

	rl.outFh.Close()
	//压缩旧文件
	go func() {
		rl.compressLogFiles()
	}()
	rl.outFh = fh
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
	lockfn := filename + common.LockSuffix
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
		tmpLinkName := filename + common.SymlinkSuffix

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

//删除所有_lock、_symlink文件
func (rl *RotateLogs) deleteLockSymlinkFile() {
	matches, err := filepath.Glob(rl.globLogPattern)
	if err != nil {
		fmt.Println(err)
	}
	removeFiles := make([]string, 0, len(matches))
	for _, path := range matches {
		if strings.HasSuffix(path, common.LockSuffix) || strings.HasSuffix(path, common.SymlinkSuffix) {
			removeFiles = append(removeFiles, path)
		}
	}
	for _, path := range removeFiles {
		os.Remove(path)
	}
}

//清除已被压缩的.log文件
func (rl *RotateLogs) deleteSameLogFile() error {
	matches, err := filepath.Glob(rl.globLogPattern)
	if err != nil {
		return err
	}
	removeSuffixFilesMap := make(map[string]string, len(matches))
	for _, path := range matches {
		if !strings.HasSuffix(path, common.CompressSuffix) {
			continue
		}
		removeSuffixFile := strings.TrimSuffix(path, common.CompressSuffix)
		removeSuffixFilesMap[removeSuffixFile] = removeSuffixFile
	}
	removeFiles := make([]string, 0, len(matches))
	for _, path := range matches {
		if strings.HasSuffix(path, common.CompressSuffix) {
			continue
		}
		if _, ok := removeSuffixFilesMap[path]; ok {
			removeFiles = append(removeFiles, path)
		}
	}
	for _, path := range removeFiles {
		os.Remove(path)
	}
	return nil
}

//压缩日志文件
func (rl *RotateLogs) compressLogFiles() error {
	matches, err := filepath.Glob(rl.globLogPattern)
	if err != nil {
		return err
	}
	files := make([]string, 0, len(matches))
	for _, path := range matches {
		// Ignore lock files
		if strings.HasSuffix(path, common.LockSuffix) || strings.HasSuffix(path, common.SymlinkSuffix) || strings.HasSuffix(path, common.CompressSuffix) {
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
		if fl.Mode()&os.ModeSymlink == os.ModeSymlink {
			continue
		}
		fiName2Time, err := fileutil.ParseTimeFromFileName(common.TimeFormat, fi.Name(), rl.clock.Now())
		if err != nil {
			continue
		}
		if fi.Name() != rl.curFn && !timeutil.IsToday(fiName2Time, rl.clock.Now()) {
			files = append(files, fi.Name())
		}
	}
	fileutil.CompressLogFiles(files, rl.filePath)
	return nil
}

//删除文件: .log 、 .gz
func (rl *RotateLogs) deleteFile() error {
	matches, err := filepath.Glob(rl.globLogPattern)
	if err != nil {
		return err
	}
	removeFiles := make([]string, 0, len(matches))
	cutoff := rl.clock.Now().Add(-1 * rl.maxAge)
	for _, path := range matches {
		// Ignore lock files
		if strings.HasSuffix(path, common.LockSuffix) || strings.HasSuffix(path, common.SymlinkSuffix) {
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
		if fl.Mode()&os.ModeSymlink == os.ModeSymlink {
			continue
		}
		//按天数判断是否保留
		fiName2Time, err := fileutil.ParseTimeFromFileName(common.TimeFormat, fi.Name(), rl.clock.Now())
		if err != nil {
			continue
		}
		if rl.maxAge > 0 && timeutil.IsMaxDay(cutoff, fiName2Time) {
			removeFiles = append(removeFiles, path)
		}
	}
	for _, path := range removeFiles {
		os.Remove(path)
	}
	return nil
}

// 定时任务
func (rl *RotateLogs) cronTask(cronTime string) {
	cronObj := cron.NewWithLocation(rl.clock.Now().Location())
	err := cronObj.AddFunc(cronTime, rl.cronFunc)
	if err != nil {
		fmt.Println(err)
	}
	cronObj.Start()
}

func (rl *RotateLogs) cronFunc() {
	//删除过期文件
	if rl.maxAge > 0 {
		if err := rl.deleteFile(); err != nil {
			fmt.Println(err)
		}
	}
	//压缩非当天文件
	if rl.compressFile {
		//删除已解压的文件
		if err := rl.deleteSameLogFile(); err != nil {
			fmt.Println(err)
		}
		if err := rl.compressLogFiles(); err != nil {
			fmt.Println(err)
		}
	}
}

func (rl *RotateLogs) Init() {
	if rl.cronTime != common.IsNull {
		rl.cronTask(rl.cronTime)
		rl.cronFunc()
	}
	rl.deleteLockSymlinkFile()
}
