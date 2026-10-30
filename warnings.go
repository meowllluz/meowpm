package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var kernelRe = regexp.MustCompile(`^linux[0-9]*$`)

// emitWarnings prints small orange warnings to stderr (or logf if not a tty).
// Lowercase, no bold. ESCALATOR из конфига подставляется как есть.
func emitWarnings(cfg *Config, pkgs []*Pkg, logf *os.File) {
	useColor := isatty(os.Stderr.Fd())
	w := os.Stderr
	if !useColor {
		w = logf
	}
	if w == nil {
		return
	}
	seen := map[string]bool{}
	say := func(key, msg string) {
		if seen[key] {
			return
		}
		seen[key] = true
		if useColor {
			fmt.Fprintf(w, "\033[38;5;208m%s\033[0m\n", msg)
		} else {
			fmt.Fprintln(w, msg)
		}
	}
	esc := cfg.Escalator
	for _, p := range pkgs {
		switch {
		case kernelRe.MatchString(p.Pkgname):
			say("kernel", "warning: kernel updated, run: "+esc+" dracut --force")
		case strings.HasPrefix(p.Pkgname, "font-"),
			strings.HasPrefix(p.Pkgname, "ttf-"),
			strings.Contains(p.Pkgname, "-font-"):
			say("font", "warning: fonts updated, run: fc-cache -fv")
		}
		for path := range p.Files {
			switch {
			case strings.HasPrefix(path, "/usr/lib/") && strings.HasSuffix(path, ".so"):
				say("ldconfig", "warning: libs updated, run: "+esc+" ldconfig")
			case strings.HasPrefix(path, "/etc/ssl/certs"):
				say("ca", "warning: ca certs updated, run: "+esc+" update-ca-certificates")
			case strings.HasPrefix(path, "/usr/lib/udev/rules.d/") && strings.HasSuffix(path, ".rules"):
				say("udev", "warning: udev rules updated, run: "+esc+" udevadm control --reload-rules")
			case strings.HasSuffix(path, ".desktop"):
				say("desktop", "warning: desktop db stale, run: update-desktop-database")
			case strings.HasSuffix(path, ".gschema.xml"):
				say("gschema", "warning: glib schemas stale, run: glib-compile-schemas /usr/share/glib-2.0/schemas")
			case strings.HasPrefix(path, "/usr/share/icons/"):
				say("icons", "warning: icon cache stale, run: gtk-update-icon-cache")
			case strings.HasSuffix(path, ".mime.xml"):
				say("mime", "warning: mime db stale, run: update-mime-database /usr/share/mime")
			}
		}
	}
}
