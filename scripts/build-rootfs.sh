#!/bin/sh
# Meow Linux — сборка rootfs (Этап 1 плана).
#
#   1. Скачивает свежий Void rootfs (glibc, x86_64).
#   2. Через нативный xbps ставит зависимости meow (curl tar zstd shadow runit)
#      + стек: seatd, iwd, dbus, wpa_supplicant.
#   3. Собирает meow и xbps2meow, кладёт в /usr/bin.
#   4. Переносит xbps-БД в meow (xbps2meow).
#   5. Удаляет xbps и libxbps через meow (временный конфиг без IGNORE).
#   6. Накладывает OVERLAYS поверх rootfs.
#   7. Чистит кеши.
#
# Требует root (chroot). Запуск:  sudo ./scripts/build-rootfs.sh
# Пути можно переопределить: ROOT=... X2M_DIR=... OVERLAYS_DIR=...
set -eu

MEOW_DIR="$(cd "$(dirname "$0")/.." && pwd)"
X2M_DIR="${X2M_DIR:-/home/llluz/xbps2meow}"
OVERLAYS_DIR="${OVERLAYS_DIR:-/home/llluz/OVERLAYS}"
ROOT="${ROOT:-$MEOW_DIR/.build-rootfs}"
REPO="https://repo-default.voidlinux.org/current"
LIVE="https://repo-default.voidlinux.org/live/current"

echo "== 1/7 скачивание Void rootfs =="
rm -rf "$ROOT"
mkdir -p "$ROOT"
ROOTFS_URL="$(curl -fsSL "$LIVE/" | grep -oE 'void-x86_64-ROOTFS-[0-9]+\.tar\.xz' | sort -V | tail -1)"
echo "    $ROOTFS_URL"
curl -fsSL "$LIVE/$ROOTFS_URL" | tar -xJ -C "$ROOT"
cp /etc/resolv.conf "$ROOT/etc/resolv.conf"

echo "== 2/7 mount + bootstrap пакетов =="
mount --bind /dev "$ROOT/dev" || true
mount --bind /proc "$ROOT/proc" || true
mount --bind /sys "$ROOT/sys" || true
chroot "$ROOT" xbps-install -Syu xbps || true
chroot "$ROOT" xbps-install -Sy curl tar zstd shadow runit
chroot "$ROOT" xbps-install -Sy seatd iwd dbus wpa_supplicant

echo "== 3/7 сборка meow + xbps2meow =="
(cd "$MEOW_DIR" && go build -o "$ROOT/usr/bin/meow" .)
(cd "$X2M_DIR" && go build -o "$ROOT/usr/bin/xbps2meow" .)

echo "== 4/7 перенос xbps -> meow =="
chroot "$ROOT" /usr/bin/xbps2meow

echo "== 5/7 удаление xbps через meow =="
mkdir -p "$ROOT/etc/meow"
printf 'REPO_MAIN=%s\nARCH=x86_64\nESCALATOR=doas\n' "$REPO" > "$ROOT/etc/meow/pm.conf"
chroot "$ROOT" /usr/bin/meow remove -R -y xbps

echo "== 6/7 наложение overlay =="
cp -a "$OVERLAYS_DIR"/. "$ROOT"/

echo "== 7/7 очистка =="
rm -rf "$ROOT/var/cache/xbps" 2>/dev/null || true
rm -f "$ROOT/var/cache/meow"/* 2>/dev/null || true

echo
echo "== rootfs готов: $ROOT =="
echo "   проверка брендинга: cat $ROOT/etc/os-release"
echo "   для ручного chroot: sudo chroot $ROOT /bin/sh"
