package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"howett.net/plist"
)

type RepoPkg struct {
	Repo           string   `json:"repo"`
	Pkgname        string   `json:"pkgname"`
	Version        string   `json:"version"`
	Architecture   string   `json:"architecture"`
	ShortDesc      string   `json:"short_desc"`
	RunDepends     []string `json:"run_depends"`
	Provides       []string `json:"provides"`
	Conflicts      []string `json:"conflicts"`
	FilenameSHA256 string   `json:"filename-sha256,omitempty"`
}

func cmdSync(cfg *Config) error {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}
	logf, err := openLog("sync")
	if err != nil {
		return err
	}
	defer logf.Close()

	repos := []struct{ name, url string }{
		{"main", cfg.RepoMain},
		{"nonfree", cfg.RepoNonfree},
	}
	for _, r := range repos {
		if r.url == "" {
			continue
		}
		if err := syncRepo(cfg, r.name, r.url, logf); err != nil {
			return err
		}
	}
	return nil
}

// syncRepo скачивает <url>/<arch>-repodata (tar.zst), извлекает из него
// index.plist и парсит его.
func syncRepo(cfg *Config, name, url string, logf *os.File) error {
	repodataURL := strings.TrimRight(url, "/") + "/" + cfg.Arch + "-repodata"
	repodataPath := filepath.Join(cacheDir, "repo_"+name+".repodata")

	// 1. Скачиваем архив repodata.
	if err := runCmd(logf, "curl", "-LSs", "-f",
		"--connect-timeout", "15", "--max-time", "600",
		"--retry", "3", "--retry-delay", "2", "--retry-connrefused",
		"-o", repodataPath, repodataURL); err != nil {
		return fmt.Errorf("curl %s: %w", repodataURL, err)
	}

	// 2. Проверяем, что файл не пустой и не слишком маленький.
	st, err := os.Stat(repodataPath)
	if err != nil {
		return err
	}
	if st.Size() < 10*1024 {
		os.Remove(repodataPath)
		return fmt.Errorf("repodata is too small, likely invalid: %s", repodataURL)
	}

	// 3. Извлекаем index.plist из архива через системный tar.
	indexData, err := exec.Command("tar", "-xOf", repodataPath, "index.plist").Output()
	if err != nil {
		// Альтернативный путь с ./ префиксом.
		indexData, err = exec.Command("tar", "-xOf", repodataPath, "./index.plist").Output()
		if err != nil {
			return fmt.Errorf("cannot extract index.plist from %s: %w", repodataPath, err)
		}
	}
	if len(indexData) == 0 {
		return fmt.Errorf("index.plist is empty in %s", repodataPath)
	}

	// 4. Сохраняем извлечённый index.plist (для отладки и повторного парсинга).
	indexPath := filepath.Join(cacheDir, "repo_"+name+".plist")
	if err := atomicWrite(indexPath, indexData); err != nil {
		return err
	}

	// 5. Парсим.
	return parseIndex(indexPath, name)
}

// parseIndex читает index.plist и сохраняет repo_<name>.json и search_<name>.txt.
//
// Формат XBPS index.plist (v1.5):
//
//	<dict>
//	  <key>pkgname</key>
//	  <dict>
//	    <key>pkgver</key><string>pkgname-version</string>
//	    <key>architecture</key><string>x86_64</string>
//	    <key>short_desc</key><string>...</string>
//	    <key>run_depends</key><array>...</array>
//	    ...
//	  </dict>
//	  ...
//	</dict>
//
// Имя пакета — внешний ключ. Поле "pkgver" содержит "<pkgname>-<version>",
// отделяем префикс.
func parseIndex(plistPath, name string) error {
	data, err := os.ReadFile(plistPath)
	if err != nil {
		return err
	}
	var raw map[string]interface{}
	if _, err := plist.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw) == 0 {
		return fmt.Errorf("parseIndex: empty index.plist: %s", plistPath)
	}

	pkgs := make([]RepoPkg, 0, len(raw))
	for key, v := range raw {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		pkgs = append(pkgs, extractRepoPkg(key, m, name))
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("parseIndex: parsed 0 packages from %s", plistPath)
	}

	jsonData, err := json.Marshal(pkgs)
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(cacheDir, "repo_"+name+".json"), jsonData); err != nil {
		return err
	}

	var sb strings.Builder
	for _, p := range pkgs {
		sb.WriteString(p.Pkgname)
		sb.WriteByte('|')
		sb.WriteString(p.Version)
		sb.WriteByte('|')
		sb.WriteString(p.ShortDesc)
		sb.WriteByte('\n')
	}
	return atomicWrite(filepath.Join(cacheDir, "search_"+name+".txt"), []byte(sb.String()))
}

// extractRepoPkg: key — имя пакета, m — его dict из index.plist.
// Поле "pkgname" в dict отсутствует; имя берём из ключа.
// Поле "pkgver" содержит "<pkgname>-<version>", отделяем префикс.
func extractRepoPkg(key string, m map[string]interface{}, repo string) RepoPkg {
	fullVer := dictStr(m, "pkgver")
	version := fullVer
	if strings.HasPrefix(fullVer, key+"-") {
		version = fullVer[len(key)+1:]
	}
	return RepoPkg{
		Repo:           repo,
		Pkgname:        key,
		Version:        version,
		Architecture:   dictStr(m, "architecture"),
		ShortDesc:      dictStr(m, "short_desc"),
		RunDepends:     dictStrs(m, "run_depends"),
		Provides:       dictStrs(m, "provides"),
		Conflicts:      dictStrs(m, "conflicts"),
		FilenameSHA256: dictStr(m, "filename-sha256"),
	}
}

func dictStr(m map[string]interface{}, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func dictStrs(m map[string]interface{}, k string) []string {
	arr, ok := m[k].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
