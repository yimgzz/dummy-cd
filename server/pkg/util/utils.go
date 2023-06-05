package util

import (
	log "github.com/sirupsen/logrus"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

func GetUserHome() *string {
	home, err := os.UserHomeDir()

	if err != nil {
		return nil
	}

	return &home
}

func PanicOnError(err error) {
	if err != nil {
		panic(err)
	}
}

func ReadFile(path *string) *[]byte {
	file, err := os.ReadFile(*path)

	if err != nil {
		return nil
	}

	return &file
}

func WriteFile(path *string, data *[]byte) error {
	file, err := os.OpenFile(*path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0660)

	if err != nil {
		return err
	}

	defer file.Close()

	_, err = file.Write(*data)

	return err
}

func GetFilesFullPath(path *string, fileNames *[]string) []string {
	var results []string

	for _, file := range *fileNames {
		results = append(results, filepath.Join(*path, file))
	}

	return results
}

func FindFilesWithRegex(root *string, regExpr string) []string {
	var results []string

	rx, err := regexp.Compile(regExpr)

	if err != nil {
		log.Error(err)
		return nil
	}

	err = filepath.Walk(*root,
		func(path string, info fs.FileInfo, err error) error {
			if err == nil && rx.MatchString(info.Name()) {
				results = append(results, info.Name())
			}
			return nil
		})

	if err != nil {
		log.Error(err)
		return nil
	}

	return results
}
