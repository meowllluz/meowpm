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
	arr, ok := raw["files"].([]interface{})
	if !ok {
		return out, nil
	}
	for _, e := range arr {
		m, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		p := dictStr(m, "path")
		if p != "" {
			out[strings.TrimPrefix(p, "./")] = FileInfo{
				Mode:   dictStr(m, "mode"),
				SHA256: dictStr(m, "sha256"),
			}
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
	if st, err := os.Stat(dest); err == nil {
		if st.Size() > 0 {
			return dest, nil
		}
		os.Remove(dest)
	}
	url := packageURL(cfg, p)
	if url == "" || !strings.HasPrefix(url, "http") {
		return "", fmt.Errorf("invalid package URL: %q", url)
	}
	if err := runCmd(logf, "curl", "-LSs", "-f",
		"--connect-timeout", "15", "--max-time", "600",
		"--retry", "3", "--retry-delay", "2", "--retry-connrefused",
		"-o", dest, url); err != nil {
			os.Remove(dest)
			return "", fmt.Errorf("curl %s: %w", url, err)
		}
		st, err := os.Stat(dest)
		if err != nil {
			return "", fmt.Errorf("curl reported success but %s missing (url=%s)", dest, url)
		}
		if st.Size() == 0 {
			os.Remove(dest)
			return "", fmt.Errorf("downloaded zero-byte package: %s", url)
		}
		fmt.Fprintf(logf, "downloaded %s (%d bytes) from %s\n", dest, st.Size(), url)
		return dest, nil
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
	return out.Close()
}

func place(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(src)
			if err != nil {
				return err
			}
			os.Remove(dst)
			return os.Symlink(target, dst)
		case info.IsDir():
			if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(dst, info.Mode().Perm())
		default:
			os.Remove(dst)
			return copyFile(src, dst, info.Mode().Perm())
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
		target := "/" + rel
		isRegular := info.Mode().IsRegular()

		if isRegular && strings.HasPrefix(target, "/etc/") {
			if _, err := os.Lstat(target); err == nil {
				old := sha256File(target)
				want := files[target].SHA256
				if want != "" && old != want {
					nf := target + ".meow-new"
					if err := place(path, nf); err != nil {
						return err
					}
					fmt.Fprintf(logf, "warning: %s differs, saved as .meow-new\n", target)
					out[nf] = FileInfo{
						Mode:   fmt.Sprintf("%04o", info.Mode().Perm()),
			     SHA256: sha256File(nf),
					}
					return nil
				}
			}
		}
		if err := place(path, target); err != nil {
			return err
		}
		fi := FileInfo{Mode: fmt.Sprintf("%04o", info.Mode().Perm())}
		if isRegular && strings.HasPrefix(target, "/etc/") {
			fi.SHA256 = sha256File(target)
		}
		out[target] = fi
		return nil
	})
	return out, err
}

func runInstallScript(archive, action string, update bool, p *RepoPkg, logf *os.File) error {
	if err := checkInterrupted(); err != nil {
		return err
	}
	data, err := readFromArchive(archive, "INSTALL")
	if err != nil || len(data) == 0 {
		return nil
	}
	tmp, err := os.CreateTemp("", "meow-install-*")
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

	cmd := exec.Command("sh", tmp.Name(), action, "yes")
	cmd.Env = append(os.Environ(),
			 "PKG_DESTDIR=/",
		  "ACTION="+strings.ToLower(strings.TrimSuffix(action, "-INSTALL"))+"install",
			 "PKGNAME="+p.Pkgname,
		  "VERSION="+p.Version,
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

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func setupAccounts(archive string, logf *os.File) error {
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
		parts := strings.Split(a, ":")
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		name := parts[0]
		if exec.Command("getent", "passwd", name).Run() == nil {
			continue
		}
		args := []string{"-r"}
		withUID := false
		if len(parts) > 1 && parts[1] != "" {
			args = append(args, "-u", parts[1])
			withUID = true
		}
		if len(parts) > 2 && parts[2] != "" {
			args = append(args, "-g", parts[2])
		}
		if len(parts) > 3 && parts[3] != "" {
			args = append(args, "-d", parts[3])
		}
		if len(parts) > 4 && parts[4] != "" {
			args = append(args, "-s", parts[4])
		}
		if len(parts) > 5 && parts[5] != "" {
			args = append(args, "-c", parts[5])
		}
		args = append(args, name)
		if err := runCmd(logf, "useradd", args...); err != nil && withUID {
			if errors.Is(err, ErrInterrupted) {
				return err
			}
			fmt.Fprintln(logf, "useradd failed, retrying without -u")
			filtered := make([]string, 0, len(args))
			skip := false
			for _, x := range args {
				if skip {
					skip = false
					continue
				}
				if x == "-u" {
					skip = true
					continue
				}
				filtered = append(filtered, x)
			}
			if err := runCmd(logf, "useradd", filtered...); err != nil {
				return err
			}
		}
	}
	return nil
}

func setupRunitPerms(pkgname string) {
	dir := filepath.Join("/etc/sv", pkgname)
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
	if err := runInstallScript(archive, "PRE-INSTALL", update, p, logf); err != nil {
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
		if err := runInstallScript(archive, "POST-INSTALL", update, p, logf); err != nil {
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
