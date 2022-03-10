package fileutil

import (
	"compress/gzip"
	"fmt"
	"github.com/chriszhangmq/file-rotatelogs/internal/common"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/lestrrat-go/strftime"
	"github.com/pkg/errors"
)

// GenerateFn creates a file name based on the pattern, the current time, and the
// rotation time.
//
// The bsase time that is used to generate the filename is truncated based
// on the rotation time.
func GenerateFn(pattern *strftime.Strftime, clock interface{ Now() time.Time }, rotationTime time.Duration) string {
	now := clock.Now()

	// XXX HACK: Truncate only happens in UTC semantics, apparently.
	// observed values for truncating given time with 86400 secs:
	//
	// before truncation: 2018/06/01 03:54:54 2018-06-01T03:18:00+09:00
	// after  truncation: 2018/06/01 03:54:54 2018-05-31T09:00:00+09:00
	//
	// This is really annoying when we want to truncate in local time
	// so we hack: we take the apparent local time in the local zone,
	// and pretend that it's in UTC. do our math, and put it back to
	// the local zone
	var base time.Time
	if now.Location() != time.UTC {
		base = time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), time.UTC)
		base = base.Add(20 * time.Second)
		base = base.Truncate(rotationTime)
		base = time.Date(base.Year(), base.Month(), base.Day(), base.Hour(), base.Minute(), base.Second(), base.Nanosecond(), base.Location())
	} else {
		base = base.Add(20 * time.Second)
		base = now.Truncate(rotationTime)
	}

	return pattern.FormatString(base)
}

//产生新的文件名（用于按大小分割文件）
func GenerateFileNme(path string, name string, clock interface{ Now() time.Time }, timeFormat string) (string, string) {
	now := clock.Now()

	var base time.Time
	if now.Location() != time.UTC {
		base = time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), time.UTC)
		//base = base.Truncate(rotationTime)
		base = time.Date(base.Year(), base.Month(), base.Day(), base.Hour(), base.Minute(), base.Second(), base.Nanosecond(), base.Location())
	}

	//拼接文件名
	date := fmt.Sprintf("%s", base.Format(timeFormat))
	fileName := fmt.Sprintf("%s-%s", name, date)
	fileNameWhitPath := fmt.Sprintf("%s%s", path, fileName)
	return fileNameWhitPath, fileName
}

//产生新的文件名（用于按大小分割文件）
func GenerateFnForFileSize(pattern *strftime.Strftime, clock interface{ Now() time.Time }) string {
	now := clock.Now()

	var base time.Time
	if now.Location() != time.UTC {
		base = time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), time.UTC)
		//base = base.Truncate(rotationTime)
		base = time.Date(base.Year(), base.Month(), base.Day(), base.Hour(), base.Minute(), base.Second(), base.Nanosecond(), base.Location())
	}
	base = base.Add(20 * time.Second)
	return pattern.FormatString(base)
}

// CreateFile creates a new file in the given path, creating parent directories
// as necessary
func CreateFile(filename string) (*os.File, error) {
	// make sure the dir is existed, eg:
	// ./foo/bar/baz/hello.log must make sure ./foo/bar/baz is existed
	dirname := filepath.Dir(filename)
	if err := os.MkdirAll(dirname, 0755); err != nil {
		return nil, errors.Wrapf(err, "failed to create directory %s", dirname)
	}
	// if we got here, then we need to create a file
	fh, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, errors.Errorf("failed to open file %s: %s", filename, err)
	}

	return fh, nil
}

func ParseTimeFromFileName(fileNameTimeFormat string, fileName string, clock time.Time) (time.Time, error) {
	//正则表达式：获取时间字符串
	fileNameTime := getTimeFromStr(fileName)
	if len(fileNameTime) <= 0 || fileNameTime == common.IsNull {
		return time.Time{}, errors.New("cannot resolve time")
	}
	//字符串转换为时间
	var err error
	var fileNameInTime time.Time
	//当前时间区域
	if clock.Location() != time.UTC {
		fileNameInTime, err = time.ParseInLocation(fileNameTimeFormat, fileNameTime, time.Local)
	} else {
		fileNameInTime, err = time.Parse(fileNameTimeFormat, fileNameTime)
	}
	if err != nil {
		return fileNameInTime, err
	}
	return fileNameInTime, nil
}

func getTimeFromStr(str string) string {
	planRegx := regexp.MustCompile("([1-9])([0-9]|[ ]|[-]|[:]|[T])+")
	subs := planRegx.FindStringSubmatch(str)
	if len(subs) > 0 {
		return strings.TrimSpace(subs[0])
	}
	return ""
}

func CompressLogFiles(compressFile []string, filePath string) {
	for _, f := range compressFile {
		fn := filepath.Join(dir(filePath), f)
		//The destination compressed file does not exist
		if _, err := os.Stat(fn); err != nil {
			continue
		}
		//Compressed file already exists
		if _, err := os.Stat(fn + common.CompressSuffix); err == nil {
			continue
		}
		errCompress := compressLogFile(fn, fn+common.CompressSuffix)
		//Delete after successful compression
		if _, err := os.Stat(fn + common.CompressSuffix); err == nil && errCompress == nil {
			os.Remove(f)
		}
	}
}

func CompressLogFile(fileName string) {
	//The destination compressed file does not exist
	if _, err := os.Stat(fileName); err != nil {
		return
	}
	//Compressed file already exists
	if _, err := os.Stat(fileName + common.CompressSuffix); err == nil {
		return
	}
	errCompress := compressLogFile(fileName, fileName+common.CompressSuffix)
	//Delete after successful compression
	if _, err := os.Stat(fileName + common.CompressSuffix); err == nil && errCompress == nil {
		os.Remove(fileName)
	}
}

func compressLogFile(src, dst string) (err error) {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open log file: %v", err)
	}
	defer f.Close()

	fi, err := os.Stat(src)
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

func dir(filePath string) string {
	return filepath.Dir(filename(filePath))
}

func filename(filePath string) string {
	if filePath != common.IsNull {
		return filePath
	}
	name := filepath.Base(os.Args[0]) + "-lumberjack.log"
	return filepath.Join(os.TempDir(), name)
}

func GetNewFileName(filePath string, fileName string, rotationSize int64, clock interface{ Now() time.Time }) (string, string) {
	index := 1
	newFileName := ""
	newFileNameWithPath := common.IsNull
	//生成文件名
	newFileNameWithPath, newFileName = GenerateFileNme(filePath, fileName, clock, common.TimeFormat)
	//添加后缀
	newFileNameWithPath = fmt.Sprintf("%s%s", newFileNameWithPath, common.FileSuffix)
	newFileName = fmt.Sprintf("%s%s", newFileName, common.FileSuffix)
	fileInfo, err := os.Stat(newFileNameWithPath)
	if err != nil {
		//文件不存在且该文件的压缩文件也不存在：创建新的文件
		if _, err := os.Stat(newFileNameWithPath + common.CompressSuffix); err != nil {
			return newFileNameWithPath, newFileName
		}
	}
	//文件存在：不按照大小划分
	if rotationSize <= 0 {
		//不存在压缩文件时，直接把数据写到这个文件中
		if _, err := os.Stat(newFileNameWithPath + common.CompressSuffix); err != nil {
			return newFileNameWithPath, newFileName
		}
	}
	//文件存在：需要按照大小划分
	if rotationSize > 0 {
		//仅当文件存在时，判断文件大小是否满足大小限制。
		if fileInfo, err = os.Stat(newFileNameWithPath); err == nil && (rotationSize > fileInfo.Size()) {
			return newFileNameWithPath, newFileName
		}
	}
	for {
		//生成文件名
		newFileNameWithPath, newFileName = GenerateFileNme(filePath, fileName, clock, common.TimeFormat)
		//添加后缀
		newFileNameWithPath = fmt.Sprintf("%s.%d%s", newFileNameWithPath, index, common.FileSuffix)
		newFileName = fmt.Sprintf("%s.%d%s", newFileName, index, common.FileSuffix)
		index++
		fileInfo, err := os.Stat(newFileNameWithPath)
		if err != nil {
			//文件不存在且该文件的压缩文件也不存在：创建新的文件
			if _, err := os.Stat(newFileNameWithPath + common.CompressSuffix); err != nil {
				return newFileNameWithPath, newFileName
			} else {
				continue
			}
		}
		//文件存在：判断大小
		if rotationSize > 0 && rotationSize > fileInfo.Size() {
			return newFileNameWithPath, newFileName
		}
	}
}

func createNewFileName() {

}
