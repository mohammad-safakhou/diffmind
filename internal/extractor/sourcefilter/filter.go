// Package sourcefilter centralizes repository traversal rules for deterministic
// analysis. It replaces the old snapshot copy filter while preserving the same
// "only useful source/config input" boundary.
package sourcefilter

import (
	"io/fs"
	"path/filepath"
	"strings"
)

var skippedDirs = map[string]struct{}{
	".git": {}, ".hg": {}, ".svn": {}, ".idea": {}, ".vscode": {}, ".vs": {},
	".fleet": {}, ".history": {},
	".gradle": {}, ".mvn": {}, "target": {}, "build": {}, ".classpath": {}, ".settings": {},
	"node_modules": {}, "bower_components": {}, ".pnpm-store": {}, ".yarn": {},
	"dist": {}, "out": {}, "public/build": {}, ".next": {}, ".nuxt": {},
	".svelte-kit": {}, ".astro": {}, ".turbo": {}, ".vercel": {}, ".netlify": {},
	"coverage":      {},
	".pytest_cache": {}, "__pycache__": {}, ".venv": {}, "venv": {}, "env": {},
	".tox": {}, ".mypy_cache": {}, ".ruff_cache": {}, ".pytype": {}, "htmlcov": {},
	".gocache": {}, "target.rust": {},
	".terraform": {}, ".serverless": {}, ".aws-sam": {}, ".cache": {}, ".diffmind": {},
	".DS_Store": {},

	// These were skipped by the AST walker before snapshot removal and are
	// still not deterministic source inputs.
	"vendor": {}, ".bundle": {}, "bin": {}, "tmp": {}, ".m2": {}, ".ivy2": {}, "testdata": {}, "fixtures": {},
}

const maxFileBytes int64 = 4 << 20

var skippedExtensions = map[string]struct{}{
	".class": {}, ".jar": {}, ".war": {}, ".ear": {},
	".pyc": {}, ".pyo": {}, ".whl": {},
	".o": {}, ".a": {}, ".so": {}, ".dylib": {}, ".dll": {}, ".exe": {},
	".wasm": {}, ".lock": {}, ".tsbuildinfo": {}, ".map": {},
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {}, ".bmp": {},
	".ico": {}, ".svg": {}, ".tiff": {}, ".heic": {},
	".mp3": {}, ".wav": {}, ".ogg": {}, ".flac": {}, ".m4a": {},
	".mp4": {}, ".mov": {}, ".avi": {}, ".webm": {}, ".mkv": {},
	".pdf": {}, ".doc": {}, ".docx": {}, ".xls": {}, ".xlsx": {}, ".ppt": {}, ".pptx": {},
	".zip": {}, ".tar": {}, ".tgz": {}, ".gz": {}, ".bz2": {}, ".xz": {},
	".rar": {}, ".7z": {},
	".ttf": {}, ".otf": {}, ".woff": {}, ".woff2": {}, ".eot": {},
}

func SkipDirName(name string) bool {
	_, ok := skippedDirs[name]
	return ok
}

func SkipFileName(name string) bool {
	_, ok := skippedExtensions[strings.ToLower(filepath.Ext(name))]
	return ok
}

func SkipFileInfo(info fs.FileInfo) bool {
	if info == nil {
		return true
	}
	if !info.Mode().IsRegular() {
		return true
	}
	if SkipFileName(info.Name()) {
		return true
	}
	return info.Size() > maxFileBytes
}
