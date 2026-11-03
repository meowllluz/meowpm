#include <sys/mount.h>
#include <sys/stat.h>
#include <unistd.h>
#include <stdio.h>

int main(void) {
	/* ядро exec'ит /init напрямую (PID 1), без shebang — нужен бинарный static init */
	mount("proc", "/proc", "proc", 0, NULL);
	mount("sysfs", "/sys", "sysfs", 0, NULL);
	mount("devtmpfs", "/dev", "devtmpfs", 0, NULL);
	mkdir("/run", 0755);

	/* дальше всю логику делает busybox sh (static) */
	char *argv[] = {"/bin/busybox", "sh", "/init.sh", NULL};
	execv("/bin/busybox", argv);
	perror("exec /bin/busybox");
	return 127;
}
