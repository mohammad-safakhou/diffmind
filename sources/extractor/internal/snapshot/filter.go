package snapshot

var defaultSkipDirs = map[string]struct{}{
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
}

const defaultMaxFileBytes int64 = 4 << 20

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
