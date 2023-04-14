package util

import (
	"fmt"
	"os"
	"path"
)

// MkdirIfNotExist 如果输入路径不存在，则创建目录
func MkdirIfNotExist(dir string) error {
	if len(dir) == 0 {
		return nil
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return os.MkdirAll(dir, os.ModePerm)
	}

	return nil
}

func FileExists(file string) bool {
	_, err := os.Stat(file)
	return err == nil
}

// CreateIfNotExist creates a file if it is not exists.
func CreateIfNotExist(file string) (*os.File, error) {
	_, err := os.Stat(file)
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("%s already exist", file)
	}

	return os.Create(file)
}

// MaybeCreateFile creates file if not exists
func MaybeCreateFile(dir, subdir, file string) (fp *os.File, created bool, err error) {
	MkdirIfNotExist(path.Join(dir, subdir))
	fpath := path.Join(dir, subdir, file)
	if FileExists(fpath) {
		fmt.Printf("%s exists, ignored generation\n", fpath)
		return nil, false, nil
	}

	fp, err = CreateIfNotExist(fpath)
	created = err == nil
	return
}
