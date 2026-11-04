package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"howett.net/plist"
)

var archiveMeta = map[string]bool{
	"INSTALL": true, "REMOVE": true,
	"files.plist": true, "props.plist": true,
}

func sha256File(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func parseFilesPlist(data []byte) (map[string]FileInfo, error) {
	var raw map[string]interface{}
	if _, err := plist.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := map[string]FileInfo{}
	// files.plist: {"files": [...], "conf_files": [...], "links": [...]}.
	// Путь в ключе "file" (например "./usr/bin/foo"). Нормализуем к "/usr/bin/foo",
	// чтобы совпадало с target = "/" + rel в distribute.
	for _, key := range []string{"files", "conf_files"} {
		arr, ok := raw[key].([]interface{})
		if !ok {
			continue
		}
		for _, e := range arr {
			m, ok := e.(map[string]interface{})
			if !ok {
				continue
			}
			p := dictStr(m, "file")
			if p == "" {
				continue
			}
			p = strings.TrimPrefix(p, "./")
			if !strings.HasPrefix(p, "/") {
				p = "/" + p
			}
			out[p] = FileInfo{SHA256: dictStr(m, "sha256")}
		}
	}
	return out, nil
}

func readFromArchive(archive, name string) ([]byte, error) {
	if out, err := exec.Command("tar", "-xOf", archive, name).Output(); err == nil {
		return out, nil
	}
	if out, err := exec.Command("tar", "-xOf", archive, "./"+name).Output(); err == nil {
		return out, nil
	}
	listing, _ := exec.Command("tar", "-tf", archive).Output()
	lines := strings.Split(string(listing), "\n")
	if len(lines) > 30 {
		lines = lines[:30]
	}
	return nil, fmt.Errorf("readFromArchive: %q not found in %s; archive contents:\n%s",
		name, archive, strings.Join(lines, "\n"))
}

func packageURL(cfg *Config, p *RepoPkg) string {
	base := cfg.RepoMain
	if p.Repo == "nonfree" {
		base = cfg.RepoNonfree
	}
	base = strings.TrimRight(base, "/")
	return fmt.Sprintf("%s/%s-%s.%s.xbps", base, p.Pkgname, p.Version, p.Architecture)
}

func downloadPackage(cfg *Config, p *RepoPkg, logf *os.File) (string, error) {
	dest := filepath.Join(cacheDir, p.Pkgname+"-"+p.Version+"."+p.Architecture+".tar.zst")
	if st, err := os.Stat(dest); err == nil && st.Size() > 0 {
		if p.FilenameSHA256 == "" || sha256File(dest) == p.FilenameSHA256 {
			return dest, nil
		}
		os.Remove(dest) // sha256 в кеше не совпал — перекачиваем
	}
	url := packageURL(cfg, p)
	if url == "" || !strings.HasPrefix(url, "http") {
		return "", fmt.Errorf("invalid package URL: %q", url)
	}
	if err := checkInterrupted(); err != nil {
		return "", err
	}

	// -s подавляет собственный прогресс curl — рисуем свой.
	args := []string{"-L", "-f", "-s",
		"--connect-timeout", "15", "--max-time", "600",
		"--retry", "3", "--retry-delay", "2", "--retry-connrefused",
		"-o", dest, url}
	cmd := exec.Command("curl", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = logf
	cmd.Stderr = logf

	if !isatty(os.Stderr.Fd()) {
		fmt.Fprintf(logf, "downloading %s-%s (%s)...\n", p.Pkgname, p.Version, p.Architecture)
		if err := cmd.Run(); err != nil {
			os.Remove(dest)
			return "", fmt.Errorf("curl %s: %w", url, err)
		}
	} else {
		if err := cmd.Start(); err != nil {
			os.Remove(dest)
			return "", fmt.Errorf("curl %s: %w", url, err)
		}
		done := make(chan struct{})
		go progressLoop(dest, p.Pkgname, p.Version, p.FilenameSize, done)
		err := cmd.Wait()
		close(done)
		fmt.Fprintln(os.Stderr) // завершить строку прогресса
		if err != nil {
			os.Remove(dest)
			return "", fmt.Errorf("curl %s: %w", url, err)
		}
	}

	if err := checkInterrupted(); err != nil {
		os.Remove(dest)
		return "", err
	}

	st, err := os.Stat(dest)
	if err != nil {
		return "", fmt.Errorf("curl reported success but %s missing (url=%s)", dest, url)
	}
	if st.Size() == 0 {
		os.Remove(dest)
		return "", fmt.Errorf("downloaded zero-byte package: %s", url)
	}
	if p.FilenameSHA256 != "" {
		if got := sha256File(dest); got != p.FilenameSHA256 {
			os.Remove(dest)
			return "", fmt.Errorf("sha256 mismatch for %s-%s: got %s, want %s",
				p.Pkgname, p.Version, got, p.FilenameSHA256)
		}
	}
	fmt.Fprintf(logf, "downloaded %s (%d bytes) from %s\n", dest, st.Size(), url)
	return dest, nil
}

// progressLoop рисует прогресс загрузки в stderr:
// "installing name-ver [pct%, cur / total, speed]".
func progressLoop(dest, name, version string, total int64, done <-chan struct{}) {
	prev := int64(0)
	prevT := time.Now()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
			st, err := os.Stat(dest)
			if err != nil {
				continue
			}
			cur := st.Size()
			dt := time.Since(prevT).Seconds()
			var speed float64
			if dt > 0 {
				speed = float64(cur-prev) / dt
			}
			if total > 0 {
				pct := int(float64(cur) / float64(total) * 100)
				if pct > 100 {
					pct = 100
				}
				fmt.Fprintf(os.Stderr, "\rinstalling %s-%s [%d%%, %s / %s, %s/s]    ",
					name, version, pct, humanSize(float64(cur)), humanSize(float64(total)), humanSize(speed))
			} else {
				fmt.Fprintf(os.Stderr, "\rinstalling %s-%s [%s, %s/s]    ",
					name, version, humanSize(float64(cur)), humanSize(speed))
			}
			prev = cur
			prevT = time.Now()
		}
	}
}

// humanSize форматирует байты в человеко-читаемый вид (1024-основа).
func humanSize(n float64) string {
	units := []string{"B", "KiB", "MiB", "GiB"}
	u := 0
	for n >= 1024 && u < len(units)-1 {
		n /= 1024
		u++
	}
	if u == 0 {
		return fmt.Sprintf("%.0f%s", n, units[u])
	}
	return fmt.Sprintf("%.1f%s", n, units[u])
}

func detectStrip(archive string) (int, error) {
	out, err := exec.Command("tar", "-tf", archive).Output()
	if err != nil {
		return 0, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return 0, fmt.Errorf("empty archive: %s", archive)
	}
	withDot, without := 0, 0
	for _, l := range lines {
		if l == "" || l == "./" {
			continue
		}
		if strings.HasPrefix(l, "./") {
			withDot++
		} else {
			without++
		}
	}
	switch {
	case withDot > 0 && without > 0:
		return 0, fmt.Errorf("Malformed archive: mixed path prefixes")
	case withDot > 0:
		return 1, nil
	}
	return 0, nil
}

// permBits сохраняет setuid/setgid/sticky: os.FileMode.Perm() их отбрасывает,
// а без них doas/passwd/su/mount в установленной системе не работают.
func permBits(m os.FileMode) os.FileMode {
	return m & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
}

// modeString — unix-режим для записи в БД ("4755", "0644").
func modeString(m os.FileMode) string {
	b := uint32(m.Perm())
	if m&os.ModeSetuid != 0 {
		b |= 04000
	}
	if m&os.ModeSetgid != 0 {
		b |= 02000
	}
	if m&os.ModeSticky != 0 {
		b |= 01000
	}
	return fmt.Sprintf("%04o", b)
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// umask при создании срезает биты — ставим режим явно.
	return os.Chmod(dst, mode)
}

func place(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	mode := permBits(info.Mode())
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		os.Remove(dst)
		return os.Symlink(target, dst)
	case info.IsDir():
		if err := os.MkdirAll(dst, mode.Perm()); err != nil {
			return err
		}
		return os.Chmod(dst, mode)
	default:
		os.Remove(dst)
		return copyFile(src, dst, mode)
	}
}

func distribute(tmpdir string, files map[string]FileInfo, logf *os.File) (map[string]FileInfo, error) {
	out := map[string]FileInfo{}
	err := filepath.Walk(tmpdir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(tmpdir, path)
		if err != nil || rel == "." {
			return err
		}
		if archiveMeta[rel] {
			return nil
		}
		target := "/" + rel     // логический путь (ключ БД / files.plist)
		dst := rootJoin(target) // физический путь на диске
		isRegular := info.Mode().IsRegular()

		if isRegular {
			// Проверяем целостность распакованного файла против sha256 из files.plist.
			if want := files[target].SHA256; want != "" {
				if got := sha256File(path); got != want {
					return fmt.Errorf("sha256 mismatch: %s (got %s, want %s)", target, got, want)
				}
			}
		}

		if isRegular && strings.HasPrefix(target, "/etc/") {
			if _, err := os.Lstat(dst); err == nil {
				old := sha256File(dst)
				want := files[target].SHA256
				if want != "" && old != want {
					nf := dst + ".meow-new"
					if err := place(path, nf); err != nil {
						return err
					}
					fmt.Fprintf(logf, "warning: %s differs, saved as .meow-new\n", target)
					out[target+".meow-new"] = FileInfo{
						Mode:   modeString(info.Mode()),
						SHA256: sha256File(nf),
					}
					return nil
				}
			}
		}
		if err := place(path, dst); err != nil {
			return err
		}
		fi := FileInfo{Mode: modeString(info.Mode())}
		if isRegular && strings.HasPrefix(target, "/etc/") {
			fi.SHA256 = sha256File(dst)
		}
		out[target] = fi
		return nil
	})
	return out, err
}

// execScript запускает shell-скрипт пакета по XBPS-контракту:
// sh <script> <phase> <update>, phase ∈ {pre,post,pre-remove,post-remove},
// update ∈ {yes,no}. Env: PKGNAME, VERSION, UPDATE.
func execScript(data []byte, phase string, update bool, pkgname, version string, logf *os.File) error {
	if rootDir != "" {
		fmt.Fprintf(logf, "skipping %s script under --root\n", phase)
		return nil
	}
	if len(data) == 0 {
		return nil
	}
	if err := checkInterrupted(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "meow-script-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	os.Chmod(tmp.Name(), 0755)

	cmd := exec.Command("sh", tmp.Name(), phase, boolStr(update))
	cmd.Env = append(os.Environ(),
		"PKGNAME="+pkgname,
		"VERSION="+version,
		"UPDATE="+boolStr(update),
	)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Run(); err != nil {
		if interrupted.Load() {
			return ErrInterrupted
		}
		return err
	}
	return checkInterrupted()
}

func runInstallScript(archive, phase string, update bool, p *RepoPkg, logf *os.File) error {
	data, err := readFromArchive(archive, "INSTALL")
	if err != nil || len(data) == 0 {
		return nil
	}
	return execScript(data, phase, update, p.Pkgname, p.Version, logf)
}

func runRemoveScript(scriptPath, phase string, pkg *Pkg, logf *os.File) error {
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return nil // REMOVE-скрипта нет
	}
	return execScript(data, phase, false, pkg.Pkgname, pkg.Version, logf)
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func setupAccounts(archive string, logf *os.File) error {
	if rootDir != "" {
		fmt.Fprintln(logf, "skipping system accounts/groups under --root")
		return nil
	}
	data, err := readFromArchive(archive, "props.plist")
	if err != nil || len(data) == 0 {
		return nil
	}
	var raw map[string]interface{}
	if _, err := plist.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, g := range dictStrs(raw, "system_groups") {
		parts := strings.Split(g, ":")
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		name := parts[0]
		if exec.Command("getent", "group", name).Run() == nil {
			continue
		}
		args := []string{}
		if len(parts) > 1 && parts[1] != "" {
			args = append(args, "-g", parts[1])
		}
		args = append(args, name)
		if err := runCmd(logf, "groupadd", args...); err != nil {
			return err
		}
	}
	for _, a := range dictStrs(raw, "system_accounts") {
		// Формат XBPS: "name" или "name:uid" (одно число = и uid, и gid).
		// homedir/shell/descr задаются отдельными переменными шаблона и в props.plist
		// бинарника не попадают — дефолты берёт useradd.
		parts := strings.Split(a, ":")
		name := parts[0]
		if name == "" {
			continue
		}
		uid := ""
		if len(parts) > 1 {
			uid = parts[1]
		}
		// Группа для аккаунта (primary group = name, gid = uid при наличии).
		if exec.Command("getent", "group", name).Run() != nil {
			args := []string{}
			if uid != "" {
				args = append(args, "-g", uid)
			}
			args = append(args, name)
			if err := runCmd(logf, "groupadd", args...); err != nil {
				return err
			}
		}
		// Пользователь.
		if exec.Command("getent", "passwd", name).Run() == nil {
			continue
		}
		args := []string{"-r"}
		if uid != "" {
			args = append(args, "-u", uid, "-g", uid)
		} else {
			args = append(args, "-g", name)
		}
		args = append(args, name)
		if err := runCmd(logf, "useradd", args...); err != nil {
			return err
		}
	}
	return nil
}

func setupRunitPerms(pkgname string) {
	dir := rootJoin(filepath.Join("/etc/sv", pkgname))
	for _, f := range []string{"run", "finish", "check", "log/run"} {
		p := filepath.Join(dir, f)
		if _, err := os.Stat(p); err == nil {
			_ = os.Chmod(p, 0755)
		}
	}
}

// installOne выполняет фазы 0-6 и управляет статусом пакета в журнале и БД.
// auto=true означает, что пакет ставится как зависимость (cascade его потом удалит).
func installOne(cfg *Config, p *RepoPkg, update bool, auto bool, j *Journal, idx int, logf *os.File) (rerr error) {
	defer func() {
		switch {
		case rerr == nil:
			_ = j.setStatus(idx, "done")
		case errors.Is(rerr, ErrInterrupted):
			// leave journal as-is
		case errors.Is(rerr, ErrPostInstall):
			_ = j.setStatus(idx, "half-configured")
		default:
			_ = j.setStatus(idx, "failed")
		}
	}()

	archive, err := downloadPackage(cfg, p, logf)
	if err != nil {
		return err
	}
	// Сохраняем REMOVE-скрипт (если есть) для выполнения при удалении.
	if data, err := readFromArchive(archive, "REMOVE"); err == nil && len(data) > 0 {
		if err := atomicWrite(filepath.Join(dbDir, p.Pkgname+".remove"), data); err != nil {
			return err
		}
	}
	filesData, err := readFromArchive(archive, "files.plist")
	if err != nil {
		return err
	}
	files, err := parseFilesPlist(filesData)
	if err != nil {
		return err
	}

	if err := j.setPhase(idx, "pre-hooks"); err != nil {
		return err
	}
	if err := setupAccounts(archive, logf); err != nil {
		return err
	}
	if err := checkInterrupted(); err != nil {
		return err
	}

	if err := j.setPhase(idx, "pre-install"); err != nil {
		return err
	}
	if err := runInstallScript(archive, "pre", update, p, logf); err != nil {
		return err
	}
	if err := checkInterrupted(); err != nil {
		return err
	}

	if err := j.setPhase(idx, "extract"); err != nil {
		return err
	}
	strip, err := detectStrip(archive)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(cacheDir, "extract-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := runCmd(logf, "tar", "-xf", archive, "-C", tmp,
		fmt.Sprintf("--strip-components=%d", strip)); err != nil {
		return err
	}
	placed, err := distribute(tmp, files, logf)
	if err != nil {
		return err
	}
	doneList := make([]string, 0, len(placed))
	var etcFiles []string
	for path := range placed {
		doneList = append(doneList, path)
		if strings.HasPrefix(path, "/etc/") {
			etcFiles = append(etcFiles, path)
		}
	}
	if err := j.setFilesDone(idx, doneList); err != nil {
		return err
	}
	if err := checkInterrupted(); err != nil {
		return err
	}

	setupRunitPerms(p.Pkgname)

	pkg := &Pkg{
		Pkgname:      p.Pkgname,
		Version:      p.Version,
		Architecture: p.Architecture,
		ShortDesc:    p.ShortDesc,
		RunDepends:   p.RunDepends,
		Provides:     p.Provides,
		Conflicts:    p.Conflicts,
		Files:        placed,
		EtcFiles:     etcFiles,
		InstallDate:  time.Now().UTC().Format(time.RFC3339),
		Status:       "half-configured",
		Auto:         auto,
	}
	if update {
		if old, err := readPkg(p.Pkgname); err == nil {
			pkg.Auto = old.Auto
			// Удаляем файлы, которых больше нет в новой версии (кроме изменённых конфигов).
			for oldPath, fi := range old.Files {
				if _, ok := placed[oldPath]; ok {
					continue
				}
				if strings.HasPrefix(oldPath, "/etc/") && fi.SHA256 != "" {
					continue
				}
				_ = os.Remove(rootJoin(oldPath))
			}
		} else {
			fmt.Fprintf(logf, "warning: update=true but %s missing from DB, treating as fresh install\n", p.Pkgname)
		}
	}
	if err := writePkg(pkg); err != nil {
		return err
	}
	if err := updateInstalledList(pkg); err != nil {
		return err
	}

	if err := j.setPhase(idx, "post-install"); err != nil {
		return err
	}
	if err := runInstallScript(archive, "post", update, p, logf); err != nil {
		if errors.Is(err, ErrInterrupted) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrPostInstall, err)
	}
	if err := checkInterrupted(); err != nil {
		return err
	}

	pkg.Status = "installed"
	return writePkg(pkg)
}

func updateInstalledList(pkg *Pkg) error {
	list, err := readInstalled()
	if err != nil {
		return err
	}
	found := false
	for i := range list {
		if list[i].Name == pkg.Pkgname {
			list[i].Version = pkg.Version
			list[i].Date = pkg.InstallDate
			list[i].Auto = pkg.Auto
			found = true
			break
		}
	}
	if !found {
		list = append(list, Installed{pkg.Pkgname, pkg.Version, pkg.InstallDate, pkg.Auto})
	}
	return writeInstalled(list)
}
