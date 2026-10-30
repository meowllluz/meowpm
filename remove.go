package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var protectedRoots = map[string]bool{
	"/usr": true, "/var": true, "/etc": true, "/bin": true,
	"/sbin": true, "/lib": true, "/lib64": true, "/opt": true,
	"/root": true, "/home": true, "/boot": true, "/dev": true,
	"/proc": true, "/sys": true, "/run": true,
}

// isProtected учитывает --root: логический путь системной директории защищён
// и под alt-root.
func isProtected(dir string) bool {
	if protectedRoots[dir] {
		return true
	}
	if rootDir != "" {
		if r, err := filepath.Rel(rootDir, dir); err == nil {
			return protectedRoots["/"+r]
		}
	}
	return false
}

func removeOne(pkg *Pkg, logf *os.File) error {
	svc := rootJoin(filepath.Join("/var/service", pkg.Pkgname))
	if _, err := os.Lstat(svc); err == nil {
		_ = runCmd(logf, "sv", "stop", pkg.Pkgname)
		_ = os.Remove(svc)
	}
	_ = os.RemoveAll(rootJoin(filepath.Join("/etc/sv", pkg.Pkgname)))

	for path, info := range pkg.Files {
		dst := rootJoin(path)
		if strings.HasPrefix(path, "/etc/") && info.SHA256 != "" {
			if cur := sha256File(dst); cur != "" && cur != info.SHA256 {
				_ = os.Rename(dst, dst+".meow-save")
				continue
			}
		}
		_ = os.Remove(dst)
	}
	for i := 0; i < 3; i++ {
		for path := range pkg.Files {
			dir := filepath.Dir(rootJoin(path))
			if isProtected(dir) {
				continue
			}
			_ = os.Remove(dir)
		}
	}
	_ = os.Remove(filepath.Join(dbDir, pkg.Pkgname+".json"))

	list, _ := readInstalled()
	out := list[:0]
	for _, e := range list {
		if e.Name != pkg.Pkgname {
			out = append(out, e)
		}
	}
	return writeInstalled(out)
}

// cascade: ref-counting с резолвом виртуальных зависимостей через provides.
// Если какую-то зависимость нельзя однозначно резолвить — считаем её
// «потенциально нужной» и прерываем каскад с ошибкой (безопаснее, чем
// молча удалить чужой пакет).
func cascade(targets []string) ([]string, error) {
	rc, err := loadRepoCache()
	if err != nil {
		return nil, err
	}
	inst, err := readInstalled()
	if err != nil {
		return nil, err
	}
	auto := map[string]bool{}
	deps := map[string][]string{}
	for _, e := range inst {
		auto[e.Name] = e.Auto
		p, err := readPkg(e.Name)
		if err != nil {
			continue
		}
		for _, d := range p.RunDepends {
			real, err := rc.resolveReal(d)
			if err != nil {
				return nil, fmt.Errorf("cannot resolve dependency %q of %s: %w", d, e.Name, err)
			}
			deps[e.Name] = append(deps[e.Name], real)
		}
	}
	removed := map[string]bool{}
	queue := append([]string(nil), targets...)
	for _, t := range targets {
		removed[t] = true
	}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for _, dep := range deps[x] {
			if removed[dep] || !auto[dep] {
				continue
			}
			needed := false
			for name, dlist := range deps {
				if removed[name] {
					continue
				}
				for _, d := range dlist {
					if d == dep {
						needed = true
						break
					}
				}
				if needed {
					break
				}
			}
			if !needed {
				removed[dep] = true
				queue = append(queue, dep)
			}
		}
	}
	out := make([]string, 0, len(removed))
	for n := range removed {
		out = append(out, n)
	}
	return out, nil
}

func cmdRemove(cfg *Config, args []string) error {
	cascadeFlag := false
	dryRun := false
	var targets []string
	for _, a := range args {
		switch a {
		case "-R":
			cascadeFlag = true
		case "-n", "--dry-run":
			dryRun = true
		default:
			targets = append(targets, a)
		}
	}
	if len(targets) == 0 {
		return fmt.Errorf("usage: meow remove [-R] [-n] <pkg>...")
	}
	for _, t := range targets {
		if cfg.Ignore[t] {
			return fmt.Errorf("package %s is in IGNORE_PACKAGES", t)
		}
		if _, err := readPkg(t); err != nil {
			return fmt.Errorf("package %s is not installed", t)
		}
	}
	if cascadeFlag {
		var err error
		targets, err = cascade(targets)
		if err != nil {
			return err
		}
	}
	if dryRun {
		for _, t := range targets {
			fmt.Println(t)
		}
		return nil
	}
	if !confirm("removed", targets, false) {
		fmt.Println("aborted")
		return nil
	}
	logf, err := openLog("remove")
	if err != nil {
		return err
	}
	defer logf.Close()
	for _, name := range targets {
		pkg, err := readPkg(name)
		if err != nil {
			fmt.Fprintf(logf, "skip %s: %v\n", name, err)
			continue
		}
		if err := removeOne(pkg, logf); err != nil {
			return err
		}
	}
	return nil
}
