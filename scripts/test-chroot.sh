#!/bin/sh
# meowpm — интеграционный тест в chroot на свежем Void rootfs.
#
# Что делает:
#   1. Скачивает последний Void rootfs (glibc, x86_64) и распаковывает в каталог.
#   2. Через нативный xbps (он есть в rootfs) доустанавливает зависимости meow:
#      curl, tar, shadow, runit.
#   3. Монтирует /dev /proc /sys (иначе нет /dev/null — Go подключает его при
#      cmd.Stdin == nil), копирует собранный meow внутрь, пишет /etc/meow/pm.conf.
#   4. Прогоняет сценарий: sync -> install (с зависимостями) -> list/info/search
#      -> remove.
#
# Требует root (chroot). Запуск:  sudo ./scripts/test-chroot.sh
# Каталог alt-root можно переопределить:  ROOT=/tmp/void ./scripts/test-chroot.sh
# При повторном запуске готовый rootfs переиспользуется (пропускается скачивание).
set -eu

MEOW_DIR="$(cd "$(dirname "$0")/.." && pwd)"
ROOT="${ROOT:-$MEOW_DIR/.test-rootfs}"
REPO="https://repo-default.voidlinux.org/current"
LIVE="https://repo-default.voidlinux.org/live/current"

if [ -x "$ROOT/usr/bin/xbps" ]; then
    echo ">> rootfs уже готов в $ROOT (пропускаю скачивание/bootstrap)"
else
    echo "== 1/5 поиск последнего rootfs =="
    ROOTFS_URL="$(curl -fsSL "$LIVE/" | grep -oE 'void-x86_64-ROOTFS-[0-9]+\.tar\.xz' | sort -V | tail -1)"
    ROOTFS_URL="$LIVE/$ROOTFS_URL"
    echo "    $ROOTFS_URL"

    echo "== 2/5 распаковка в $ROOT =="
    rm -rf "$ROOT"
    mkdir -p "$ROOT"
    curl -fsSL "$ROOTFS_URL" | tar -xJ -C "$ROOT"

    cp /etc/resolv.conf "$ROOT/etc/resolv.conf"

    echo "== 3/5 bootstrap зависимостей meow через xbps =="
    chroot "$ROOT" xbps-install -Syu xbps || true
    chroot "$ROOT" xbps-install -Sy curl tar shadow runit
fi

echo "== 4/5 mount /dev /proc /sys + сборка meow =="
mount --bind /dev "$ROOT/dev" || true
mount --bind /proc "$ROOT/proc" || true
mount --bind /sys "$ROOT/sys" || true
trap 'umount "$ROOT/dev" 2>/dev/null; umount "$ROOT/proc" 2>/dev/null; umount "$ROOT/sys" 2>/dev/null' EXIT

(cd "$MEOW_DIR" && go build -o "$ROOT/usr/bin/meow" .)
mkdir -p "$ROOT/etc/meow"
cat > "$ROOT/etc/meow/pm.conf" <<EOF
REPO_MAIN=$REPO
ARCH=x86_64
ESCALATOR=doas
EOF

echo "== 5/5 сценарий =="
chroot "$ROOT" /usr/bin/meow sync
chroot "$ROOT" /usr/bin/meow install -y tree
chroot "$ROOT" /usr/bin/meow list
chroot "$ROOT" /usr/bin/meow info tree
chroot "$ROOT" /usr/bin/meow search tree
chroot "$ROOT" /usr/bin/meow remove -y tree
# пакет с зависимостями — проверяет resolveOrder, sha256, extract, INSTALL-хуки:
chroot "$ROOT" /usr/bin/meow install -y htop
chroot "$ROOT" /usr/bin/meow remove -y htop

echo "== OK =="
