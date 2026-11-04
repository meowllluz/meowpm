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

# Только root: иначе tar при распаковке Void rootfs сохранит владельца llluz
# и срежет setuid-биты, а tar при упаковке запишет uid 1000 вместо 0.
[ "$(id -u)" = 0 ] || { echo "build-rootfs: запускай через sudo" >&2; exit 1; }

echo "== 1/8 скачивание Void rootfs =="
rm -rf "$ROOT"
mkdir -p "$ROOT"
ROOTFS_URL="$(curl -fsSL "$LIVE/" | grep -oE 'void-x86_64-ROOTFS-[0-9]+\.tar\.xz' | sort -V | tail -1)"
echo "    $ROOTFS_URL"
curl -fsSL "$LIVE/$ROOTFS_URL" | tar -xJ -C "$ROOT"
cp /etc/resolv.conf "$ROOT/etc/resolv.conf"

echo "== 2/8 mount + bootstrap пакетов =="
mount --bind /dev "$ROOT/dev" || true
mount --bind /proc "$ROOT/proc" || true
mount --bind /sys "$ROOT/sys" || true
trap 'umount "$ROOT/dev" 2>/dev/null; umount "$ROOT/proc" 2>/dev/null; umount "$ROOT/sys" 2>/dev/null' EXIT
chroot "$ROOT" xbps-install -Syu xbps || true
chroot "$ROOT" xbps-install -Sy curl tar zstd shadow runit
chroot "$ROOT" xbps-install -Sy seatd iwd dbus wpa_supplicant

echo "== 3/8 сборка meow + xbps2meow =="
(cd "$MEOW_DIR" && go build -o "$ROOT/usr/bin/meow" .)
(cd "$X2M_DIR" && go build -o "$ROOT/usr/bin/xbps2meow" .)

echo "== 4/8 перенос xbps -> meow =="
chroot "$ROOT" /usr/bin/xbps2meow

echo "== 5/8 удаление xbps через meow =="
mkdir -p "$ROOT/etc/meow"
printf 'REPO_MAIN=%s\nARCH=x86_64\nESCALATOR=doas\n' "$REPO" > "$ROOT/etc/meow/pm.conf"
chroot "$ROOT" /usr/bin/meow remove -R -y xbps

echo "== 6/8 наложение overlay =="
cp -a "$OVERLAYS_DIR"/. "$ROOT"/

echo "== 7/8 очистка =="
rm -rf "$ROOT/var/cache/xbps" 2>/dev/null || true
rm -f "$ROOT/var/cache/meow"/* 2>/dev/null || true

echo "== 8/8 упаковка rootfs.tar.zst =="
# --owner=0 --group=0 + --numeric-owner: архив должен быть root-овым и с setuid,
# иначе установленная система получит файлы uid 1000 и без suid-битов.
tar -c --numeric-owner --owner=0 --group=0 -C "$ROOT" \
    --exclude=./dev --exclude=./proc --exclude=./sys --exclude=./run \
    --exclude=./var/cache --exclude=./tmp --exclude=./var/tmp \
    --exclude=./root/.bash_history . | zstd -T0 -q -o "$MEOW_DIR/rootfs.tar.zst"

echo "   проверка архива:"
ARCHIVE="$MEOW_DIR/rootfs.tar.zst"
LIST="$(zstd -dc "$ARCHIVE" | tar -tvf -)"
BAD_OWNER="$(printf '%s\n' "$LIST" | awk '$2 != "0/0" {c++} END {print c+0}')"
SUID="$(printf '%s\n' "$LIST" | awk '$1 ~ /^-/ && $1 ~ /s/ {c++} END {print c+0}')"
for f in ./etc/shadow ./etc/passwd ./etc/group ./usr/bin/meow; do
    printf '%s\n' "$LIST" | grep -qx "$f.*" || { echo "build-rootfs: в архиве нет $f" >&2; exit 1; }
done
[ "$BAD_OWNER" = 0 ] || { echo "build-rootfs: $BAD_OWNER записей не от root в $ARCHIVE" >&2; exit 1; }
[ "$SUID" -ge 3 ] || { echo "build-rootfs: в архиве нет setuid-битов ($SUID) — сборка шла не от root?" >&2; exit 1; }
echo "   владельцы: все 0/0, setuid-файлов: $SUID, размер: $(du -h "$ARCHIVE" | cut -f1)"

echo
echo "== rootfs готов: $ROOT =="
echo "   архив для установщика: $ARCHIVE"
echo "   проверка брендинга: cat $ROOT/etc/os-release"
echo "   для ручного chroot: sudo chroot $ROOT /bin/sh"
