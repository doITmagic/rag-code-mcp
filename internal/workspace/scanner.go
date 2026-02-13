package workspace

import (
	"fmt"
	"hash/fnv"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type workspaceScan struct {
	LanguageDirs  map[string][]string
	LanguageFiles map[string][]string // Track individual files per language
	DocFiles      []string
	TotalFiles    int
	GeneratedAt   time.Time
}

var defaultSkipDirs = map[string]struct{}{
	".git":         {},
	".idea":        {},
	".vscode":      {},
	"node_modules": {},
	"vendor":       {},
	"dist":         {},
	"build":        {},
	"storage":      {},
	"public":       {},
}

func addDirForLanguage(scan *workspaceScan, cache map[string]map[string]struct{}, language, dir string) {
	if dir == "" {
		return
	}
	lang := strings.ToLower(language)
	if _, ok := cache[lang]; !ok {
		cache[lang] = make(map[string]struct{})
	}
	if _, exists := cache[lang][dir]; exists {
		return
	}
	cache[lang][dir] = struct{}{}
	if scan.LanguageDirs == nil {
		scan.LanguageDirs = make(map[string][]string)
	}
	scan.LanguageDirs[lang] = append(scan.LanguageDirs[lang], dir)
}

func addFileForLanguage(scan *workspaceScan, language, path string) {
	lang := strings.ToLower(language)
	if scan.LanguageFiles == nil {
		scan.LanguageFiles = make(map[string][]string)
	}
	scan.LanguageFiles[lang] = append(scan.LanguageFiles[lang], path)
}

func (m *Manager) scanWorkspace(info *Info) (*workspaceScan, error) {
	// Validate root path before scanning to prevent broad filesystem access
	if isInvalidRoot(info.Root) {
		return nil, fmt.Errorf("cannot scan invalid workspace root: %s", info.Root)
	}

	scan := &workspaceScan{
		LanguageDirs:  make(map[string][]string),
		LanguageFiles: make(map[string][]string),
		DocFiles:      make([]string, 0),
		GeneratedAt:   time.Now(),
	}
	dirCache := make(map[string]map[string]struct{})
	err := filepath.WalkDir(info.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == info.Root {
				return nil
			}
			if _, skip := defaultSkipDirs[d.Name()]; skip {
				return filepath.SkipDir
			}
			return nil
		}

		scan.TotalFiles++
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".go":
			addDirForLanguage(scan, dirCache, "go", filepath.Dir(path))
			addFileForLanguage(scan, "go", path)
		case ".php":
			addDirForLanguage(scan, dirCache, "php", filepath.Dir(path))
			addFileForLanguage(scan, "php", path)
		case ".py":
			addDirForLanguage(scan, dirCache, "python", filepath.Dir(path))
			addFileForLanguage(scan, "python", path)
		case ".html", ".htm":
			addDirForLanguage(scan, dirCache, "html", filepath.Dir(path))
			addFileForLanguage(scan, "html", path)
		case ".md":
			scan.DocFiles = append(scan.DocFiles, path)
		default:
			// ignored
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return scan, nil
}

func (s *workspaceScan) fingerprint(language string) string {
	h := fnv.New64a()
	lang := strings.ToLower(language)
	fmt.Fprintf(h, "%s|%d|%d", lang, s.TotalFiles, len(s.DocFiles))
	dirs := append([]string(nil), s.LanguageDirs[lang]...)
	sort.Strings(dirs)
	for _, dir := range dirs {
		h.Write([]byte(dir))
		h.Write([]byte("|"))
	}
	docs := append([]string(nil), s.DocFiles...)
	sort.Strings(docs)
	for _, doc := range docs {
		h.Write([]byte(doc))
		h.Write([]byte("|"))
	}
	return fmt.Sprintf("%x", h.Sum64())
}

func (m *Manager) fingerprintKey(info *Info, language string) string {
	return info.ID + "-" + strings.ToLower(language)
}

func (m *Manager) recordFingerprint(info *Info, language string, scan *workspaceScan) {
	if scan == nil {
		return
	}
	fp := scan.fingerprint(language)
	key := m.fingerprintKey(info, language)
	m.scanMu.Lock()
	if m.scanFingerprints == nil {
		m.scanFingerprints = make(map[string]string)
	}
	m.scanFingerprints[key] = fp
	m.scanMu.Unlock()
}

// NeedsReindex rescans the workspace and determines if tracked files changed for the language.
// Returns true when changes are detected or no previous fingerprint exists.
func (m *Manager) NeedsReindex(info *Info, language string) (bool, error) {
	scan, err := m.scanWorkspace(info)
	if err != nil {
		return false, err
	}
	fp := scan.fingerprint(language)
	key := m.fingerprintKey(info, language)
	m.scanMu.RLock()
	prev := m.scanFingerprints[key]
	m.scanMu.RUnlock()
	if prev == "" {
		return true, nil
	}
	return prev != fp, nil
}
