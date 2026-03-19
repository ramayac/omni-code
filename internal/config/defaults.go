package config

// DefaultSkipDirs contains directory names skipped during file walking by default.
var DefaultSkipDirs = []string{
	".git", "node_modules", "dist", "build",
	"vendor", ".next", "__pycache__", ".venv", ".tox",
	".gradle", ".cargo", "vcpkg_installed", "target", "Pods",
	".idea", ".vscode", ".settings",
	"__MACOSX", ".cache", ".pytest_cache",
}

// DefaultSkipExtensions contains file extensions skipped by default.
var DefaultSkipExtensions = []string{
	".pdf", ".png", ".jpg", ".jpeg", ".gif",
	".svg", ".mp4", ".mp3", ".zip", ".tar",
	".gz", ".exe", ".dll", ".so", ".dylib",
	".wasm", ".bin", ".dat", ".db", ".sqlite",
	".ttf", ".otf", ".woff", ".woff2", ".dfont",
	".avif", ".webp", ".jxl", ".tiff", ".tif", ".mkv", ".mka", ".webm", ".ogg",
	".jar", ".iso", ".rar", ".7z",
	".tvg", ".pbm", ".ppm", ".wav",
	".pyc", ".pyo", ".class", ".o", ".obj",
	".pptx", ".docx", ".xlsx",
	".keystore", ".jks", ".pepk", ".ico",
	".eot", ".rdb", ".woff", ".woff2", ".ttf", ".otf",
}

// DefaultSkipFilenames contains exact filenames skipped by default.
var DefaultSkipFilenames = []string{
	".env", "package-lock.json", "yarn.lock",
	"go.sum", ".DS_Store", "Thumbs.db",
}

// DefaultSkipDirsMap is DefaultSkipDirs as a fast-lookup map.
var DefaultSkipDirsMap = listToMap(DefaultSkipDirs)

// DefaultSkipExtensionsMap is DefaultSkipExtensions as a fast-lookup map.
var DefaultSkipExtensionsMap = listToMap(DefaultSkipExtensions)

// DefaultSkipFilenamesMap is DefaultSkipFilenames as a fast-lookup map.
var DefaultSkipFilenamesMap = listToMap(DefaultSkipFilenames)

func listToMap(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}
