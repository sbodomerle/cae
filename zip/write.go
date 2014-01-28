// Copyright 2013 cae authors
//
// Licensed under the Apache License, Version 2.0 (the "License"): you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
// License for the specific language governing permissions and limitations
// under the License.

package zip

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var Verbose = true

func extractFile(f *zip.File, destPath string) error {
	// Create diretory before create file
	os.MkdirAll(path.Join(destPath, path.Dir(f.Name)), os.ModePerm)

	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	fw, _ := os.Create(path.Join(destPath, f.Name))
	if err != nil {
		return err
	}
	_, err = io.Copy(fw, rc)
	return err
}

func isEntry(name string, entries []string) bool {
	for _, e := range entries {
		if e == name {
			return true
		}
	}
	return false
}

var defaultExtractFunc = func(fullName string, fi os.FileInfo) error {
	if !Verbose {
		return nil
	}

	fmt.Println("Unzipping file..." + fullName)
	return nil
}

// ExtractTo extracts the complete archive or the given files to the specified destination.
// It accepts a function as a middleware for custom-operations.
func (z *ZipArchive) ExtractToFunc(destPath string, fn func(fullName string, fi os.FileInfo) error, entries ...string) (err error) {
	destPath = strings.Replace(destPath, "\\", "/", -1)
	isHasEntry := len(entries) > 0
	if Verbose {
		fmt.Println("Unzipping " + z.FileName + "...")
	}
	os.MkdirAll(destPath, os.ModePerm)

	for _, f := range z.File {
		f.Name = strings.Replace(f.Name, "\\", "/", -1)

		// Directory.
		if strings.HasSuffix(f.Name, "/") {
			if isHasEntry {
				if isEntry(f.Name, entries) {
					if err := fn(f.Name, f.FileInfo()); err != nil {
						return err
					}
					os.MkdirAll(path.Join(destPath, f.Name), os.ModePerm)
				}
				continue
			}
			if err := fn(f.Name, f.FileInfo()); err != nil {
				return err
			}
			os.MkdirAll(path.Join(destPath, f.Name), os.ModePerm)
			continue
		}

		// File.
		if isHasEntry {
			if isEntry(f.Name, entries) {
				if err := fn(f.Name, f.FileInfo()); err != nil {
					return err
				}
				err = extractFile(f, destPath)
			}
		} else {
			if err := fn(f.Name, f.FileInfo()); err != nil {
				return err
			}
			err = extractFile(f, destPath)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ExtractTo extracts the complete archive or the given files to the specified destination.
// Call Flush() to apply changes before this.
func (z *ZipArchive) ExtractTo(destPath string, entries ...string) (err error) {
	return z.ExtractToFunc(destPath, defaultExtractFunc, entries...)
}

func (z *ZipArchive) extractFile(f *File) error {
	if !z.isHasWriter {
		for _, zf := range z.ReadCloser.File {
			if f.Name == zf.Name {
				return extractFile(zf, f.absPath)
			}
		}
	}

	return copy(f.absPath, f.Name) // from -> to
}

// Flush saves changes to original zip file if any.
func (z *ZipArchive) Flush() error {
	if !z.isHasChanged || (z.ReadCloser == nil && !z.isHasWriter) {
		return nil
	}

	// Extract to tmp path and pack back.
	tmpPath := path.Join(os.TempDir(), "cae", path.Base(z.FileName))
	os.RemoveAll(tmpPath)
	defer os.RemoveAll(tmpPath)

	for _, f := range z.files {
		if strings.HasSuffix(f.Name, "/") {
			os.MkdirAll(path.Join(tmpPath, f.Name), os.ModePerm)
			continue
		}

		f.Name = path.Join(tmpPath, f.Name)
		if err := z.extractFile(f); err != nil {
			return err
		}
	}

	if z.isHasWriter {
		return packToWriter(tmpPath, z.writer, defaultPackFunc, true)
	}

	if err := PackTo(tmpPath, z.FileName); err != nil {
		return err
	}
	return z.Open(z.FileName, os.O_RDWR|os.O_TRUNC, z.Permission)
}

func packDir(srcPath string, recPath string, zw *zip.Writer, fn func(fullName string, fi os.FileInfo) error) error {
	dir, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer dir.Close()

	// Get file info slice
	fis, err := dir.Readdir(0)
	if err != nil {
		return err
	}

	for _, fi := range fis {
		if globalFilter(fi.Name()) {
			continue
		}
		// Append path
		curPath := srcPath + "/" + fi.Name()
		tmpRecPath := filepath.Join(recPath, fi.Name())
		if err = fn(curPath, fi); err != nil {
			return err
		}

		// Check it is directory or file
		if fi.IsDir() {
			if err = packFile(srcPath, tmpRecPath, zw, fi); err != nil {
				return err
			}

			err = packDir(curPath, tmpRecPath, zw, fn)
		} else {
			err = packFile(curPath, tmpRecPath, zw, fi)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func packFile(srcFile string, recPath string, zw *zip.Writer, fi os.FileInfo) (err error) {
	if fi.IsDir() {
		// Create zip header
		fh := new(zip.FileHeader)
		fh.Name = recPath + "/"
		fh.UncompressedSize = 0

		_, err = zw.CreateHeader(fh)
	} else {
		// Create zip header
		fh := new(zip.FileHeader)
		fh.Name = recPath
		fh.UncompressedSize = uint32(fi.Size())
		var fw io.Writer
		fw, err = zw.CreateHeader(fh)
		if err != nil {
			return err
		}

		var f *os.File
		f, err = os.Open(srcFile)
		if err != nil {
			return err
		}
		_, err = io.Copy(fw, f)
	}
	return err
}

func packToWriter(srcPath string, w io.Writer, fn func(fullName string, fi os.FileInfo) error, includeDir bool) error {
	zw := zip.NewWriter(w)
	defer zw.Close()

	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		return err
	}

	basePath := path.Base(srcPath)

	if fi.IsDir() {
		if includeDir {
			if err = packFile(srcPath, basePath, zw, fi); err != nil {
				return err
			}
		} else {
			basePath = ""
		}
		return packDir(srcPath, basePath, zw, fn)
	}

	return packFile(srcPath, basePath, zw, fi)
}

func packTo(srcPath, destPath string, fn func(fullName string, fi os.FileInfo) error, includeDir bool) error {
	fw, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer fw.Close()

	return packToWriter(srcPath, fw, fn, includeDir)
}

var defaultPackFunc = func(fullName string, fi os.FileInfo) error {
	if !Verbose {
		return nil
	}

	if fi.IsDir() {
		fmt.Printf("Adding dir...%s\n", fullName)
	} else {
		fmt.Printf("Adding file...%s\n", fullName)
	}

	return nil
}

// PackTo packs the complete archive to the specified destination.
// It accepts a function as a middleware for custom-operations.
func PackToFunc(srcPath, destPath string, fn func(fullName string, fi os.FileInfo) error, includeDir ...bool) error {
	isIncludeDir := false
	if len(includeDir) > 0 && includeDir[0] {
		isIncludeDir = true
	}

	return packTo(srcPath, destPath, fn, isIncludeDir)
}

// PackTo packs the complete archive to the specified destination.
// Call Flush() will automatically call this in the end.
func PackTo(srcPath, destPath string, includeDir ...bool) error {
	return PackToFunc(srcPath, destPath, defaultPackFunc, includeDir...)
}

// Close opened or created archive and save changes.
func (z *ZipArchive) Close() (err error) {
	if err = z.Flush(); err != nil {
		return err
	}

	if z.ReadCloser != nil {
		if err = z.ReadCloser.Close(); err != nil {
			return err
		}
		z.ReadCloser = nil
	}
	return nil
}
