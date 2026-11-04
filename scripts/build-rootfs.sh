#!/bin/sh
# Meow Linux — сборка rootfs (Этап 1 плана).
#
#   1. Скачивает свежий Void rootfs (glibc, x86_64).
#   2. Через нативный xbps ставит зависимости meow (curl tar zstd shadow runit)
#      + стек: seatd, iwd, dbus, wpa_supplicant.
#   3. Собирает meow и xbps2meow, кладёт в /usr/bin.
#   4. Переносит xbps-БД в meow (xbps2meow).
#   5. Удаляет xbps, ставит opendoas вместо sudo, синхронизирует индекс.
#   6. Накладывает OVERLAYS поверх rootfs (каждый файл на своё место).
#   7. Чистит кеши.
#   8. Пакует rootfs.tar.zst от root (--owner=0) и проверяет архив.
#
# Требует root (chroot). Запуск:
#   sudo ./scripts/build-rootfs.sh 2>&1 | tee build-rootfs.log
# Пути можно переопределить: ROOT=... X2M_DIR=... OVERLAYS_DIR=...
# RESUME=1 — не пересобирать с нуля, продолжить с шага 5 на существующем $ROOT.
set -eu
# какая строка упала — сразу видно (bash)
trap 'echo "build-rootfs: ОШИБКА на строке $LINENO" >&2' ERR

MEOW_DIR="$(cd "$(dirname "$0")/.." && pwd)"
X2M_DIR="${X2M_DIR:-/home/llluz/xbps2meow}"
OVERLAYS_DIR="${OVERLAYS_DIR:-/home/llluz/OVERLAYS}"
ROOT="${ROOT:-$MEOW_DIR/.build-rootfs}"
REPO="https://repo-default.voidlinux.org/current"
LIVE="https://repo-default.voidlinux.org/live/current"
RESUME="${RESUME:-0}"

# Только root: иначе tar при распаковке Void rootfs сохранит владельца llluz
# и срежет setuid-биты, а tar при упаковке запишет uid 1000 вместо 0.
[ "$(id -u)" = 0 ] || { echo "build-rootfs: запускай через sudo" >&2; exit 1; }

mount_virtual() {
    mkdir -p "$ROOT/dev" "$ROOT/proc" "$ROOT/sys"
    mount --bind /dev  "$ROOT/dev"  || true
    mount --bind /proc "$ROOT/proc" || true
    mount --bind /sys  "$ROOT/sys"  || true
    trap 'umount "$ROOT/dev" 2>/dev/null; umount "$ROOT/proc" 2>/dev/null; umount "$ROOT/sys" 2>/dev/null' EXIT
}

if [ "$RESUME" = 1 ]; then
    [ -x "$ROOT/usr/bin/meow" ] || {
        echo "build-rootfs: RESUME=1, но в $ROOT нет собранного meow — запусти без RESUME" >&2
        exit 1
    }
    echo "== 1-4/8 пропущены (RESUME=1): используем готовый $ROOT =="
    mount_virtual
    cp /etc/resolv.conf "$ROOT/etc/resolv.conf"
else
    echo "== 1/8 скачивание Void rootfs =="
    rm -rf "$ROOT"
    mkdir -p "$ROOT"
    ROOTFS_URL="$(curl -fsSL "$LIVE/" | grep -oE 'void-x86_64-ROOTFS-[0-9]+\.tar\.xz' | sort -V | tail -1)"
    [ -n "$ROOTFS_URL" ] || { echo "build-rootfs: не нашёл Void rootfs в $LIVE/" >&2; exit 1; }
    echo "    $ROOTFS_URL"
    curl -fsSL "$LIVE/$ROOTFS_URL" | tar -xJ -C "$ROOT"
    cp /etc/resolv.conf "$ROOT/etc/resolv.conf"

    echo "== 2/8 mount + bootstrap пакетов =="
    mount_virtual
    chroot "$ROOT" xbps-install -Syu xbps || true
    chroot "$ROOT" xbps-install -Sy curl tar zstd shadow runit
    chroot "$ROOT" xbps-install -Sy seatd iwd dbus wpa_supplicant

    echo "== 3/8 сборка meow + xbps2meow =="
    (cd "$MEOW_DIR" && go build -o "$ROOT/usr/bin/meow" .)
    (cd "$X2M_DIR" && go build -o "$ROOT/usr/bin/xbps2meow" .)

    echo "== 4/8 перенос xbps -> meow =="
    chroot "$ROOT" /usr/bin/xbps2meow
fi

echo "== 5/8 удаление xbps + index + doas =="
mkdir -p "$ROOT/etc/meow"
printf 'REPO_MAIN=%s\nARCH=x86_64\nESCALATOR=doas\n' "$REPO" > "$ROOT/etc/meow/pm.conf"
chroot "$ROOT" /usr/bin/meow remove -R -y xbps || echo "   xbps уже удалён"

# без индекса репозитория install не находит пакеты ("dependency '...' not found")
echo "   индекс репозитория (meow sync)"
chroot "$ROOT" /usr/bin/meow sync

# doas вместо sudo: в Void пакет называется opendoas (бинарь — /usr/bin/doas),
# в базовом rootfs стоит sudo, поэтому его убираем, а sudo оставляем симлинком.
grep -q '"pkgname":"opendoas"' "$ROOT/var/cache/meow/repo_main.json" \
    || { echo "build-rootfs: в индексе нет пакета opendoas" >&2; exit 1; }
chroot "$ROOT" /usr/bin/meow install -y opendoas
if chroot "$ROOT" /usr/bin/meow list | grep -q '^sudo '; then
    chroot "$ROOT" /usr/bin/meow remove -y sudo \
        || echo "   внимание: sudo не удалился, заменяю симлинком"
fi
ln -sf /usr/bin/doas "$ROOT/usr/bin/sudo"
printf 'permit persist :wheel\n' > "$ROOT/etc/doas.conf"
chmod 600 "$ROOT/etc/doas.conf"
[ -x "$ROOT/usr/bin/doas" ] || { echo "build-rootfs: doas не установился (пакет opendoas)" >&2; exit 1; }
DOAS_MODE="$(stat -c %a "$ROOT/usr/bin/doas")"
[ "$DOAS_MODE" = 4755 ] || echo "   внимание: /usr/bin/doas mode=$DOAS_MODE, ожидался 4755"

echo "== 6/8 наложение overlay =="
# Кладём каждый файл на СВОЁ место: `cp -a OVERLAYS/. ROOT/` разложил бы
# os-release, meow/, fastfetch/, issue, hostname, host.conf прямо в корень /.
OV="$OVERLAYS_DIR"
[ -d "$OV" ] || { echo "build-rootfs: нет каталога оверлеев $OV" >&2; exit 1; }
[ -f "$OV/os-release" ] || { echo "build-rootfs: нет $OV/os-release" >&2; exit 1; }

# /etc/os-release в Void — симлинк на ../usr/lib/os-release, пишем в настоящий файл
install -Dm644 "$OV/os-release" "$ROOT/usr/lib/os-release"
for f in issue hostname host.conf; do
    [ -f "$OV/$f" ] && install -Dm644 "$OV/$f" "$ROOT/etc/$f"
done
install -Dm644 "$OV/meow/pm.conf"    "$ROOT/etc/meow/pm.conf"
install -Dm644 "$OV/meow/ascii_logo" "$ROOT/etc/meow/ascii_logo"
[ -f "$OV/fastfetch/config.jsonc" ] && install -Dm644 "$OV/fastfetch/config.jsonc" "$ROOT/etc/fastfetch/config.jsonc"
[ -f "$OV/wifi-connect" ] && install -Dm755 "$OV/wifi-connect" "$ROOT/usr/local/bin/wifi-connect"
# если остались плоские копии от старых сборок — убираем, чтобы не мусорили в /
rm -rf "$ROOT/os-release" "$ROOT/meow" "$ROOT/fastfetch" "$ROOT/issue" \
       "$ROOT/hostname" "$ROOT/host.conf" "$ROOT/wifi-connect" 2>/dev/null || true

BRAND="$(grep -s '^PRETTY_NAME' "$ROOT/etc/os-release" | head -1)"
case "$BRAND" in
    *Meow*) echo "   брендинг: $BRAND" ;;
    *) echo "build-rootfs: брендинг не применился, /etc/os-release = $BRAND" >&2; exit 1 ;;
esac
[ -x "$ROOT/usr/local/bin/wifi-connect" ] || echo "   внимание: wifi-connect не установлен"

echo "== 7/8 очистка =="
rm -rf "$ROOT/var/cache/xbps" 2>/dev/null || true
rm -f "$ROOT/var/cache/meow"/* 2>/dev/null || true

echo "== 8/8 упаковка rootfs.tar.zst =="
# --owner=0 --group=0 + --numeric-owner: архив должен быть root-овым и с setuid,
# иначе установленная система получит файлы uid 1000 и без suid-битов.
tar -c --numeric-owner --owner=0 --group=0 -C "$ROOT" \
    --exclude=./dev --exclude=./proc --exclude=./sys --exclude=./run \
    --exclude=./var/cache --exclude=./tmp --exclude=./var/tmp \
    --exclude=./root/.bash_history . | zstd -T0 -q -f -o "$MEOW_DIR/rootfs.tar.zst"

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
