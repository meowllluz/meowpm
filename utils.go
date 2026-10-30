package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"
)

var (
	dbDir    string
	cacheDir string
	confDir  string
	rootDir  string // пустая = реальный корень; иначе путь к alt-root (готовой ОС) для тестов
)

// initPaths вычисляет пути БД/кеша/конфига в зависимости от rootDir.
// Должна вызываться до любого использования dbDir/cacheDir/confDir.
func initPaths() {
	if rootDir == "" {
		dbDir = "/var/db/meow"
		cacheDir = "/var/cache/meow"
		confDir = "/etc/meow"
		return
	}
	dbDir = filepath.Join(rootDir, "var/db/meow")
	cacheDir = "/var/cache/meow" // кеш загрузок общий с хостом
	confDir = filepath.Join(rootDir, "etc/meow")
}

// rootJoin подставляет rootDir как префикс к абсолютному пути ("/usr/bin/x" →
// "<root>/usr/bin/x"). При пустом rootDir возвращает путь как есть.
func rootJoin(path string) string {
	if rootDir == "" {
		return path
	}
	return filepath.Join(rootDir, path)
}

var (
	ErrInterrupted = errors.New("interrupted")
	ErrPostInstall = errors.New("POST-INSTALL failed")

	interrupted atomic.Bool
)

type Lock struct{ f *os.File }

func AcquireLock() (*Lock, error) {
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dbDir, "lock"), os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("Another meow process is running")
	}
	return &Lock{f: f}, nil
}

func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	l.f.Close()
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func checkInterrupted() error {
	if interrupted.Load() {
		return ErrInterrupted
	}
	return nil
}

// runCmd runs name with args. Child gets its own process group so Ctrl+C
// does not kill it mid-operation. Ошибки дочерних процессов печатаются
// в stderr для диагностики.
func runCmd(logf *os.File, name string, args ...string) error {
	if err := checkInterrupted(); err != nil {
		return err
	}
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if logf != nil {
		cmd.Stdout = logf
		cmd.Stderr = logf
	}
	err := cmd.Run()
	if err := checkInterrupted(); err != nil {
		return err
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "[meow] %s %s: %v\n", name, strings.Join(args, " "), err)
	}
	return err
}

func isatty(fd uintptr) bool {
	var t syscall.Termios
	_, _, err := syscall.Syscall6(syscall.SYS_IOCTL, fd, syscall.TCGETS,
		uintptr(unsafe.Pointer(&t)), 0, 0, 0)
	return err == 0
}
