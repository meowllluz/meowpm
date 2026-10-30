package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

func main() {
	installSignalHandler()
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		os.Exit(0)
	}
	// Вынимаем глобальный --root <path> (можно указывать в любой позиции).
	var cmd string
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--root" {
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "meow: --root requires a path")
				os.Exit(1)
			}
			rootDir = args[i+1]
			i++
			continue
		}
		if cmd == "" {
			cmd = a
		} else {
			rest = append(rest, a)
		}
	}
	if cmd == "" || cmd == "-h" || cmd == "--help" || cmd == "help" {
		usage()
		os.Exit(0)
	}
	initPaths()
	if err := dispatch(cmd, rest); err != nil {
		if errors.Is(err, ErrInterrupted) {
			fmt.Fprintln(os.Stderr, "interrupted. run 'meow repair' to continue.")
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "meow:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`meow — package manager

	usage:
	meow [--root <path>] <command> [args]

	commands:
	sync              update repository index
	install <pkg>     install package (-y skip prompt, -n dry-run)
	remove <pkg>      remove package (-R cascade, -n dry-run)
	upgrade           upgrade installed packages (-n dry-run)
	repair            resume interrupted transaction
	list              list installed packages
	search <query>    search repository index
	info <pkg>        show package info
	enable <svc>      enable runit service
	disable <svc>     disable runit service
	etc-update        merge .meow-new configs
	clean             clean cache and old logs
	log [txid]        show transaction log

	--root <path>  install into an alt-root (prepared OS directory) for testing
	`)
}

var readOnly = map[string]bool{
	"list": true, "search": true, "info": true, "log": true,
}

func dispatch(cmd string, args []string) error {
	_ = os.MkdirAll(dbDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	if readOnly[cmd] {
		switch cmd {
		case "list":
			return cmdList()
		case "search":
			return cmdSearch(args)
		case "info":
			return cmdInfo(args)
		case "log":
			return cmdLog(args)
		}
	}

	lock, err := AcquireLock()
	if err != nil {
		return err
	}
	defer lock.Release()

	cfg, err := LoadConfig(filepath.Join(confDir, "pm.conf"))
	if err != nil {
		return err
	}

	switch cmd {
	case "sync":
		return cmdSync(cfg)
	case "install":
		return cmdInstall(cfg, args)
	case "remove":
		return cmdRemove(cfg, args)
	case "upgrade":
		return cmdUpgrade(cfg, args)
	case "repair":
		return cmdRepair(cfg)
	case "enable":
		return cmdEnable(args)
	case "disable":
		return cmdDisable(args)
	case "etc-update":
		return cmdEtcUpdate()
	case "clean":
		return cmdClean()
	}
	return fmt.Errorf("unknown command: %s", cmd)
}

func installSignalHandler() {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		interrupted.Store(true)
		fmt.Fprintln(os.Stderr, "interrupted. finishing current operation... (press again to abort now)")
		<-ch
		os.Exit(130)
	}()
}

func openLog(op string) (*os.File, error) {
	dir := filepath.Join(dbDir, "log")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	name := time.Now().Format("20060102-150405") + "-" + op + ".log"
	return os.Create(filepath.Join(dir, name))
}

func confirm(action string, items []string, auto bool) bool {
	if auto {
		return true
	}
	fmt.Fprintf(os.Stderr, "the following packages will be %s:\n", action)
	for _, s := range items {
		fmt.Fprintf(os.Stderr, "  %s\n", s)
	}
	fmt.Fprintf(os.Stderr, "total: %d. proceed? [Y/n] ", len(items))
	in := bufio.NewReader(os.Stdin)
	line, _ := in.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "" || line == "y" || line == "yes"
}

func cmdList() error {
	list, err := readInstalled()
	if err != nil {
		return err
	}
	for _, e := range list {
		fmt.Println(e.Name, e.Version)
	}
	return nil
}

func cmdSearch(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: meow search <query>")
	}
	q := strings.ToLower(args[0])
	files, _ := filepath.Glob(filepath.Join(cacheDir, "search_*.txt"))
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			if strings.Contains(strings.ToLower(sc.Text()), q) {
				fmt.Println(sc.Text())
			}
		}
		fh.Close()
	}
	return nil
}

func cmdInfo(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: meow info <pkg>")
	}
	rc, err := loadRepoCache()
	if err != nil {
		return err
	}
	p, ok := rc.byName[args[0]]
	if !ok {
		return fmt.Errorf("package not found: %s", args[0])
	}
	fmt.Printf("name: %s\nversion: %s\narch: %s\ndesc: %s\n",
		p.Pkgname, p.Version, p.Architecture, p.ShortDesc)
	return nil
}

func cmdLog(args []string) error {
	dir := filepath.Join(dbDir, "log")
	entries, _ := os.ReadDir(dir)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(args) == 0 {
		n := 10
		if len(names) < n {
			n = len(names)
		}
		for _, n2 := range names[:n] {
			fmt.Println(n2)
		}
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(dir, args[0]+"-*.log"))
	for _, m := range matches {
		data, _ := os.ReadFile(m)
		os.Stdout.Write(data)
	}
	return nil
}

func cmdInstall(cfg *Config, args []string) error {
	auto := false
	dryRun := false
	var names []string
	for _, a := range args {
		switch a {
		case "-y":
			auto = true
		case "-n", "--dry-run":
			dryRun = true
		default:
			names = append(names, a)
		}
	}
	if len(names) == 0 {
		return fmt.Errorf("usage: meow install [-y] [-n] <pkg>...")
	}

	rc, err := loadRepoCache()
	if err != nil {
		return err
	}
	order, err := resolveOrder(rc, cfg, names)
	if err != nil {
		return err
	}

	requested := map[string]bool{}
	for _, a := range names {
		real, err := rc.resolveReal(a)
		if err != nil {
			return err
		}
		requested[real] = true
	}

	list, _ := readInstalled()
	inst := map[string]string{}
	for _, e := range list {
		inst[e.Name] = e.Version
	}

	var filtered []*RepoPkg
	for _, p := range order {
		v, ok := inst[p.Pkgname]
		if !ok {
			filtered = append(filtered, p)
			continue
		}
		c := CompareVersions(p.Version, v)
		switch {
		case c == 0:
			if requested[p.Pkgname] {
				if existing, err := readPkg(p.Pkgname); err == nil && existing.Status == "half-configured" {
					return fmt.Errorf("package %s is half-configured. run 'meow repair' first", p.Pkgname)
				}
				return fmt.Errorf("package %s is already installed", p.Pkgname)
			}
			continue
		case c < 0:
			if requested[p.Pkgname] {
				return fmt.Errorf("downgrade not supported: %s %s -> %s", p.Pkgname, v, p.Version)
			}
			continue
		}
		filtered = append(filtered, p)
	}
	order = filtered
	if len(order) == 0 {
		fmt.Println("nothing to do")
		return nil
	}

	items := make([]string, len(order))
	for i, p := range order {
		items[i] = p.Pkgname + "-" + p.Version
	}
	if dryRun {
		for _, it := range items {
			fmt.Println(it)
		}
		return nil
	}
	if !confirm("installed", items, auto) {
		fmt.Println("aborted")
		return nil
	}

	queue := make([]QueueItem, len(order))
	for i, p := range order {
		queue[i] = QueueItem{
			Name:    p.Pkgname,
			Version: p.Version,
			Update:  inst[p.Pkgname] != "",
			Status:  "pending",
		}
	}
	logf, err := openLog("install")
	if err != nil {
		return err
	}
	defer logf.Close()
	j, err := newJournal("install", queue)
	if err != nil {
		return err
	}
	var installed []*Pkg
	total := len(order)
	for i, p := range order {
		pct := (i * 100) / total
		fmt.Fprintf(os.Stderr, "[%d/%d %d%%] installing %s-%s\n",
			i+1, total, pct, p.Pkgname, p.Version)
		isAuto := !requested[p.Pkgname]
		if err := installOne(cfg, p, queue[i].Update, isAuto, j, i, logf); err != nil {
			return err
		}
		pkg, err := mustPkg(p.Pkgname)
		if err != nil {
			return err
		}
		installed = append(installed, pkg)
	}
	fmt.Fprintf(os.Stderr, "[%d/%d 100%%] done\n", total, total)
	j.remove()
	emitWarnings(cfg, installed, logf)
	return nil
}

func mustPkg(name string) (*Pkg, error) {
	pkg, err := readPkg(name)
	if err != nil {
		return nil, fmt.Errorf("package %s missing from DB after install: %w", name, err)
	}
	return pkg, nil
}

func cmdUpgrade(cfg *Config, args []string) error {
	dryRun := false
	for _, a := range args {
		if a == "-n" || a == "--dry-run" {
			dryRun = true
		}
	}
	rc, err := loadRepoCache()
	if err != nil {
		return err
	}
	list, err := readInstalled()
	if err != nil {
		return err
	}
	inst := map[string]string{}
	for _, e := range list {
		inst[e.Name] = e.Version
	}

	var targets []string
	for _, e := range list {
		rp, ok := rc.byName[e.Name]
		if !ok {
			continue
		}
		if CompareVersions(rp.Version, e.Version) > 0 {
			targets = append(targets, e.Name)
		}
	}
	if len(targets) == 0 {
		fmt.Println("nothing to do")
		return nil
	}
	order, err := resolveOrder(rc, cfg, targets)
	if err != nil {
		return err
	}

	var filtered []*RepoPkg
	for _, p := range order {
		v, ok := inst[p.Pkgname]
		if !ok {
			filtered = append(filtered, p)
			continue
		}
		if CompareVersions(p.Version, v) <= 0 {
			continue
		}
		filtered = append(filtered, p)
	}
	order = filtered
	if len(order) == 0 {
		fmt.Println("nothing to do")
		return nil
	}

	items := make([]string, len(order))
	for i, p := range order {
		items[i] = p.Pkgname + "-" + p.Version
	}
	if dryRun {
		for _, it := range items {
			fmt.Println(it)
		}
		return nil
	}
	if !confirm("upgraded", items, false) {
		fmt.Println("aborted")
		return nil
	}

	queue := make([]QueueItem, len(order))
	for i, p := range order {
		queue[i] = QueueItem{Name: p.Pkgname, Version: p.Version, Update: true, Status: "pending"}
	}
	logf, err := openLog("upgrade")
	if err != nil {
		return err
	}
	defer logf.Close()
	j, err := newJournal("upgrade", queue)
	if err != nil {
		return err
	}
	var installed []*Pkg
	total := len(order)
	for i, p := range order {
		pct := (i * 100) / total
		fmt.Fprintf(os.Stderr, "[%d/%d %d%%] upgrading %s-%s\n",
			i+1, total, pct, p.Pkgname, p.Version)
		if p.Pkgname == "glibc" {
			fmt.Fprintln(os.Stderr, "warning: glibc upgrade, reboot recommended")
		}
		isAuto := false
		if existing, err := readPkg(p.Pkgname); err == nil {
			isAuto = existing.Auto
		}
		if err := installOne(cfg, p, true, isAuto, j, i, logf); err != nil {
			return err
		}
		pkg, err := mustPkg(p.Pkgname)
		if err != nil {
			return err
		}
		installed = append(installed, pkg)
	}
	fmt.Fprintf(os.Stderr, "[%d/%d 100%%] done\n", total, total)
	j.remove()
	emitWarnings(cfg, installed, logf)
	return nil
}

func cmdRepair(cfg *Config) error {
	j, err := loadJournal()
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("no interrupted transaction found")
			return nil
		}
		return err
	}
	rc, err := loadRepoCache()
	if err != nil {
		return err
	}
	logf, err := openLog("repair")
	if err != nil {
		return err
	}
	defer logf.Close()

	allDone := true

	for i := range j.Queue {
		q := &j.Queue[i]
		if q.Status == "in_progress" && q.Phase == "post-install" {
			q.Status = "half-configured"
		}

		switch q.Status {
		case "done":
			continue
		case "half-configured":
			p, ok := rc.byName[q.Name]
			if !ok {
				fmt.Fprintf(os.Stderr, "cannot resolve %s, keeping journal\n", q.Name)
				allDone = false
				continue
			}
			archive, err := downloadPackage(cfg, p, logf)
			if err != nil {
				return err
			}
			if err := runInstallScript(archive, "POST-INSTALL", q.Update, p, logf); err != nil {
				return err
			}
			pkg, err := readPkg(q.Name)
			if err != nil {
				return fmt.Errorf("package %s missing from DB after repair: %w", q.Name, err)
			}
			pkg.Status = "installed"
			if err := writePkg(pkg); err != nil {
				return err
			}
			q.Status = "done"
			if err := j.save(); err != nil {
				return err
			}
		case "in_progress", "failed":
			// Та же логика: удалить частично разложенные файлы,
			// вернуть в pending, переустановить с нуля.
			for _, f := range q.FilesDone {
				os.Remove(f)
			}
			q.FilesDone = nil
			q.Status = "pending"
			if err := j.save(); err != nil {
				return err
			}
			allDone = false
		case "pending":
			p, ok := rc.byName[q.Name]
			if !ok {
				fmt.Fprintf(os.Stderr, "cannot resolve %s, keeping journal\n", q.Name)
				allDone = false
				continue
			}
			isAuto := false
			if existing, err := readPkg(q.Name); err == nil {
				isAuto = existing.Auto
			}
			if err := installOne(cfg, p, q.Update, isAuto, j, i, logf); err != nil {
				if errors.Is(err, ErrInterrupted) {
					return err
				}
				allDone = false
				continue
			}
		}

		if q.Status != "done" {
			allDone = false
		}
	}

	if allDone {
		j.remove()
	} else {
		fmt.Fprintln(os.Stderr, "some packages still unresolved, journal kept, run 'meow repair' again")
	}
	return nil
}

// ensureServiceDir: если /var/service существует и его цель (следуя по
// симлинкам) — рабочая директория, ничего не делаем. Иначе — разыменовываем
// симлинк и создаём цель. Если /var/service отсутствует — создаём директорию.
// Это корректно работает в nspawn-контейнерах, где /var/service — битый
// симлинк на /etc/runit/runsvdir/default (runit не инициализирован).
func ensureServiceDir() error {
	if st, err := os.Stat("/var/service"); err == nil && st.IsDir() {
		return nil
	}
	if target, err := os.Readlink("/var/service"); err == nil {
		return os.MkdirAll(target, 0755)
	}
	return os.Mkdir("/var/service", 0755)
}

func cmdEnable(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: meow enable <svc>")
	}
	name := args[0]
	if _, err := os.Stat(filepath.Join("/etc/sv", name, "run")); err != nil {
		return fmt.Errorf("service %s not found in /etc/sv", name)
	}
	if err := ensureServiceDir(); err != nil {
		return err
	}
	dst := filepath.Join("/var/service", name)
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("service %s is already enabled", name)
	}
	if err := os.Symlink(filepath.Join("/etc/sv", name), dst); err != nil {
		return err
	}
	return runCmd(nil, "sv", "up", name)
}

func cmdDisable(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: meow disable <svc>")
	}
	name := args[0]
	dst := filepath.Join("/var/service", name)
	if _, err := os.Lstat(dst); err != nil {
		return fmt.Errorf("service %s is not enabled", name)
	}
	_ = runCmd(nil, "sv", "stop", name)
	return os.Remove(dst)
}

func cmdEtcUpdate() error {
	var files []string
	filepath.Walk("/etc", func(path string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(path, ".meow-new") {
			files = append(files, path)
		}
		return nil
	})
	in := bufio.NewReader(os.Stdin)
	for _, f := range files {
		orig := strings.TrimSuffix(f, ".meow-new")
		fmt.Printf("%s\n[r]eplace [k]eep [d]elete [q]uit: ", orig)
		line, _ := in.ReadString('\n')
		switch strings.TrimSpace(line) {
		case "r":
			os.Remove(orig)
			os.Rename(f, orig)
		case "k", "d":
			os.Remove(f)
		case "q":
			return nil
		}
	}
	return nil
}

func cmdClean() error {
	entries, _ := os.ReadDir(cacheDir)
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "repo_") || strings.HasPrefix(n, "search_") {
			continue
		}
		os.RemoveAll(filepath.Join(cacheDir, n))
	}
	logDir := filepath.Join(dbDir, "log")
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	if entries, err := os.ReadDir(logDir); err == nil {
		for _, e := range entries {
			info, err := e.Info()
			if err == nil && info.ModTime().Before(cutoff) {
				os.Remove(filepath.Join(logDir, e.Name()))
			}
		}
	}
	return nil
}
